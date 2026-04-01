# VMSmith Project Roadmap

> **Last updated:** 2026-03-30
> **Status:** Draft — active work started on Phase 1.1 CI, Phase 1.2 / 1.3 validation and error-handling improvements, contributor/developer workflow docs, and container/distribution packaging

This document outlines planned improvements, new features, and technical debt items for VMSmith. Tasks are organized into phases by theme, with rough effort estimates and dependency notes.

**Effort key:** S = small (hours), M = medium (1-2 days), L = large (3-5 days), XL = extra-large (1+ week)

---

## Phase 1: Foundation & Quality (Week 1-2)

These tasks strengthen the project's reliability, developer experience, and code quality before adding new features.

### 1.1 CI/CD Pipeline

There are currently no automated checks. This is the single highest-impact improvement.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 1.1.1 | Create GitHub Actions workflow for Go build + unit tests on every PR | S | ✅ Done — `.github/workflows/ci.yml` runs `make build-go` and `make test-unit` on Ubuntu 22.04 with Go 1.22 + `libvirt-dev` |
| 1.1.2 | Add `golangci-lint` step to CI | S | ✅ Done — `.github/workflows/ci.yml` runs `golangci-lint-action` (currently scoped to `govet`) in CI |
| 1.1.3 | Add frontend build + Playwright mock tests to CI | M | ✅ Done — `.github/workflows/ci.yml` runs a dedicated frontend job that installs Node dependencies, builds the frontend bundle, installs Chromium via Playwright, and runs `make test-web` |
| 1.1.4 | Add API integration test step (`make test-integration`) | S | ✅ Done — included in `.github/workflows/ci.yml` backend job |
| 1.1.5 | Create release workflow: build + attach `vmsmith-linux-amd64` binary on tag push | M | ✅ Done — `.github/workflows/release.yml` builds the frontend + `make dist` on `v*` tags and publishes `bin/vmsmith-linux-amd64` via GitHub Releases |
| 1.1.6 | Add branch protection rules for `main` (require CI pass, no force push) | S | GitHub repo settings |

### 1.2 Input Validation & Error Handling

Several API inputs currently pass through to libvirt without validation, producing confusing 500 errors instead of clear 400 responses.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 1.2.1 | Validate VM name: non-empty, max 64 chars, alphanumeric + hyphens only, unique | M | Check in API handler before calling Manager. Return 400 with specific message |
| 1.2.2 | Validate CPU/RAM bounds: CPUs 1-128, RAM 128MB-1TB, Disk 1GB-10TB | S | Add to VMSpec validation, also enforce in VMUpdateSpec |
| 1.2.3 | Validate port forward ranges: host/guest port 1-65535, protocol tcp/udp only | S | Check before calling store or iptables |
| 1.2.4 | Validate image upload: reject empty files, enforce `.qcow2` extension, check disk space | M | ✅ Done — upload handler rejects empty/non-`.qcow2` files with `invalid_image` and returns 507 `insufficient_storage` when free disk is too low |
| 1.2.5 | Standardize error responses: introduce error codes (`invalid_name`, `resource_not_found`, `disk_shrink_not_allowed`, etc.) | M | Extend `pkg/types/errors.go` with a `Code` field; update all handlers |
| 1.2.6 | Return 400 (not 500) for all client input errors; reserve 500 for internal failures | M | Audit all handlers; most need `http.StatusBadRequest` paths |
| 1.2.7 | Sanitize error messages: strip libvirt internal details from user-facing responses | S | Wrap libvirt errors with user-friendly messages in lifecycle.go |

### 1.3 Test Coverage Improvements

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 1.3.1 | Add unit tests for VM name/CPU/RAM validation rules (after 1.2.1-1.2.2) | S | Table-driven tests in `internal/api/` or `pkg/types/` |
| 1.3.2 | Add API tests for all 400-class error paths (invalid JSON, missing fields, out-of-range values) | M | Extend `api_test.go` with negative test cases |
| 1.3.3 | Add port forward collision test (duplicate host:port+protocol) | S | MockManager + httptest |
| 1.3.4 | Add image upload edge-case tests: zero-byte file, oversized file, non-qcow2 file | M | ✅ Done — `internal/api/api_test.go` covers zero-byte, non-`.qcow2`, and insufficient-storage upload paths via multipart `httptest` cases |
| 1.3.5 | Add CLI output tests: verify `vmsmith vm list` table format, `vmsmith image list` output | S | ✅ Done — `internal/cli/commands_test.go` captures stdout and verifies table headers/rows for both `vm list` and `image list` |

---

## Phase 2: Core Feature Additions (Week 3-5)

New features that fill the most-requested gaps.

### 2.1 VM Cloning

Currently the only way to duplicate a VM is export-to-image then create-from-image — a slow multi-step process.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 2.1.1 | Add `Clone(ctx, sourceID, newName) (*VM, error)` to `vm.Manager` interface | S | |
| 2.1.2 | Implement in `LibvirtManager`: `qemu-img create` overlay from source disk, new domain XML, new cloud-init ISO | L | Must handle: new MAC, new DHCP reservation, new VM ID. Source VM should be stopped |
| 2.1.3 | Implement in `MockManager` | S | Copy in-memory state |
| 2.1.4 | Add `POST /api/v1/vms/{id}/clone` endpoint | S | Body: `{ "name": "clone-name" }` |
| 2.1.5 | Add `vmsmith vm clone <id> --name <name>` CLI command | S | |
| 2.1.6 | Add "Clone" button to VMDetail page in frontend | S | |
| 2.1.7 | Add integration + E2E tests | M | |

### 2.2 VM Tags & Metadata

No way to organize or annotate VMs. Tags enable filtering, grouping, and automation.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 2.2.1 | Add `Tags []string` and `Description string` fields to `types.VM` | S | JSON tags with `omitempty` |
| 2.2.2 | Accept tags/description in `POST /vms` and `PATCH /vms/{id}` | S | |
| 2.2.3 | Add `--tag` flag (repeatable) and `--description` to `vmsmith vm create` and `vmsmith vm edit` | S | |
| 2.2.4 | Add `GET /vms?tag=<tag>` filter support | M | Filter in handler or bbolt iteration |
| 2.2.5 | Show tags as badges in VMList and VMDetail frontend pages | S | |
| 2.2.6 | Add `vmsmith vm list --tag <tag>` CLI filter | S | |

### 2.3 Bulk Operations

Operating on VMs one-at-a-time is tedious when managing many.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 2.3.1 | Add `POST /api/v1/vms/bulk` endpoint: `{ "action": "start|stop|delete", "ids": [...] }` | M | Return per-VM success/failure results |
| 2.3.2 | Add `vmsmith vm start --all`, `vmsmith vm stop --all`, with `--tag` filter | M | |
| 2.3.3 | Add multi-select checkboxes + bulk action bar to VMList frontend page | M | |

### 2.4 VM Templates

Save and reuse VM configurations without re-specifying every parameter.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 2.4.1 | Define `VMTemplate` type: name, image, CPUs, RAM, disk, networks, tags, default_user | S | Store in new bbolt bucket `templates` |
| 2.4.2 | Add `POST /api/v1/templates`, `GET /api/v1/templates`, `DELETE /api/v1/templates/{id}` | M | |
| 2.4.3 | Add `POST /vms` support for `template_id` field — merges template defaults with request overrides | M | |
| 2.4.4 | Add `vmsmith template create|list|delete` CLI commands | M | |
| 2.4.5 | Add template selector dropdown to Create VM modal in frontend | S | |

---

## Phase 3: Operational Excellence (Week 5-8)

Features for running VMSmith in production or team environments.

### 3.1 Authentication & Authorization

The API is completely open. This blocks any multi-user or network-exposed deployment.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.1.1 | Add API key authentication middleware (check `Authorization: Bearer <key>` header) | M | Keys stored in config file. Bypass for localhost if configured |
| 3.1.2 | Add `daemon.auth.enabled` and `daemon.auth.api_keys` config fields | S | |
| 3.1.3 | Add `--api-key` flag to CLI commands for remote daemon usage | S | |
| 3.1.4 | Add login screen to frontend when auth is enabled | M | Store token in localStorage |
| 3.1.5 | (Future) Role-based access: `admin` (full), `operator` (start/stop/list), `viewer` (read-only) | L | Optional follow-up; start with single-role API keys |

### 3.2 TLS / HTTPS Support

Required for any non-localhost deployment.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.2.1 | Add `daemon.tls.cert_file` and `daemon.tls.key_file` config fields | S | ✅ Done — `internal/config/config.go` defines `daemon.tls.cert_file` / `key_file`, and `internal/config/config_test.go` covers loading them from YAML |
| 3.2.2 | Switch `http.ListenAndServe` to `http.ListenAndServeTLS` when TLS configured | S | ✅ Done — `internal/daemon/daemon.go` switches to `ListenAndServeTLS` when both TLS files are configured, with daemon tests covering both HTTP and HTTPS paths |
| 3.2.3 | Add `daemon.tls.auto_cert` option for Let's Encrypt via `autocert` package | M | Only practical if daemon has a public FQDN |
| 3.2.4 | Document reverse proxy setup (nginx/caddy) as alternative to built-in TLS | S | ✅ Done — `docs/PRODUCTION_DEPLOYMENT.md` covers reverse proxy deployment with both nginx and Caddy, TLS, and firewall guidance |

### 3.3 Systemd Integration

Make VMSmith a proper system service.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.3.1 | Create `vmsmith.service` systemd unit file | S | ✅ Done — `vmsmith.service` is committed at the repo root with `Wants=libvirtd.service` and `After=network-online.target libvirtd.service` |
| 3.3.2 | Add `make install-service` target to copy unit file and enable service | S | ✅ Done — `make install-service` now installs `vmsmith.service` into `/etc/systemd/system`, reloads systemd, and enables/starts the unit |
| 3.3.3 | Add `vmsmith daemon status` command (check if daemon is running) | S | ✅ Done — `internal/cli/daemon.go` implements `vmsmith daemon status`, and the command is documented in `README.md` |
| 3.3.4 | Implement graceful shutdown: drain in-flight requests, close libvirt connection cleanly | M | Signal handling exists but could be more graceful |

### 3.4 API Rate Limiting & Request Size Limits

Prevent abuse and resource exhaustion.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.4.1 | Add per-IP rate limiting middleware (token bucket, configurable rate) | M | Use `golang.org/x/time/rate` |
| 3.4.2 | Add configurable max request body size (default 50MB, override for image uploads) | S | ✅ Done — added `daemon.max_request_body_bytes` and `daemon.max_upload_body_bytes`, applied request-size middleware, and covered 413 behavior in API tests |
| 3.4.3 | Add concurrent VM creation limit (prevent fork-bombing the host) | S | ✅ Done — `daemon.max_concurrent_creates` bounds simultaneous `POST /api/v1/vms` operations and returns HTTP 429 `create_limit_reached` when the queue is full |

### 3.5 Resource Quotas

Prevent VMs from consuming all host resources.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.5.1 | Add `quotas.max_vms`, `quotas.max_total_cpus`, `quotas.max_total_ram_mb`, `quotas.max_total_disk_gb` config fields | S | |
| 3.5.2 | Check quotas before VM create and VM update; return 429 or 403 if exceeded | M | Sum current allocations from bbolt |
| 3.5.3 | Show quota usage in dashboard (e.g., "12/32 CPUs allocated") | S | |

---

## Phase 4: Monitoring & Observability (Week 7-10)

### 4.1 VM Resource Metrics

Users have no visibility into what's happening inside VMs.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 4.1.1 | Collect CPU, memory, disk, and network I/O stats from libvirt `DomainStats` API | M | Poll on interval (e.g., 10s), store in ring buffer per VM |
| 4.1.2 | Add `GET /api/v1/vms/{id}/stats` endpoint: current + recent history | M | Return time-series array |
| 4.1.3 | Add real-time resource graphs to VMDetail page (CPU/RAM/disk/network) | L | Use lightweight chart library (e.g., recharts or uPlot) |
| 4.1.4 | Add host-level stats to dashboard: total CPU/RAM usage, disk space, VM count | M | |
| 4.1.5 | Add `vmsmith vm stats <id>` CLI command | S | |

### 4.2 Event System & Notifications

No way to know when a VM crashes, completes creation, or changes state.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 4.2.1 | Subscribe to libvirt domain lifecycle events (start, stop, crash, reboot) | M | Use `ConnectDomainEventLifecycleRegister` |
| 4.2.2 | Store events in new bbolt bucket with timestamp, VM ID, event type | S | |
| 4.2.3 | Add `GET /api/v1/events` endpoint with optional `?vm_id=` and `?since=` filters | M | |
| 4.2.4 | Add Server-Sent Events (SSE) stream at `GET /api/v1/events/stream` | M | Real-time push instead of polling |
| 4.2.5 | Add event timeline to VMDetail page | M | |
| 4.2.6 | (Future) Webhook support: POST to configured URL on configurable events | L | |

### 4.3 OpenAPI / Swagger Spec

API documentation is hand-written in ARCHITECTURE.md. Auto-generated spec enables client SDKs and API explorers.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 4.3.1 | Add OpenAPI 3.0 annotations to all handlers (or generate from types) | L | Consider `swaggo/swag` or hand-write `openapi.yaml` |
| 4.3.2 | Serve Swagger UI at `/api/docs` | S | Embed swagger-ui dist |
| 4.3.3 | Generate TypeScript API client from OpenAPI spec for frontend | M | Replace hand-written `client.js` |

---

## Phase 5: Advanced Features (Week 10+)

Larger features for power users and advanced use cases.

### 5.1 VNC Console Access

VNC is already configured in domain XML but not exposed. This would allow browser-based console access.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 5.1.1 | Add websocket proxy endpoint `GET /api/v1/vms/{id}/console` | L | Proxy TCP connection to VNC socket via websocket (noVNC protocol) |
| 5.1.2 | Embed noVNC client in frontend, add "Console" tab to VMDetail | L | noVNC is ~200KB, MIT-licensed |
| 5.1.3 | Add VNC password support (per-VM, stored in bbolt) | M | |
| 5.1.4 | Add serial console alternative for headless VMs | M | `virsh console` equivalent via websocket |

### 5.2 Scheduled Operations

Automate routine tasks like snapshots and backups.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 5.2.1 | Add in-process cron scheduler (e.g., `robfig/cron`) | M | |
| 5.2.2 | Define `Schedule` type: VM ID, action (snapshot, start, stop), cron expression, retention count | M | Store in bbolt bucket `schedules` |
| 5.2.3 | Add CRUD endpoints for schedules | M | |
| 5.2.4 | Add `vmsmith schedule create|list|delete` CLI commands | M | |
| 5.2.5 | Add schedule management UI to VMDetail page | M | |
| 5.2.6 | Implement snapshot retention: auto-delete oldest when count exceeds limit | S | |

### 5.3 VM Import/Export (OVA/OVF)

Enable interoperability with other virtualization platforms.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 5.3.1 | Export VM as OVA (tar of OVF descriptor + qcow2→vmdk converted disk) | L | Use `qemu-img convert -O vmdk` |
| 5.3.2 | Import VM from OVA/OVF: extract, convert disk to qcow2, create VM with matching specs | L | Parse OVF XML for CPU/RAM/disk specs |
| 5.3.3 | Add export/import endpoints and CLI commands | M | |

### 5.4 Pagination & Filtering for Large Deployments

Current list endpoints return all records. This won't scale beyond ~1000 VMs.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 5.4.1 | Add `?page=&per_page=` query params to `GET /vms`, `GET /images`, `GET /logs` | M | Return `X-Total-Count` header |
| 5.4.2 | Add `?status=running&sort=created_at&order=desc` filtering to `GET /vms` | M | |
| 5.4.3 | Update frontend tables to support server-side pagination | M | |
| 5.4.4 | Add `--limit` and `--offset` flags to CLI list commands | S | |

### 5.5 Multi-Host Management (Future Vision)

Manage VMs across multiple physical hosts from a single VMSmith instance.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 5.5.1 | Design multi-host architecture: central coordinator + per-host agents, or remote libvirt URIs | XL | Architecture decision needed |
| 5.5.2 | Add `hosts` config section with libvirt URI per host | M | |
| 5.5.3 | Add host selection to VM create (`--host <name>`) | L | |
| 5.5.4 | Add host overview dashboard showing per-host resource usage | L | |

---

## Phase 6: Developer & Community (Ongoing)

### 6.1 Developer Experience

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 6.1.1 | Add `make dev` target that runs both `dev-api` and `dev-web` in parallel (e.g., via `goreman` or `concurrently`) | S | ✅ Done — `make dev` now launches both targets in parallel and cleans up both child processes on Ctrl-C |
| 6.1.2 | Add pre-commit hook: `make fmt && make lint` | S | ✅ Done — versioned `.githooks/pre-commit` hook added with `make install-githooks` helper and contributor docs |
| 6.1.3 | Add `CONTRIBUTING.md` with setup instructions, PR conventions, test requirements | S | ✅ Done — `CONTRIBUTING.md` added with setup, workflow, testing, and PR guidance |
| 6.1.4 | Add `.editorconfig` for consistent formatting across editors | S | ✅ Done — root `.editorconfig` defines Go/Makefile tab rules and 2-space defaults for docs/web assets |

### 6.2 Packaging & Distribution

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 6.2.1 | Create DEB package build (for Ubuntu/Debian) | M | Include systemd unit, default config, man page |
| 6.2.2 | Create RPM package build (for Rocky/RHEL/Fedora) | M | |
| 6.2.3 | Create container image for VMSmith daemon (requires privileged mode for libvirt) | M | ✅ Done — added multi-stage `Dockerfile`, `scripts/docker-entrypoint.sh`, `.dockerignore`, and `docs/CONTAINER.md` for privileged local/lab usage |
| 6.2.4 | Add installation script (`curl -sSL https://... \| sh`) | S | Download binary + install to `/usr/local/bin` |

### 6.3 Documentation Expansion

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 6.3.1 | Write production deployment guide (systemd, TLS, reverse proxy, firewall rules) | M | ✅ Done — `docs/PRODUCTION_DEPLOYMENT.md` covers systemd, TLS via reverse proxy, firewall rules, logging, backups, and upgrade guidance |
| 6.3.2 | Write networking deep-dive (NAT vs macvtap vs bridge, when to use each, troubleshooting) | M | ✅ Done — added `docs/NETWORKING.md` covering mode selection, tradeoffs, examples, and troubleshooting |
| 6.3.3 | Add example automation scripts (bash/python) for common workflows | S | ✅ Done — added `examples/` with bash and Python API automation examples for common create/wait/port-forward flows |
| 6.3.4 | Create short video/GIF demos for README | S | |

---

## Summary: Suggested Priority Order

For maximum impact, work through these in roughly this order:

| Priority | Area | Key Tasks | Why |
|----------|------|-----------|-----|
| **P0** | CI/CD | 1.1.1 – 1.1.5 | Prevents regressions, enables confident development |
| **P0** | Validation | 1.2.1 – 1.2.6 | Users hit confusing 500 errors on bad input today |
| **P1** | Tests | 1.3.1 – 1.3.5 | Lock in validation work with tests |
| **P1** | VM Cloning | 2.1.1 – 2.1.7 | Most-requested missing feature |
| **P1** | Tags | 2.2.1 – 2.2.6 | Essential for organizing VMs at any scale |
| **P2** | Auth | 3.1.1 – 3.1.4 | Blocks network-exposed deployments |
| **P2** | Systemd | 3.3.1 – 3.3.3 | Required for production use |
| **P2** | Metrics | 4.1.1 – 4.1.4 | Users need visibility into VM health |
| **P2** | Events | 4.2.1 – 4.2.5 | Know when things go wrong |
| **P3** | TLS | 3.2.1 – 3.2.4 | Needed with auth for secure deployments |
| **P3** | Quotas | 3.5.1 – 3.5.3 | Protect host from over-provisioning |
| **P3** | Templates | 2.4.1 – 2.4.5 | Quality-of-life for repeat VM creation |
| **P3** | Bulk Ops | 2.3.1 – 2.3.3 | Quality-of-life for managing many VMs |
| **P4** | Console | 5.1.1 – 5.1.4 | High value but high effort |
| **P4** | Schedules | 5.2.1 – 5.2.6 | Automation for ops teams |
| **P4** | Pagination | 5.4.1 – 5.4.4 | Only needed at scale |
| **P5** | OpenAPI | 4.3.1 – 4.3.3 | Nice-to-have for API consumers |
| **P5** | OVA | 5.3.1 – 5.3.3 | Interop, niche use case |
| **P5** | Multi-host | 5.5.1 – 5.5.4 | Major architecture change; long-term vision |

---

## Notes

- Effort estimates assume familiarity with the codebase. First-time contributors should add ~50% buffer.
- Phases are not strictly sequential — items from Phase 2 can begin as soon as Phase 1 CI is in place.
- Each task should be a single PR where possible. Larger tasks (L/XL) may need multiple PRs.
- All new features should include: API endpoint, CLI command, frontend UI, tests, and CLAUDE.md update.
