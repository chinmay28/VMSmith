# CLAUDE.md — VM Smith Codebase Guide

VM Smith is a single-binary CLI tool, REST API server, and embedded React web GUI for managing QEMU/KVM virtual machines on Linux. This document describes the codebase structure, conventions, and development workflows for AI assistants.

---

## Project Overview

| Aspect | Details |
|---|---|
| Language | Go 1.22.5 pinned via `toolchain go1.22.5` (CGO_ENABLED=1 required for libvirt C bindings) |
| Module | `github.com/vmsmith/vmsmith` |
| Entry point | `cmd/vmsmith/main.go` → `cli.Execute()` |
| VM backend | libvirt + QEMU/KVM |
| REST framework | Chi v5 |
| CLI framework | Cobra |
| Metadata store | bbolt (embedded, no external DB) |
| Frontend | React 18 + Vite + Tailwind CSS |
| Frontend embed | `go:embed dist/*` in `internal/web/embed.go` |
| Target OS | Linux x86_64 (Ubuntu 22.04+, Rocky Linux 8+) |

---

## Directory Structure

```
vmsmith/
├── cmd/vmsmith/main.go          # Binary entrypoint → cli.Execute()
├── internal/
│   ├── api/
│   │   ├── router.go            # Chi router with middleware wiring
│   │   ├── handlers_vm.go       # VM CRUD, lifecycle, and bulk action endpoints
│   │   ├── handlers_snapshot.go # Snapshot endpoints
│   │   ├── handlers_image.go    # Image upload/download/list/delete
│   │   ├── handlers_network.go  # Port forward + host interface endpoints
│   │   ├── handlers_logs.go     # Log viewer endpoint (GET /api/v1/logs)
│   │   ├── handlers_console.go  # Console ticket issuance endpoint (POST /api/v1/vms/{id}/console/ticket)
│   │   └── middleware.go        # Request logging, CORS, error response helpers
│   ├── cli/
│   │   ├── root.go              # Root Cobra command, global --config flag
│   │   ├── vm.go                # vmsmith vm create|edit|set-virtio|list|start|stop|force-stop|restart|reboot|suspend|resume|delete|lock|unlock (every lifecycle verb — `start|stop|restart|force-stop|reboot|suspend|resume` — accepts a single VM id or `--all [--tag <t>]` to fan out across the matching state; `vm lock|unlock <id>` toggle delete-protection; `vm force-stop <id>` does an immediate libvirt destroy without ACPI shutdown; `vm reboot <id>` sends an ACPI reboot signal to the guest OS without power-cycling QEMU; `vm suspend|resume <id>` pause / unpause CPU+memory; `vm edit <id> --disk-bus|--nic-model` flips the system disk bus / every NIC model on an existing VM (roadmap 5.6.12 switch-to-virtio); `vm set-virtio <id>` is the shortcut that PATCHes both to virtio at once)
│   │   ├── snapshot.go          # vmsmith snapshot create|restore|list|edit|delete (`create --tag <t>` and `edit --tag <t>` / `--clear-tags` set the snapshot tag list; `list --tag <t>` filters; TAGS column always rendered)
│   │   ├── image.go             # vmsmith image list|create|delete|push|pull (`image delete --tag <tag>` bulk-deletes every image carrying that tag, mirroring the snapshot bulk_delete shape)
│   │   ├── net.go               # vmsmith net interfaces
│   │   ├── network.go           # vmsmith port add|edit|remove|list (`port remove` accepts a positional id for single-delete, or `--vm <id> [--protocol tcp|udp]` to bulk-delete every rule on a VM; `port add --tag <t>` and `port edit <id> --description "..." --tag <t> --clear-tags` mutate the description and tag set — pass `""` to clear description, `--clear-tags` to drop every tag; `port list --tag <t>` filters by tag and prints a TAGS column; `port list --min-host-port <n> --max-host-port <n>` filters by an inclusive host_port range; `port list --min-guest-port <n> --max-guest-port <n>` filters by an inclusive guest_port range)
│   │   ├── logs.go              # vmsmith logs list (HTTP client for the daemon's /api/v1/logs ring buffer; supports `--level --source --since --search --sort --order --limit --page --fields`)
│   │   ├── host.go              # vmsmith host stats|quotas|gpus (thin HTTP clients for `GET /api/v1/host/stats`, `GET /api/v1/quotas/usage`, and `GET /api/v1/host/gpus`; `host gpus` lists PCI display controllers + IOMMU groups for VFIO passthrough; `--json` for scripting)
│   │   ├── schedule.go          # vmsmith schedule create|list|show|runs|edit|delete|run-now (HTTP client for the daemon's /api/v1/schedules CRUD; `create --name --vm|--tag --action --cron --timezone --enabled --catch-up --retention --max-concurrent`; `list --vm --tag-selector --action --catch-up --timezone --enabled --search --since --until --next-fire-since --next-fire-until --last-fired-since --last-fired-until --prefix --sort --order --limit --page`; `show <id>` prints the definition plus the last 20 runs; `runs <id> --status --skip-reason --vm --search --since --until --finished-since --finished-until --min-duration-ms --max-duration-ms --sort --order --limit --page` lists the filtered run history)
│   │   └── daemon.go            # vmsmith daemon start
│   ├── config/config.go         # Config struct, DefaultConfig(), EnsureDirs()
│   ├── console/
│   │   └── store.go             # In-memory single-use console ticket store + janitor
│   ├── daemon/daemon.go         # HTTP server startup, libvirt connect, graceful shutdown orchestration, logger init
│   ├── logger/
│   │   └── logger.go            # Structured logger: global singleton, ring buffer, file output
│   ├── scheduler/
│   │   ├── engine.go            # Scheduler engine: per-timezone cron instances, bounded worker pool, per-schedule concurrency, register/deregister on CRUD, startup catch-up, run-now, retry+backoff, event emission
│   │   ├── actions.go           # Action registry (snapshot/start/stop/restart) + auto-snapshot retention trim + typed skip reasons
│   │   └── catchup.go           # Pure missed-fire computation over a cron schedule (last_tick → now, capped)
│   ├── host/
│   │   └── gpu.go               # Host GPU (PCI display controller) discovery via sysfs + IOMMU-group expansion for VFIO passthrough (powers GET /host/gpus and the VM create hostdev assembly)
│   ├── network/
│   │   ├── nat.go               # libvirt NAT network setup + stale-dnsmasq cleanup
│   │   ├── portforward.go       # iptables DNAT rules — add, remove, restore
│   │   └── discover.go          # Host interface enumeration via /sys/class/net
│   ├── storage/
│   │   ├── image.go             # qcow2 import/export/list (shells out to qemu-img)
│   │   └── transfer.go          # SCP push/pull helpers for image transfer
│   ├── store/
│   │   ├── bolt.go              # bbolt CRUD: VMs, images, port forwards
│   │   └── models.go            # Stored data structures
│   ├── vm/
│   │   ├── manager.go           # VMManager interface definition
│   │   ├── lifecycle.go         # LibvirtManager: Create/Start/Stop/Delete/Get/List + snapshots
│   │   ├── quota_manager.go     # Optional quota enforcement wrapper + usage aggregation
│   │   ├── domain.go            # libvirt XML generation, multi-network, cloud-init ISO, OS-family device tuning (Linux: virtio/utc; Windows: SATA/e1000e/localtime/Hyper-V/tablet/QXL/virtio-win), VFIO GPU passthrough <hostdev> entries
│   │   └── mock_manager.go      # In-memory mock VMManager for tests
│   └── web/
│       ├── embed.go             # go:embed dist/*
│       └── dist/                # Built SPA (gitignored; generated by `make build`)
├── pkg/types/
│   ├── vm.go                    # VM, VMSpec, VMState types
│   ├── snapshot.go              # Snapshot type
│   ├── image.go                 # Image type
│   ├── network.go               # NetworkAttachment, PortForward, HostInterface
│   ├── gpu.go                   # GPUDevice type + PCI-address validation/normalization helpers + VMSpec.ResolvedGPUs
│   ├── quota.go                 # Quota usage response types
│   ├── console.go               # Console ticket + endpoint response types
│   ├── schedule.go              # Schedule + CreateScheduleRequest + ScheduleUpdateSpec + action/catch-up constants + validation/sort helpers
│   ├── schedule_run.go          # ScheduleRun + status/skip-reason constants
│   └── errors.go                # Typed API errors
├── web/                         # React source (separate npm project)
│   ├── src/api/client.ts        # OpenAPI-backed REST API client wrapper (vms, snapshots, images, ports, host, logs, webhooks, schedules)
│   ├── src/api/generated/       # Generated OpenAPI TypeScript schema (`npm run generate:api`)
│   ├── src/components/          # Layout, Shared (StatusBadge, Modal, etc.)
│   ├── src/hooks/useFetch.js    # Data fetching with polling + mutation helpers
│   ├── src/pages/               # Dashboard, VMList, VMDetail, ImageList, TemplateList, Schedules, LogViewer (Schedules is the dedicated page for recurring VM-action schedules: list with enabled toggle / next-fire / last-result chip / run-now, create+edit modal with cron preset chips, per-row recent-runs expander; VMList's bulk-action bar fans out start/stop/restart/force-stop/reboot/suspend/resume/delete over the existing per-VM endpoints, with eligibility filtered by VM state; ImageList includes upload progress UI for image imports; TemplateList is the dedicated admin page for the templates store with a "New Template" modal (name / base-image dropdown / cpus / ram_mb / disk_gb / default_user / description / tags, POSTed to `/templates`) / search / sort / per-row edit (description + tags via PATCH) / single-delete / select-all + Delete-selected wired to `/templates/bulk_delete`; the VMList "New VM" modal's Advanced tab includes a GPU Passthrough section that lists host GPUs from `GET /host/gpus` as checkboxes (vendor / PCI address / bound-driver chip / IOMMU group), POSTing the selected addresses as `spec.gpus`, and VMDetail renders a GPU Passthrough card for VMs with assigned GPUs)
│   └── vite.config.js           # Build outputs to ../internal/web/dist/
├── tests/web/
│   ├── gui.spec.js              # Playwright E2E test specs (mock server)
│   ├── mock-server.js           # Node.js mock API server for browser tests
│   └── run-gui-tests.js         # Playwright runner script
├── tests/e2e/
│   ├── conftest.py              # Fixtures, CLI option registration, prereq checks
│   ├── helpers.py               # CLI runner, API client, SSH/ping, polling utilities
│   ├── pytest.ini               # Pytest config (markers, timeouts)
│   ├── requirements.txt         # Python deps (pytest, requests, paramiko)
│   ├── test_cli_vm_lifecycle.py # CLI E2E: create, snapshot, image, re-create
│   ├── test_cli_networking.py   # CLI E2E: multi-NIC, port forwarding
│   ├── test_api_vm_lifecycle.py # API E2E: same lifecycle via REST
│   ├── test_api_networking.py   # API E2E: same networking via REST
│   ├── gui-e2e.spec.js          # Playwright E2E against live daemon
│   └── playwright.config.js     # Playwright config for live daemon
├── scripts/
│   ├── build-deb.sh             # Build a Debian package from the release binary + service/config assets
│   ├── build-rpm.sh             # Build an RPM from bin/vmsmith-linux-amd64 using rpmbuild
│   ├── install-deps-ubuntu.sh
│   ├── install-deps-rocky.sh
│   └── install.sh               # Release installer for curl|sh installs to /usr/local/bin
├── docs/ARCHITECTURE.md         # Detailed architecture reference
├── docs/openapi.yaml            # OpenAPI 3 schema for the implemented REST API surface
├── docs/apidocs.go              # Embedded Swagger UI + OpenAPI spec handlers
├── Makefile
└── vmsmith.yaml.example         # Reference configuration
```

---

## Build System

All common operations are in the `Makefile`. Always use `make` targets rather than raw `go build` commands.

| Target | What it does |
|---|---|
| `make build` | Build frontend + backend → `./bin/vmsmith` |
| `make build-go` | Backend only (skips frontend rebuild) |
| `make web` | Build React frontend into `internal/web/dist/` |
| `make web-install` | `npm install` in `./web/` |
| `make deps` | `go mod tidy && go mod download` |
| `make test` | All Go tests with race detector |
| `make test-unit` | Unit tests only (store, config, vm, cli, storage) |
| `make test-integration` | API integration tests only (internal/api) |
| `make test-web` | Headless Playwright E2E tests (mock server) |
| `make test-all` | All Go + web tests |
| `make test-web-deps` | Install Playwright + Chromium (run once) |
| `make test-e2e` | All real E2E tests (CLI + API + GUI, requires daemon) |
| `make test-e2e-cli` | CLI E2E tests only (pytest) |
| `make test-e2e-api` | API E2E tests only (pytest) |
| `make test-e2e-gui` | GUI E2E tests (Playwright against live daemon) |
| `make test-e2e-networking` | Multi-NIC networking E2E tests only |
| `make test-e2e-portforward` | Port forwarding E2E tests only |
| `make test-e2e-deps` | Install Python + Playwright deps for E2E tests |
| `make lint` | golangci-lint |
| `make fmt` | gofmt (use the pinned Go 1.22.5 toolchain from `go.mod`) |
| `make install` | Build + install to `/usr/local/bin/vmsmith` |
| `sh scripts/install.sh` | Download the latest GitHub release binary and install it to `/usr/local/bin/vmsmith` |
| `make clean` | Remove `./bin/`, `internal/web/dist/`, `web/node_modules/` |
| `make purge` | Remove all VMSmith runtime resources (VMs, network, images, DB, log) — requires root; supports `PURGE_ARGS="--dry-run"` |
| `make dist` | Cross-compile linux/amd64 release binary |
| `make rpm` | Build an RPM package in `./bin/packages/` from `bin/vmsmith-linux-amd64` using `rpmbuild` |
| `make deb` | Build a Debian package at `bin/packages/*.deb` from the release binary, config, and systemd unit |
| `make dev-api` | Build Go backend and start daemon on :8080 |
| `make dev-web` | Start Vite dev server on :3000 (proxies /api → :8080) |
| `cd web && npm run generate:api` | Regenerate the frontend API types from `docs/openapi.yaml` |

**Important build requirement:** `CGO_ENABLED=1` is mandatory — the libvirt Go bindings use cgo.

---

## Key Architectural Patterns

### VMManager Interface

All VM operations go through the `vm.Manager` interface (`internal/vm/manager.go`). The production implementation is `LibvirtManager` (`internal/vm/lifecycle.go`). Tests use `MockManager` (`internal/vm/mock_manager.go`), an in-memory implementation with error injection.

The interface includes a `Update(ctx, id, VMUpdateSpec) (*VM, error)` method. `VMUpdateSpec` carries `CPUs`, `RAMMB`, `DiskGB`, `Description`, `Tags`, `NatStaticIP`, `NatGateway`, `ClockOffset`, `DiskBus`, and `NICModel`; zero/empty values are treated as "no change" (except `Tags`, where a provided slice replaces the current tag set, and `ClockOffset` / `DiskBus` / `NICModel` which use `*string` pointer semantics — nil = no change, pointer-to-`""` = clear the override and fall back to the OS-family default at next render). `OSType` / `OSVariant` are present on `VMUpdateSpec` purely as `*string` rejection sentinels — sending either field on PATCH (any value including the empty string) returns 400 `os_type_immutable` because the OS family is baked at create time. GPU passthrough is likewise immutable after create: `VMUpdateSpec` intentionally has no `gpus` field, so an existing VM keeps its current GPU assignment through edits and clone clears GPUs to avoid silently sharing passthrough devices. The `LibvirtManager` implementation stops the VM if running, then applies each changed field: metadata is persisted in bbolt, IP change updates the DHCP host reservation and regenerates the cloud-init ISO with a new instance-id (forces cloud-init re-run on restart), CPU/RAM/ClockOffset/DiskBus/NICModel change redefines the domain XML (preserving the existing UUID), disk growth calls `qemu-img resize` (shrink is rejected). The VM is then restarted. The switch-to-virtio helper (roadmap 5.6.12) is a shortcut on top of `DiskBus`/`NICModel`: `vmsmith vm set-virtio <id>` (or PATCH with `{"disk_bus":"virtio","nic_model":"virtio"}`) flips a Windows guest to virtio after the operator installs the virtio-net/virtio-blk drivers in-guest.

Quota enforcement is implemented as a wrapper (`vm.WithQuotas`) around any `vm.Manager`. It checks configured aggregate caps before create/update by summing current allocations from `List()`/`Get()`, so the daemon and direct CLI both share the same quota rules.

Never call libvirt directly from handlers — always go through the `Manager` interface.

### Structured Logging

`internal/logger` provides a global singleton structured logger with:
- **Log levels:** `debug` < `info` < `warn` < `error` — configurable minimum level
- **In-memory ring buffer** of 2000 entries, always available for GUI polling via `GET /api/v1/logs`
- **File output** to `daemon.log_file` (default `~/.vmsmith/vmsmith.log`)
- **Sources:** `daemon` (startup/shutdown), `api` (every HTTP request via middleware), `cli` (every command)

**Initialization order:**
- Daemon: `logger.Init(cfg.Daemon.LogFile, logger.LevelInfo)` called at top of `daemon.New()`
- Shutdown path: the API router exposes `BeginShutdown()`/`WaitForDrain(ctx)` so daemon signal handling can reject new requests with HTTP 503, wait for in-flight handlers, then close VM/network/store resources in order
- CLI: initialized via `PersistentPreRunE` on `rootCmd` so all subcommands share one log file

**HTTP request middleware** (`middleware.go`) logs every request except `GET /api/v1/logs` (to avoid self-noise). POST/PUT body snippets (up to 4096 bytes) are captured and re-injected into `r.Body`.

**Package-level helpers** for convenience: `logger.Info("source", "message", "key", "val", ...)`

### Public Types vs Internal Packages

- **`pkg/types/`** — shared types used by both the API layer and the VM/storage packages. These are the wire format types (JSON-serializable). Do not add business logic here.
- **`internal/`** — all implementation code. Not importable by external packages.

### REST API

- All endpoints are prefixed `/api/v1/`
- The router is defined in `internal/api/router.go`
- Handlers receive a `vm.Manager`, `*store.BoltStore`, and config via dependency injection (not globals)
- Error responses use typed errors from `pkg/types/errors.go`
- `GET /api/v1/quotas/usage` returns current allocations and configured quota caps for dashboard/ops visibility
- The static web GUI is served from the same port — the router handles both `/api/v1/*` and the SPA fallback

### bbolt Data Model

Three buckets in `~/.vmsmith/vmsmith.db`:

| Bucket | Key | Value |
|---|---|---|
| `vms` | VM ID (`vm-<unix-nano>`) | JSON `types.VM` |
| `images` | image ID | JSON `types.Image` |
| `port_forwards` | `{vmID}/{hostPort}` | JSON `types.PortForward` |
| `snapshots` | `{vmID}/{name}` | JSON `{tags: [...]}` — snapshot tag list, persisted out-of-band because the libvirt domainsnapshot XML schema does not accept `<metadata>` for round-tripping tags alongside `<description>` |
| `schedules` | schedule ID (`sched-<unix-nano>`) | JSON `types.Schedule` |
| `schedule_runs` | `{scheduleID}/{ts_be}` (big-endian nanos suffix) | JSON `types.ScheduleRun` — per-fire history, trimmed to 200 newest per schedule |
| `schedule_meta` | `last_tick` | RFC3339Nano timestamp — scheduler catch-up cursor |

### Scheduled Operations

The `internal/scheduler` package drives recurring VM actions. A single `Engine`
runs one `robfig/cron/v3` instance per distinct timezone, fans fired schedules
onto a bounded worker pool, resolves `tag_selector` targets at fire time, and
records each attempt as a `ScheduleRun`. It is wired in `daemon.New()` (shares
the store, VM manager, and event bus) and exposed to the API via the
`ScheduleController` / `ScheduleStore` interfaces (mirroring the webhook
subsystem decoupling so the `api` package never imports `scheduler`). On
startup the engine replays missed fires per each schedule's catch-up policy
(`skip` / `run_once` / `run_all`, capped at `max_catch_up`). Snapshot actions
honor `retention_count`, trimming only `auto-<schedule-name>-*` snapshots so
operator snapshots are never deleted. Fires emit `schedule.*` events with
`actor: "scheduler"`. Full operator contract: `docs/SCHEDULES.md`.

### Networking

- Every VM gets a primary interface on the `vmsmith-net` libvirt NAT network (`192.168.100.0/24`). The OS-visible name depends on the distro (`eth0` on Ubuntu, `enp1s0`/`ens3` on Rocky/RHEL)
- Extra interfaces (`--network eth1`) attach as macvtap or bridge; their OS-visible names are also distro-dependent
- Port forwarding uses `iptables -t nat PREROUTING DNAT` rules — stored in bbolt and restored at daemon startup
- Cloud-init ISO (`cidata.iso`) is **always** generated. `user-data` uses `buildCloudConfig()` which sets up the SSH user and writes a NetworkManager keyfile for the primary NAT interface via `write_files` + `runcmd` — this is the primary networking mechanism on Rocky/RHEL. A Netplan v2 `network-config` is also included for Ubuntu/Debian (belt-and-suspenders). Both files match interfaces by MAC address, not by distro-specific name
- **Root-by-default:** When no `default_user` is specified, `buildCloudConfig()` emits `disable_root: false`, injects the SSH public key into root's `authorized_keys`, and writes `/etc/ssh/sshd_config.d/99-vmsmith-root.conf` with `PermitRootLogin prohibit-password` to ensure key-based root SSH works across all distros. When `default_user` is provided, a named sudo user is created with the SSH key, root login is disabled (`disable_root: true`), and the image's built-in default user is preserved alongside it
- **Static IP pre-assignment:** Before generating the cloud-init ISO, `Create()` picks an available IP from the DHCP range and calls `netMgr.AddDHCPHost(mac, ip, name)` to register a DHCP host reservation. The resulting static IP is embedded in the NM keyfile (`method=manual`) so the interface comes up deterministically on first boot — no DHCP race. The IP is shown in `vmsmith vm create` output immediately. If DHCP range is exhausted or the reservation fails, it falls back to dynamic assignment. Any stale reservation left by a previous failed create with the same VM name is removed via `RemoveDHCPHostByName` before adding the new one. If `dom.Create()` fails, the reservation is also cleaned up.

### Frontend Build Integration

The React app in `web/` builds into `internal/web/dist/`. The Go binary embeds this directory via `go:embed`. This means:
- After changing frontend code, run `make web` or `make build` before testing with the embedded binary
- For frontend development, use `make dev-web` (Vite dev server with `/api` proxy to `:8080`)
- `internal/web/dist/` is gitignored — it must be built locally

---

## Testing Conventions

### Test Tiers

| Tier | Command | Requires daemon? | Description |
|---|---|---|---|
| 1. Unit | `make test-unit` | No | `internal/logger`, `internal/store`, `config`, `vm`, `cli`, `storage`, `network` |
| 2. API integration | `make test-integration` | No | `internal/api/api_test.go` with `httptest` + `MockManager`; includes `/logs` endpoint tests |
| 3. GUI mock tests | `make test-web` | No | Playwright against `tests/web/mock-server.js`; includes Log Viewer tests |
| 4. **Real E2E** | `make test-e2e` | **Yes** | pytest + Playwright against live daemon + QEMU/KVM |

Tiers 1–3 need no real libvirt or QEMU — they run entirely in-process or with mock processes.
Tier 4 (real E2E) requires a running daemon, a Rocky Linux qcow2 image, and SSH key access.

### MockManager Usage

When writing API integration tests (tier 2), use `vm.MockManager`:

```go
mock := vm.NewMockManager()
mock.SeedVM(someVM)           // pre-populate state
mock.CreateErr = someError    // inject errors for error-path tests
```

`MockManager` is thread-safe (`sync.RWMutex`).

### Running Unit / Integration Tests

```bash
make test                  # all Go tests (use this before committing)
make test-unit             # faster; no httptest overhead
go test -v -run TestFoo ./internal/vm/...  # single test
```

### Running Real E2E Tests

E2E tests live in `tests/e2e/` and use **pytest** (CLI + API) and **Playwright** (GUI).
They test against a live vmsmith daemon with real QEMU/KVM virtual machines.

**Setup (one-time):**

```bash
make test-e2e-deps         # install pytest, requests, paramiko, Playwright + Chromium
```

**Configuration** — every setting can be passed as a pytest CLI option or env var
(CLI option takes precedence):

| CLI option | Env variable | Default | Description |
|---|---|---|---|
| `--rocky-image` | `VMSMITH_ROCKY_IMAGE` | *(required)* | Rocky Linux qcow2 image path |
| `--ssh-key` | `VMSMITH_SSH_PRIVATE_KEY` | `~/.ssh/id_rsa` | SSH private key |
| `--ssh-user` | `VMSMITH_SSH_USER` | `root` | SSH username |
| `--vmsmith-bin` | `VMSMITH_BIN` | `vmsmith` | vmsmith binary path |
| `--vmsmith-api` | `VMSMITH_API` | `http://localhost:8080` | Daemon API URL |
| `--host-iface` | `VMSMITH_HOST_IFACE` | — | Host interface for multi-NIC tests |
| `--host-iface2` | `VMSMITH_HOST_IFACE2` | — | Second host interface for dual-NIC |
| `--ip-timeout` | `VMSMITH_IP_TIMEOUT` | `120` | Seconds to wait for VM IP |
| `--ssh-timeout` | `VMSMITH_SSH_TIMEOUT` | `180` | Seconds to wait for SSH |

**Running:**

```bash
# All E2E tests (CLI + API + GUI)
make test-e2e

# Individual layers
make test-e2e-cli
make test-e2e-api
make test-e2e-gui

# By scenario
make test-e2e-networking
make test-e2e-portforward

# pytest directly with CLI options
cd tests/e2e && python -m pytest test_cli_vm_lifecycle.py \
    --rocky-image /images/rocky-9.qcow2 \
    --ssh-key ~/.ssh/e2e_key \
    --host-iface eth1 -v

# By marker
cd tests/e2e && python -m pytest -m networking -v
cd tests/e2e && python -m pytest -m portforward -v

# GUI E2E (Playwright against live daemon)
VMSMITH_GUI_URL=http://localhost:8080 \
    npx playwright test --config tests/e2e/playwright.config.js
```

**E2E test scenarios:**

1. **VM Lifecycle** — Create Rocky VM → verify management IP → ping → SSH → list
2. **Snapshots & Images** — Snapshot → modify → restore → export image → create from image → verify SSH
3. **Multi-NIC Networking** — Extra macvtap interfaces → DHCP IPs → inter-VM ping → dual-NIC
4. **Port Forwarding** — Add DNAT rule → SSH via forwarded port → CRUD operations

See `tests/e2e/README.md` for full documentation.

---

## Adding New Features

### Adding a New API Endpoint

1. Add a handler function to the relevant `internal/api/handlers_*.go` file
2. Register the route in `internal/api/router.go`
3. Add the corresponding type to `pkg/types/` if new wire types are needed
4. Add integration tests in `internal/api/api_test.go` using `MockManager`

### Adding a New CLI Command

1. Add the Cobra command to the relevant `internal/cli/*.go` file (or create a new file)
2. Register it with the root command in `internal/cli/root.go`
3. Add tests in `internal/cli/cli_test.go` or `commands_test.go`

### Adding a New VM Feature (e.g., new libvirt capability)

1. Add the method to the `vm.Manager` interface in `internal/vm/manager.go`
2. Implement it in `LibvirtManager` in `internal/vm/lifecycle.go`
3. Implement it in `MockManager` in `internal/vm/mock_manager.go`
4. Add the corresponding domain XML changes in `internal/vm/domain.go` if needed
5. Wire through the API handler and CLI command

---

## Configuration

Config file is loaded from (in order): `--config` flag → `~/.vmsmith/config.yaml` → `/etc/vmsmith/config.yaml`.

The config struct is in `internal/config/config.go`. `DefaultConfig()` returns usable defaults. `EnsureDirs()` creates required storage directories.

Key config fields:
- `daemon.listen` — HTTP listen address (default `0.0.0.0:8080`)
- `daemon.log_file` — structured log output path (default `~/.vmsmith/vmsmith.log`); leave empty to disable file logging
- `daemon.tls.cert_file` / `daemon.tls.key_file` — when both are set, the daemon serves HTTPS via `ListenAndServeTLS`
- `daemon.tls.auto_cert` — enable automatic Let's Encrypt certificates via Go `autocert`
- `daemon.tls.auto_cert_hosts` — hostnames to request certificates for; the daemon must be reachable on public `:443` for TLS-ALPN validation
- `daemon.tls.auto_cert_cache_dir` — on-disk cache directory for ACME account/certificate state
- `daemon.tls.auto_cert_email` — optional contact email for ACME registration
- Manual cert/key files take precedence over auto-cert if both are configured
- `daemon.max_concurrent_creates` — maximum number of simultaneous `POST /api/v1/vms` operations; extra create requests fail fast with HTTP 429 / `create_limit_reached`
- `libvirt.uri` — libvirt connection URI (use `qemu:///session` for rootless)
- `storage.images_dir` — must be world-readable (libvirt-qemu user reads VM disks)
- `storage.base_dir` — VM disk overlays (must also be world-readable)
- `storage.db_path` — bbolt database path
- `network.*` — NAT network name and DHCP range
- `schedules.enabled` — master switch for the scheduled-operations subsystem (default true); when false the `/api/v1/schedules` endpoints return 503 `schedules_disabled`
- `schedules.worker_pool_size` / `schedules.queue_size` — bound concurrent fires (default 4) and the dispatch backlog (default 64; overflow records a `queue_full` skip)
- `schedules.max_retries` / `schedules.action_timeout_seconds` — transient-error retry count (default 2) and per-attempt timeout (default 300)
- `schedules.max_catch_up` / `schedules.tick_interval_seconds` — cap on replayed missed fires per schedule on startup (default 100) and the catch-up cursor advance interval (default 60)
- `defaults.cpus/ram_mb/disk_gb` — default VM resource sizes
- `defaults.ssh_user` — retained for config file compatibility but no longer used as a VM default. VMs use `root` by default; override per-VM with `VMSpec.DefaultUser` / `--default-user` CLI flag / `default_user` JSON field to create a named sudo user instead

---

## Code Conventions

- **Error handling:** Return errors; do not panic. API handlers wrap errors into typed `pkg/types` error responses via middleware helpers.
- **Context:** All `vm.Manager` methods take `context.Context` as the first argument. Pass it through.
- **Logging:** Use `internal/logger` package helpers (`logger.Info`, `logger.Warn`, etc.) for all structured log output. Never use `log.Printf` or `fmt.Printf` for operational messages — those bypass the ring buffer and file output. Use `fmt.Printf` only for direct terminal output to end-users (e.g., CLI result tables).
- **JSON tags:** All public types in `pkg/types/` have `json:"..."` tags. Use `omitempty` for optional fields.
- **IDs:** VM IDs are `vm-<unix-nano>` (e.g., `vm-1741234567890123`). Image IDs follow a similar convention.
- **CGO:** Required for libvirt bindings. Do not set `CGO_ENABLED=0` anywhere.
- **No globals:** Handlers receive dependencies via closure or struct injection. No package-level state in `internal/api/`.

---

## Common Pitfalls

- **Missing frontend build:** If the web GUI shows stale content, run `make web` to rebuild the embedded assets.
- **Permission denied on VM disk:** Storage dirs must be at `/var/lib/vmsmith/` with world-execute permission (755). Home directory paths will fail because libvirt-qemu cannot enter mode-750 home dirs.
- **Orphaned dnsmasq:** If the daemon exits uncleanly, the next startup automatically kills the stale process via the libvirt PID file at `/run/libvirt/network/<name>.pid`.
- **VM gets no IP on Rocky/RHEL:** The primary mechanism is the NM keyfile written via `write_files` in `user-data` (`buildCloudConfig`). The Netplan v2 `network-config` is ignored on Rocky/RHEL but Ubuntu/Debian use it. Do not add conditions that skip ISO generation or omit the NM keyfile — if the ISO is not attached the primary NAT interface never comes up on RHEL-based images.
- **Image files must have a `.qcow2` extension:** libvirt's AppArmor driver scans backing-file chains only for files whose names end in `.qcow2`. An extension-less base image (e.g. `rocky9` instead of `rocky9.qcow2`) causes QEMU to fail with "Permission denied" at boot. `lifecycle.go` tries the name as-is first; if not found, appends `.qcow2` automatically. Always store images as `<name>.qcow2` in the images directory.
- **Use the GenericCloud image, not the OCP image:** Rocky Linux offers several image variants. The `Rocky-9-OCP-Base` image is designed for OpenShift nodes and uses **Ignition** for first-boot configuration — cloud-init is ignored, so the NM keyfile is never written and the VM gets no network. Always use `Rocky-9-GenericCloud-Base` which includes cloud-init and works with vmsmith. Download command:
  ```bash
  wget -O /var/lib/vmsmith/images/rocky9.qcow2 \
    https://download.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base.latest.x86_64.qcow2
  ```
- **Stale DHCP reservations:** If VM creation fails after a DHCP reservation is registered, the reservation is automatically cleaned up. If a previous failed create left a reservation with the same VM name, `RemoveDHCPHostByName` removes it before adding the new one (both MAC and name are unique keys in libvirt DHCP host entries).
- **SSH key injection in CLI:** Use `"$(cat ~/.ssh/id_ed25519.pub)"` — not `"$(~/.ssh/id_ed25519.pub)"` (which executes the file as a shell command).
- **CGO_ENABLED=0 builds fail:** The `libvirt.org/go/libvirt` package requires cgo.

---

## REST API Reference (Quick)

Most API routes are under `/api/v1/`. Full reference in `docs/ARCHITECTURE.md` (the event bus, SSE protocol, retention, and webhook contract are documented in Section 5 "Event System").

Additional docs routes:
- `GET /api/docs` — embedded Swagger UI for the REST API
- `GET /api/openapi.yaml` — served OpenAPI schema consumed by Swagger UI
- `GET /api/version` — public build identification (`version`, `commit`, `build_date`, `go_version`, `os`, `arch`). Sits outside the authenticated `/api/v1` tree so health checks and the GUI footer can read it without an API key. Values are populated at link time via `-X github.com/vmsmith/vmsmith/pkg/version.Version=…` (see `Makefile`); on an unconfigured build the response carries `version: "dev"`, `commit: "unknown"`, `build_date: "unknown"`. The CLI exposes the same payload via `vmsmith version` (human-readable) or `vmsmith version --json`.

```
GET    /vms                            List all VMs. Filters: `?tag=<tag>`, `?status=<state>`, `?search=<text>` (case-insensitive substring across name/description/tags; whitespace-trimmed; ID intentionally excluded — combine with `tag` / `status` for narrower scoping), `?image=<name>` (case-insensitive exact-match on `spec.image`; whitespace-trimmed; empty = no filter; closes the "show me every VM built from rocky9.qcow2" operator query that `?search=` matches fuzzily and `?tag=` requires pre-tagging to answer), `?default_user=<user>` (case-insensitive exact-match on `spec.default_user`; whitespace-trimmed; empty disables; an empty `spec.default_user` is treated as `root` to mirror `lifecycle.go`'s runtime semantics — `?default_user=root` matches both explicit-root and unset VMs), `?os_type=<linux|windows>` (case-insensitive exact-match on the resolved OS family via `VMSpec.ResolvedOSType`; whitespace-trimmed; empty disables; an empty `spec.os_type` resolves to `linux` — mirrors the `?default_user=root` empty-means-X semantics — so `?os_type=linux` matches both explicit-linux and unset VMs; any value other than `linux`/`windows` returns 400 `invalid_os_type`, matching the create-path validation contract; CLI flag: `--os-type`), `?os_variant=<windows-10|windows-11|windows-server-2019|windows-server-2022|windows-server-2025>` (case-insensitive exact-match on `spec.os_variant`; whitespace-trimmed; empty disables; any value outside the five known variants returns 400 `invalid_os_variant`; the sub-axis of `?os_type=windows` — `?os_type=` narrows to the OS family, `?os_variant=` slices the Windows cohort by edition; unlike `?os_type=linux` (which matches empty-stored via the linux default) the `?os_variant=` filter has NO documented default — an empty `spec.os_variant` means "operator did not specify an edition" and is filtered OUT whenever the filter is set, mirroring the webhook `?event_type=` membership semantics; CLI flag: `--os-variant`), `?firmware=<bios|uefi|ovmf>` (case-insensitive exact-match on `spec.firmware`; whitespace-trimmed; empty disables; any other value returns 400 `invalid_firmware`, matching the create-path validation contract; `?firmware=bios` matches both stored `"bios"` AND VMs with no firmware override — the SeaBIOS default, mirroring the `?os_type=linux` empty-means-linux contract; `?firmware=uefi` and `?firmware=ovmf` strict-match the stored value because uefi and ovmf are stored separately even though they map to the same libvirt `firmware='efi'` attribute at render time — preserves the operator's chosen alias on the filter round-trip; applied right after `?os_variant=` and before `?network=` so it composes additively with every other VM filter; closes the fleet-audit operator queries *"which VMs boot via OVMF / which can boot Windows 11"* and *"which VMs still use SeaBIOS"* that `?os_type=windows` (which narrows to the OS family but tells you nothing about firmware choice) cannot answer; CLI flag: `--firmware`), `?disk_bus=<virtio|sata>` (case-insensitive exact-match on the VM's *effective* disk bus via `VMSpec.ResolvedDiskBus`; whitespace-trimmed; empty disables; any other value returns 400 `invalid_disk_bus`, matching the create-path validation contract; resolution defers to the OS-family default for empty stored values — Linux VMs match `?disk_bus=virtio` (the historical default), Windows VMs match `?disk_bus=sata` (the boot-without-virtio-drivers default), mirroring the `?firmware=bios` empty-defaults-to-SeaBIOS contract and the `?os_type=linux` empty-defaults-to-linux contract; an explicit `spec.disk_bus` always wins over the OS-family default, so a Windows guest flipped to virtio after the operator installs the virtio-blk drivers in-guest via 5.6.12 appears under `?disk_bus=virtio` rather than `?disk_bus=sata`; applied right after `?firmware=` and before `?network=` so it composes additively with every other VM filter; closes the fleet-audit operator queries *"which VMs still run on SATA / which have been migrated to virtio"* that `?os_type=` (OS family) and `?firmware=` (boot path) cannot answer; CLI flag: `--disk-bus`), `?nic_model=<virtio|e1000e>` (case-insensitive exact-match on the VM's *effective* NIC model via `VMSpec.ResolvedNICModel`; whitespace-trimmed; empty disables; any other value returns 400 `invalid_nic_model`; resolution defers to the OS-family default for empty stored values — a Linux VM with empty `spec.nic_model` matches `?nic_model=virtio` (the historical Linux default), a Windows VM with empty `spec.nic_model` matches `?nic_model=e1000e` (the boot-without-virtio-drivers default), mirroring the `?disk_bus=virtio` empty-defaults-to-virtio-on-Linux contract; an explicit `spec.nic_model` always wins, so a Windows guest flipped to virtio via 5.6.12 appears under `?nic_model=virtio`; applied right after `?disk_bus=` and before `?machine=`; CLI flag: `--nic-model`), `?machine=<type>` (case-sensitive exact-match on the VM's *effective* libvirt machine type via `VMSpec.ResolvedMachine`; whitespace-trimmed; empty disables; free-form value bounded by the libvirt machine-type alphabet `[A-Za-z0-9._-]+` (e.g. `pc-q35-6.2`, `q35`, `virt-7.2`); garbage failing the alphabet check returns 400 `invalid_machine`; resolution defers to the daemon default `pc-q35-6.2` for empty stored values so `?machine=pc-q35-6.2` matches stored value AND VMs with no override, mirroring the `?firmware=bios` empty-defaults-to-SeaBIOS contract; applied right after `?nic_model=` and before `?clock_offset=` so it composes additively with every other VM filter; completes the per-VM device-override filter quartet (firmware/disk_bus/nic_model/machine) on the VM list; CLI flag: `--machine`), `?clock_offset=<utc|localtime>` (case-insensitive exact-match on the VM's *effective* libvirt clock offset via `VMSpec.ResolvedClockOffset`; whitespace-trimmed; empty disables; any other value returns 400 `invalid_clock_offset`, matching the create-path / update-path validation contract; resolution defers to the OS-family default for empty stored values — a Linux VM with empty `spec.clock_offset` matches `?clock_offset=utc` (the historical Linux default) and a Windows VM with empty `spec.clock_offset` matches `?clock_offset=localtime` (the Windows RTC convention), mirroring the `?nic_model=virtio` empty-defaults-to-virtio-on-Linux contract; an explicit `spec.clock_offset` always wins over the OS-family default, so a Windows guest pinned to utc for an NTP-synced fleet appears under `?clock_offset=utc` rather than `?clock_offset=localtime`; applied right after `?machine=` and before `?network=` so it composes additively with every other VM filter; closes the fleet-audit operator queries *"which Windows VMs are still on the default localtime clock"* and *"which VMs have been pinned to utc for NTP consistency"* that `?os_type=windows` (which narrows to the OS family but tells you nothing about clock choice) cannot answer; CLI flag: `--clock-offset`), `?network=<name>` (case-insensitive exact-match against the name of any of the VM's additional network attachments `spec.networks[].name` — any-of; whitespace-trimmed; empty disables; the implicit primary NAT network is NOT matched since it is not represented in `spec.networks`, so this only scopes to explicitly-attached extra networks operators name and group by like `data-net` / `storage-net`), `?prefix=<value>` (case-sensitive `HasPrefix(vm.name, prefix)` filter; whitespace-trimmed; empty disables; mirrors the 5.4.75 snapshot `?prefix=` selector and the case-sensitive `vmsmith` VM-name alphabet `[A-Za-z0-9-]` — operators routinely cohort their fleet by name prefix `web-prod-` / `db-prod-` / `web-staging-` and need exact-prefix discrimination before running fan-out actions; applied right after `?network=` and before the `?since=` / `?until=` time range so it composes additively with every other VM filter; closes the cohort-discrimination operator query that `?search=` (case-insensitive substring) cannot answer cleanly — searching for `web-prod-` also surfaces `legacy-web-prod-2024-bak` because of substring matching; CLI flag: `--prefix`), `?nat_static_ip=<addr>` (case-insensitive exact-match on `spec.nat_static_ip`; whitespace-trimmed; empty disables; matches when the filter equals the stored CIDR — e.g. `192.168.100.50/24` — or just the IP portion — `192.168.100.50` — so operators can answer *"which VM lives at 192.168.100.50?"* whether they remember the CIDR suffix or not; VMs with an empty `nat_static_ip` (DHCP-assigned) drop out whenever the filter is set, mirroring the empty-stored exclusion contract on the port-forward `?guest_ip=` filter (5.4.73); no validation rejection — `nat_static_ip` is a free-form value operators paste verbatim and garbage simply matches no VMs; applied right after `?prefix=` and before the `?since=` / `?until=` time range so it composes additively with every other VM filter; CLI flag: `--nat-static-ip`), `?nat_gateway=<addr>` (case-insensitive exact-match on `spec.nat_gateway` — a plain gateway IP, no CIDR dual-form since gateways are always stored as bare IPs; whitespace-trimmed; empty disables; VMs with an empty `nat_gateway` (no explicit gateway override) drop out whenever the filter is set, mirroring the empty-stored-excludes contract on `?nat_static_ip=` (5.4.79); no validation rejection — `nat_gateway` is a free-form value operators paste verbatim and garbage simply matches no VMs; applied right after `?nat_static_ip=` and before the `?since=` / `?until=` time range so it composes additively with every other VM filter; closes the fleet-audit operator query *"which VMs are routed through the non-default gateway 192.168.100.254"* that `?nat_static_ip=` (the address axis) cannot answer because two VMs sharing a non-default gateway often live on different static IPs; CLI flag: `--nat-gateway`), `?ip=<addr>` (case-insensitive exact-match on the VM's runtime-discovered IP `vm.ip` — the value shown in the VM list/detail table, populated by the libvirt DHCP lease lookup with fallback to the IP portion of `spec.nat_static_ip` for static-IP VMs; whitespace-trimmed; empty disables; VMs with an empty IP — stopped, no lease yet, no static configured — drop out whenever the filter is set, mirroring the empty-stored-excludes contract on `?nat_static_ip=` / `?nat_gateway=`; no validation rejection — `ip` is a free-form value operators paste verbatim and garbage simply matches no VMs; applied right after `?nat_gateway=` and before the `?since=` / `?until=` time range so it composes additively with every other VM filter; closes the operator query *"which VM is at 192.168.100.42 right now?"* that `?nat_static_ip=` (5.4.79) cannot answer for DHCP-assigned VMs because those VMs have an empty `spec.nat_static_ip` — covers the runtime IP axis (DHCP + static) where `?nat_static_ip=` only covers configured-static; CLI flag: `--ip`), `?auto_start=true|false` and `?locked=true|false` (tristate boolean exact-match on the VM's `auto_start` / `locked` flag; case-insensitive `true`/`false` with `1`/`0` aliases; whitespace-trimmed; empty disables the filter; anything else returns 400 `invalid_auto_start` / `invalid_locked`), `?since=<rfc3339>` / `?until=<rfc3339>` (inclusive bounds on the VM's `created_at`; whitespace-trimmed; invalid values return 400 `invalid_since` / `invalid_until`; a VM with a zero `created_at` is filtered OUT whenever any bound is set — mirrors the snapshot / image time-range filters), `?min_cpus=<n>` / `?max_cpus=<n>` (inclusive integer range on the VM's `spec.cpus` vCPU count; whitespace-trimmed; empty disables the bound; non-numeric or negative values return 400 `invalid_min_cpus` / `invalid_max_cpus`; no zero-value exclusion — mirrors the image `?min_size=` / `?max_size=` byte-range filter; closes the "show me every VM with ≥ 8 vCPUs" capacity-audit query that `?search=` / `?tag=` cannot answer), `?min_ram_mb=<n>` / `?max_ram_mb=<n>` (inclusive integer range on the VM's `spec.ram_mb` RAM in MB; whitespace-trimmed; empty disables the bound; non-numeric or negative values return 400 `invalid_min_ram_mb` / `invalid_max_ram_mb`; no zero-value exclusion — the symmetric RAM counterpart to `?min_cpus=` / `?max_cpus=`, applied immediately after it; closes the "show me every VM with ≥ 8 GB RAM" capacity-audit query), `?min_disk_gb=<n>` / `?max_disk_gb=<n>` (inclusive integer range on the VM's `spec.disk_gb` disk in GB; whitespace-trimmed; empty disables the bound; non-numeric or negative values return 400 `invalid_min_disk_gb` / `invalid_max_disk_gb`; no zero-value exclusion — the symmetric disk counterpart to `?min_ram_mb=` / `?max_ram_mb=`, applied immediately after them; closes the "show me every VM with ≥ 100 GB disk" capacity-audit query and completes the cpus/ram/disk capacity-audit trio on the VM list). Sorting: `?sort=<id|name|created_at|state|cpus|ram_mb|disk_gb|ip|image|default_user|gpu>` (default `id`) and `?order=<asc|desc>` (default `asc`); unknown values return 400 `invalid_sort` / `invalid_order`. `cpus` / `ram_mb` / `disk_gb` are numeric capacity axes over `spec.cpus` / `spec.ram_mb` / `spec.disk_gb` (asc = smallest first, desc = largest first) — they complete the symmetry with the existing `?min_cpus=` / `?max_cpus=` / `?min_ram_mb=` / `?max_ram_mb=` / `?min_disk_gb=` / `?max_disk_gb=` range filters so the same capacity-audit query can be sorted as well as filtered. `ip` is a numeric IP-address sort axis over the runtime `vm.ip` field — parsed with `net.ParseIP` and compared byte-wise on the canonical 16-byte `To16()` form so `192.168.100.2` sorts before `192.168.100.10` instead of lexicographically; VMs with an empty or unparseable IP (stopped, no DHCP lease, no static IP) sink to the tail of `asc` and the head of `desc`, mirroring the nil-trailing semantics on the schedule `last_fired_at` / `next_fire_at` sort axes; it is the symmetric sort counterpart to the `?ip=` filter (5.4.81) so the same runtime-IP cohort can be both sorted and narrowed. `image` (5.4.88) is a case-insensitive sort axis over `spec.image` and the symmetric sort counterpart to the case-insensitive `?image=` filter (5.4.22) so the same base-image cohort can be both filtered and sorted on the same column; VMs with an empty `spec.image` sink to the tail of `asc` and the head of `desc`, mirroring the nil-trailing semantics on every other nullable sort axis (ip, guest_ip, last_fired_at, last_delivery_at, actor). `default_user` (5.4.91) is a case-insensitive sort axis over `spec.default_user` and the symmetric sort counterpart to the case-insensitive `?default_user=` filter (5.4.23) so the same default-user cohort can be both filtered and sorted on the same column; **diverges from the nil-trailing convention on every other nullable axis** because this column has a documented default — an empty stored `spec.default_user` resolves to `root` (mirrors `lifecycle.go`'s runtime semantics and the `?default_user=root` empty-means-root filter contract) so empty VMs collate with explicit-root VMs in alphabetical order rather than sinking to the tail. `gpu` (5.7.13) is a lexicographic sort axis over each VM's smallest assigned GPU PCI address (canonical long form via `NormalizePCIAddress`, so a VM persisted with `01:00.0` collates identically to one persisted with `0000:01:00.0`) and the symmetric sort counterpart to the `?gpu=` filter (5.7.9) so the same passthrough cohort can be both filtered and sorted on the same column; VMs with no requested GPUs sink to the tail of `asc` and the head of `desc`, mirroring the nil-trailing semantics on every other nullable axis (ip, guest_ip, image, actor, last_fired_at, last_delivery_at). All comparators tiebreak on `id` so paginated responses are deterministic. CLI mirrors via `vmsmith vm list --tag --status --search --image --default-user --os-type --os-variant --firmware --disk-bus --nic-model --machine --clock-offset --network --prefix --nat-static-ip --nat-gateway --ip --auto-start --locked --since --until --min-cpus --max-cpus --min-ram-mb --max-ram-mb --min-disk-gb --max-disk-gb --sort --order --limit --offset`
POST   /vms                            Create VM (VMSpec JSON body: name, image, cpus, ram_mb, disk_gb, ssh_pub_key, default_user, networks, auto_start, locked, os_type, os_variant, admin_password, clock_offset, disk_bus, nic_model, machine, firmware, virtio_win_iso, gpus; VM names must be unique, 1-64 chars, alphanumeric/hyphen). When `auto_start=true`, the daemon will start this VM automatically at boot via the auto-start sweep. When `locked=true`, the VM is delete-protected; deletion returns HTTP 409 `vm_locked`. `os_type` selects the guest family (`linux` default, or `windows`); a `windows` guest gets a Windows-tuned domain (SATA disk, e1000e NIC, localtime clock, Hyper-V enlightenments, USB tablet, QXL video, attached virtio-win ISO when `storage.virtio_win_iso` is set/probed) and is provisioned via a cloudbase-init NoCloud datasource instead of cloud-init. `os_variant` (windows-10/11, windows-server-2019/2022/2025) is advisory; `admin_password` is write-only (injected into the datasource, then redacted from the stored/returned record) — when omitted on a Windows create, the daemon auto-generates a 20-char password and surfaces it **once** as `generated_admin_password` on the create response (CLI banner / GUI one-time-reveal modal); the field never appears on Get/List and is never persisted (5.6.17, see `docs/WINDOWS_GUESTS.md`). `clock_offset` (`utc` / `localtime`, case-insensitive; empty resolves to the OS-family default — utc for Linux, localtime for Windows) overrides the libvirt `<clock offset='...'>` so operators can pin a Windows guest to `utc` (NTP-synced fleet) or a Linux guest to `localtime` (RTC-shared dual-boot); 400 `invalid_clock_offset` on any other value. Per-VM device overrides (5.6.15) — all optional, baked at create time, falling back to the OS-family default when omitted: `disk_bus` (`virtio` / `sata`; flips the disk-target letter + cloud-init cdrom slot accordingly; 400 `invalid_disk_bus`), `nic_model` (`virtio` / `e1000e`; applied to every `<interface>` entry including extras; 400 `invalid_nic_model`), `machine` (libvirt machine type, default `pc-q35-6.2`; conservative `[A-Za-z0-9._-]+` alphabet; 400 `invalid_machine`), `firmware` (`bios` default / `uefi` / `ovmf` — uefi+ovmf both map to libvirt's `firmware='efi'` shorthand, required for Windows 11; does NOT enable Secure Boot or vTPM, see 5.6.9; 400 `invalid_firmware`), `virtio_win_iso` (per-VM driver ISO path overriding the daemon-wide `storage.virtio_win_iso`; missing override logs a warning and falls back to the daemon config rather than failing the create). Windows guests require ram_mb ≥ 2048 and disk_gb ≥ 32. Invalid values → 400 `invalid_os_type` / `invalid_os_variant`. See `docs/WINDOWS_GUESTS.md`. `gpus` is an optional list of host PCI GPU addresses (long `0000:01:00.0` or short `01:00.0` form) to pass through via VFIO — each requested GPU is expanded to its full IOMMU group (GPU + companion functions like HDMI audio, bridges excluded) and emitted as managed='yes' `<hostdev>` entries so libvirt rebinds the devices to vfio-pci at VM start; invalid addresses → 400 `invalid_gpu`; discover assignable GPUs via `GET /host/gpus`; host must have IOMMU enabled and the GPU free of the host console; see `docs/GPU_PASSTHROUGH.md`. CLI: `vmsmith vm create --os windows --os-variant <v> --admin-password <pw> --clock-offset utc|localtime --disk-bus virtio|sata --nic-model virtio|e1000e --machine <type> --firmware bios|uefi|ovmf --virtio-win-iso <path> --gpu <pci-addr>`.
GET    /vms/{id}                       Get VM
POST   /vms/{id}/clone                 Clone VM (body: `{ "name": "clone-name" }`; validates the new name and returns the cloned VM in stopped state)
PATCH  /vms/{id}                       Update VM resources (VMUpdateSpec: cpus, ram_mb, disk_gb, nat_static_ip, nat_gateway, auto_start, locked, clock_offset, disk_bus, nic_model — zero/empty ignored; `auto_start` and `locked` use `*bool` so omit them to keep the current value; disk grow-only; IP change updates DHCP reservation + regenerates cloud-init ISO with new instance-id; `clock_offset` / `disk_bus` / `nic_model` are `*string` so omit = no change, pointer-to-`""` clears the override and falls back to the OS-family default at next render. Allowed values: `clock_offset` ∈ {utc, localtime} (else 400 `invalid_clock_offset`); `disk_bus` ∈ {virtio, sata} (else 400 `invalid_disk_bus`); `nic_model` ∈ {virtio, e1000e} (else 400 `invalid_nic_model`). Roadmap 5.6.12 — `disk_bus` / `nic_model` are the mutable surface behind the switch-to-virtio helper (`vmsmith vm set-virtio`) so a Windows guest can be flipped to virtio after the operator installs the virtio drivers in-guest. `os_type` / `os_variant` are **immutable** — sending either (including the empty string) returns 400 `os_type_immutable` because the OS family drives the device profile at create time. GPU passthrough is also create-time only: PATCH does not accept `gpus`, so changing or clearing GPU assignment requires creating/cloning a new VM definition. CLI: `vmsmith vm edit <id> [--clock-offset utc|localtime|""] [--disk-bus virtio|sata|""] [--nic-model virtio|e1000e|""]`.
POST   /vms/{id}/set-virtio (CLI only)  Convenience shortcut implemented as a CLI command `vmsmith vm set-virtio <id>` that PATCHes both `disk_bus` and `nic_model` to `virtio` atomically (roadmap 5.6.12). No dedicated HTTP route — the existing PATCH path is the API surface.
POST   /vms/{id}/start                 Start VM
POST   /vms/{id}/stop                  Stop VM
POST   /vms/{id}/force-stop            Force-stop VM (immediate `dom.Destroy()`, skips ACPI shutdown — equivalent to pulling the power cord). Returns HTTP 409 `vm_already_stopped` when the VM is not running. CLI: `vmsmith vm force-stop <id>`.
POST   /vms/{id}/restart               Restart VM (graceful stop, 30s grace before forced destroy, then start)
POST   /vms/{id}/reboot                Reboot a running VM via libvirt's `dom.Reboot()` (ACPI signal to guest, no power cycle). Preserves IP / MAC / DHCP reservation; differs from restart which is stop+start. Returns HTTP 409 `vm_not_running` when the VM is not running. CLI: `vmsmith vm reboot <id>`.
POST   /vms/{id}/suspend               Suspend a running VM (libvirt pause): freezes CPU + memory without releasing host resources. State becomes `paused`. Returns HTTP 409 `vm_not_running` when stopped or `vm_already_paused` when already paused.
POST   /vms/{id}/resume                Resume a paused VM, restoring it to `running`. Returns HTTP 409 `vm_not_paused` if the VM is not currently paused.
POST   /vms/bulk                       Apply a lifecycle action to many VMs in one call. Body: `{"action": "start|stop|delete|restart|force-stop|reboot|suspend|resume", "ids": [...]}`. Returns `{"action": "<verb>", "results": [{id, success, code?, message?}]}` so partial failures (one VM in the wrong state, the rest succeeded) surface together. Each successful per-VM action emits the matching `vm.<action>_requested` (or `vm.deleted`) event with `bulk=true`. CLI mirrors via `vmsmith vm start|stop|restart|force-stop|reboot|suspend|resume --all [--tag <t>]`.
DELETE /vms/{id}                       Delete VM (returns HTTP 409 `vm_locked` if `Spec.Locked=true`; unlock first via PATCH or `vmsmith vm unlock`)
GET    /vms/{id}/snapshots             List snapshots (each entry carries `name`, optional `description`, optional `tags []string`, and a libvirt-parsed `created_at`). Sorting: `?sort=<id|name|created_at>` (default `id`) and `?order=<asc|desc>` (default `asc`); unknown values return 400 `invalid_sort` / `invalid_order`. Within a VM the snapshot ID is `<vmID>/<name>` so id-asc == name-asc; all comparators tiebreak on `name` so paginated responses are deterministic. Filtering: `?tag=<tag>` is a case-insensitive exact-match (a snapshot matches when any of its tags equals the value) applied **before** `?prefix=`; `?prefix=<value>` is a case-sensitive `HasPrefix(snap.name, prefix)` filter applied between `?tag=` and the time-range — mirrors the `prefix` selector on `POST /vms/{vmID}/snapshots/bulk_delete` so the same query an operator runs to inspect (`?prefix=auto-nightly-`) round-trips 1:1 with the bulk-delete request body and closes the "preview the cohort before deleting" operator query that `?search=` (which does fuzzy substring matching) can't answer; whitespace-trimmed; empty disables; `?since=<rfc3339>` / `?until=<rfc3339>` form an inclusive time-range filter on `created_at` (whitespace-trimmed; invalid values return 400 `invalid_since` / `invalid_until`; snapshots with a zero `created_at` are filtered OUT when any bound is set); `?search=<text>` is a case-insensitive substring match across `name`, `description`, and `tags` (the haystack excludes the snapshot ID and `vm_id` to avoid noisy false positives on short numeric queries); whitespace-trimmed; applied before sort + pagination so `X-Total-Count` reflects the post-filter / post-search count. Filters compose additively with `sort` / `order` / pagination. CLI mirrors via `vmsmith snapshot list <vm-id> --sort --order --search --tag --prefix --since --until`.
POST   /vms/{id}/snapshots             Create snapshot (body: `{ "name": "...", "description": "...", "tags": ["..."] }` — description optional ≤1024 chars; tags optional, lowercased + deduped + alphabetised server-side via the shared `[a-z0-9][a-z0-9._:-]*` alphabet; the description round-trips through libvirt's `<description>` element while tags persist in the `snapshots` bbolt bucket out-of-band because the libvirt domainsnapshot XML schema does not permit `<metadata>`).
POST   /vms/{id}/snapshots/bulk_delete Delete multiple snapshots in a single request. Body: `{"names": [...]}` or `{"prefix": "..."}` (exactly one). Returns `{"results": [{name, success, code?, message?}]}`. Emits one `snapshot.deleted` event per successful target with `bulk=true`. CLI: `vmsmith snapshot delete <vm-id> --prefix <s>`.
POST   /vms/{id}/snapshots/{name}/restore  Restore snapshot
PATCH  /vms/{id}/snapshots/{name}      Update snapshot metadata. Body: `{"description": "...", "tags": [...]}` — both fields use pointer semantics (omit = leave unchanged; `"description": ""` clears the description; `"tags": []` clears every tag). Description is persisted via libvirt's `SnapshotCreateXML(REDEFINE)` so the snapshot's disk/memory state, parent pointer, and creation timestamp are preserved; tags are persisted in the `snapshots` bbolt bucket out-of-band. Emits `snapshot.updated`. CLI: `vmsmith snapshot edit <vm-id> <snap-name> [--description "..."] [--tag <t> ...] [--clear-tags]` (`--tag` / `--clear-tags` mutually exclusive).
DELETE /vms/{id}/snapshots/{name}      Delete snapshot
GET    /images                         List images (`?page=<n>&per_page=<n>`; `?tag=<tag>` filters case-insensitively; `?source_vm=<vm-id>` case-insensitive exact-match against the `source_vm` field — i.e. the VM ID the image was exported from; whitespace-trimmed; empty disables; `?search=<q>` is a case-insensitive substring filter across `name`, `description`, and `tags` — whitespace is trimmed and ID is intentionally excluded; `?since=<rfc3339>` / `?until=<rfc3339>` form an inclusive time-range filter on `created_at` (whitespace-trimmed; invalid values return 400 `invalid_since` / `invalid_until`; images with a zero `created_at` are filtered OUT when any bound is set); `?min_size=<bytes>` / `?max_size=<bytes>` form an inclusive byte-range filter on `size_bytes` (whitespace-trimmed; empty disables; non-numeric or negative values return 400 `invalid_min_size` / `invalid_max_size`; no zero-value exclusion — a zero-byte size matches whenever it falls in the window); `?prefix=<value>` is a case-sensitive `HasPrefix(img.Name, prefix)` filter (whitespace-trimmed; empty disables; mirrors the snapshot list `?prefix=` (5.4.75) and the VM list `?prefix=` (5.4.76) so the same fleet-audit query — `?prefix=rocky-` — round-trips 1:1 across the three name-prefix axes; applied right after the size-range filter and before the time-range filter so it composes additively with every other image filter); returns `X-Total-Count`). Sorting: `?sort=<id|name|size|created_at>` (default `id`) and `?order=<asc|desc>` (default `asc`); unknown values return 400 `invalid_sort` / `invalid_order`. Filters compose additively with sort and pagination. All comparators tiebreak on `id` so paginated responses are deterministic. CLI mirrors via `vmsmith image list --sort --order --tag --source-vm --search --prefix --since --until --min-size --max-size --limit --offset`
POST   /images                         Create image from VM (`vm_id`, `name`, optional `description`, optional `tags[]`)
POST   /images/upload                  Upload qcow2 file (multipart `file` + `name` + optional `description` + repeated `tags` form fields, or a single comma-separated `tags` value)
PATCH  /images/{id}                    Update image `description` and/or `tags`. Empty description = no change; nil tags = no change; `[]` clears tags.
DELETE /images/{id}                    Delete image
POST   /images/bulk_delete             Delete multiple images in a single request. Body: `{"ids": [...]}` or `{"tag": "..."}` (exactly one). Tag matching is case-insensitive. Returns `{"results": [{id, success, code?, message?}]}`. Emits one `image.deleted` event per successful target with `bulk=true`. CLI: `vmsmith image delete --tag <tag>`.
GET    /images/{id}/download           Download image file
GET    /vms/{id}/ports                 List port forwards. Filters: `?tag=<tag>` (case-insensitive exact match, applied before protocol + search + sort), `?protocol=<tcp|udp>` (case-insensitive exact match against the rule's transport protocol; empty disables; anything other than tcp/udp returns 400 `invalid_protocol`; mirrors the bulk_delete `protocol` selector), `?min_host_port=<n>` / `?max_host_port=<n>` (inclusive numeric range on the rule's `host_port`; whitespace-trimmed; empty disables the bound; non-numeric or negative values return 400 `invalid_min_host_port` / `invalid_max_host_port`; no zero-value exclusion — mirrors the image `?min_size=`/`?max_size=` range filter; applied after `protocol` and before `search`), `?min_guest_port=<n>` / `?max_guest_port=<n>` (inclusive numeric range on the rule's `guest_port`; the symmetric counterpart to the host-port range — same whitespace-trim / empty-disables / 400 `invalid_min_guest_port` / `invalid_max_guest_port` contract; applied right after the host-port range and before `search`), `?guest_ip=<addr>` (case-insensitive exact-match on the rule's `guest_ip`; whitespace-trimmed; empty disables; no validation rejection — `guest_ip` is a free-form value operators paste verbatim and garbage simply matches no rules; closes the multi-NIC audit query *"show me every forward landing on 192.168.100.50"* that `?search=` can only fuzzy-match — `192.168.100.5` also surfaces when searching for `192.168.100.50`; applied right after the guest-port range and before `search` so it composes additively with every other filter; `X-Total-Count` reflects the post-filter population; CLI flag: `--guest-ip`). Sorting: `?sort=<id|host_port|guest_port|protocol|description|guest_ip>` (default `id`) and `?order=<asc|desc>` (default `asc`). The `description` sort is case-insensitive; `guest_ip` (5.4.86) is the symmetric sort counterpart to the `?guest_ip=` filter (5.4.73) and uses numeric IP comparison via `net.ParseIP` + `bytes.Compare` on the canonical 16-byte `To16()` form so `192.168.100.2` sorts before `192.168.100.10` instead of lexicographically; rules with an empty or unparseable `guest_ip` sink to the tail of `asc` and the head of `desc`, mirroring the nil-trailing semantics on the VM list `ip` sort axis (5.4.85) and the schedule `last_fired_at` / `next_fire_at` sort axes; unknown values return 400 `invalid_sort` / `invalid_order` with the error message advertising the full supported set. All comparators tiebreak on `id` so repeated requests return a deterministic order. Free-text filter: `?search=<text>` — case-insensitive substring match across `description`, `protocol`, `host_port`, `guest_port`, `guest_ip`, and `tags`; whitespace-trimmed; rule ID and `vm_id` are intentionally excluded from the haystack. Pagination: `?page=<n>&per_page=<n>` (or `?limit=<n>` as a synonym for `per_page`); applied after filter + sort so the `X-Total-Count` header reflects the post-filter / pre-pagination population. Filters compose additively with `sort` / `order`. CLI mirrors via `vmsmith port list <vm-id> --sort --order --search --tag --protocol --min-host-port --max-host-port --min-guest-port --max-guest-port --guest-ip --limit --offset`
POST   /vms/{id}/ports                 Add port forward (`host_port`, `guest_port`, `protocol?`, `description?`, `tags?` — `description` ≤256 chars; `tags[]` is optional, normalised lowercase + deduped + alphabetised, 1-32 chars per tag using the `[a-z0-9][a-z0-9._:-]*` alphabet shared with every other tagged resource)
POST   /vms/{id}/ports/bulk_delete     Delete multiple port forwards in a single request. Body: `{"ids": [...]}` or `{"protocol": "tcp"|"udp"}` (exactly one). Protocol is always scoped to the URL VM. Returns `{"results": [{id, success, code?, message?}]}`. Emits one `port_forward.removed` event per successful target with `bulk=true`. CLI: `vmsmith port remove --vm <id> [--protocol tcp|udp]`.
PATCH  /vms/{id}/ports/{portId}        Update editable port-forward metadata (currently `description` ≤256 chars and `tags[]`). Body: `{"description": "..."}` / `{"tags": [...]}` — empty description clears, empty `tags: []` clears the tag set, missing/null leaves unchanged. The iptables 5-tuple (host_port/guest_port/guest_ip/protocol) is intentionally immutable. The URL VM is the authoritative scope: a `portId` belonging to a different VM returns 404 `resource_not_found`. Emits `port_forward.updated`. CLI: `vmsmith port edit <port-id> --description "..." --tag <t> --clear-tags`.
DELETE /vms/{id}/ports/{portId}        Remove port forward
GET    /templates                      List templates. Filters: `?tag=<tag>` (case-insensitive, applied before sort + pagination), `?search=<q>` (case-insensitive substring across `name`, `description`, and `tags`; whitespace-trimmed; ID / image / default_user / networks intentionally excluded from the haystack), `?image=<value>` (case-insensitive exact-match against the template's `image` field; whitespace-trimmed; empty value disables the filter; applied after `?tag=` and before `?search=` so they compose additively — `X-Total-Count` reflects the post-filter population), `?default_user=<value>` (case-insensitive exact-match against the template's `default_user` field; whitespace-trimmed; empty disables; applied after `?image=`; unlike the VM `?default_user=` filter there is NO empty-means-root fallback — a template's empty `default_user` means "use the image's built-in user" so an empty stored value never matches a non-empty query), `?os_type=<linux|windows>` (case-insensitive exact-match on the template's resolved OS family via `VMTemplate.ResolvedOSType`; whitespace-trimmed; empty disables; an empty stored `os_type` resolves to `linux` (mirrors VM semantics — OS family is a closed two-member axis with a documented default, so unlike `default_user` it must belong to one bucket); any value other than `linux`/`windows` returns 400 `invalid_os_type`; CLI flag: `--os-type`. Templates inherit `os_type`/`os_variant` to derived VMs when the create request leaves them empty — see 5.6.7), `?os_variant=<windows-10|windows-11|windows-server-2019|windows-server-2022|windows-server-2025>` (case-insensitive exact-match on the template's `os_variant` field; whitespace-trimmed; empty disables; any value outside the five known variants returns 400 `invalid_os_variant`; the sub-axis of `?os_type=windows` on the template cohort — mirrors the VM `?os_variant=` filter (5.4.66) so the same fleet-audit query can be sliced by Windows edition across both VMs and templates; unlike `?os_type=linux` (which matches empty-stored via the linux default) there is NO documented default — an empty stored `os_variant` means "operator did not specify an edition" and is filtered OUT whenever the filter is set, mirroring the webhook `?event_type=` membership semantics; applied right after `?os_type=` and before `?network=` so it composes additively with every other template filter; CLI flag: `--os-variant`), `?network=<name>` (case-insensitive exact-match (any-of) against the name of any of the template's additional network attachments `networks[].name`; whitespace-trimmed; empty disables; applied after `?default_user=`; mirrors the VM `?network=` filter (5.4.36) — the implicit primary NAT network is NOT matched since it is not represented in `networks`, so this only scopes to explicitly-attached extra networks like `data-net` / `storage-net`), `?prefix=<value>` (case-sensitive `HasPrefix(tpl.name, prefix)` filter — 5.4.78; the fourth and final member of the name-prefix filter family alongside snapshots (5.4.75), VMs (5.4.76), and images (5.4.77); whitespace-trimmed; empty disables; applied between `?network=` and the time-range filter so the same cohort-discrimination query (`?prefix=rocky9-base-`, `?prefix=web-prod-`, `?prefix=auto-nightly-`) round-trips 1:1 across all four name-prefix axes; case-sensitive because template names share the case-sensitive `[A-Za-z0-9-]` alphabet with VM names; CLI flag: `--prefix`), `?since=<rfc3339>` / `?until=<rfc3339>` (inclusive bounds on `created_at`; whitespace-trimmed; empty disables; invalid values return 400 `invalid_since` / `invalid_until`; templates with a zero `created_at` are filtered OUT when any bound is set; applied between `?network=` and `?search=` so the post-filter `X-Total-Count` stays correct), `?min_cpus=<n>` / `?max_cpus=<n>` (inclusive integer range on the template's `cpus` field; whitespace-trimmed; empty disables the bound; non-numeric or negative values return 400 `invalid_min_cpus` / `invalid_max_cpus`; no zero-value exclusion — mirrors the VM `?min_cpus=` / `?max_cpus=` capacity-audit filter (5.4.44); applied immediately after the time-range filter and before `?search=`), `?min_ram_mb=<n>` / `?max_ram_mb=<n>` (inclusive integer range on the template's `ram_mb` field; whitespace-trimmed; empty disables the bound; non-numeric or negative values return 400 `invalid_min_ram_mb` / `invalid_max_ram_mb`; no zero-value exclusion — mirrors the VM `?min_ram_mb=` / `?max_ram_mb=` capacity-audit filter (5.4.48); applied immediately after the vCPU range filter and before `?search=`), `?min_disk_gb=<n>` / `?max_disk_gb=<n>` (inclusive integer range on the template's `disk_gb` field; whitespace-trimmed; empty disables the bound; non-numeric or negative values return 400 `invalid_min_disk_gb` / `invalid_max_disk_gb`; no zero-value exclusion — completes the cpus/ram/disk capacity-audit trio on the template list alongside the vCPU/RAM range filters; mirrors the VM `?min_disk_gb=` / `?max_disk_gb=` filter (5.4.50); applied immediately after the RAM range filter and before `?search=`; closes the "show me every template that provisions ≥ 100 GB disk VMs" capacity-audit query against the template cohort). Sorting: `?sort=<id|name|created_at|cpus|ram_mb|disk_gb|image|default_user>` (default `id`) and `?order=<asc|desc>` (default `asc`); unknown values return 400 `invalid_sort` / `invalid_order`. `cpus` / `ram_mb` / `disk_gb` are numeric capacity axes over the template's `cpus` / `ram_mb` / `disk_gb` fields (asc = smallest first, desc = largest first) — they complete the symmetry with the existing `?min_cpus=` / `?max_cpus=` / `?min_ram_mb=` / `?max_ram_mb=` / `?min_disk_gb=` / `?max_disk_gb=` range filters so the same capacity-audit query can be sorted as well as filtered on the template cohort, mirroring the VM list (5.4.56). `image` (5.4.89) is the symmetric sort counterpart to the `?image=` exact-match filter on the same column — case-insensitive comparison mirrors the case-insensitive `?image=` filter contract, so operators paste base-image names verbatim from the images directory (`rocky9.qcow2`, `Rocky9.qcow2`) without the cohort splitting across multiple buckets; templates with an empty `image` sort to the tail of `asc` / head of `desc`, mirroring the nil-trailing semantics on every other nullable sort axis (the VM list `image` axis at 5.4.88 / `ip` / `guest_ip` / `last_fired_at` / `last_delivery_at` / `actor`). `default_user` (5.4.92) is the symmetric sort counterpart to the `?default_user=` exact-match filter on the same column — case-insensitive comparison mirrors the filter contract; diverges from the VM list `default_user` sort axis (5.4.91), which collapses empty → "root", because templates store an empty `default_user` as "use the image's built-in user" (cloud-init's `cloud-user` / `ec2-user` / `ubuntu`), not root, so templates with an empty `default_user` sort to the tail of `asc` / head of `desc`, mirroring the template `image` axis nil-trailing semantics. All comparators tiebreak on `id` so paginated responses are deterministic. Filters compose additively with sort and pagination. Pagination: `?page=<n>&per_page=<n>`; returns `X-Total-Count`. CLI mirrors via `vmsmith template list --tag --search --image --default-user --os-type --os-variant --network --prefix --since --until --min-cpus --max-cpus --min-ram-mb --max-ram-mb --min-disk-gb --max-disk-gb --sort --order --limit --offset`
POST   /templates                      Create template (CreateTemplateRequest body; rejects duplicate names)
PATCH  /templates/{id}                 Update template description / tags (TemplateUpdateSpec: empty `description` = no change; nil `tags` = no change; explicit `[]` clears the tag set; image, resources, name, networks immutable post-create)
DELETE /templates/{id}                 Delete template
POST   /templates/bulk_delete          Delete multiple templates in a single request. Body: `{"ids": [...]}` or `{"tag": "..."}` (exactly one). Tag matching is case-insensitive. Returns `{"results": [{id, success, code?, message?}]}`. Emits one `template.deleted` event per successful target with `bulk=true`. CLI: `vmsmith template delete --tag <tag>`.
GET    /host/interfaces                List host network interfaces
GET    /host/gpus                      List host GPUs (PCI display controllers) available for VFIO passthrough. Each entry: `{address, vendor_id, device_id, vendor, class, driver, iommu_group, group_devices[]}`. `group_devices` lists every assignable PCI function sharing the GPU's IOMMU group (GPU + HDMI audio; bridges excluded) — the set vmsmith attaches when the GPU is requested. Read via sysfs (`/sys/bus/pci/devices` + `/sys/kernel/iommu_group`); returns `[]` on a host with no GPU / no IOMMU. CLI: `vmsmith host gpus` (`--json` for scripting). See `docs/GPU_PASSTHROUGH.md`.
GET    /host/stats                     Host capacity + utilisation snapshot used by the GUI Dashboard cards. Returns `vm_count`, `cpu` / `ram` / `disk` `HostResourceUsageSummary` (RAM + disk in bytes, CPU as `0-100` percentage), and `event_stream_connections` (active SSE clients). CLI mirrors via `vmsmith host stats` (thin HTTP client; `--json` for raw pass-through).
GET    /quotas/usage                   Current quota allocation vs configured caps for `vms` / `cpus` / `ram_mb` / `disk_gb` / `gpus` (5.7.11 — aggregate VFIO passthrough GPU count across all VMs, cap `quotas.max_total_gpus`). `limit: 0` means uncapped. GPU assignment is immutable post-create (5.7.4) so PATCH /vms/{id} never affects the GPU counter. CLI mirrors via `vmsmith host quotas` (thin HTTP client; `--json` for raw pass-through).
POST   /vms/{id}/console/ticket        Issue a single-use console ticket. Returns `{ticket, expires_at, websocket_url}`. 404 on unknown VM, 409 `vm_not_running` when VM is not running, 503 `service_unavailable` when the console subsystem is disabled.
GET    /vms/{id}/console               Websocket proxy for the live VM console. Requires `?ticket=` from the ticket-issuance endpoint, negotiates websocket subprotocol `binary`, returns 401 on missing/expired/reused ticket, 403 `mixed_content_blocked` when TLS is enabled but the websocket is not `wss` (or reverse-proxy `X-Forwarded-Proto: https`), 429 `console_session_limit_reached` when the daemon is at its configured concurrent-session cap, 502 `console_unreachable` when the loopback VNC socket cannot be reached, and 503 when the console subsystem is disabled or the live console endpoint is unavailable.
GET    /logs                           Query log entries. Filters: `level`, `source`, `limit`/`per_page`, `?vm_id=<id>` — exact-match against the entry's structured `vm_id` field (case-sensitive — VM IDs are opaque `vm-<unix-nano>` strings; whitespace-trimmed; empty = no filter), and `?search=<q>` — a case-insensitive substring match across the entry's `message`, `source`, `level`, and every *value* in the structured fields map. Whitespace-trimmed; field **keys** are intentionally excluded (small repeating vocabulary like `vm_id` / `method` / `error` would generate noisy matches). Time range: `?since=<rfc3339>` (strict-after — preserves the legacy `logger.Entries` contract) / `?until=<rfc3339>` (inclusive at-or-before — mirrors the snapshot / image / VM / template / webhook time-range filters); whitespace-trimmed; empty disables the bound; invalid values return 400 `invalid_since` / `invalid_until`. Sorting: `?sort=<timestamp|level|source|vm_id>` (default `timestamp`) and `?order=<asc|desc>` (default `asc` — preserves the legacy oldest-first contract). `level` orders by severity rank (debug < info < warn < error), not alphabetically; `source` matches case-insensitively. `vm_id` (5.4.94) is the symmetric sort counterpart to the `?vm_id=` exact-match filter (5.4.18) so the same per-VM log cohort can be both filtered and sorted on the same column — case-sensitive (VM IDs are opaque `vm-<unix-nano>` strings); entries with no `vm_id` field (host-level lines like `daemon startup`) sink to the tail of `asc` and the head of `desc`, mirroring the events `vm_id` sort axis (5.4.93) and every other nullable sort axis (ip, guest_ip, last_fired_at, last_delivery_at). Unknown values return 400 `invalid_sort` / `invalid_order`. All comparators tiebreak on timestamp+source so paginated responses are deterministic. Filters compose additively, so `X-Total-Count` reflects the post-search population. CLI mirrors via `vmsmith logs list --level --source --vm-id --since --until --search --sort --order --limit --page --fields` (talks to the daemon's ring buffer over HTTP; reuses the persistent `--api-url` / `--api-key` flags).
GET    /events                         List events (newest-first by default). Filters: `?vm_id=`, `?type=`, `?source=`, `?severity=` (exact match), `?min_severity=<info|warn|error>` — a severity floor (info < warn < error) returning events ranked at-or-above the value, closing the "show me everything that needs attention (warn + error)" query the exact-match `?severity=` can't answer; whitespace-trimmed, case-insensitive, empty disables, unknown values return 400 `invalid_min_severity`; composes additively with `?severity=`; mirrors the logs `level` severity-floor. `?actor=<name>` — case-sensitive exact-match against the event's `actor` field (e.g. `system`, `app`, or an API-key alias); whitespace-trimmed; empty disables the filter; mirrors `?vm_id=`'s contract (case-insensitive matching is the job of `?search=`, not the exact-match filter). `?resource_id=<id>` (exact match, case-sensitive — mirrors the `vm_id` / `actor` contract; operators reference opaque server-issued IDs verbatim). `?type_prefix=<prefix>` — case-insensitive prefix match on the event's `type` field (e.g. `?type_prefix=snapshot.` matches every `snapshot.*` subtype, `?type_prefix=vm.` matches every `vm.*` subtype) so operators can slice an entire event family without enumerating each fully-qualified type. Whitespace is trimmed and the prefix is lowercased before comparison; empty value disables the filter. `?since=<rfc3339>`, `?until=<seq>`, and `?search=<text>` — a case-insensitive substring match across `message`, `type`, `source`, `severity`, `actor`, `vm_id`, `resource_id`, and every value in `attributes`. The numeric event ID is intentionally excluded from the haystack. Sorting: `?sort=<id|occurred_at|type|source|severity|actor|resource_id|vm_id>` (default `id`) and `?order=<asc|desc>` (default `desc`). `type`, `source`, and `severity` match case-insensitively; `actor` (5.4.87) is **case-sensitive** to mirror the case-sensitive `?actor=` exact-match filter contract — operators reference actor identifiers verbatim (e.g. `system`, `app`, `ops-alice`); events with an empty `actor` sort to the tail of `asc` and the head of `desc`, mirroring the nil-trailing semantics on the VM list `ip` axis (5.4.85) and the schedule `last_fired_at` (5.4.84) / `next_fire_at` axes. `resource_id` (5.4.90) is the symmetric sort counterpart to the case-sensitive `?resource_id=` exact-match filter — same case-sensitive comparison, same nil-trailing semantics, so the same operator query that filters to one resource cohort (`?resource_id=snap-prod`) can also order across the whole timeline by resource id; empty resource ids sink to the tail of `asc` and the head of `desc`. `vm_id` (5.4.93) is the symmetric sort counterpart to the case-sensitive `?vm_id=` exact-match filter on the same column — same case-sensitive comparison (VM IDs are opaque `vm-<unix-nano>` strings), same nil-trailing semantics, so the same operator query that filters to one VM cohort can also order across the whole timeline by vm id; events with an empty `vm_id` (host-level events like `system.daemon_started`) sink to the tail of `asc` and the head of `desc`. All comparators tiebreak on `id` so paginated responses are deterministic. Unknown values return 400 `invalid_sort` / `invalid_order`. Filters compose additively; CLI mirrors via `vmsmith events list --actor --resource-id --type-prefix --min-severity --search --sort --order --show-actor --attrs` (and `events follow --actor --resource-id --type-prefix --min-severity --search --show-actor --attrs` for the SSE stream). Opt-in `--show-actor` adds an ACTOR column between TYPE and VM; `--attrs` adds a trailing ATTRIBUTES column that folds in `resource_id` plus sorted `key=value` pairs from the structured `attributes` map. Activity page in the GUI surfaces the same fields via a per-row chevron toggle that expands a details sub-row, plus a debounced "Filter by actor" input next to the VM-ID filter that round-trips through `?actor=` in the URL, a separate "Filter by resource ID" input that round-trips through `?resource_id=`, a "Filter by type prefix" input that round-trips through `?type_prefix=`, and a "Min severity" dropdown that round-trips through `?min_severity=`.
GET    /events/stream                  Server-sent events (SSE) live stream. After the `Last-Event-ID` / `?since=<seq>` replay window, frames are pushed in real time as the bus emits them. Server-side filters mirror the predicate set on `GET /events`: `?vm_id=`, `?type=`, `?type_prefix=`, `?source=`, `?severity=`, `?min_severity=`, `?actor=`, `?resource_id=`, `?search=`. Filters apply to both replayed and live events so the SSE stream and the paginated list endpoint agree on membership; non-matching events are dropped server-side so clients tailing a single VM / event family / actor no longer pay for cross-tenant noise. Empty params disable the corresponding predicate; an unknown `?min_severity=` returns 400 before the stream is committed. The pre-existing 410 `event_stream_replay_window_exceeded` short-circuit and the seq-based `?since=` replay cursor are unchanged. CLI: `vmsmith events follow --vm --type --type-prefix --source --severity --min-severity --actor --resource-id --search` (also re-applies the predicate client-side as defense-in-depth). GUI `useEventStream` accepts an optional `filter` object that is forwarded verbatim as query params.
GET    /webhooks                       List registered webhooks (secrets redacted). Filter: `?tag=<tag>` — case-insensitive exact-match on the webhook tag list (matches when any tag equals the value); whitespace-trimmed; applied before `?event_type=` / `?since=` / `?until=` / `?delivery_status=` / `?search=`. Filter: `?event_type=<type>` — case-insensitive exact-match against entries in the webhook's `event_types` filter list (matches when any entry equals the value); whitespace-trimmed; catch-all webhooks (empty `event_types`) are NOT matched, mirroring the bulk_delete `event_type` selector semantics so the list and bulk-action surfaces agree on "explicit-membership" semantics; applied between `?tag=` and `?since=`. Filter: `?since=<rfc3339>` / `?until=<rfc3339>` form an inclusive time-range filter on the webhook's `created_at` (whitespace-trimmed; invalid values return 400 `invalid_since` / `invalid_until`; webhooks with a zero `created_at` are filtered OUT when any bound is set); applied between `?event_type=` and `?last_delivery_since=`. Filter: `?last_delivery_since=<rfc3339>` / `?last_delivery_until=<rfc3339>` (5.4.61) form an inclusive time-range filter on the webhook's `last_delivery_at` (whitespace-trimmed; invalid values return 400 `invalid_last_delivery_since` / `invalid_last_delivery_until`; never-delivered webhooks — zero `last_delivery_at` — are filtered OUT whenever either bound is set, mirroring the `created_at` range's zero-time exclusion; use `?delivery_status=never` when the intent is to *find* never-delivered webhooks; closes the delivery-audit operator query *"which webhooks delivered events in the last hour"* / *"which receivers haven't fired since the maintenance window"* that the categorical `?delivery_status=` and the `?since=`/`?until=` filter on `created_at` cannot answer); applied between the `created_at` range and `?delivery_status=`. Filter: `?delivery_status=<never|healthy|failing>` — categorical filter on the webhook's most-recent delivery classification (5.4.35): `never` = `last_delivery_at` is zero (no attempt yet); `healthy` = last attempt returned 2xx and `last_error` is empty; `failing` = last attempt existed and did not meet the healthy contract (transport error, 4xx, 5xx, 3xx, or 2xx with a stale `last_error`). Case-insensitive; whitespace-trimmed; empty disables; unknown values return 400 `invalid_delivery_status`; applied between `?until=` and `?active=`. Filter: `?active=true|false` (5.4.37) — tristate boolean exact-match on the webhook's `active` flag; case-insensitive `true`/`false` with `1`/`0` aliases; whitespace-trimmed; empty disables; anything else returns 400 `invalid_active`; mirrors the VM `?auto_start=`/`?locked=` tristate filters and closes the "show me only disabled/live webhooks" query that `?delivery_status=` (runtime health) and `?event_type=` (subscription) can't answer; applied between `?delivery_status=` and `?url_prefix=`. Filter: `?url_prefix=<value>` (5.4.83) — case-insensitive `HasPrefix(wh.URL, value)` filter on the webhook's URL; whitespace-trimmed; empty disables; case-insensitive because URL schemes and hosts are case-insensitive per RFC 3986 and matches the existing URL haystack in `WebhookMatchesSearch`; diverges from the case-sensitive name-prefix family (snapshots 5.4.75 / VMs 5.4.76 / images 5.4.77 / templates 5.4.78 / schedules 5.4.82) because URLs are not free-form name alphabets; closes the receiver-cohort operator queries *"which webhooks point to my Slack workspace?"* / *"which receivers fire into our test environment?"* that `?search=` (case-insensitive substring across URL + description + event_types + tags) can answer only with noisy fuzzy matches — `hooks.slack.com` also surfaces a description that name-drops Slack; applied between `?active=` and `?search=` so it composes additively with every other webhook list filter; `X-Total-Count` reflects the post-filter population; CLI flag: `--url-prefix`. Filter: `?search=<text>` — case-insensitive substring match across `url`, `description`, `event_types`, and `tags`; whitespace-trimmed; `id`, `secret`, and `last_error` are intentionally excluded from the haystack. Sorting: `?sort=<id|url|created_at|last_delivery_at>` (default `id`) and `?order=<asc|desc>` (default `asc`); unknown values return 400 `invalid_sort` / `invalid_order`. `url` matches case-insensitively; `last_delivery_at` puts never-delivered webhooks at the tail of `asc` / head of `desc`. All comparators tiebreak on `id` so paginated responses are deterministic. Pagination: `?page=<n>&per_page=<n>` (or `?limit=<n>` as a synonym for `per_page`); applied after filter + sort so the `X-Total-Count` header reflects the post-filter / pre-pagination population. CLI mirrors via `vmsmith webhook list --tag --event-type --since --until --last-delivery-since --last-delivery-until --delivery-status --active --url-prefix --search --sort --order --limit --page`
POST   /webhooks                       Register webhook (URL + HMAC secret + optional `event_types` filters + optional `description` ≤1024 chars + optional `tags[]` normalised lowercase)
PATCH  /webhooks/{id}                  Update editable fields on an existing webhook (`url`, `secret`, `event_types`, `active`, `description`, `tags`). All six fields use pointer semantics — omit a key to leave it unchanged; `event_types: []` clears the filter so the webhook matches every event again; `description: ""` clears the description (trimmed; capped at 1024 chars); `tags: []` clears the tag list (normalised lowercase, deduplicated, alphabetised on persistence); rotating `secret` cannot be cleared (empty string returns 400 `missing_secret`). The in-memory delivery worker is bounced on success so the next event is delivered with the new config. Returns 400 `noop_update` when no editable field is present. CLI: `vmsmith webhook edit <id> [--url --secret --event-types --clear-event-types --active --description --tag --clear-tags]`.
DELETE /webhooks/{id}                  Unregister webhook
POST   /webhooks/bulk_delete           Delete multiple webhooks in a single request. Body: `{"ids": [...]}` or `{"event_type": "..."}` (exactly one). The `event_type` selector matches webhooks whose `event_types` filter list contains this exact value — catch-all webhooks (empty `event_types`) are NOT swept by the categorical selector. Returns `{"results": [{id, success, code?, message?}]}`. CLI: `vmsmith webhook delete --event-type <type>` (alongside the existing `webhook delete <id>`); GUI: Settings page multi-select checkboxes + "Delete selected" button.
POST   /webhooks/{id}/test             Synthesise a `system.webhook_test` event and deliver it once; returns `WebhookTestResult` with success / status_code / duration / error and updates `last_delivery_at` / `last_status` / `last_error`. Used by the Settings page "Test" button. CLI mirrors via `vmsmith webhook test <id>` (thin HTTP client; `--json` for raw pass-through; exits non-zero when delivery failed so it composes with shell `||` pipelines).
GET    /schedules                      List recurring VM-action schedules. Filters (applied before sort + pagination so `X-Total-Count` reflects the post-filter population): `?vm_id=<id>` (exact), `?tag_selector=<tag>` (case-insensitive exact-membership against the schedule's `tag_selector` list — matches when any entry equals the value; whitespace-trimmed; empty disables; schedules with an empty `tag_selector` (vm_id-targeted or all-VMs) are NOT matched, mirroring the webhook `?event_type=` membership semantics; the symmetric counterpart to `?vm_id=` for tag-selector-targeted schedules), `?action=<snapshot|start|stop|restart>` (case-insensitive exact), `?catch_up_policy=<skip|run_once|run_all>` (case-insensitive exact-match on the schedule's catch-up policy; whitespace-trimmed; empty disables; invalid → 400 `invalid_catch_up_policy`; a schedule persisted with an empty policy is treated as `skip` — the engine's default — so `?catch_up_policy=skip` matches it, mirroring the VM `?default_user=root` empty-means-root semantics), `?timezone=<IANA>` (case-sensitive exact-match against the stored `timezone` field — IANA names are case-sensitive: `America/New_York` not `america/new_york`; whitespace-trimmed; empty disables; no default-fallback for empty stored values since the engine's effective default is host-dependent `time.Local`, so operators querying by timezone must supply the literal value they stored; mirrors the `?vm_id=` / `?actor=` / `?resource_id=` exact-match contracts; applied between `?catch_up_policy=` and `?enabled=` so it composes additively with every other schedule filter), `?enabled=true|false` (tristate; invalid → 400 `invalid_enabled`), `?since=<rfc3339>` / `?until=<rfc3339>` (inclusive bounds on `created_at`; whitespace-trimmed; empty disables; invalid → 400 `invalid_since`/`invalid_until`; a schedule with a zero `created_at` is filtered OUT when any bound is set; mirrors the snapshot/image/VM/template/webhook time-range family), `?next_fire_since=<rfc3339>` / `?next_fire_until=<rfc3339>` (inclusive bounds on `next_fire_at` — the cron-computed wall-clock time of the schedule's next planned fire; closes the *"what fires in the next N hours"* operator query that the `next_fire_at` sort axis can order but not narrow; whitespace-trimmed; empty disables; invalid → 400 `invalid_next_fire_since`/`invalid_next_fire_until`; schedules with a nil `next_fire_at` — disabled or stalled — are filtered OUT when any bound is set, mirroring the zero-`created_at` handling), `?last_fired_since=<rfc3339>` / `?last_fired_until=<rfc3339>` (5.4.74) (inclusive bounds on `last_fired_at` — the wall-clock time of the schedule's most recent fire; closes the SRE triage operator queries *"which schedules fired during yesterday's maintenance window"* / *"which schedules haven't fired since the last daemon restart"* that the categorical `?enabled=` filter and the `?next_fire_since=`/`?next_fire_until=` filter on the *next* fire cannot answer; whitespace-trimmed; empty disables; invalid → 400 `invalid_last_fired_since`/`invalid_last_fired_until`; schedules with a nil `last_fired_at` — never-fired schedules — are filtered OUT when any bound is set, mirroring the webhook `?last_delivery_since=`/`?last_delivery_until=` nil-handling and the `next_fire_at` filter nil-exclusion; applied right after the `next_fire_at` range and before `?search=` so it composes additively with every other schedule filter), `?prefix=<value>` (5.4.82 — case-sensitive `HasPrefix(sched.name, prefix)` filter; whitespace-trimmed; empty disables; the fifth and final member of the name-prefix filter family alongside snapshots (5.4.75), VMs (5.4.76), images (5.4.77), and templates (5.4.78) so the same cohort-discrimination query (`?prefix=nightly-`, `?prefix=backup-`, `?prefix=auto-`) round-trips 1:1 across every name-prefix axis operators reach for most; case-sensitive because schedule names share the same case-sensitive free-form alphabet as VM / template names; applied right after the time-range filters and before `?search=` so it composes additively with every other schedule filter; CLI flag: `--prefix`), `?search=<q>` (case-insensitive substring across name/action/vm_id/tag_selector). Sorting: `?sort=<id|name|created_at|next_fire_at|last_fired_at|vm_id|action>` (default `id`) + `?order=<asc|desc>` (default `asc`); unknown → 400 `invalid_sort`/`invalid_order`; all comparators tiebreak on `id`. `next_fire_at` and `last_fired_at` (5.4.84) both sort schedules with a nil timestamp to the tail of `asc` and the head of `desc`. `last_fired_at` is the symmetric backward-looking sort counterpart to `next_fire_at`, the same way `?last_fired_since=`/`?last_fired_until=` (5.4.74) complements `?next_fire_since=`/`?next_fire_until=` (5.4.60) — so the SRE triage cohort *"which schedules fired least recently / which have never fired"* can now be both sorted and filtered on the same axis. `vm_id` (5.4.97) is the symmetric sort counterpart to the case-sensitive `?vm_id=` exact-match filter on the same column — case-sensitive ASCII compare (VM IDs are opaque `vm-<unix-nano>` strings); schedules with an empty `vm_id` (tag_selector-targeted or all-VMs schedules) sink to the tail of `asc` and the head of `desc`, mirroring the events/logs/schedule-runs vm_id sort axes. `action` (5.4.99) is the symmetric sort counterpart to the existing case-insensitive `?action=` exact-match filter on the same column — case-insensitive alphabetical compare on the four-member action enum (`restart` < `snapshot` < `start` < `stop`); action is closed-and-total (every schedule resolves to exactly one of the four values at create time), so the `action` branch diverges from the nil-trailing convention the same way the webhook `delivery_status` sort axis (5.4.98) does — there is no empty bucket to sink. Pagination: `?page=&per_page=`. 503 `schedules_disabled` when the subsystem is off. CLI: `vmsmith schedule list --vm --tag-selector --action --catch-up --timezone --enabled --search --since --until --next-fire-since --next-fire-until --last-fired-since --last-fired-until --prefix --sort --order --limit --page`.
POST   /schedules                      Create a schedule. Body (`CreateScheduleRequest`): `name` (1-128), `vm_id?` / `tag_selector?[]` (mutually exclusive — both empty = all VMs), `action` (snapshot|start|stop|restart), `cron_spec` (6-field with seconds, e.g. `0 0 2 * * *`), `timezone?` (IANA), `enabled?` (default true), `catch_up_policy?` (skip|run_once|run_all, default skip), `max_concurrent?`, `retention_count?`, `params?`. 400 codes: `invalid_name`, `invalid_action`, `invalid_cron_spec`, `invalid_timezone`, `invalid_target`, `invalid_catch_up_policy`. Registers the schedule with the running engine and emits `schedule.created`. CLI: `vmsmith schedule create --name --vm|--tag --action --cron --timezone --enabled --catch-up --retention --max-concurrent`.
GET    /schedules/{id}                 Get a schedule. 404 `resource_not_found`. CLI: `vmsmith schedule show <id>` (also prints the last 20 runs).
PATCH  /schedules/{id}                 Update editable fields (`ScheduleUpdateSpec`, pointer semantics — omit = no change): `name`, `vm_id`, `tag_selector`, `action`, `cron_spec`, `timezone`, `enabled`, `catch_up_policy`, `max_concurrent`, `retention_count`, `params`. Empty body → 400 `noop_update`; cron/timezone re-validated; the engine is re-registered and `next_fire_at` recomputed. Emits `schedule.updated`. CLI: `vmsmith schedule edit <id> [--name --vm --tag --clear-tags --action --cron --timezone --enabled --catch-up --retention --max-concurrent]`.
DELETE /schedules/{id}                 Delete a schedule (and its run history). De-registers from the engine; emits `schedule.deleted`. CLI: `vmsmith schedule delete <id>`.
GET    /schedules/{id}/runs            List the schedule's run history (newest first by default; `?page=&per_page=`; `X-Total-Count`). Each run: `{id, schedule_id, vm_id, started_at, finished_at?, status (running|success|error|skipped), error?, skip_reason?}`. 404 if the schedule is unknown. Filters (applied before sort + pagination so `X-Total-Count` reflects the post-filter population): `?status=<running|success|error|skipped>` (case-insensitive exact-match; empty disables; unknown → 400 `invalid_status`), `?skip_reason=<vm_not_found|vm_already_stopped|vm_already_running|concurrent_run|catch_up_skipped|queue_full>` (case-insensitive exact-match on the run's `skip_reason` field (5.4.65); whitespace-trimmed; empty disables; invalid → 400 `invalid_skip_reason`; runs with an empty `skip_reason` — every non-skipped run, and any skipped run persisted without a reason — are filtered OUT whenever the filter is set, mirroring the nil-`finished_at` exclusion in the `?finished_since=`/`?finished_until=` family; the symmetric categorical sub-axis to `?status=skipped` since `?status=` cannot answer *which* reason a run was skipped — closes the *"show me every run skipped because the queue was full"* / *"every catch-up skip from yesterday's startup"* SRE triage query; composes additively with every other run filter; applied right after `?status=`), `?vm_id=<id>` (case-sensitive exact-match against the run's `vm_id` — VM IDs are opaque `vm-<unix-nano>` strings; whitespace-trimmed; empty disables; useful for tag-selector schedules that target many VMs — closes the *"which runs of this tag-selector schedule targeted VM X"* operator query that `?status=` / `?since=` cannot answer; mirrors the events handler's `?vm_id=` contract), `?since=<rfc3339>` / `?until=<rfc3339>` (inclusive bounds on `started_at`; whitespace-trimmed; invalid → 400 `invalid_since` / `invalid_until`; a run with a zero `started_at` is filtered OUT when any bound is set — mirrors the schedule/snapshot/image time-range family), `?finished_since=<rfc3339>` / `?finished_until=<rfc3339>` (inclusive bounds on the nullable `finished_at` (5.4.62); whitespace-trimmed; empty disables; invalid → 400 `invalid_finished_since` / `invalid_finished_until`; runs with a nil `finished_at` — typically still-running runs — are filtered OUT when any bound is set, mirroring the schedule `?next_fire_since=` / `?next_fire_until=` nil-handling; closes the *"which runs completed inside this maintenance window"* triage query that the `?since=` / `?until=` filter on `started_at` cannot answer when a run can start inside a window but finish well outside it; applied right after the `started_at` range and before `?search=` so it composes additively with every other run filter), `?min_duration_ms=<n>` / `?max_duration_ms=<n>` (inclusive non-negative integer bounds on each run's `finished_at - started_at` duration in milliseconds (5.4.64); whitespace-trimmed; empty disables; non-numeric or negative → 400 `invalid_min_duration_ms` / `invalid_max_duration_ms`; runs with a nil `finished_at` — still-running runs have no known duration — are filtered OUT when any bound is set, mirroring the `finished_since`/`finished_until` nil-handling; closes the *"show me every run that took ≥ 5 minutes"* SRE triage query that `?status=` cannot answer; the symmetric range counterpart to the `duration` sort axis added in 5.4.63; applied right after the `finished_at` range and before `?search=`), `?search=<text>` — case-insensitive substring match across the run's `error` and `skip_reason` fields (5.4.58); whitespace-trimmed; empty disables; `id` / `schedule_id` / `vm_id` / `status` are intentionally excluded from the haystack so short numeric needles don't generate noisy matches against opaque IDs; closes the *"show me every run that timed out / hit `vm_already_stopped`"* triage query that `?status=` (which only narrows to the four enum buckets) can't answer; composes additively with `?status=` / `?vm_id=` / `?since=` / `?until=` / `?finished_since=` / `?finished_until=`. Sorting (5.4.59): `?sort=<id|started_at|finished_at|status|duration|vm_id|skip_reason>` (default `started_at` to preserve the legacy newest-first contract) and `?order=<asc|desc>` (default `desc` when `sort` is omitted, `asc` otherwise); unknown values return 400 `invalid_sort` / `invalid_order`; `finished_at` puts still-running runs (nil `finished_at`) at the tail when ascending and at the head when descending; `duration` orders by `finished_at - started_at` (5.4.63) and applies the same nil-trailing semantics — runs with no known duration sink to the tail in asc; `status` matches alphabetically (`error` < `running` < `success`); `vm_id` (5.4.95) is the symmetric sort counterpart to the case-sensitive `?vm_id=` exact-match filter on the same column — same case-sensitive ASCII compare (VM IDs are opaque `vm-<unix-nano>` strings), and runs with an empty `vm_id` (e.g. `queue_full` skips recorded on an all-VMs schedule without a resolved target) sink to the tail in `asc` / head in `desc`, mirroring the nil-trailing semantics on the events `vm_id` sort axis (5.4.93), the logs `vm_id` sort axis (5.4.94), and every other nullable sort axis (ip, guest_ip, last_fired_at, last_delivery_at, actor, resource_id, image, default_user, gpu); `skip_reason` (5.4.96) is the symmetric sort counterpart to the `?skip_reason=` exact-match filter (5.4.65) — populated reasons fall in alphabetical order (catch_up_skipped < concurrent_run < queue_full < vm_already_running < vm_already_stopped < vm_not_found) and runs with an empty `skip_reason` (every non-skipped run, plus skipped runs persisted without a reason) sink to the tail in asc / head in desc, mirroring the `finished_at` / `duration` nil-trailing semantics; all comparators tiebreak on `id` so paginated responses are deterministic. CLI: `vmsmith schedule runs <id> --status --skip-reason --vm --search --since --until --finished-since --finished-until --min-duration-ms --max-duration-ms --sort --order --limit --page`; GUI: per-row "Filter runs by VM id" input, "Search error / skip-reason" input, "Filter runs by status" dropdown, "Filter skipped runs by skip reason" dropdown (5.4.65), "Sort" / "Order" dropdowns, two `datetime-local` "finished_since / finished_until" controls, and two `<input type="number">` "min duration ms / max duration ms" controls on the Schedules page recent-runs expander.
POST   /schedules/{id}/run-now         Fire the schedule immediately (out of band of cron), attributing runs to `actor: "api"`. Returns the schedule. 404 if unknown. CLI: `vmsmith schedule run-now <id>`.
```

---

## Development Workflow

```bash
# Full build (required after frontend changes)
make build

# Backend-only iteration (faster)
make build-go

# Two-terminal dev setup
# Terminal 1:
make dev-api        # Go daemon on :8080

# Terminal 2:
make dev-web        # Vite dev server on :3000 (proxies /api → :8080)
# Open http://localhost:3000

# Before committing
make test           # all Go tests with race detector
make fmt            # format Go code
make lint           # run golangci-lint
```

---

## Dependencies

Go dependencies are managed with `go mod`. Key dependencies:

| Package | Purpose |
|---|---|
| `libvirt.org/go/libvirt` | libvirt C bindings (requires CGO) |
| `github.com/go-chi/chi/v5` | HTTP router |
| `github.com/spf13/cobra` | CLI framework |
| `go.etcd.io/bbolt` | Embedded key-value store |
| `gopkg.in/yaml.v3` | YAML config parsing |

Frontend dependencies (in `web/package.json`):
- `react` + `react-dom` + `react-router-dom` — SPA framework
- `lucide-react` — icon library
- `tailwindcss` — utility CSS
- `vite` — build tool and dev server

The frontend API client lives in `web/src/api/client.js`. It automatically adds `Authorization: Bearer <key>` when an API key is present in `localStorage` (`vmsmith.apiKey`). When the daemon returns HTTP 401, the UI flips into an auth-gate screen (`web/src/components/AuthGate.jsx`) so the user can enter or replace the API key without a full page reload.
