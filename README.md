# VM Smith

<img align="right" width="43%" alt="vmsmith" src="https://github.com/user-attachments/assets/10b7ffb5-ebd9-47f9-bcf2-9b5212a55492" />

CLI tool, HTTP REST server, and embedded web GUI for provisioning and managing QEMU/KVM virtual machines on Linux.

- **VM lifecycle** — create, start, stop, delete with a single command
- **Cloud-init** — inject SSH keys and custom config at first boot
- **Snapshots** — capture and restore VM state at any point
- **Portable images** — upload, export, download, and share qcow2 images
- **NAT networking + port forwarding** — expose VM services to your network
- **Multi-network** — attach VMs to additional host interfaces (macvtap/bridge)
- **REST API + Web GUI** — React dashboard embedded in the binary
- **Zero external dependencies** — embedded bbolt database, no PostgreSQL/Redis/etc.

---

## Quick Start

### 1. Install dependencies

```bash
# Ubuntu / Debian
sudo bash scripts/install-deps-ubuntu.sh

# Rocky Linux / RHEL
sudo bash scripts/install-deps-rocky.sh
```

Reload your PATH if Go was freshly installed:

```bash
source /etc/profile.d/go.sh
```

### 2. Build

```bash
make deps     # download Go modules
make build    # build frontend + backend → single binary at ./bin/vmsmith
```

### 3. Start the daemon

```bash
sudo ./bin/vmsmith daemon start --port 8080
```

Open **http://localhost:8080** for the web GUI, or use the CLI alongside.

---

## End-to-End User Guide

### Step 1 — Get a base image

VM Smith boots VMs from `.qcow2` cloud images. You need at least one before creating VMs.

**Option A — download directly to the image store:**

```bash
sudo wget -O /var/lib/vmsmith/images/ubuntu-22.04.qcow2 \
  https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img
```

**Option B — upload via the GUI:**

Open **Images → Upload Image**, drag-and-drop a `.qcow2` file, give it a name.

**Option C — use an absolute path inline:**

```bash
sudo ./bin/vmsmith vm create my-server \
  --image /path/to/ubuntu-22.04.qcow2 ...
```

---

### Step 2 — Create a VM

```bash
# Minimal — defaults: 2 vCPU, 2 GB RAM, 20 GB disk
sudo ./bin/vmsmith vm create web01 --image ubuntu-22.04

# With resources and SSH key
sudo ./bin/vmsmith vm create web01 \
  --image ubuntu-22.04 \
  --cpus 4 \
  --ram 4096 \
  --disk 40 \
  --ssh-key "$(cat ~/.ssh/id_rsa.pub)"

# With a named sudo user instead of root (disables root login)
sudo ./bin/vmsmith vm create rocky01 \
  --image rocky-9 \
  --default-user alice \
  --ssh-key "$(cat ~/.ssh/id_rsa.pub)"

# List VMs
sudo ./bin/vmsmith vm list
```

Output:
```
ID                     NAME    STATE    IP               CPUS  RAM (MB)
vm-1741234567890123    web01   running  192.168.100.10   4     4096
```

The VM boots immediately and gets a DHCP address on the `192.168.100.0/24` NAT network.

---

### Step 3 — Access the VM

**SSH via port forward (recommended):**

```bash
# Forward host port 2222 → VM port 22
sudo ./bin/vmsmith port add vm-1741234567890123 --host 2222 --guest 22

# SSH from any machine on the network
ssh -p 2222 root@<host-machine-ip>
```

**Direct SSH (from the host only):**

```bash
ssh root@192.168.100.10
```

VMs use `root` by default — the SSH key you provide is injected into root's `authorized_keys`. Pass `--default-user <name>` to create a named sudo user and disable root instead. The SSH connection string is shown in `vm info` and the web GUI VM detail page.

---

### Step 4 — Manage VM lifecycle

```bash
# Stop a VM (graceful shutdown)
sudo ./bin/vmsmith vm stop web01

# Start it again
sudo ./bin/vmsmith vm start web01

# Delete permanently (removes disk and metadata)
sudo ./bin/vmsmith vm delete web01
```

### Step 4b — Edit VM resources and IP

Increase vCPU count, RAM, disk size, or change the primary NAT IP address. The VM is powered off automatically, the changes are applied, and it is powered back on.

```bash
# Scale up to 8 vCPUs and 16 GB RAM
sudo ./bin/vmsmith vm edit web01 --cpus 8 --ram 16384

# Grow the disk (can only grow, not shrink)
sudo ./bin/vmsmith vm edit web01 --disk 80

# Change the primary NAT IP
sudo ./bin/vmsmith vm edit web01 --nat-ip 192.168.100.50/24

# Combine resource and IP changes in one call
sudo ./bin/vmsmith vm edit web01 --cpus 4 --ram 8192 --disk 60 --nat-ip 192.168.100.50/24
```

Output:
```
VM updated successfully:
  ID:    vm-1741234567890123
  Name:  web01
  State: running
  CPUs:  4
  RAM:   8192 MB
  Disk:  60 GB
  IP:    192.168.100.50
```

You can also edit resources and IP from the **VM detail page** in the web GUI — click the **Edit** button next to start/stop.

> **Note — Disk:** resize only grows the virtual device; the guest OS filesystem still needs expanding manually (`growpart` + `resize2fs` / `xfs_growfs`).
>
> **Note — IP:** changing the IP updates the DHCP reservation and regenerates the cloud-init config with a new instance-id. cloud-init re-runs on the next boot (which Update triggers automatically) and overwrites the NM keyfile with the new static address.

---

### Step 5 — Snapshots

Take a point-in-time snapshot before a risky change, restore if something goes wrong.

```bash
# Create snapshot
sudo ./bin/vmsmith snapshot create vm-1741234567890123 --name before-update

# List snapshots
sudo ./bin/vmsmith snapshot list vm-1741234567890123

# Restore to snapshot
sudo ./bin/vmsmith snapshot restore vm-1741234567890123 --name before-update

# Delete a snapshot
sudo ./bin/vmsmith snapshot delete vm-1741234567890123 --name before-update
```

Snapshots are also manageable from the VM detail page in the GUI.

---

### Step 6 — Port Forwarding

```bash
# Expose a web server on port 80
sudo ./bin/vmsmith port add <vm-id> --host 8080 --guest 80

# Expose a database on port 5432
sudo ./bin/vmsmith port add <vm-id> --host 5432 --guest 5432 --proto tcp

# List all port forwards for a VM
sudo ./bin/vmsmith port list <vm-id>

# Remove a port forward
sudo ./bin/vmsmith port remove <vm-id> --host 8080
```

Port forward rules are persisted in the embedded database and restored automatically when the daemon restarts.

---

### Step 7 — Multiple Networks

Each VM always gets **eth0** on the `vmsmith-net` NAT network (`192.168.100.0/24`). You can attach additional host interfaces as **eth1, eth2, …** using `--network`.

**List available host interfaces:**

```bash
sudo ./bin/vmsmith net interfaces
```

**Attach a second network interface (DHCP from that network):**

```bash
sudo ./bin/vmsmith vm create db01 \
  --image ubuntu-22.04 \
  --network eth1
```

**Multiple networks with static IPs:**

```bash
sudo ./bin/vmsmith vm create db01 \
  --image ubuntu-22.04 \
  --network eth1:ip=192.168.1.100/24,gw=192.168.1.1,name=data \
  --network eth2:ip=192.168.2.100/24,name=storage \
  --network eth3
```

**Bridge mode** (for full host↔VM communication on the same NIC):

```bash
sudo ./bin/vmsmith vm create db01 \
  --image ubuntu-22.04 \
  --network eth1:mode=bridge,bridge=br-data
```

Extra interfaces are configured automatically by cloud-init on first boot (DHCP or static). The GUI's Create Machine modal also exposes this as "Extra Networks" with an interface dropdown.

---

### Step 8 — Images (portable disk images)

Export a configured VM as a reusable base image:

```bash
# Export VM disk as a standalone qcow2 image
sudo ./bin/vmsmith image create <vm-id> --name ubuntu-configured

# List images
sudo ./bin/vmsmith image list

# Delete an image
sudo ./bin/vmsmith image delete ubuntu-configured
```

Images are stored in `/var/lib/vmsmith/images/` and can be used as the `--image` argument for new VMs or downloaded via the GUI.

**Transfer to another host:**

```bash
# Push to a remote host running VM Smith
sudo ./bin/vmsmith image push ubuntu-configured user@other-host

# Pull from a remote host
sudo ./bin/vmsmith image pull user@other-host/ubuntu-configured

# Download via HTTP (when daemon is running on the remote)
sudo ./bin/vmsmith image pull http://other-host:8080/api/v1/images/<id>/download
```

---

### Step 9 — Web GUI

Open **http://localhost:8080** after starting the daemon.

GUI: VM Dashboard

<img width="1641" height="922" alt="Screenshot from 2026-03-07 00-05-00" src="https://github.com/user-attachments/assets/76ca8e97-0b90-4a2f-aebf-25677310f4af" />

GUI: Machine Management

<img width="1641" height="922" alt="Screenshot from 2026-03-07 00-02-23" src="https://github.com/user-attachments/assets/56357819-f9bb-4a1b-b24b-d4ecb1a40d5d" />

GUI: VM Image Management

<img width="1641" height="922" alt="Screenshot from 2026-03-07 00-05-22" src="https://github.com/user-attachments/assets/12b5b86e-85dc-4839-95ab-35402284431c" />

**Pages:**
| Route | What you can do |
|---|---|
| `/` | Dashboard — VM count, state overview, quick actions |
| `/vms` | List VMs; Create modal (Basic / Advanced tabs) |
| `/vms/:id` | VM detail — edit resources, snapshots, port forwards, attached networks |
| `/images` | Upload, list, download, delete portable qcow2 images |

**Create Machine modal (Basic / Advanced tabs):**
- **Basic** — name, image, vCPU, RAM, disk; everything you need to launch a VM immediately
- **Advanced** — SSH key, default user, primary NAT static IP, extra network interfaces (static IP by default; "Use DHCP" checkbox to opt out)

---

## REST API

The daemon exposes a full REST API at `/api/v1/`. Example:

```bash
# Start daemon
sudo ./bin/vmsmith daemon start --port 8080

# List VMs
curl http://localhost:8080/api/v1/vms

# Create a VM
curl -X POST http://localhost:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "web01",
    "image": "ubuntu-22.04",
    "cpus": 2,
    "ram_mb": 2048,
    "disk_gb": 20,
    "ssh_pub_key": "ssh-rsa AAAA...",
    "default_user": "",
    "networks": [
      {"host_interface": "eth1", "mode": "macvtap"}
    ]
  }'

# Get VM
curl http://localhost:8080/api/v1/vms/<id>

# Update VM resources (stops VM, applies, restarts)
curl -X PATCH http://localhost:8080/api/v1/vms/<id> \
  -H 'Content-Type: application/json' \
  -d '{"cpus": 4, "ram_mb": 8192, "disk_gb": 60}'

# Port forward
curl -X POST http://localhost:8080/api/v1/vms/<id>/ports \
  -H 'Content-Type: application/json' \
  -d '{"host_port": 2222, "guest_port": 22, "protocol": "tcp"}'
```

Full API reference: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#5-rest-api)

---

## CLI Reference

```
vmsmith vm create <name>   --image <name|path> [--cpus N] [--ram MB] [--disk GB]
                           [--ssh-key "ssh-rsa ..."] [--default-user <user>]
                           [--cloud-init <file>]
                           [--network <iface[:key=val,...]>]...
vmsmith vm edit   <id>     [--cpus N] [--ram MB] [--disk GB] [--nat-ip CIDR]
vmsmith vm list
vmsmith vm start  <id>
vmsmith vm stop   <id>
vmsmith vm delete <id>

vmsmith snapshot create  <vm-id> --name <name>
vmsmith snapshot restore <vm-id> --name <name>
vmsmith snapshot list    <vm-id>
vmsmith snapshot delete  <vm-id> --name <name>

vmsmith image list
vmsmith image create <vm-id> --name <name>
vmsmith image delete <name>
vmsmith image push   <name> <user@host>
vmsmith image pull   <user@host>/<name>

vmsmith port add    <vm-id> --host <port> --guest <port> [--proto tcp|udp]
vmsmith port remove <vm-id> --host <port>
vmsmith port list   <vm-id>

vmsmith net interfaces [--all]

vmsmith daemon start [--port 8080] [--config ~/.vmsmith/config.yaml]
```

---

## Configuration

Copy `vmsmith.yaml.example` to `~/.vmsmith/config.yaml`:

```yaml
daemon:
  listen: "0.0.0.0:8080"
  tls:
    cert_file: ""   # optional; set both cert_file + key_file to serve HTTPS
    key_file: ""
  max_request_body_bytes: 52428800
  max_upload_body_bytes: 53687091200

libvirt:
  uri: "qemu:///system"

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
  ssh_user: ubuntu   # retained for config compatibility; VMs use root by default (override per-VM with --default-user)
```

---

## Testing

```bash
make test             # all Go tests (unit + integration)
make test-unit        # unit tests only
make test-integration # API integration tests only
make test-web-deps    # install Playwright (run once)
make test-web         # headless browser E2E tests
make test-all         # everything
```

Tests span unit, API integration, and Playwright E2E tiers — no real libvirt or QEMU needed for any of them. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#testing-strategy) for details.

---

## Troubleshooting

### Permission denied accessing VM disk

```
Error: creating VM: starting domain: virError(Code=38, Message='Cannot access storage file ... Permission denied')
```

The `libvirt-qemu` user cannot read home directories (mode `750`). VM Smith stores disks in `/var/lib/vmsmith/` to avoid this. If you set up manually:

```bash
sudo mkdir -p /var/lib/vmsmith/vms /var/lib/vmsmith/images
sudo chown -R "$(whoami):$(whoami)" /var/lib/vmsmith
sudo chmod -R 755 /var/lib/vmsmith
```

### dnsmasq "Address already in use" on daemon restart

```
Error: ensuring network: starting network: dnsmasq: failed to create listening socket for 192.168.100.1
```

A previous daemon run left an orphaned dnsmasq process. VM Smith automatically kills it via the libvirt PID file on startup. If it still happens, force-clean manually:

```bash
sudo kill $(cat /run/libvirt/network/vmsmith-net.pid 2>/dev/null) 2>/dev/null || true
sudo ./bin/vmsmith daemon start --port 8080
```

### Extra network interfaces have no IP after boot

Make sure you are running build `ed9ee2b` or later. Earlier builds only generated cloud-init network-config when static IPs were specified; the fix ensures DHCP interfaces are also configured.

### Network not found: vmsmith-net

```
Error: creating VM: ensuring NAT network: Network not found
```

The NAT network is created automatically on first `vm create` or daemon start. If it was manually deleted from libvirt, restart the daemon and it will be recreated.

---

## Development

```bash
# Run backend + frontend together
make dev

# Enable the repo's versioned pre-commit hook
make install-githooks

# Or run them separately
# Terminal 1: Go backend on :8080
make dev-api

# Terminal 2: React frontend on :3000 (proxies /api → :8080)
make dev-web
```

`make dev` starts both processes together and cleans them up on Ctrl-C. `make install-githooks` configures Git to use the repository's `.githooks/pre-commit` hook, which runs `make fmt && make lint` before each commit.

Contributor setup, test expectations, and PR conventions live in [CONTRIBUTING.md](CONTRIBUTING.md).

For scriptable API workflows, see the bash and Python examples in [examples/](examples/README.md).
See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for design details.

---

## Requirements

- Linux x86_64 (Ubuntu 22.04+ or Rocky Linux 8+)
- QEMU/KVM with hardware virtualization (`/dev/kvm` must exist)
- libvirt (`libvirtd` running)
- Go 1.22+ and Node.js 22+ (for building from source)

## License

MIT
