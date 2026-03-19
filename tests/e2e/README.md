# VMSmith E2E Tests

Real end-to-end tests that exercise the full stack: CLI, REST API, and web GUI
against a running vmsmith daemon with actual QEMU/KVM virtual machines.

## Prerequisites

1. **Running vmsmith daemon** on `localhost:8080` (or set `VMSMITH_API`/`VMSMITH_GUI_URL`)
2. **Rocky Linux qcow2 image** accessible to the daemon
3. **SSH key pair** for VM access
4. **libvirt + QEMU/KVM** configured on the host

## Setup

```bash
# Install Python test dependencies
make test-e2e-deps

# Or manually:
pip install -r tests/e2e/requirements.txt
npx playwright install chromium
```

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `VMSMITH_ROCKY_IMAGE` | Yes | — | Path to Rocky Linux qcow2 image |
| `VMSMITH_SSH_PRIVATE_KEY` | No | `~/.ssh/id_rsa` | SSH private key for VM access |
| `VMSMITH_SSH_USER` | No | `rocky` | SSH username for the Rocky image |
| `VMSMITH_BIN` | No | `vmsmith` | Path to vmsmith binary |
| `VMSMITH_API` | No | `http://localhost:8080` | Daemon API URL |
| `VMSMITH_GUI_URL` | No | `http://localhost:8080` | GUI URL for Playwright tests |
| `VMSMITH_HOST_IFACE` | No* | — | Host interface for multi-NIC tests |
| `VMSMITH_HOST_IFACE2` | No* | — | Second host interface for dual-NIC tests |
| `VMSMITH_IP_TIMEOUT` | No | `120` | Seconds to wait for VM IP |
| `VMSMITH_SSH_TIMEOUT` | No | `180` | Seconds to wait for SSH readiness |
| `VMSMITH_SSH_PUBKEY` | No | — | SSH public key content (for GUI tests) |

\* Required only for networking tests (test 3). Tests are skipped if not set.

## Running Tests

```bash
# All E2E tests
make test-e2e

# CLI tests only
make test-e2e-cli

# API tests only
make test-e2e-api

# GUI tests only
make test-e2e-gui

# Only networking tests
make test-e2e-networking

# Only port forwarding tests
make test-e2e-portforward

# Run specific test file
cd tests/e2e && python -m pytest test_cli_vm_lifecycle.py -v

# Run specific test class
cd tests/e2e && python -m pytest test_api_networking.py::TestAPIPortForward -v
```

## Test Coverage

### Test 1: VM Lifecycle (CLI + API + GUI)
- Create a VM from Rocky qcow2 image
- Verify it gets a management IP on the NAT network
- Verify the IP is reachable (ping)
- Verify SSH access works
- Verify `vm list` / `GET /vms` includes the VM

### Test 2: Snapshots & Images (CLI + API + GUI)
- Create a snapshot of a running VM
- Make changes inside the VM (create a marker file)
- Restore from snapshot, verify changes are reverted
- Export VM as a reusable image
- Create a new VM from the exported image
- Verify the new VM boots and is SSH-accessible

### Test 3: Multi-NIC Networking (CLI + API)
- Create VMs with extra macvtap interfaces
- Verify extra interfaces get IPs (DHCP)
- Test inter-VM connectivity on the extra network (ping)
- Create VM with dual extra interfaces, verify all get IPs

### Test 4: Port Forwarding (CLI + API + GUI)
- Create a VM, add a port forward (host:N → guest:22)
- SSH into the VM via the forwarded port on localhost
- Add multiple port forwards, list them, remove selectively
