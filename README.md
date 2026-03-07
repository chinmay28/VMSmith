# VM Smith

<img align="right" width="43%" alt="vmsmith" src="https://github.com/user-attachments/assets/10b7ffb5-ebd9-47f9-bcf2-9b5212a55492" />

CLI tool, a HTTP REST server, and a handy GUI for provisioning and managing QEMU/KVM virtual machines on Linux.

Features

- **VM lifecycle management** — create, start, stop, delete VMs with a single command
- **Cloud-init support** — inject SSH keys and configuration at boot
- **Snapshots** — capture and restore VM state at any point
- **Portable images** — export VMs to standalone qcow2 images and distribute via SCP or HTTP
- **NAT networking with port forwarding** — expose VM services to the local network
- **REST API** — run as a daemon for programmatic access
- **Web GUI** — React dashboard embedded in the binary, served alongside the API
- **Embedded metadata store** — zero-config bbolt database, no external DB required

## Quick Start

### 1. Install dependencies

The install scripts set up all required system packages, Go 1.22+, and Node.js 18+:

```bash
# Ubuntu
sudo bash scripts/install-deps-ubuntu.sh

# Rocky Linux
sudo bash scripts/install-deps-rocky.sh
```

After the script finishes, reload your PATH if Go was freshly installed:

```bash
source /etc/profile.d/go.sh
```

### 2. Download Go modules

```bash
make deps
```

This runs `go mod tidy` and `go mod download` to fetch all Go dependencies. The
`go.sum` lockfile is generated here — **this step is required before building**.

### 3. Build

```bash
# Full build: frontend + backend → single binary with embedded GUI
make build

# Backend only (if frontend already built)
make build-go
```

### 4. Download a base image

```bash
# Example: Ubuntu 22.04 cloud image
wget -O /var/lib/vmsmith/images/ubuntu-22.04.qcow2 \
  https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img
```

### 5. Create and manage VMs

```bash
# Create a VM
vmsmith vm create my-server --image ubuntu-22.04.qcow2 --cpus 2 --ram 2048 --disk 20 \
  --ssh-key "$(cat ~/.ssh/id_rsa.pub)"

# List VMs
vmsmith vm list

# Forward SSH port
vmsmith port add <vm-id> --host 2222 --guest 22

# SSH into the VM from any host on the network
ssh -p 2222 ubuntu@<host-ip>

# Take a snapshot
vmsmith snapshot create <vm-id> --name before-update

# Create a portable image
vmsmith image create <vm-id> --name my-golden-image

# Push image to another host
vmsmith image push my-golden-image user@other-host
```

### Run as a daemon

```bash
# Start the REST API server + Web GUI
vmsmith daemon start --port 8080

# Open the web GUI
open http://localhost:8080

# Use the API
curl http://localhost:8080/api/v1/vms
```

### Development (frontend)

```bash
# Terminal 1: Go backend
make dev-api

# Terminal 2: React frontend with hot reload
make dev-web
# Open http://localhost:3000
```

## Configuration

Copy `vmsmith.yaml.example` to `~/.vmsmith/config.yaml` and adjust as needed.

## Testing

```bash
# Run all Go tests (unit + integration)
make test

# Set up Playwright for E2E tests (run once after cloning)
make test-web-deps

# Run web GUI E2E tests (headless Chromium via Playwright)
make test-web

# Run everything
make test-all
```

The test suite includes 83 tests across three tiers: unit tests for each package, API integration tests using a mock VM manager + httptest, and end-to-end headless browser tests using Playwright against a mock API server. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#testing-strategy) for details.

## Troubleshooting

### Permission denied accessing VM disk

```
Error: creating VM: starting domain: virError(Code=38, Message='Cannot access storage file ... Permission denied')
```

The `libvirt-qemu` system user cannot traverse home directories (default mode `750`). VM Smith stores VM disks in `/var/lib/vmsmith/` to avoid this — the install scripts create that directory with the correct ownership. If you set up dependencies manually, create it yourself:

```bash
sudo mkdir -p /var/lib/vmsmith/vms /var/lib/vmsmith/images
sudo chown -R "$(whoami):$(whoami)" /var/lib/vmsmith
sudo chmod -R 755 /var/lib/vmsmith
```

### Network not found: vmsmith-net

```
Error: creating VM: ensuring NAT network: Network not found
```

The NAT network is created automatically on first `vm create`. If it was manually deleted, simply run `vm create` again to recreate it.

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for detailed design documentation.

## Requirements

- Linux x86_64 (Ubuntu 22.04+ or Rocky Linux 8+)
- QEMU/KVM with hardware virtualization support
- libvirt
- Go 1.22+ (for building from source)
- Node.js 18+ and npm (for building the web GUI)

## License

MIT License
