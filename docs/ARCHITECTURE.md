# VM Smith — Architecture

## Overview

VM Smith is a CLI tool and daemon for provisioning and managing QEMU/KVM virtual machines on Linux hosts. It provides a unified interface for VM lifecycle management, networking, snapshotting, and image distribution.

**Design principles:**
- Single static binary — CLI + REST API + embedded React GUI, no sidecar processes
- Minimal runtime dependencies — only libvirt and qemu-kvm on the host
- Backend-agnostic VM management layer (QEMU/libvirt today, KubeVirt later)
- Simple, predictable networking: NAT by default, explicit port forwarding
- Image portability via qcow2 + SCP/HTTP distribution

---

## Technology Stack

| Component       | Choice                        | Rationale                                         |
|-----------------|-------------------------------|---------------------------------------------------|
| Language        | Go 1.22+, CGO_ENABLED=1       | Single binary, strong concurrency, libvirt C bindings |
| VM backend      | libvirt + QEMU/KVM            | Mature API, snapshot support, network management  |
| REST framework  | Chi v5                        | Idiomatic, lightweight, standard net/http          |
| CLI framework   | Cobra                         | Industry standard for Go CLIs                     |
| Metadata store  | bbolt                         | Embedded, pure Go, zero config, transactional     |
| Image format    | qcow2                         | CoW, snapshot support, thin provisioning          |
| Networking      | libvirt NAT + iptables DNAT   | No host bridge setup required                     |
| Frontend        | React 18 + Vite + Tailwind    | Embedded via `go:embed`, dark industrial theme    |
| Target OS       | Ubuntu 22.04+, Rocky Linux 8+ | Broad enterprise + community coverage             |

---

## Project Structure

```
vmsmith/
├── cmd/vmsmith/main.go              # Entrypoint → cli.Execute()
├── internal/
│   ├── api/
│   │   ├── router.go                # Chi router, middleware wiring
│   │   ├── handlers_vm.go           # VM CRUD + lifecycle endpoints
│   │   ├── handlers_snapshot.go     # Snapshot endpoints
│   │   ├── handlers_image.go        # Image upload/download/list/delete
│   │   ├── handlers_network.go      # Port forward + host interface endpoints
│   │   └── middleware.go            # Logging, CORS, error response helpers
│   ├── cli/
│   │   ├── root.go                  # Root command, global --config flag
│   │   ├── vm.go                    # vmsmith vm create|list|start|stop|delete
│   │   ├── snapshot.go              # vmsmith snapshot create|restore|list|delete
│   │   ├── image.go                 # vmsmith image list|create|delete|push|pull
│   │   ├── net.go                   # vmsmith net interfaces
│   │   ├── network.go               # vmsmith port add|remove|list
│   │   └── daemon.go                # vmsmith daemon start
│   ├── config/config.go             # Config struct, DefaultConfig(), EnsureDirs()
│   ├── daemon/daemon.go             # HTTP server, libvirt connect, signal handling
│   ├── network/
│   │   ├── nat.go                   # libvirt NAT network setup + stale-dnsmasq cleanup
│   │   ├── portforward.go           # iptables DNAT rules, persist + restore
│   │   └── discover.go              # Host interface enumeration (/sys/class/net)
│   ├── storage/
│   │   ├── image.go                 # qcow2 import, export, list (qemu-img)
│   │   └── transfer.go              # SCP push/pull helpers
│   ├── store/
│   │   ├── bolt.go                  # bbolt CRUD for VMs, images, port forwards
│   │   └── models.go                # Stored data structures
│   ├── vm/
│   │   ├── manager.go               # VMManager interface
│   │   ├── lifecycle.go             # LibvirtManager: Create/Start/Stop/Delete/Get/List + snapshots
│   │   ├── domain.go                # libvirt domain XML generation, multi-network, cloud-init
│   │   └── mock_manager.go          # In-memory mock for tests
│   └── web/
│       ├── embed.go                 # go:embed dist/*
│       └── dist/                    # Built SPA (gitignored; built by `make build`)
├── pkg/types/
│   ├── vm.go                        # VM, VMSpec, VMState
│   ├── snapshot.go                  # Snapshot
│   ├── image.go                     # Image
│   ├── network.go                   # NetworkAttachment, PortForward, HostInterface
│   └── errors.go                    # Typed API errors
├── web/                             # React source
│   ├── src/api/client.js            # REST API client (vms, snapshots, images, ports, host)
│   ├── src/components/              # Layout, Shared (StatusBadge, Modal, etc.)
│   ├── src/pages/                   # Dashboard, VMList, VMDetail, ImageList
│   ├── src/hooks/useFetch.js        # Data fetching with polling + mutation helpers
│   └── vite.config.js               # Outputs to ../internal/web/dist/
├── scripts/
│   ├── install-deps-ubuntu.sh
│   └── install-deps-rocky.sh
├── Makefile
├── vmsmith.yaml.example
└── docs/ARCHITECTURE.md             # This file
```

---

## Core Architecture

```
┌──────────────────────────────────────────────────────────┐
│                        VM Smith                          │
│                                                          │
│  ┌──────────┐    ┌──────────────┐    ┌────────────────┐  │
│  │  CLI      │    │  REST API    │    │  Web GUI       │  │
│  │ (Cobra)   │    │  (Chi v5)    │    │ (React, embed) │  │
│  └─────┬─────┘    └──────┬───────┘    └───────┬────────┘  │
│        └────────────┬────┴───────────────────┘           │
│                     ▼                                    │
│  ┌─────────────────────────────────────────────────┐     │
│  │                 Service Layer                   │     │
│  │  ┌─────────────┐  ┌──────────────┐  ┌────────┐  │     │
│  │  │  VMManager  │  │StorageManager│  │Network │  │     │
│  │  │ (libvirt)   │  │ (qemu-img)   │  │Manager │  │     │
│  │  └──────┬──────┘  └──────┬───────┘  └───┬────┘  │     │
│  └─────────┼────────────────┼──────────────┼───────┘     │
│            ▼                ▼              ▼             │
│  ┌──────────────────────────────────────────────────┐    │
│  │  libvirt   │  qcow2 files  │  iptables DNAT      │    │
│  │  bbolt (metadata: VMs, images, port forwards)    │    │
│  └──────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────┘
```

---

## Detailed Design

### 1. VM Lifecycle

**Creating a VM (`lifecycle.go`):**

1. `EnsureNetwork()` — create or activate the vmsmith-net NAT network (idempotent)
2. Validate + default `VMSpec` fields (CPUs, RAM, disk)
3. Validate extra `NetworkAttachment` entries if present
4. Generate a unique VM ID (`vm-<unix-nano>`)
5. `qemu-img create -f qcow2 -b <base> <overlay>` — thin CoW disk
6. `createCloudInitISO()` if SSH key, custom cloud-init, or extra network interfaces are present
7. `DomainParamsFromSpec()` + `GenerateDomainXML()` — build libvirt XML
8. `conn.DomainDefineXML()` + `dom.Create()` — register and boot
9. Persist VM record in bbolt

**VM states:** `running → stopped → deleted`

**Cloud-init (NoCloud datasource):**

A cloud-init ISO (`cidata.iso`) is attached as a CD-ROM when:
- An SSH public key is provided
- A custom cloud-init file is provided
- **Any extra network interfaces are attached** — even DHCP ones, because without a `network-config` entry cloud-init leaves extra interfaces unconfigured and they receive no IP address

The ISO contains `meta-data`, `user-data`, and (when needed) `network-config` (Netplan v2 format).

---

### 2. Networking

#### Default NAT Network

Every VM gets **eth0** on `vmsmith-net` (`192.168.100.0/24`):

- Created automatically by `network.Manager.EnsureNetwork()` on first daemon start or VM create
- Implemented as a libvirt NAT network with built-in dnsmasq DHCP
- VMs get DHCP addresses in the configured range (default `.10–.254`)
- Outbound internet access via libvirt's NAT/masquerade
- Host can always reach VMs directly on the NAT subnet

**Restart resilience:** When the daemon is killed without clean shutdown, libvirtd marks the network inactive but leaves the dnsmasq process running (orphaned). On the next `EnsureNetwork()` call, VM Smith reads the libvirt PID file at `/run/libvirt/network/<name>.pid` and sends SIGTERM to the orphan before calling `net.Create()`.

#### Port Forwarding

```
External host                  VM Smith host                  VM
──────────────                 ─────────────                 ───
ssh -p 2222 hostip  ──────►  iptables DNAT ──────►  192.168.100.x:22
                              hostport → vmip:guestport
```

- Rules stored in bbolt and restored via `portforward.RestoreAll()` on daemon startup
- Implemented with `iptables -t nat -A PREROUTING -j DNAT` + corresponding FORWARD rules
- `-w 5` timeout prevents races with libvirt's own iptables usage

#### Multi-Network (macvtap / bridge)

Additional interfaces are specified as `--network <iface[:opts]>` (CLI) or via the `networks` array in the API/GUI. They become **eth1, eth2, …** inside the VM.

| Mode     | Libvirt XML                             | When to use                                     |
|----------|-----------------------------------------|-------------------------------------------------|
| macvtap  | `<interface type='direct' mode='bridge'>` | VM needs its own MAC/IP on the physical network; no host bridge config needed |
| bridge   | `<interface type='bridge'>`             | Full host↔VM communication on the same subnet; requires pre-configured Linux bridge |

**Cloud-init network-config** is always written when extra interfaces are present, configuring each as DHCP or static IP. Without it the OS never brings up extra interfaces.

**CLI syntax:**

```bash
vmsmith net interfaces                    # discover available host NICs

# DHCP on eth1
vmsmith vm create db01 --image ubuntu --network eth1

# Static IP on eth1, DHCP on eth3
vmsmith vm create db01 --image ubuntu \
  --network eth1:ip=192.168.1.100/24,gw=192.168.1.1,name=data \
  --network eth3

# Bridge mode
vmsmith vm create db01 --image ubuntu \
  --network eth1:mode=bridge,bridge=br-data
```

**Network layout inside VM:**

```
eth0  192.168.100.x   ← vmsmith-net NAT (always, DHCP)
eth1  <host-net IP>   ← first --network attachment
eth2  <host-net IP>   ← second --network attachment
...
```

---

### 3. Snapshots and Images

**Snapshots** — point-in-time state, stays on the same host:
- libvirt internal snapshot mechanism (memory + disk)
- Metadata tracked by libvirt; listed via `dom.ListAllSnapshots()`

**Images** — portable qcow2 files for distribution:
- Created by flattening a VM overlay onto its base: `qemu-img convert -O qcow2`
- Uploaded via GUI (drag-and-drop) or `vmsmith image create`
- Stored in `/var/lib/vmsmith/images/`
- Transferred between hosts via SCP (`image push/pull`) or HTTP download

---

### 4. Daemon Mode

`vmsmith daemon start` (`internal/daemon/daemon.go`):

1. Opens and holds a libvirt connection (`qemu:///system`)
2. Calls `EnsureNetwork()` to set up the NAT network
3. Calls `portforward.RestoreAll()` to re-apply iptables rules from bbolt
4. Starts the Chi HTTP server on the configured port
5. Handles `SIGTERM`/`SIGINT` for clean shutdown (network teardown, libvirt disconnect)

---

### 5. REST API

All endpoints are prefixed `/api/v1/`.

| Method | Path                                     | Description                   |
|--------|------------------------------------------|-------------------------------|
| GET    | /vms                                     | List all VMs                  |
| POST   | /vms                                     | Create a new VM               |
| GET    | /vms/{id}                                | Get VM details                |
| POST   | /vms/{id}/start                          | Start a stopped VM            |
| POST   | /vms/{id}/stop                           | Stop a running VM             |
| DELETE | /vms/{id}                                | Delete a VM                   |
| GET    | /vms/{id}/snapshots                      | List snapshots                |
| POST   | /vms/{id}/snapshots                      | Create snapshot               |
| POST   | /vms/{id}/snapshots/{name}/restore       | Restore snapshot              |
| DELETE | /vms/{id}/snapshots/{name}               | Delete snapshot               |
| GET    | /images                                  | List images                   |
| POST   | /images                                  | Create image from VM disk     |
| POST   | /images/upload                           | Upload qcow2 file             |
| DELETE | /images/{id}                             | Delete image                  |
| GET    | /images/{id}/download                    | Download image file           |
| GET    | /vms/{id}/ports                          | List port forwards            |
| POST   | /vms/{id}/ports                          | Add port forward              |
| DELETE | /vms/{id}/ports/{portId}                 | Remove port forward           |
| GET    | /host/interfaces                         | List host network interfaces  |

---

### 6. Web GUI

The React SPA is embedded into the binary via `go:embed dist/*`. The same port serves both the API and the GUI.

**Pages:**

| Route     | Features                                                               |
|-----------|------------------------------------------------------------------------|
| `/`       | Dashboard — VM count, state breakdown, quick actions                   |
| `/vms`    | VM list; Create modal with name, image, resources, SSH key, extra networks |
| `/vms/:id`| VM detail — info cards, extra network display, snapshots, port forwards |
| `/images` | Upload (drag-and-drop), list, download, delete qcow2 images            |

**Network UI (Create VM modal):**
- "Extra Networks" section with Add/Remove buttons
- Fetches physical host interfaces from `GET /api/v1/host/interfaces`
- Per-attachment: mode (macvtap/bridge), interface dropdown or text input, optional static IP + gateway

**VM Detail network display:**
- Shows attached networks (eth1…) with mode, host interface, and IP or DHCP label

**Development:**

```bash
make dev-api   # Go daemon on :8080
make dev-web   # Vite dev server on :3000, proxies /api/* → :8080
```

---

### 7. Configuration

File: `~/.vmsmith/config.yaml` or `/etc/vmsmith/config.yaml`

```yaml
daemon:
  listen: "0.0.0.0:8080"
  pid_file: "/var/run/vmsmith.pid"

libvirt:
  uri: "qemu:///system"        # use qemu:///session for rootless

storage:
  images_dir: "/var/lib/vmsmith/images"
  base_dir:   "/var/lib/vmsmith/vms"
  db_path:    "~/.vmsmith/vmsmith.db"

network:
  name:       "vmsmith-net"
  subnet:     "192.168.100.0/24"
  dhcp_start: "192.168.100.10"
  dhcp_end:   "192.168.100.254"

defaults:
  cpus:    2
  ram_mb:  2048
  disk_gb: 20
```

---

### 8. Data Model (bbolt)

| Bucket         | Key                        | Value                        |
|----------------|----------------------------|------------------------------|
| `vms`          | VM ID                      | JSON `types.VM`              |
| `images`       | image ID                   | JSON `types.Image`           |
| `port_forwards`| `{vmID}/{hostPort}`        | JSON `types.PortForward`     |

---

## Testing Strategy

VM Smith uses a three-tier approach. No real libvirt or QEMU is needed for any test.

### Test Structure

```
internal/
├── store/bolt_test.go           # bbolt CRUD: VMs, images, port forwards, persistence
├── config/config_test.go        # Config loading, defaults, YAML merge, EnsureDirs
├── network/
│   ├── discover_test.go         # Host interface enumeration (mocked /sys/class/net)
│   └── portforward_test.go      # iptables rule construction and restoration
├── vm/
│   ├── domain_test.go           # XML generation, multi-network, MAC, validation
│   ├── mock_manager.go          # In-memory VM manager (implements Manager interface)
│   └── mock_manager_test.go     # Mock lifecycle, snapshots, error injection
├── cli/
│   ├── cli_test.go              # parseNetworkFlags, humanSize, command wiring
│   └── commands_test.go         # Additional CLI command tests
├── storage/image_test.go        # ImportImage, ListImages, GetImage, DeleteImage
└── api/api_test.go              # All REST endpoints via httptest + MockManager
tests/web/
├── mock-server.js               # Node.js mock API server for browser tests
├── gui.spec.js                  # Playwright test specs
└── run-gui-tests.js             # Playwright runner
```

### Mock VM Manager

`internal/vm/mock_manager.go` implements `vm.Manager` entirely in-memory:
- Full VM lifecycle with state tracking
- Snapshot CRUD
- Error injection via exported `CreateErr`, `StartErr`, etc.
- `SeedVM()` helper for test data setup
- Thread-safe via `sync.RWMutex`

This enables API integration tests with zero libvirt dependency.

### Running Tests

```bash
make test             # all Go tests
make test-unit        # unit tests only
make test-integration # API integration tests (httptest + mock)
make test-web-deps    # install Playwright (run once)
make test-web         # headless Chromium E2E
make test-all         # everything
```

### Coverage Summary

| Layer       | Package       | Tests | Coverage                                              |
|-------------|---------------|-------|-------------------------------------------------------|
| Unit        | store         | 9     | CRUD for VMs, images, ports; persistence              |
| Unit        | config        | 5     | Defaults, file load, YAML merge, invalid YAML         |
| Unit        | vm/domain     | 18    | XML gen, multi-net, macvtap/bridge/NAT, MAC           |
| Unit        | vm/mock       | 8     | Mock lifecycle, snapshots, error injection            |
| Unit        | cli           | 17    | Network flag parsing, all modes, humanSize            |
| Unit        | network       | 10    | Interface discovery, portforward rule construction    |
| Unit        | storage       | 11    | Import, list, get, delete, path handling              |
| Integration | api           | 28    | All REST endpoints incl. upload; error paths          |
| E2E         | web           | 50    | Dashboard, VM CRUD, snapshots, images, navigation     |
| **Total**   |               | **156** |                                                     |

---

## Future Enhancements

- **KubeVirt backend** — `KubeVirtVMManager` behind the same interface
- **Second NAT networks** — create per-VM isolated private subnets
- **VNC/SPICE proxy** — browser-accessible console via the web GUI
- **VM templates** — named resource presets ("small", "medium", "large")
- **OCI image support** — pull cloud images from container registries
- **Cluster mode** — multiple VM Smith hosts with shared image catalog
