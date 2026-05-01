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
│   │   ├── handlers_logs.go         # Log viewer endpoint (GET /api/v1/logs)
│   │   └── middleware.go            # Request logging, CORS, error response helpers
│   ├── cli/
│   │   ├── root.go                  # Root command, global --config flag
│   │   ├── vm.go                    # vmsmith vm create|edit|list|start|stop|delete
│   │   ├── snapshot.go              # vmsmith snapshot create|restore|list|delete
│   │   ├── image.go                 # vmsmith image list|create|delete|push|pull
│   │   ├── net.go                   # vmsmith net interfaces
│   │   ├── network.go               # vmsmith port add|remove|list
│   │   └── daemon.go                # vmsmith daemon start
│   ├── config/config.go             # Config struct, DefaultConfig(), EnsureDirs()
│   ├── daemon/daemon.go             # HTTP server, libvirt connect, signal handling, logger init
│   ├── metrics/
│   │   ├── collector.go             # libvirt bulk-stats sampler + rate math
│   │   ├── metrics.go               # metrics Manager interface + mock
│   │   └── ring.go                  # fixed-size in-memory history ring per VM
│   ├── logger/
│   │   └── logger.go                # Structured logger: ring buffer, levels, file output
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
│   ├── vm.go                        # VM, VMSpec, VMUpdateSpec, VMState
│   ├── snapshot.go                  # Snapshot
│   ├── image.go                     # Image
│   ├── network.go                   # NetworkAttachment, PortForward, HostInterface
│   ├── template.go                  # VMTemplate
│   ├── quota.go                     # Quota usage response types
│   ├── event.go                     # Event (system / VM lifecycle audit log)
│   ├── metrics.go                   # MetricSample, VMStatsSnapshot
│   └── errors.go                    # Typed API errors
├── web/                             # React source
│   ├── src/api/client.js            # REST API client (vms, snapshots, images, ports, host, logs)
│   ├── src/components/              # Layout, Shared (StatusBadge, Modal, etc.)
│   ├── src/pages/                   # Dashboard, VMList, VMDetail, ImageList, LogViewer
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
3. Validate extra `NetworkAttachment` entries if present; pre-assign MACs for each extra interface
4. Generate a NAT interface MAC address (`natMAC`)
5. **Static IP pre-assignment** — pick an available IP from the DHCP range and call `netMgr.AddDHCPHost(natMAC, ip, name)` so dnsmasq always gives this VM a predictable address. Any stale reservation for the same VM name is removed first via `RemoveDHCPHostByName`. The IP is embedded in the NM keyfile (`method=manual`) to eliminate DHCP race conditions on Rocky/RHEL. Falls back to dynamic assignment if the range is exhausted or the reservation fails.
6. Generate a unique VM ID (`vm-<unix-nano>`)
7. **Image path resolution** — if the image name is relative, it is looked up under `storage.images_dir`. If the exact path does not exist, `.qcow2` is appended (e.g. `rocky9` → `rocky9.qcow2`). Images **must** have a `.qcow2` extension so libvirt's AppArmor driver follows the backing-file chain and allows QEMU to open them.
8. `qemu-img create -f qcow2 -b <base> <overlay>` — thin CoW disk
9. `createCloudInitISO()` — always created; generates `meta-data`, `user-data`, and `network-config` (Netplan v2) with MAC-based interface matching so it works on any distro
10. `DomainParamsFromSpec()` + `GenerateDomainXML()` — build libvirt XML; `detectQEMUBinary()` probes `/usr/libexec/qemu-kvm` (RHEL/Rocky) then `/usr/bin/qemu-system-x86_64` (Debian/Ubuntu) to set the `<emulator>` path automatically
11. `conn.DomainDefineXML()` + `dom.Create()` — register and boot; on failure, the DHCP reservation and VM directory are cleaned up
12. Persist VM record in bbolt; launch `startIPMonitor` goroutine (120 s timeout)

**Updating a VM (`Update` in `lifecycle.go`):**

`PATCH /api/v1/vms/{id}` (body: `VMUpdateSpec{cpus, ram_mb, disk_gb, nat_static_ip, nat_gateway}`) — zero/empty values are ignored:

1. Look up VM in store; look up libvirt domain by name
2. If running → graceful `Shutdown()`; poll for `DOMAIN_SHUTOFF` (up to 60 s); force `Destroy()` if graceful fails
3. If static IP changed → remove old DHCP host reservation → add new reservation → regenerate cloud-init ISO with updated NM keyfile **and a new instance-id** so cloud-init re-runs on next boot and applies the new address
4. If CPU or RAM changed → regenerate domain XML using updated spec + stored disk/ISO paths → `DomainDefineXML()` (the existing domain UUID is preserved to avoid the "domain already exists" error)
5. If disk size increased → `qemu-img resize <diskPath> <NGB>`. Shrinking is rejected with an error.
6. Persist updated `Spec` (incl. `NatStaticIP`, `NatGateway`, `IP`) in bbolt
7. Restart domain (`dom.Create()`) if it was running before
8. Return updated `VM`

**VM states:** `running → stopped → deleted`

**Cloud-init (NoCloud datasource):**

A cloud-init ISO (`cidata.iso`) is **always** attached as a CD-ROM. It contains three files:

| File | Purpose |
|------|---------|
| `meta-data` | Instance ID and local hostname |
| `user-data` | SSH keys, user creation, NM keyfile for primary NAT interface |
| `network-config` | Netplan v2 belt-and-suspenders config for Ubuntu/Debian |

MAC addresses are generated in `lifecycle.go` before creating either the ISO or the domain XML, so the same value appears in both the libvirt `<interface>` definition and the cloud-init configs.

**`user-data` — NM keyfile approach (`buildCloudConfig`):**

The `user-data` uses `write_files` to drop a NetworkManager keyfile for the primary NAT interface, then activates it via `runcmd`. This is more reliable on Rocky/RHEL than cloud-init's Netplan/NM renderer. When a static IP was pre-assigned, the keyfile uses `method=manual`; otherwise `method=auto` (DHCP):

```yaml
#cloud-config
write_files:
  - path: /etc/NetworkManager/system-connections/vmsmith-nat.nmconnection
    permissions: '0600'
    makedirs: true
    content: |
      [connection]
      id=vmsmith-nat
      type=ethernet
      ...
      [ipv4]
      method=manual          # static; or method=auto for DHCP fallback
      addresses=192.168.100.x/24
      gateway=192.168.100.1
runcmd:
  - restorecon -v /etc/NetworkManager/system-connections/vmsmith-nat.nmconnection 2>/dev/null || true
  - nmcli connection reload
  - nmcli connection up vmsmith-nat
```

`restorecon` sets the correct SELinux file context on Rocky/RHEL so NetworkManager can read the keyfile.

**SSH user injection:**

VMs boot with **root login enabled by default**. `buildCloudConfig` always emits `disable_root: false` and writes a drop-in sshd config (`/etc/ssh/sshd_config.d/99-vmsmith-root.conf`) with `PermitRootLogin prohibit-password` to allow key-based root SSH across all distros. When an SSH key is provided with no `DefaultUser`, it is injected directly into root's `authorized_keys`:

```yaml
disable_root: false
users:
  - name: root
    ssh_authorized_keys:
      - ssh-rsa AAAA...
```

When `DefaultUser` is set, root login is disabled and a named sudo user is created with the SSH key instead. The image's built-in default user is preserved alongside it:

```yaml
disable_root: true
users:
  - default                    # preserves the image's built-in default user
  - name: alice
    ssh_authorized_keys:
      - ssh-rsa AAAA...
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: true
```

**Supported Rocky Linux image variants:**

| Variant | Works? | Notes |
|---|---|---|
| `Rocky-9-GenericCloud-Base` | **Yes** | Designed for VMs/clouds; includes cloud-init and NetworkManager |
| `Rocky-9-OCP-Base` | **No** | OpenShift nodes; uses Ignition (not cloud-init) — NM keyfile is ignored, no network |

Download the correct image:
```bash
wget -O /var/lib/vmsmith/images/rocky9.qcow2 \
  https://download.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base.latest.x86_64.qcow2
```

**`network-config` — Netplan v2 (belt-and-suspenders):**

A Netplan v2 `network-config` is also written. It uses `match: macaddress:` for every interface (primary NAT + any extra attachments). Ubuntu/Debian apply this directly; Rocky/RHEL ignore it in favour of the NM keyfile above.

---

### 2. Networking

#### Default NAT Network

Every VM gets a primary interface on `vmsmith-net` (`192.168.100.0/24`). The OS may name it `eth0` (Ubuntu) or `enp1s0` / `ens3` (Rocky Linux / RHEL) — cloud-init configures it via MAC address matching regardless of name:

- Created automatically by `network.Manager.EnsureNetwork()` on first daemon start or VM create
- Implemented as a libvirt NAT network with built-in dnsmasq DHCP
- VMs get DHCP addresses in the configured range (default `.10–.254`)
- Outbound internet access via libvirt's NAT/masquerade
- Host can always reach VMs directly on the NAT subnet

**Static IP pre-assignment:** At VM creation time, `LibvirtManager.Create()` picks the first unused address in the DHCP range and registers a static DHCP host entry (`netMgr.AddDHCPHost`) before generating the cloud-init ISO. The IP is written directly into the NM keyfile as `method=manual`, so the VM interface comes up on first boot without any DHCP exchange. Any stale reservation left by a previous failed create with the same VM name is removed first via `RemoveDHCPHostByName`. The IP is shown immediately in `vmsmith vm create` output — no polling needed. On `dom.Create()` failure, the reservation is removed automatically.

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

Additional interfaces are specified as `--network <iface[:opts]>` (CLI) or via the `networks` array in the API/GUI. Their OS-visible names depend on the distro (`eth1`, `enp2s0`, etc.) — cloud-init matches them by MAC address.

| Mode     | Libvirt XML                             | When to use                                     |
|----------|-----------------------------------------|-------------------------------------------------|
| macvtap  | `<interface type='direct' mode='bridge'>` | VM needs its own MAC/IP on the physical network; no host bridge config needed |
| bridge   | `<interface type='bridge'>`             | Full host↔VM communication on the same subnet; requires pre-configured Linux bridge |

**Cloud-init network-config** is always written for every interface (NAT and extra), matching each by MAC address and configuring DHCP or static IP as requested. Without it the OS never brings up extra interfaces.

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

**Network layout inside VM** (interface names are distro-dependent; cloud-init matches by MAC):

```
<NAT iface>   192.168.100.x   ← vmsmith-net NAT (always, DHCP)  e.g. eth0 / enp1s0
<extra iface> <host-net IP>   ← first --network attachment       e.g. eth1 / enp2s0
<extra iface> <host-net IP>   ← second --network attachment      e.g. eth2 / enp3s0
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

### 4. Metrics

VM Smith collects per-VM resource metrics in-process via `internal/metrics/`. The daemon polls libvirt's bulk stats API on a fixed interval, converts cumulative counters into rates, and stores the results in bounded in-memory rings keyed by VM ID.

**Sampling pipeline:**

1. `LibvirtMetricsManager` calls `GetAllDomainStats` on the configured interval (default 10 s).
2. A short-lived resolver maps libvirt domain names back to VMSmith VM IDs so the API and CLI can look metrics up by stable VM ID.
3. Each sample records point-in-time values for CPU, memory, disk throughput, and network throughput.
4. Cumulative libvirt counters are converted into per-second rates by diffing against the previous sample for the same VM.
5. Samples are pushed into a fixed-size ring buffer per VM (default 360 samples, about 1 hour at 10 s).
6. Rings and previous-counter state are pruned when a VM disappears from sampling output for long enough, so deleted or long-stopped VMs do not accumulate unbounded memory.

**Metric contract (`pkg/types/metrics.go`):**

- `MetricSample` uses pointer fields for every metric so `nil` means "unavailable" rather than zero.
- This matters for first-sample rate math, stopped VMs, and guests without the qemu guest agent.
- `VMStatsSnapshot` returns `current`, bounded `history`, `last_sampled_at`, and the sampler's configured interval/history size.

**Rate math and missing data:**

- CPU percent is derived from cumulative CPU time across active vCPUs.
- Disk and network values are emitted as bytes/sec by summing libvirt's per-device counters, then dividing the delta by wall-clock elapsed time.
- Counter resets or missing prior samples produce `nil` for that datapoint instead of a misleading spike.
- Memory availability depends on guest-agent-backed balloon stats. If the guest does not expose them, memory pressure fields stay `nil`.

**Persistence model:**

- Metrics history is intentionally in-memory only today. It is optimized for recent troubleshooting, live UI views, and lightweight CLI/API reads.
- The REST API freezes the last known history for stopped VMs instead of backfilling synthetic zeros.
- This keeps the implementation simple while avoiding a local TSDB dependency.

**API and scrape surfaces:**

- `GET /api/v1/vms/{id}/stats` returns the latest snapshot plus bounded history, with optional `since` and `fields` projection filters.
- `GET /metrics` emits Prometheus text-format gauges for the latest per-VM values.
- `metrics.scrape_listen` can expose `/metrics` on a separate listener when operators want scraping isolated from the main API port.

**Prometheus integration:**

The current `/metrics` endpoint exposes the latest in-memory values only, labeled by `vm_id` and `vm_name`. Prometheus is the intended durable-history layer: scrape VMSmith periodically, then use Prometheus or Grafana for long retention, alerting, and dashboards beyond the daemon's in-memory ring.

---

### 5. Event System

VM Smith ships an in-process event bus that captures every state-changing action — libvirt lifecycle callbacks, API/CLI mutations, and daemon internals — and exposes them to operators via REST, SSE, and the web GUI's Activity page. The implementation lives in `internal/events/`, the persistence layer lives in `internal/store/bolt.go`, and the wire types live in `pkg/types/event.go`.

**Event taxonomy** (three sources, one bus):

| Source | Origin | Examples |
|---|---|---|
| `libvirt` | `DomainEventLifecycleRegister` callback in `internal/vm/events.go` | `vm.started`, `vm.stopped`, `vm.crashed`, `vm.shutdown`, `vm.suspended`, `vm.resumed` |
| `app` | API/CLI mutating handlers, post-success | `vm.created`, `vm.updated`, `vm.cloned`, `vm.deleted`, `vm.start_requested`, `vm.stop_requested`, `snapshot.created`, `snapshot.restored`, `snapshot.deleted`, `image.uploaded`, `image.created`, `image.deleted`, `port_forward.added`, `port_forward.removed` |
| `system` | Daemon internals (`internal/daemon/`, `internal/api/quotas.go`, `internal/events/retention.go`) | `daemon.started`, `daemon.shutdown`, `quota.exceeded`, `dhcp.exhausted`, `events.retention_pruned` |

**Event record (`pkg/types/event.go`):**

```go
type Event struct {
    ID         string            `json:"id"`           // stringified uint64 from EventBus
    Type       string            `json:"type"`         // dotted form: "vm.started", "image.uploaded"
    Source     string            `json:"source"`       // libvirt | app | system
    VMID       string            `json:"vm_id,omitempty"`
    ResourceID string            `json:"resource_id,omitempty"`
    Severity   string            `json:"severity"`     // info | warn | error
    Message    string            `json:"message"`
    Attributes map[string]string `json:"attributes,omitempty"`
    Actor      string            `json:"actor,omitempty"`
    OccurredAt time.Time         `json:"occurred_at"`
    CreatedAt  time.Time         `json:"created_at,omitempty"` // backward-compat mirror of OccurredAt
}
```

`EventSchemaVersion = 1` is exported so future webhook consumers can detect breaking changes without sniffing fields. The `Source` and `Severity` enum values are also exported as `EventSourceLibvirt`/`EventSeverityInfo` style constants.

**EventBus (`internal/events/bus.go`):**

A single goroutine serializes ID assignment, persistence, and fan-out. Producers call `EventBus.Publish(evt)` from any goroutine; consumers register via `Subscribe(name) (<-chan *Event, cancel)`.

- `publishCh` is a `256`-deep buffered channel. If a producer outruns the bus the publish is **dropped with a warning** rather than blocking (relevant: the libvirt callback goroutine must not stall on a slow store).
- Each subscriber gets a `64`-deep channel. If a consumer drains too slowly the bus drops events for that subscriber and rate-limits the warning to once per 60 s per subscriber name.
- `Subscribe` appends a monotonic counter to the caller-supplied name (`"sse-1.2.3.4#42"`) so two callers passing the same name (e.g. two SSE clients behind the same NAT IP) never collide.
- The bus stamps `OccurredAt` (and a backward-compat `CreatedAt` mirror) when the producer left them zero.
- Persistence happens **before** fan-out. If the store call returns an error the event is still fanned out with a `transient-<ts>` ID so live consumers see it, and a warning is logged.

Helpers `events.NewAppEvent(type, vmID, message, attrs)` and `events.NewSystemEvent(type, severity, message)` (plus `NewSystemEventWithAttrs`) hand-build events with the right defaults so handler code stays a single line.

**Persistence layout (`internal/store/bolt.go`, `events` bucket):**

- `Store.AppendEvent(evt)` calls bbolt's per-bucket `NextSequence` to assign a monotonic `uint64`, encodes it big-endian as the bucket key, and writes the event. Big-endian encoding makes the natural cursor walk produce events in chronological order.
- The legacy `Store.PutEvent` (string `evt-<unix-nano>` keys) is retained for backward compatibility with old data already on disk. Keys with `len != 8` are simply skipped during normal scans.
- `Store.ListEventsFiltered(filter)` walks the cursor newest → oldest, applies a server-side filter (`VMID`, `Type`, `Source`, `Severity`, `Since` — either RFC3339 timestamp or seq cursor — `UntilSeq`), and paginates. It returns the filtered slice plus a total-match count for `X-Total-Count`.
- `Store.ListEventsAfterSeq(afterSeq, limit)` walks chronologically forward from `afterSeq+1`. Used by the SSE replay path on reconnect.
- `Store.PruneEvents(maxRecords)` deletes the oldest events until the count is at or below `maxRecords`. Capped at 5 000 deletions per call so a backlog cannot stall the writer.
- `Store.PruneEventsByAge(maxAge)` walks forward and deletes everything older than `now-maxAge`, stopping at the first non-stale entry. Same 5 000-per-sweep cap.

**Retention loop (`internal/events/retention.go`):**

`Retention.Run(ctx)` ticks at `daemon.events.retention_interval` (default 60 s), running both the count-based and age-based sweeps. A non-positive interval, or both `max_records` and `max_age` non-positive, disables the loop. Each non-empty sweep emits a `system.events.retention_pruned` info event with deletion counts in attributes — the events stream is therefore self-describing about its own retention.

**REST API (`internal/api/handlers_events.go`):**

| Endpoint | Description |
|---|---|
| `GET /api/v1/events` | Filtered, paginated list. Query params: `vm_id`, `type`, `source`, `severity`, `since` (RFC3339 timestamp), `until` (uint64 seq cursor), `page`, `per_page`. Returns newest-first. Responds with `X-Total-Count`. |
| `GET /api/v1/events/stream` | SSE feed (see below). |

**SSE protocol (`GET /api/v1/events/stream`):**

- Frame format: standard SSE — `id: <seq>\nevent: <type>\ndata: <json>\n\n`. The `id` is the event's `uint64` sequence ID so the browser's `EventSource.lastEventId` (or a custom client's `Last-Event-ID` header) can resume after a reconnect.
- **Replay rules**: on connect the handler reads `Last-Event-ID` (header) or `?since=<seq>` (query param fallback for environments that strip the header — e.g. Authorization-replacing EventSource polyfills) and replays events with `seq > since` from the store. Replay is capped at **1 000 events**; if the gap is bigger, the handler responds **`410 Gone`** with code `event_stream_replay_window_exceeded`. The client should then fetch `GET /api/v1/events` paginated to catch up.
- **Live tail**: after replay the handler subscribes to the bus and forwards each event as a frame.
- **Heartbeat**: every 30 s the handler writes a `: keepalive` SSE comment to keep idle proxies from closing the connection.
- **Backpressure**: a slow client whose 64-deep channel fills up will start losing live events (logged once per minute), but its connection stays open and continues to receive heartbeats. Clients that care about gap-free delivery should reconnect with `Last-Event-ID` after any gap is detected.
- **Connection observability**: the active SSE consumer count is exposed on `GET /api/v1/host/stats` as `event_stream_connections`.

**CLI (`internal/cli/events.go`):**

- `vmsmith events list [--vm-id ...] [--type ...] [--source ...] [--severity ...] [--since ...] [--limit N]` — paginated REST query.
- `vmsmith events follow [--filter ...]` — opens the SSE stream and tails new events to stdout, with reconnect on idle errors. Equivalent to `tail -F` for the event log.

**Webhook contract (planned, see roadmap 4.2.15-17):**

The webhook subsystem is not yet implemented, but the wire shape is fixed by the same `types.Event` JSON above. When it lands, each delivery will:

- `POST` the JSON `Event` body to the configured target URL.
- Sign the request with `X-VMSmith-Signature: sha256=<hex>`, computed as `HMAC-SHA256(secret, body)`.
- Include `X-VMSmith-Event-Id`, `X-VMSmith-Event-Type`, and `X-VMSmith-Schema-Version: 1` headers so receivers can dedupe, route, and version-gate without parsing the body.
- Retry with exponential backoff and jitter (1 s / 5 s / 30 s / 2 m / 10 m). After the final failure the bus emits a `webhook.delivery_failed` system event so delivery health is itself observable on the events stream.
- Reject SSRF targets (loopback, link-local, 169.254.169.254, the VM NAT range) unless explicitly allowed via `daemon.webhooks.allowed_hosts`.

A 6-line bash example for verifying a webhook signature:

```bash
# Verify a webhook signature server-side.  $SECRET is the per-webhook secret
# configured at registration; $BODY is the raw request body; $SIG is the value
# of the X-VMSmith-Signature header (with the "sha256=" prefix stripped).
expected="$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex \
  | awk '{print $2}')"
[[ "$expected" == "$SIG" ]] || { echo "bad signature" >&2; exit 1; }
```

**Wiring into the daemon (`internal/daemon/daemon.go`):**

The bus is constructed against the bbolt store, started with `bus.Start()`, and stopped during shutdown. Long-lived consumers (the `VMStatePersister` for libvirt → store sync, the SSE handler) all subscribe through it. The startup sequence emits `daemon.started`; the shutdown handler publishes `daemon.shutdown` before closing the bus so the final frame is delivered to in-flight SSE clients.

**Configuration (`vmsmith.yaml.example`):**

```yaml
events:
  max_records: 50000        # cap on persisted events; 0 disables count-based pruning
  max_age_seconds: 2592000  # delete events older than this many seconds (default 30 days); 0 disables age-based pruning
  retention_interval: 60    # seconds between retention sweeps; 0 disables the sweep entirely
```

---

### 6. Logging

VM Smith uses a structured logger (`internal/logger/logger.go`) that writes to both a file and an in-memory ring buffer. The ring buffer is drained by `GET /api/v1/logs` to power the web GUI log viewer.

**Logger design:**

```
┌─────────────────────────────────────────────┐
│  internal/logger (singleton)                │
│                                             │
│  Init(logFile, minLevel)                    │
│    └─ opens file, creates dirs              │
│    └─ installs global logger                │
│                                             │
│  Entry { Timestamp, Level, Source,          │
│          Message, Fields map[string]string } │
│                                             │
│  Ring buffer (2000 entries, FIFO)           │
│  ← Debug / Info / Warn / Error helpers      │
│                                             │
│  Entries(level, since, limit) []Entry       │
│    └─ filtered view for GUI polling         │
└─────────────────────────────────────────────┘
```

**Sources used across the codebase:**

| Source   | Where                                              |
|----------|----------------------------------------------------|
| `daemon` | `internal/daemon/daemon.go` — startup, shutdown, errors |
| `api`    | `internal/api/middleware.go` — every HTTP request  |
| `cli`    | `internal/cli/*.go` — every CLI command invocation |

**Log levels:** `debug` < `info` < `warn` < `error`

**Initialization:**
- **Daemon:** `logger.Init(cfg.Daemon.LogFile, logger.LevelInfo)` called at daemon start
- **CLI commands:** `logger.Init(cfg.Daemon.LogFile, logger.LevelInfo)` called via `PersistentPreRunE` on the root Cobra command, so every subcommand writes to the same log file

**HTTP request logging (middleware):**
- Captures method, path, status code, duration, response size, remote addr, query string
- POST/PUT body snippets (up to 4096 bytes) are buffered and re-injected into `r.Body`
- `GET /api/v1/logs` requests are **skipped** to prevent polling self-noise
- Log level: `error` for 5xx, `warn` for 4xx, `info` for all others

**File format:**
```
2026-01-01T12:00:00.000000000Z [INFO] [api] GET /api/v1/vms status_code=200 duration_ms=1
2026-01-01T12:00:01.000000000Z [WARN] [daemon] port forward restore skipped error=iptables not available
```

---

### 7. Daemon Mode

`vmsmith daemon start` (`internal/daemon/daemon.go`):

1. Opens and holds a libvirt connection (`qemu:///system`)
2. Calls `EnsureNetwork()` to set up the NAT network
3. Calls `portforward.RestoreAll()` to re-apply iptables rules from bbolt
4. Starts the Chi HTTP server on the configured port
5. Handles `SIGTERM`/`SIGINT` for clean shutdown (network teardown, libvirt disconnect)

---

### 8. REST API

Most endpoints are prefixed `/api/v1/`. The Prometheus scrape endpoint is served separately at `/metrics`.

| Method | Path                                     | Description                   |
|--------|------------------------------------------|-------------------------------|
| GET    | /vms                                     | List all VMs (`?tag=`, `?status=`, `?page=`, `?per_page=`) |
| POST   | /vms                                     | Create a new VM (accepts `template_id` for default merge) |
| GET    | /vms/{id}                                | Get VM details                |
| PATCH  | /vms/{id}                                | Update VM resources (CPU/RAM/disk/IP/tags) |
| GET    | /vms/{id}/stats                          | Latest VM metrics snapshot + bounded history (`?since=`, `?fields=cpu,mem,disk,net`) |
| POST   | /vms/{id}/clone                          | Clone a VM (copies disk, generates new MAC + IP reservation) |
| POST   | /vms/bulk                                | Bulk start/stop/delete across multiple VM IDs |
| POST   | /vms/{id}/start                          | Start a stopped VM            |
| POST   | /vms/{id}/stop                           | Stop a running VM             |
| DELETE | /vms/{id}                                | Delete a VM                   |
| GET    | /vms/{id}/snapshots                      | List snapshots                |
| POST   | /vms/{id}/snapshots                      | Create snapshot               |
| POST   | /vms/{id}/snapshots/{name}/restore       | Restore snapshot              |
| DELETE | /vms/{id}/snapshots/{name}               | Delete snapshot               |
| GET    | /images                                  | List images (`?page=`, `?per_page=`) |
| POST   | /images                                  | Create image from VM disk     |
| POST   | /images/upload                           | Upload qcow2 file             |
| DELETE | /images/{id}                             | Delete image                  |
| GET    | /images/{id}/download                    | Download image file           |
| GET    | /templates                               | List VM templates             |
| POST   | /templates                               | Create a VM template          |
| DELETE | /templates/{id}                          | Delete a VM template          |
| GET    | /vms/{id}/ports                          | List port forwards            |
| POST   | /vms/{id}/ports                          | Add port forward              |
| DELETE | /vms/{id}/ports/{portId}                 | Remove port forward           |
| GET    | /host/interfaces                         | List host network interfaces  |
| GET    | /host/stats                              | Host CPU/RAM/disk usage and VM count |
| GET    | /quotas/usage                            | Current quota allocation vs configured limits |
| GET    | /events                                  | Query lifecycle/audit events (`?vm_id=`, `?type=`, `?source=`, `?severity=`, `?since=`, `?until=`, pagination). See Section 5 for the full event schema and SSE protocol |
| GET    | /events/stream                           | SSE feed of new events with `Last-Event-ID` replay (see Section 5) |
| GET    | /logs                                    | Query structured log entries  |
| GET    | /metrics                                 | Prometheus scrape endpoint for latest per-VM gauges (served outside `/api/v1`) |

The `/logs` endpoint supports query parameters: `level` (min level: debug/info/warn/error), `limit` (max entries, capped at 2000), `since` (RFC3339Nano timestamp), `source` (daemon/api/cli).

The `/events` REST endpoint returns events from the `events` bucket in reverse-chronological order with server-side filtering and `X-Total-Count` pagination. The companion SSE feed at `/events/stream` ships replay-on-reconnect via `Last-Event-ID`, 30-second heartbeats, a 1 000-event replay window (older clients are sent `410 Gone` and should re-paginate via `/events`), and an in-flight consumer count surfaced through `host/stats`. See **Section 5 (Event System)** for the full bus, persistence, retention, SSE protocol, and webhook contract.

The `/metrics` endpoint emits Prometheus text-format gauges for the latest in-memory VM metrics. Each series is labeled with `vm_id` and `vm_name`; unavailable fields are omitted rather than emitted as zero.

---

### 9. Web GUI

The React SPA is embedded into the binary via `go:embed dist/*`. The same port serves both the API and the GUI.

**Pages:**

| Route     | Features                                                               |
|-----------|------------------------------------------------------------------------|
| `/`       | Dashboard — VM count, state breakdown, quick actions                   |
| `/vms`    | VM list; Create modal (Basic / Advanced tabs); Edit modal              |
| `/vms/:id`| VM detail — info cards (incl. SSH connection string), Edit button, extra network display, snapshots, port forwards |
| `/images` | Upload (drag-and-drop), list, download, delete qcow2 images            |
| `/logs`   | Log viewer — level/source filters, auto-scroll, pause/resume, 3s polling |

**Create VM modal (Basic / Advanced tabs):**

The modal is split into two tabs to keep the default experience simple:

- **Basic tab** — mandatory fields only: name, base image, vCPU count, RAM (MB), disk (GB) with defaults.
- **Advanced tab** — optional customisations: SSH public key, default SSH user (blank = root), primary NAT static IP + gateway, extra network interfaces. A badge on the tab header shows how many advanced fields are set.

**Extra network attachments (Advanced tab):**
- Fetches physical host interfaces from `GET /api/v1/host/interfaces` for macvtap interface selection
- Per-attachment: mode (macvtap/bridge), interface dropdown or text input
- Static IP + gateway shown by default; a "Use DHCP" checkbox hides these fields and sends no static-IP to the API

**Edit VM modal (`/vms/:id`):**
- Opened via the **Edit** button in the VM detail header
- Fields: vCPU count, RAM (MB), disk (GB), NAT IP address — all pre-filled with current values
- Shows "current: X" hint below each field; disk field enforces `min = current disk` (grow-only)
- IP field accepts a plain IP (`192.168.100.50`) or CIDR (`192.168.100.50/24`); `/24` is appended automatically if omitted
- On submit: API sends `PATCH /api/v1/vms/{id}` with only changed fields; the backend stops the VM, applies changes, then restarts it
- IP change takes effect on restart: the new instance-id in the regenerated cloud-init ISO causes cloud-init to re-run and overwrite the NM keyfile with the new static address

**VM Detail network display:**
- Shows attached networks (eth1…) with mode, host interface, and IP or DHCP label

**Development:**

```bash
make dev-api   # Go daemon on :8080
make dev-web   # Vite dev server on :3000, proxies /api/* → :8080
```

---

### 10. Configuration

File: `~/.vmsmith/config.yaml` or `/etc/vmsmith/config.yaml`

```yaml
daemon:
  listen: "0.0.0.0:8080"
  pid_file: "/var/run/vmsmith.pid"
  log_file: "~/.vmsmith/vmsmith.log"   # structured log output; leave empty to disable file logging

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
  cpus:     2
  ram_mb:   2048
  disk_gb:  20
  ssh_user: ubuntu   # retained for config compatibility; VMs now use root by default — set default_user in VMSpec to override per-VM

quotas:
  max_vms: 0
  max_total_cpus: 0
  max_total_ram_mb: 0
  max_total_disk_gb: 0

metrics:
  enabled: true
  sample_interval: 10
  history_size: 360
  scrape_listen: ""      # optional separate listener for GET /metrics
```

---

### 11. Data Model (bbolt)

| Bucket         | Key                        | Value                        |
|----------------|----------------------------|------------------------------|
| `vms`          | VM ID                      | JSON `types.VM`              |
| `images`       | image ID                   | JSON `types.Image`           |
| `snapshots`    | `{vmID}/{name}`            | JSON `types.Snapshot`        |
| `templates`    | template ID                | JSON `types.VMTemplate`      |
| `port_forwards`| `{vmID}/{hostPort}`        | JSON `types.PortForward`     |
| `events`       | big-endian `uint64` seq    | JSON `types.Event` (lifecycle / audit log; see Section 5 for the event bus, retention, and SSE protocol) |
| `config`       | config key                 | JSON daemon-managed runtime state |

---

## Testing Strategy

VM Smith uses a three-tier approach. No real libvirt or QEMU is needed for any test.

### Test Structure

```
internal/
├── logger/logger_test.go        # Ring buffer, levels, Init/Close, concurrent writes
├── store/bolt_test.go           # bbolt CRUD: VMs, images, port forwards, persistence
├── config/config_test.go        # Config loading, defaults, YAML merge, EnsureDirs, log_file
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
└── api/api_test.go              # All REST endpoints + /logs endpoint via httptest + MockManager
tests/web/
├── mock-server.js               # Node.js mock API server for browser tests (incl. /logs)
├── gui.spec.js                  # Playwright test specs (incl. Log Viewer)
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
| Unit        | logger        | 20    | Ring buffer, levels, Init/Close, file format, concurrency |
| Unit        | store         | 9     | CRUD for VMs, images, ports; persistence              |
| Unit        | config        | 5     | Defaults, file load, YAML merge, invalid YAML, log_file |
| Unit        | vm/domain     | 18    | XML gen, multi-net, macvtap/bridge/NAT, MAC           |
| Unit        | vm/mock       | 8     | Mock lifecycle, snapshots, error injection            |
| Unit        | cli           | 17    | Network flag parsing, all modes, humanSize            |
| Unit        | network       | 10    | Interface discovery, portforward rule construction    |
| Unit        | storage       | 11    | Import, list, get, delete, path handling              |
| Integration | api           | 35    | All REST endpoints incl. upload, /logs; error paths   |
| E2E         | web           | 56    | Dashboard, VM CRUD, snapshots, images, navigation, Log Viewer |
| **Total**   |               | **189** |                                                     |

---

## Future Enhancements

Active feature work — including additional VM-level metrics polish, the event-bus webhook subsystem, browser-based VNC + serial console, and the cron-style scheduler — is tracked in [`ROADMAP.md`](ROADMAP.md) (Phases 4.1, 4.2, 5.1, 5.2). VM templates and the in-process event bus / SSE feed have shipped (see Section 5 above and `/api/v1/events`, `/api/v1/events/stream`).

Longer-horizon items not yet promoted to the roadmap:

- **KubeVirt backend** — `KubeVirtVMManager` behind the same `vm.Manager` interface
- **Second NAT networks** — create per-VM isolated private subnets
- **OCI image support** — pull cloud images from container registries
- **Cluster mode** — multiple VM Smith hosts with shared image catalog (early sketch in roadmap Phase 5.5)
