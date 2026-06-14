# VMSmith E2E Tests

Real end-to-end tests that exercise the full stack — CLI, REST API, and web GUI —
against a running vmsmith daemon with actual QEMU/KVM virtual machines.

## Prerequisites

1. **Running vmsmith daemon** on `localhost:8080` (or override with `--vmsmith-api`)
2. **Rocky Linux GenericCloud qcow2 image** accessible to the daemon — use the **GenericCloud** variant (not OCP-Base, which uses Ignition instead of cloud-init and won't work). The file must have a `.qcow2` extension. Download:
   ```bash
   wget -O /var/lib/vmsmith/images/rocky9.qcow2 \
     https://download.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base.latest.x86_64.qcow2
   ```
   Then pass `--rocky-image /var/lib/vmsmith/images/rocky9.qcow2` (or just `--image rocky9` to vmsmith).
3. **SSH key pair** for VM access
4. **libvirt + QEMU/KVM** configured on the host

## Setup

```bash
# Install Python + Playwright test dependencies
make test-e2e-deps

# Or manually:
pip install -r tests/e2e/requirements.txt
npx playwright install chromium
```

## Configuration

Every setting can be provided as a **pytest CLI option**, an **environment variable**, or both.
Resolution order: CLI option → env var → built-in default.

| CLI option | Env variable | Default | Description |
|---|---|---|---|
| `--rocky-image PATH` | `VMSMITH_ROCKY_IMAGE` | *(required)* | Path to Rocky Linux qcow2 image |
| `--ssh-key PATH` | `VMSMITH_SSH_PRIVATE_KEY` | `~/.ssh/id_rsa` | SSH private key for VM access |
| `--ssh-user USER` | `VMSMITH_SSH_USER` | `root` | SSH username for VM access (VMs use root by default) |
| `--vmsmith-bin PATH` | `VMSMITH_BIN` | `vmsmith` | Path to vmsmith binary |
| `--vmsmith-api URL` | `VMSMITH_API` | `http://localhost:8080` | Daemon API base URL |
| `--host-iface NAME` | `VMSMITH_HOST_IFACE` | — | Host interface for multi-NIC tests |
| `--host-iface2 NAME` | `VMSMITH_HOST_IFACE2` | — | Second host interface for dual-NIC tests |
| `--ip-timeout SECS` | `VMSMITH_IP_TIMEOUT` | `120` | Seconds to wait for VM IP assignment |
| `--ssh-timeout SECS` | `VMSMITH_SSH_TIMEOUT` | `180` | Seconds to wait for SSH readiness |
| `--windows-image PATH` | `VMSMITH_WINDOWS_IMAGE` | — | Path to a Windows qcow2 image (Windows tests skipped when unset) |
| `--windows-ip-timeout SECS` | `VMSMITH_WINDOWS_IP_TIMEOUT` | `900` | Seconds to wait for a Windows VM IP (first boot runs cloudbase-init) |

**GUI-only** (Playwright env vars, no pytest equivalent):

| Env variable | Default | Description |
|---|---|---|
| `VMSMITH_GUI_URL` | `http://localhost:8080` | Base URL for Playwright browser tests |
| `VMSMITH_SSH_PUBKEY` | — | SSH public key content (injected into VM create form) |

Multi-NIC tests (`--host-iface` / `--host-iface2`) are **skipped** when the interfaces
are not specified. Windows guest tests (`--windows-image`) are likewise **skipped**
when no Windows image is configured.

Run `cd tests/e2e && python -m pytest --help` to see the full option list under the
"VMSmith E2E test options" group.

## Running Tests

### Via Makefile

```bash
make test-e2e                # All E2E tests (CLI + API + GUI)
make test-e2e-cli            # CLI tests only
make test-e2e-api            # API tests only
make test-e2e-gui            # GUI tests only (Playwright)
make test-e2e-networking     # Only multi-NIC networking tests
make test-e2e-portforward    # Only port forwarding tests
make test-e2e-metrics        # Only per-VM metrics tests
make test-e2e-events         # Only SSE event stream tests
make test-e2e-schedules      # Only scheduled-operations tests
make test-e2e-windows        # Only Windows guest tests (need VMSMITH_WINDOWS_IMAGE)
```

### Via pytest directly (with CLI options)

```bash
cd tests/e2e

# Minimal — just lifecycle tests
python -m pytest test_cli_vm_lifecycle.py \
    --rocky-image /var/lib/vmsmith/images/rocky-9.qcow2

# Full CLI suite with custom daemon and SSH key
python -m pytest test_cli_vm_lifecycle.py test_cli_networking.py \
    --rocky-image /images/rocky-9.qcow2 \
    --vmsmith-bin /usr/local/bin/vmsmith \
    --ssh-key ~/.ssh/vmsmith_e2e \
    --ssh-user root \
    --host-iface eth1 \
    --host-iface2 eth2 \
    --ip-timeout 180

# API tests against a remote daemon
python -m pytest test_api_vm_lifecycle.py test_api_networking.py \
    --vmsmith-api http://192.168.1.50:8080 \
    --rocky-image /images/rocky-9.qcow2

# Run by marker
python -m pytest -m networking -v
python -m pytest -m portforward -v
python -m pytest -m cli -v
python -m pytest -m api -v
python -m pytest -m metrics -v
python -m pytest -m events -v
python -m pytest -m schedules -v
python -m pytest -m windows -v --windows-image /images/win2022.qcow2

# Single test class
python -m pytest test_api_networking.py::TestAPIPortForward -v

# Single test
python -m pytest test_cli_vm_lifecycle.py::TestCLIVMLifecycle::test_create_vm_and_verify -v
```

### Via environment variables (CI-friendly)

```bash
export VMSMITH_ROCKY_IMAGE=/images/rocky-9.qcow2
export VMSMITH_SSH_PRIVATE_KEY=~/.ssh/e2e_key
export VMSMITH_HOST_IFACE=eth1
export VMSMITH_HOST_IFACE2=eth2

cd tests/e2e && python -m pytest -v
```

### GUI tests (Playwright)

```bash
# Against live daemon (default localhost:8080)
npx playwright test --config tests/e2e/playwright.config.js

# Against a different URL
VMSMITH_GUI_URL=http://192.168.1.50:8080 \
    npx playwright test --config tests/e2e/playwright.config.js
```

## Test Markers

| Marker | Description |
|---|---|
| `cli` | Tests that exercise the vmsmith CLI binary |
| `api` | Tests that exercise the REST API |
| `networking` | Multi-NIC networking tests (require `--host-iface`) |
| `portforward` | Port forwarding tests |
| `metrics` | Per-VM metrics endpoint tests (skipped when daemon metrics are disabled) |
| `events` | Live SSE event stream tests |
| `schedules` | Scheduled-operations tests (skipped when the subsystem is disabled) |
| `windows` | Windows guest tests (require `--windows-image`) |

## Test Coverage

### Test 1: VM Lifecycle (CLI + API + GUI)
- Create a VM from a Rocky qcow2 image
- Verify the management IP is shown immediately in `vmsmith vm create` output (static IP pre-assignment)
- Verify the IP is reachable via ping
- Verify SSH access works (paramiko with key auth)
- Verify `vmsmith vm list` / `GET /vms` includes the VM

### Test 2: Snapshots & Images (CLI + API + GUI)
- Create a snapshot of a running VM
- Make changes inside the VM (write a marker file via SSH)
- Restore from snapshot, verify the marker file is gone
- Stop the VM, export it as a reusable qcow2 image
- Create a new VM from the exported image
- Verify the new VM boots, gets an IP, and is SSH-accessible

### Test 3: Multi-NIC Networking (CLI + API)
- Create VMs with extra macvtap interfaces (`--network eth1`)
- Verify extra interfaces appear inside the VM and get IPs via DHCP
- Deploy two VMs on the same extra network, verify inter-VM ping
- Create a VM with two extra interfaces, verify all three NICs get IPs

### Test 4: Port Forwarding (CLI + API + GUI)
- Create a VM, add a DNAT port forward (host:N → guest:22)
- SSH into the VM via the forwarded port on localhost
- Add multiple port forwards, list them, selectively remove

### Test 5: Per-VM Metrics (API)
- Create a Rocky VM, generate CPU load (`dd if=/dev/zero of=/dev/null` busy loop)
  and network traffic (dd-over-SSH transfers) inside the guest
- Poll `GET /vms/{id}/stats` until samples report non-zero `cpu_percent` and
  non-zero `net_rx_bps`/`net_tx_bps`
- Skips cleanly when the daemon returns 503 `metrics_disabled`

### Test 6: Live Event Stream (API)
- Create a VM, stop it, then open `GET /events/stream` (SSE, server-side
  `?vm_id=` filter) with a background reader thread
- Start the VM via the API and assert the libvirt-emitted `vm.started` event
  for that VM arrives live on the stream within a timeout

### Test 7: Scheduled Operations (API)
- Create a VM, then a snapshot schedule (`POST /schedules`) with a 6-field cron
  spec firing every 15 seconds
- Poll `GET /schedules/{id}/runs` until a `status=success` run appears
- Assert the auto-snapshot (`auto-<schedule-name>-<UTC ts>`) exists via
  `GET /vms/{id}/snapshots`; the schedule is deleted on teardown
- Skips cleanly when the daemon returns 503 `schedules_disabled`

### Test 8: Windows Guest (API, requires `--windows-image`)
- Create a Windows VM (`os_type: windows`, explicit `admin_password`,
  ram_mb ≥ 2048, disk_gb ≥ 32)
- Verify no `generated_admin_password` is returned and the password is redacted
  from the stored spec
- Wait for the DHCP IP (generous `--windows-ip-timeout`, default 900s), then
  verify RDP reachability (TCP connect to port 3389)
- Optional non-fatal SSH login check when the image ships OpenSSH Server

## File Structure

```
tests/e2e/
├── conftest.py              # Fixtures, CLI option registration, prereq checks
├── helpers.py               # CLI runner, API client, SSH/ping, polling utilities
├── pytest.ini               # Pytest config (markers, timeouts)
├── requirements.txt         # Python dependencies (pytest, requests, paramiko)
├── test_cli_vm_lifecycle.py # CLI: create, IP, SSH, snapshot, image, re-create
├── test_cli_networking.py   # CLI: multi-NIC, inter-VM ping, port forwarding
├── test_api_vm_lifecycle.py # API: same lifecycle via REST endpoints
├── test_api_networking.py   # API: same networking via REST endpoints
├── test_api_metrics.py      # API: per-VM stats under generated CPU/net load
├── test_api_events_stream.py # API: live SSE stream delivers vm.started
├── test_api_schedules_e2e.py # API: snapshot schedule fires + auto-snapshot
├── test_windows_guest.py    # API: Windows guest create + RDP reachability
├── gui-e2e.spec.js          # Playwright: GUI lifecycle, snapshots, port fwd
├── playwright.config.js     # Playwright config for live daemon
└── README.md                # This file
```
