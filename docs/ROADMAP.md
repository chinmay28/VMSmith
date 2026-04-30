# VMSmith Project Roadmap

> **Last updated:** 2026-04-30
> **Status:** Active roadmap — foundation work, auth/TLS/systemd/quotas, templates, bulk ops, host + VM metrics APIs/CLI, event storage/streaming/UI, and OpenAPI tooling are now complete; the main remaining gaps are deeper cloning coverage, VM metrics streaming/charts, advanced operations, and long-tail production polish.

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
| 1.2.1 | Validate VM name: non-empty, max 64 chars, alphanumeric + hyphens only, unique | M | ✅ Done — API create validation trims names, enforces the 1-64 char alphanumeric/hyphen rule, and rejects duplicate VM names with HTTP 400 `invalid_name` before calling the manager |
| 1.2.2 | Validate CPU/RAM bounds: CPUs 1-128, RAM 128MB-1TB, Disk 1GB-10TB | S | ✅ Done — create/update validation enforces CPUs 1-128, RAM 128MB-1TB, and Disk 1GB-10TB when values are provided, while still allowing omitted create-time values to fall back to configured defaults |
| 1.2.3 | Validate port forward ranges: host/guest port 1-65535, protocol tcp/udp only | S | ✅ Done — `internal/api/validation.go` rejects out-of-range ports and non-`tcp`/`udp` protocols before any store or iptables work; covered by `internal/api/validation_test.go` and `internal/api/api_test.go` |
| 1.2.4 | Validate image upload: reject empty files, enforce `.qcow2` extension, check disk space | M | ✅ Done — upload handler rejects empty/non-`.qcow2` files with `invalid_image` and returns 507 `insufficient_storage` when free disk is too low |
| 1.2.5 | Standardize error responses: introduce error codes (`invalid_name`, `resource_not_found`, `disk_shrink_not_allowed`, etc.) | M | ✅ Done — `pkg/types/errors.go` carries typed API errors, every API error response now includes structured `code` + `message` fields, and plain handler failures use explicit codes such as `invalid_request_body`, `request_too_large`, `missing_file`, and `vm_ip_unavailable` |
| 1.2.6 | Return 400 (not 500) for all client input errors; reserve 500 for internal failures | M | ✅ Done — merged validation/error-response follow-ups moved client-input failures onto explicit 4xx paths while keeping 5xx responses for actual internal errors |
| 1.2.7 | Sanitize error messages: strip libvirt internal details from user-facing responses | S | ✅ Done — API handlers now translate backend/libvirt-facing failures into user-friendly messages instead of leaking raw internal details |

### 1.3 Test Coverage Improvements

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 1.3.1 | Add unit tests for VM name/CPU/RAM validation rules (after 1.2.1-1.2.2) | S | ✅ Done — `internal/api/validation_test.go` covers VM name trimming/rules plus CPU/RAM/disk bound validation for create/update payloads |
| 1.3.2 | Add API tests for all 400-class error paths (invalid JSON, missing fields, out-of-range values) | M | ✅ Done — `internal/api/api_test.go` now covers additional create/update validation failures for disk bounds and tags, plus upload-image missing-name handling, alongside the existing invalid JSON and missing-field cases |
| 1.3.3 | Add port forward collision test (duplicate host:port+protocol) | S | ✅ Done — duplicate `host_port` + protocol conflicts are covered in `internal/network/portforward_test.go` and `internal/api/api_test.go` |
| 1.3.4 | Add image upload edge-case tests: zero-byte file, oversized file, non-qcow2 file | M | ✅ Done — `internal/api/api_test.go` covers zero-byte, non-`.qcow2`, and insufficient-storage upload paths via multipart `httptest` cases |
| 1.3.5 | Add CLI output tests: verify `vmsmith vm list` table format, `vmsmith image list` output | S | ✅ Done — `internal/cli/commands_test.go` captures stdout and verifies table headers/rows for both `vm list` and `image list` |

---

## Phase 2: Core Feature Additions (Week 3-5)

New features that fill the most-requested gaps.

### 2.1 VM Cloning

Currently the only way to duplicate a VM is export-to-image then create-from-image — a slow multi-step process.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 2.1.1 | Add `Clone(ctx, sourceID, newName) (*VM, error)` to `vm.Manager` interface | S | ✅ Done — cloning groundwork now exists at the manager layer so API/CLI/frontend work can target a stable method |
| 2.1.2 | Implement in `LibvirtManager`: `qemu-img create` overlay from source disk, new domain XML, new cloud-init ISO | L | Must handle: new MAC, new DHCP reservation, new VM ID. Source VM should be stopped |
| 2.1.3 | Implement in `MockManager` | S | ✅ Done — mock cloning copies the source VM spec/metadata into a new stopped VM for fast API/CLI test coverage |
| 2.1.4 | Add `POST /api/v1/vms/{id}/clone` endpoint | S | ✅ Done — API now exposes VM cloning with request validation, duplicate-name checks, typed error responses, and handler coverage for success/not-found/error cases |
| 2.1.5 | Add `vmsmith vm clone <id> --name <name>` CLI command | S | ✅ Done — CLI now supports `vmsmith vm clone <id> --name <name>` with test coverage and updated docs |
| 2.1.6 | Add "Clone" button to VMDetail page in frontend | S | ✅ Done — VM detail now offers a clone action modal that posts the new VM name to `POST /api/v1/vms/{id}/clone` and redirects to the cloned VM on success |
| 2.1.7 | Add integration + E2E tests | M | |

### 2.2 VM Tags & Metadata

No way to organize or annotate VMs. Tags enable filtering, grouping, and automation.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 2.2.1 | Add `Tags []string` and `Description string` fields to `types.VM` | S | ✅ Done — added top-level VM metadata plus mirrored create/update payload support in `pkg/types/vm.go` |
| 2.2.2 | Accept tags/description in `POST /vms` and `PATCH /vms/{id}` | S | ✅ Done — API accepts, trims, normalizes, and persists metadata on create/update |
| 2.2.3 | Add `--tag` flag (repeatable) and `--description` to `vmsmith vm create` and `vmsmith vm edit` | S | ✅ Done — CLI create/edit now support tags + description |
| 2.2.4 | Add `GET /vms?tag=<tag>` filter support | M | ✅ Done — list handler supports case-insensitive tag filtering |
| 2.2.5 | Show tags as badges in VMList and VMDetail frontend pages | S | ✅ Done — VM list/detail now render description + tag badges, and the list page supports quick tag filtering |
| 2.2.6 | Add `vmsmith vm list --tag <tag>` CLI filter | S | ✅ Done — CLI list supports tag filtering and shows tags in the table |

### 2.3 Bulk Operations

Operating on VMs one-at-a-time is tedious when managing many.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 2.3.1 | Add `POST /api/v1/vms/bulk` endpoint: `{ "action": "start|stop|delete", "ids": [...] }` | M | ✅ Done — `POST /api/v1/vms/bulk` now performs start/stop/delete across multiple VM IDs and returns per-VM success/failure results, including typed error codes for missing/invalid entries |
| 2.3.2 | Add `vmsmith vm start --all`, `vmsmith vm stop --all`, with `--tag` filter | M | ✅ Done — CLI `vm start` / `vm stop` now accept `--all` with optional `--tag`, skip VMs already in the wrong state, and document/test the bulk lifecycle flow |
| 2.3.3 | Add multi-select checkboxes + bulk action bar to VMList frontend page | M | ✅ Done — VMList now supports per-row selection, select-all for the current filtered view, and a bulk action bar that fans out start/stop/delete over existing per-VM endpoints with selection-aware skip messaging |

### 2.4 VM Templates

Save and reuse VM configurations without re-specifying every parameter.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 2.4.1 | Define `VMTemplate` type: name, image, CPUs, RAM, disk, networks, tags, default_user | S | ✅ Done — `pkg/types/template.go` defines reusable template records backed by a dedicated bbolt `templates` bucket |
| 2.4.2 | Add `POST /api/v1/templates`, `GET /api/v1/templates`, `DELETE /api/v1/templates/{id}` | M | ✅ Done — API now supports template create/list/delete with validation, pagination, and CRUD test coverage |
| 2.4.3 | Add `POST /vms` support for `template_id` field — merges template defaults with request overrides | M | ✅ Done — `POST /api/v1/vms` now accepts `template_id`, applies stored template defaults for image/resources/metadata/networks, preserves explicit request overrides, and returns a clear 400 when the requested template is missing |
| 2.4.4 | Add `vmsmith template create|list|delete` CLI commands | M | ✅ Done — CLI now supports local template create/list/delete flows with coverage for the happy-path CRUD workflow |
| 2.4.5 | Add template selector dropdown to Create VM modal in frontend | S | ✅ Done — the Create VM modal now lists saved templates, prefills form defaults when one is selected, and keeps manual field edits as explicit overrides |

---

## Phase 3: Operational Excellence (Week 5-8)

Features for running VMSmith in production or team environments.

### 3.1 Authentication & Authorization

The API is completely open. This blocks any multi-user or network-exposed deployment.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.1.1 | Add API key authentication middleware (check `Authorization: Bearer <key>` header) | M | ✅ Done — API routes now enforce `Authorization: Bearer <key>` when `daemon.auth.enabled` is true |
| 3.1.2 | Add `daemon.auth.enabled` and `daemon.auth.api_keys` config fields | S | ✅ Done — config loader, example config, and tests cover `daemon.auth.enabled` / `daemon.auth.api_keys` |
| 3.1.3 | Add `--api-key` flag to CLI commands for remote daemon usage | S | ✅ Done — CLI now exposes a persistent `--api-key` flag that adds `Authorization: Bearer <key>` for HTTP-based remote daemon operations such as `image pull http://...` |
| 3.1.4 | Add login screen to frontend when auth is enabled | M | ✅ Done — the embedded web UI now prompts for an API key after a 401, stores it in `localStorage`, and retries authenticated API calls without a full reload |
| 3.1.5 | (Future) Role-based access: `admin` (full), `operator` (start/stop/list), `viewer` (read-only) | L | Optional follow-up; start with single-role API keys |

### 3.2 TLS / HTTPS Support

Required for any non-localhost deployment.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.2.1 | Add `daemon.tls.cert_file` and `daemon.tls.key_file` config fields | S | ✅ Done — `internal/config/config.go` defines `daemon.tls.cert_file` / `key_file`, and `internal/config/config_test.go` covers loading them from YAML |
| 3.2.2 | Switch `http.ListenAndServe` to `http.ListenAndServeTLS` when TLS configured | S | ✅ Done — `internal/daemon/daemon.go` switches to `ListenAndServeTLS` when both TLS files are configured, with daemon tests covering both HTTP and HTTPS paths |
| 3.2.3 | Add `daemon.tls.auto_cert` option for Let's Encrypt via `autocert` package | M | ✅ Done — `internal/config/config.go` adds `daemon.tls.auto_cert` / `auto_cert_cache_dir`, `internal/daemon/daemon.go` wires `autocert.Manager` into the HTTPS server for a single public FQDN, and tests/docs/examples cover the new config |
| 3.2.4 | Document reverse proxy setup (nginx/caddy) as alternative to built-in TLS | S | ✅ Done — `docs/PRODUCTION_DEPLOYMENT.md` covers reverse proxy deployment with both nginx and Caddy, TLS, and firewall guidance |

### 3.3 Systemd Integration

Make VMSmith a proper system service.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.3.1 | Create `vmsmith.service` systemd unit file | S | ✅ Done — `vmsmith.service` is committed at the repo root with `network-online.target` + `libvirtd.service` ordering, runtime directory settings, and installable defaults for `/etc/vmsmith/config.yaml` |
| 3.3.2 | Add `make install-service` target to copy unit file and enable service | S | ✅ Done — `make install-service` now installs `vmsmith.service` into `/etc/systemd/system`, reloads systemd, and enables/starts the unit |
| 3.3.3 | Add `vmsmith daemon status` command (check if daemon is running) | S | ✅ Done — `internal/cli/daemon.go` implements `vmsmith daemon status`, and the command is documented in `README.md` |
| 3.3.4 | Implement graceful shutdown: drain in-flight requests, close libvirt connection cleanly | M | ✅ Done — API router now rejects new requests with HTTP 503 during shutdown while in-flight requests drain, and daemon cleanup closes VM manager, network manager, store, and logger resources in order |

### 3.4 API Rate Limiting & Request Size Limits

Prevent abuse and resource exhaustion.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.4.1 | Add per-IP rate limiting middleware (token bucket, configurable rate) | M | ✅ Done — `/api/v1/*` now uses configurable per-client token buckets via `daemon.rate_limit_per_second` / `daemon.rate_limit_burst`, returning HTTP 429 `rate_limit_exceeded` plus `Retry-After` |
| 3.4.2 | Add configurable max request body size (default 50MB, override for image uploads) | S | ✅ Done — added `daemon.max_request_body_bytes` and `daemon.max_upload_body_bytes`, applied request-size middleware, and covered 413 behavior in API tests |
| 3.4.3 | Add concurrent VM creation limit (prevent fork-bombing the host) | S | ✅ Done — `daemon.max_concurrent_creates` bounds simultaneous `POST /api/v1/vms` operations and returns HTTP 429 `create_limit_reached` when the queue is full |

### 3.5 Resource Quotas

Prevent VMs from consuming all host resources.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 3.5.1 | Add `quotas.max_vms`, `quotas.max_total_cpus`, `quotas.max_total_ram_mb`, `quotas.max_total_disk_gb` config fields | S | ✅ Done — added `QuotasConfig` to `internal/config/config.go`, covered defaults/YAML loading in `internal/config/config_test.go`, and documented the new config block in README + deployment docs |
| 3.5.2 | Check quotas before VM create and VM update; return 429 or 403 if exceeded | M | ✅ Done — quota wrapper sums current allocations from `List()`/`Get()` and returns HTTP 429 `quota_exceeded` |
| 3.5.3 | Show quota usage in dashboard (e.g., "12/32 CPUs allocated") | S | ✅ Done — dashboard now polls `GET /api/v1/quotas/usage` and shows current allocation vs limit |

---

## Phase 4: Monitoring & Observability (Week 7-10)

### 4.1 VM Resource Metrics

Users have no visibility into what's happening inside VMs. Per-host stats already exist (`GET /api/v1/host/stats`, on-demand sample, see `internal/api/host_stats.go`); per-VM time-series and history are missing.

#### 4.1.0 Architectural overview

**Sampling.** A single goroutine in a new `internal/metrics/` package polls libvirt's bulk stats API every `daemon.metrics.sample_interval` (default 10s) using `Connect.GetAllDomainStats(stats, flags)` with the bitmask `STATS_STATE | STATS_CPU_TOTAL | STATS_BALLOON | STATS_VCPU | STATS_INTERFACE | STATS_BLOCK`. One bulk call returns stats for every running domain in milliseconds — far cheaper than per-VM calls.

**Counter → rate conversion.** libvirt returns cumulative counters (CPU nanoseconds, bytes in/out, blocks read/written). Charts need rates. The collector keeps the previous sample in memory and computes deltas:

| Metric | libvirt field | Computed |
|---|---|---|
| CPU % | `cpu.time` (ns, cumulative) | `(Δns / Δt_ns / vcpus) * 100` clamped to [0, 100*vcpus] |
| RAM used MB | `balloon.rss` if present, else `balloon.current` | absolute |
| RAM available MB | `balloon.available - balloon.unused` (guest-agent dependent) | absolute, may be missing |
| Disk read B/s | `block.<n>.rd.bytes` summed across disks | `Δ / Δt_seconds` |
| Disk write B/s | `block.<n>.wr.bytes` summed | `Δ / Δt_seconds` |
| Net rx B/s | `net.<n>.rx.bytes` summed across interfaces | `Δ / Δt_seconds` |
| Net tx B/s | `net.<n>.tx.bytes` summed | `Δ / Δt_seconds` |

First sample after a VM starts produces no rate (no prior counter); the API marks it `null` and the chart skips. Counter resets (libvirt reports a smaller value than the prior sample — happens after VM restart) reset the baseline; rate for that interval is `null` rather than negative.

**In-memory ring buffer per VM.** A fixed-size circular buffer of `MetricSample` per VM ID:

```go
type MetricSample struct {
    Timestamp     time.Time `json:"timestamp"`
    CPUPercent    *float64  `json:"cpu_percent,omitempty"`     // pointer for null-on-reset
    MemUsedMB     *uint64   `json:"mem_used_mb,omitempty"`
    MemAvailMB    *uint64   `json:"mem_avail_mb,omitempty"`
    DiskReadBps   *uint64   `json:"disk_read_bps,omitempty"`
    DiskWriteBps  *uint64   `json:"disk_write_bps,omitempty"`
    NetRxBps      *uint64   `json:"net_rx_bps,omitempty"`
    NetTxBps      *uint64   `json:"net_tx_bps,omitempty"`
}
```

Buffer size = `daemon.metrics.history_size` (default 360 samples = 1 hour at 10s). Memory cost: ~80 bytes per sample × 360 × 100 VMs ≈ 2.8 MB worst case.

**Why in-memory only (v1).** Persisting metrics to bbolt is rejected for v1:
1. 10s sampling × 100 VMs × 30 days = 25.9 M samples per bucket. bbolt is not a TSDB; range scans get expensive and write amplification is high.
2. The compelling use case is "what is this VM doing right now / in the last hour" — covered by an in-memory ring.
3. Long-term metrics belong in a real TSDB. Defer to 4.1.6 (Prometheus exporter), which lets operators ship metrics to Prometheus/VictoriaMetrics/Grafana Cloud without us reinventing storage.

If durable metrics become a requirement before 4.1.6, the API shape stays the same — only the storage backend swaps.

**Stale-sample handling.** When a VM stops, its ring keeps the last samples but adds no new ones. `GET /api/v1/vms/{id}/stats` includes `state` and `last_sampled_at` in the response so the UI can mark the chart "VM stopped at <time>". Rings for deleted VMs are pruned on the next sweep (which checks each ring's owner against `Store.GetVM`). Rings for VMs that vanish from `GetAllDomainStats` for >5 minutes are also pruned to handle libvirt restarts.

**Metrics manager API.**
```go
type Manager interface {
    Start(ctx context.Context) error  // launches the sampler goroutine
    Stop() error
    Snapshot(vmID string) (*VMStatsSnapshot, error)  // current + history
    Subscribe(vmID string) (<-chan MetricSample, cancel func())  // for SSE / future events
}
```

The subscription path is what powers real-time charts: when 4.2 (events) lands, the metrics manager publishes `metrics.sample` events at a sampled-down rate (1/min) so the events stream stays a low-traffic audit log; the `Subscribe` path is the high-frequency path the SSE chart endpoint uses directly.

**API contract (`GET /api/v1/vms/{id}/stats`).**

```jsonc
{
  "vm_id": "vm-1741234567890123",
  "state": "running",
  "last_sampled_at": "2026-04-28T12:34:50Z",
  "current": { /* MetricSample */ },
  "history": [ /* MetricSample, oldest first */ ],
  "interval_seconds": 10,
  "history_size": 360
}
```

Query params:
- `?since=<rfc3339>` truncates `history` to samples after the timestamp.
- `?fields=cpu,mem` projects the response (cuts payload for the dashboard's compact charts; default = all).
- 404 with `resource_not_found` if VM doesn't exist; 200 with `state: "stopped"` and frozen history if VM exists but is stopped.

**Real-time stream (`GET /api/v1/vms/{id}/stats/stream`).** SSE, one frame per sample. Reuses the SSE machinery from 4.2.10. Frames carry the same `MetricSample` JSON. No replay needed (the REST history call provides initial backfill); on connect, the client sends the most recent `history` GET, then subscribes for new samples.

**Chart library choice.** uPlot (~45 KB gzipped) is preferred over recharts (~95 KB) for time-series — its canvas renderer handles 360+ points smoothly and re-renders at every new sample without React reconciliation pressure. Wrap in a `<MetricChart>` component to keep the dependency local.

**Privacy/perf considerations.**
- `balloon.available` requires the QEMU guest agent. When absent, return `null` rather than estimating; surface an info badge in the UI ("Install qemu-guest-agent for memory pressure metrics").
- VM owners should not see other VMs' metrics — once 3.1.5 RBAC lands, `/stats` enforces VM ownership. v1 (single-tenant API key) skips this check.
- Sampling adds ~5ms per call regardless of VM count (bulk API) but holds the libvirt connection mutex briefly. Don't go below 5s sampling without measuring under load.

#### 4.1.1 Task list

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 4.1.1 | Define `pkg/types/metrics.go` (`MetricSample`, `VMStatsSnapshot`) with all fields as pointers (nil = unavailable) | S | ✅ Done — `pkg/types/metrics.go` defines the VM metrics sample/snapshot contract used by the API and CLI |
| 4.1.2 | Create `internal/metrics/` package: sampler goroutine using `Connect.GetAllDomainStats`, per-VM ring buffer, counter→rate conversion with reset detection, sweep for stopped/deleted VMs. Unit tests with a mock libvirt-stats provider | L | ✅ Done — `internal/metrics/collector.go` polls bulk libvirt stats, maintains per-VM history, and is covered by unit tests for rate math and stale/reset behavior |
| 4.1.3 | Wire `metrics.Manager` into daemon startup; honor `daemon.metrics.enabled` (default true), `sample_interval` (10s), `history_size` (360) config fields. Document in `vmsmith.yaml.example` | S | ✅ Done — metrics manager startup/config wiring landed with disable-safe behavior for tests and non-libvirt environments |
| 4.1.4 | `GET /api/v1/vms/{id}/stats` endpoint with `?since=` and `?fields=` query params; 200/404/410-on-deleted contract above | M | ✅ Done — `internal/api/handlers_metrics.go` ships the REST stats endpoint with projection/filter coverage in `internal/api/metrics_test.go` |
| 4.1.5 | `GET /api/v1/vms/{id}/stats/stream` SSE endpoint reusing 4.2.10's SSE helper. Frame per sample. Closes on VM delete or daemon shutdown | M | Depends on 4.2.10. If 4.2 is slipping, ship 4.1.4 and a polling frontend first |
| 4.1.6 | Prometheus `/metrics` endpoint exposing per-VM `vmsmith_vm_cpu_percent{vm="..."}` etc. plus host-level metrics. Allow scraping behind same auth or unauthenticated on a dedicated `daemon.metrics.scrape_listen` (e.g., `127.0.0.1:9101`) so Prometheus doesn't need an API key. Add deployment notes | M | Use `prometheus/client_golang`. Solves the durable-history problem without us building a TSDB |
<<<<<<< HEAD
| 4.1.7 | Frontend: `web/src/components/MetricChart.jsx` using uPlot; `web/src/hooks/useVMStats.js` doing initial REST fetch + SSE subscription with polling fallback. Add 4 charts (CPU, RAM, disk, net) to VMDetail under a new "Metrics" tab | L | ✅ Partial — VMDetail now has a "Metrics" tab driven by REST polling against `/api/v1/vms/{id}/stats` (no SSE, no uPlot charts yet) showing current and 5-minute average CPU/Mem/Disk/Net plus history metadata. Chart visualisation + SSE subscription deferred until 4.1.5 ships |
| 4.1.8 | Frontend: dashboard "top 5 VMs by CPU" widget driven by an aggregator endpoint `GET /api/v1/vms/stats/top?metric=cpu&limit=5` (computed from latest sample per ring; no extra storage) | S | ✅ Done — `GET /api/v1/vms/stats/top` ranks running VMs by CPU/mem/disk/net using their latest in-memory sample, the dashboard exposes a metric-switchable "Top 5 Machines" leaderboard, and `vmsmith vm top --metric <m>` provides the same view in the CLI |
| 4.1.9 | CLI: `vmsmith vm stats <id> [--watch] [--fields cpu,mem]` — one-shot prints latest sample + 5-min averages; `--watch` streams via SSE and refreshes a tabular UI in place | S | Use `tablewriter` or simple `\033[2J` redraw; document the env var to disable colors |
=======
| 4.1.7 | Frontend: `web/src/components/MetricChart.jsx` using uPlot; `web/src/hooks/useVMStats.js` doing initial REST fetch + SSE subscription with polling fallback. Add 4 charts (CPU, RAM, disk, net) to VMDetail under a new "Metrics" tab | L | Charts reuse the same hook; pass a `field` prop |
| 4.1.8 | Frontend: dashboard "top 5 VMs by CPU" widget driven by an aggregator endpoint `GET /api/v1/vms/stats/top?metric=cpu&limit=5` (computed from latest sample per ring; no extra storage) | S | |
| 4.1.9 | CLI: `vmsmith vm stats <id> [--watch] [--fields cpu,mem]` — one-shot prints latest sample + 5-min averages; `--watch` streams via SSE and refreshes a tabular UI in place | S | ✅ Done — `internal/cli/vm.go` exposes `vmsmith vm stats` with one-shot output, field filtering, and `--watch` refresh mode |
>>>>>>> 9acb81c (docs(roadmap): mark shipped metrics and events work)
| 4.1.10 | Tests: unit (rate math edge cases incl. counter reset / overflow / stopped VM), integration (`/stats` endpoint with stopped/missing/running VMs, SSE stream replay-not-supported behavior, Prometheus scrape format), E2E (real VM under load shows non-zero CPU + non-zero net during apt install) | L | E2E uses `stress-ng` over SSH to drive CPU; verify the chart hook reflects it within 30s |
| 4.1.11 | Docs: `docs/ARCHITECTURE.md` "Metrics" subsection (sampling, ring buffer, rate math, deferred persistence, Prometheus integration). Add to `docs/PRODUCTION_DEPLOYMENT.md` an example `prometheus.yml` scrape config | S | |

The original 4.1.4 (host-level stats on dashboard) is already done and remains in place above the table for context — kept here as the pre-existing baseline.

#### 4.1.2 Open architectural questions

1. **Guest-agent dependency.** RAM pressure metrics depend on `qemu-guest-agent` running inside the guest. Should the create flow install + enable it via cloud-init by default? Trade-off: smaller base image vs richer metrics out of the box. Recommendation: default-on for VMs created via vmsmith; document the cloud-init line so operators can opt out for embedded distros.
2. **Disaggregating disk/net metrics.** Right now we sum across all disks and interfaces. Once multi-NIC VMs exist (already supported), the chart loses signal. Add `?disaggregate=true` returning a per-device breakdown later.
3. **High-frequency sampling for short-lived spikes.** A 10s sample misses 1s CPU spikes. Adding adaptive sampling (5s when CPU > 80%, 10s otherwise) is tempting but doubles complexity. Defer; document as a known limitation.
4. **Bulk-stats RPC failure modes.** `GetAllDomainStats` can return partial results (some domains missing) when libvirt is under load. Detect and log when the returned VM count is less than the running-VM count, but don't gap-fill — let the chart show the gap.

#### 4.1.3 Dependencies

- **4.2 (events)** — required for 4.1.5 (SSE stream) and the `metrics.sample`-as-event flow. Without 4.2, fall back to polling for the frontend.
- **3.1 (auth)** — required before exposing `/stats` outside localhost; per-VM RBAC waits for 3.1.5.
- **5.5 (multi-host)** — Prometheus labels should include a `host` label from day one (`vmsmith_vm_cpu_percent{vm="...",host="..."}`) so a future multi-host setup can shard by host without renaming series.

### 4.2 Event System & Notifications

No way to know when a VM crashes, completes creation, or changes state. The work in this section is the foundation for several downstream features (audit log, dashboards without polling, schedules in 5.2, future alerting).

**Status (2026-04-28):** Partially started. `internal/vm/events.go` already registers a `DomainEventLifecycleRegister` callback, runs the libvirt default event loop, and propagates state into the `vms` bbolt bucket. The events themselves are **not** persisted, queried, or streamed — every consumer below has to be built. The libvirt callback is the only producer wired up; API handlers and daemon code do not yet emit events.

#### 4.2.0 Architectural overview

**Event taxonomy.** Three sources, all flowing through one in-process bus:

| Source | Origin | Examples |
|---|---|---|
| `libvirt` | `DomainEventLifecycleRegister` callback (already wired) | `vm.started`, `vm.stopped`, `vm.crashed`, `vm.shutdown`, `vm.suspended` |
| `app` | API/CLI mutating handlers, post-success | `vm.created`, `vm.cloned`, `vm.updated`, `vm.deleted`, `snapshot.created`, `snapshot.restored`, `image.uploaded`, `port_forward.added` |
| `system` | Daemon internals | `daemon.started`, `daemon.shutdown`, `quota.exceeded`, `dhcp.exhausted`, `webhook.delivery_failed` |

A typed `Event` record (new `pkg/types/event.go`):

```go
type Event struct {
    ID         string            `json:"id"`           // stringified uint64 today; opaque ordered ID for forward-compat with 5.5 multi-host
    Type       string            `json:"type"`         // e.g. "vm.started"
    Source     string            `json:"source"`       // "libvirt" | "app" | "system"
    VMID       string            `json:"vm_id,omitempty"`
    ResourceID string            `json:"resource_id,omitempty"` // generic (image/template/etc.)
    Severity   string            `json:"severity"`     // "info" | "warn" | "error"
    Message    string            `json:"message"`
    Attributes map[string]string `json:"attributes,omitempty"`
    OccurredAt time.Time         `json:"occurred_at"`
    Actor      string            `json:"actor,omitempty"` // API key alias or "system"
}
```

Outbound payloads include a top-level `schema_version: 1` so downstream consumers can detect breaking changes without sniffing fields.

**In-process event bus (new `internal/events/` package).**
- Single `EventBus` shared across the daemon. Producers call `Publish(ctx, Event)`; subscribers register via `Subscribe(filter) (<-chan Event, cancel func())`.
- Implementation: a fan-out broker with a slice of buffered subscriber channels (default 64) under `sync.RWMutex`. Slow subscribers are **dropped, not blocked** — losing a webhook target must never stall the libvirt event loop. Drops emit one `system`-source `subscriber_lagged` event with the subscriber name, throttled to 1/min.
- ID assignment is centralized in the bus using a monotonically increasing `uint64` counter, persisted in a `events_meta` bucket as 8-byte big-endian. On startup the counter is recovered as `max(persisted_next_id, last_event_id_in_bucket+1)`; persistence is best-effort, idempotent on replay.
- A single goroutine inside the bus consumes the publish channel and (1) writes to bbolt, (2) fans out to live subscribers. This guarantees the persisted log and SSE replay see events in the same order.

**Persistence (bbolt).**
- New bucket `events`, key = 8-byte big-endian `uint64` ID, value = JSON `Event`. Big-endian keys make BoltDB cursor iteration return events in chronological order — what every consumer wants for free.
- `events_meta` bucket stores `next_id` and the timestamp of the last retention sweep.
- No secondary indexes at v1: `?vm_id=` / `?type=` filters scan reverse-chronologically and short-circuit on `limit`. With caps below this is sub-millisecond. Add an `events_by_vm` (`{vm_id}/{id_be}` → empty) index later if profiling shows scan cost.
- Retention loop runs every 60s in a single Update tx: drop oldest until `count ≤ daemon.events.max_records` (default 50_000) and `age ≤ daemon.events.max_age` (default 720h / 30 days). Retention deletes are batched, capped at 5000/sweep so a backlog can't stall the writer.

**SSE protocol (`GET /api/v1/events/stream`).**
- Headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache, no-transform`, `Connection: keep-alive`, `X-Accel-Buffering: no` (defeats nginx response buffering).
- Frame format: `id: <event_id>\nevent: <type>\ndata: <json>\n\n`. Always emit the JSON event body so frontends can ignore `event:` if they want a single handler.
- **Replay.** Honor `Last-Event-ID` request header; fall back to `?since=<id>` query param (browser `EventSource` cannot set custom headers). Replay is capped at `daemon.events.sse_replay_limit` (default 1000). If the client is further behind, return `410 Gone` with code `event_stream_replay_window_exceeded` so the client falls back to paginated REST.
- **Heartbeat.** 30s `: keepalive\n\n` comment frames defeat idle proxies and let the server detect dead connections via write failure.
- **Backpressure.** Per-connection buffered channel (64). On overflow: send a final `event: stream_lagged\n` frame and close. Silently dropping events is worse than terminating — clients can reconnect with the latest received `id`.
- **Lifecycle.** Track active connections in the SSE hub; expose count in `GET /api/v1/host/stats`. On `BeginShutdown`, send `event: shutdown` to all active connections and close.
- **Auth.** Default path uses the same `Authorization: Bearer` middleware as the rest of `/api/v1/*`. EventSource cannot send custom headers, so accept `?api_key=` as a same-origin fallback for the embedded GUI; rate-limit it, never log it, and document the tradeoff. Long-term: short-lived signed cookie issued at login (deferred to 3.1.5).

**Webhook delivery (new `internal/webhooks/`).**
- Configured per webhook (new `webhooks` bucket): `id`, `url`, `secret`, `event_types` (glob list, default `*`), `severity_floor`, `enabled`.
- Dispatcher subscribes to the bus, matches each event against enabled webhooks, and queues delivery in a bounded in-memory queue (1000). Overflow drops oldest and emits `webhook.delivery_failed` (system) with reason `queue_overflow`.
- **Retry policy.** 5 attempts with exponential backoff + jitter: 1s, 5s, 30s, 2m, 10m. Final failure emits a `webhook.delivery_failed` system event with the receiver's last status code and error string — visible in the events stream itself, so operators don't need a separate UI to debug.
- **Signing.** `X-VMSmith-Signature: sha256=<hex>` over the raw request body using the webhook secret (HMAC-SHA256). Also send `X-VMSmith-Event-Id`, `X-VMSmith-Event-Type`, `X-VMSmith-Timestamp` (Unix seconds) so receivers can route quickly and reject replays.
- **SSRF protection.** `daemon.webhooks.allowed_hosts` allowlist (domain or CIDR). Default deny-list always applied: `127.0.0.0/8`, `169.254.0.0/16`, `::1`, `fc00::/7`, `fe80::/10`, and the `192.168.100.0/24` VM NAT range. DNS resolution happens once per attempt and the resolved IP is checked against the deny-list before connecting (prevents DNS-rebinding round-trips).
- **Concurrency.** 4-worker pool. `http.Client` with 10s timeout; connections are not reused across webhooks (per-target `Transport`) to keep one slow receiver from starving the rest.

**Failure modes.**
| Failure | Behavior |
|---|---|
| Bolt write error in persister | log at `error`, increment `events_dropped_total`, do **not** block the producer. The libvirt event loop must keep running. |
| Subscriber channel full (slow consumer) | drop event for that subscriber only; emit throttled `subscriber_lagged` |
| SSE replay window exceeded | 410 with `event_stream_replay_window_exceeded`; client falls back to REST pagination |
| Webhook receiver down | retry per policy; persist final failure as a `webhook.delivery_failed` event |
| Daemon crash mid-publish | producer holds a copy until `Publish` returns; on graceful shutdown, drain the publish channel within `daemon.events.shutdown_timeout` (default 5s) before closing the store |

#### 4.2.1 Task list

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 4.2.1 | Subscribe to libvirt domain lifecycle events | M | ✅ Done — `internal/vm/events.go` registers libvirt lifecycle callbacks and now feeds the event bus-backed flow |
| 4.2.2 | Extend `pkg/types/event.go` to the full record above (`Source`, `Severity`, `Attributes`, `Actor`, `OccurredAt`, `ResourceID`) and add a top-level `schema_version` constant for outbound payloads | S | ✅ Done — `pkg/types/event.go` now carries the full record with additive `omitempty` JSON tags so existing events round-trip unchanged, plus an `EventSchemaVersion` constant |
| 4.2.3 | Create `internal/events/` package: `EventBus` with `Publish`/`Subscribe`/`Close`, ring-buffered fan-out, slow-subscriber drop with throttled `subscriber_lagged` warning, monotonic `uint64` ID assignment | M | ✅ Done — `internal/events/` now owns the bus, fan-out, ID assignment, and concurrency coverage |
| 4.2.4 | Refactor `internal/vm/events.go` to call `bus.Publish` for each lifecycle callback; move state mutation into a separate `persistVMState` consumer subscribed to `libvirt` events. Removes the direct `store.PutVM` call from the libvirt goroutine. | S | Done in part — lifecycle handling and event-bus plumbing improved elsewhere, but `internal/vm/events.go` on `main` still mutates VM state directly via `store.PutVM` inside the libvirt callback |
| 4.2.5 | Promote the existing `events` bucket to the indexed scheme: add `events_meta` (next_id), key events by big-endian `uint64` ID, expose `Store.ListEvents(filter, limit)`, `Store.EventByID(id)`, `Store.PruneEvents(maxRecords, maxAge)` | M | Done in part — indexed storage, filtered listing, replay-by-seq, and record-count pruning now live in `internal/store/bolt.go`; `maxAge` pruning remains future work |
| 4.2.6 | Emit `app`-source events from API handlers: VM create / clone / update / delete, snapshot create / restore / delete, image upload / delete, port forward add / remove. Provide an `events.PublishAppEvent(ctx, type, vmID, attrs)` helper that pulls the actor from the auth middleware context | M | ✅ Done — mutating API handlers publish app events via the shared helper |
| 4.2.7 | Emit `system`-source events for daemon start / shutdown, quota exceeded, DHCP exhaustion, port-forward restore failure | S | ✅ Done in part — daemon startup/shutdown and retention activity now emit system events; remaining niche failure cases can land as follow-ups |
| 4.2.8 | Retention loop (every 60s) honoring `daemon.events.max_records` (default 50_000) and `daemon.events.max_age` (default 720h); cap deletes per sweep at 5000; emit `system` event when retention drops events | S | ✅ Done in part — max-record retention plus observable prune events shipped; age-based pruning remains future work |
| 4.2.9 | Extend `GET /api/v1/events` with `?type=`, `?source=`, `?severity=`, `?until=<id>` filters (in addition to the existing `?vm_id=`, `?since=`, page / per_page), align replies to use `since`/`until` IDs alongside the current `since` timestamp, ensure same auth + rate-limit as other API routes | M | ✅ Done — event listing now supports the additional filters and `until` cursor alongside pagination |
| 4.2.10 | `GET /api/v1/events/stream` SSE endpoint: `Last-Event-ID` header support, `?since=` query fallback, 30s heartbeat, `daemon.events.sse_replay_limit` (default 1000), 410 with `event_stream_replay_window_exceeded` on overflow, `?api_key=` query auth fallback for browser EventSource. New `internal/api/sse.go` helper for headers / heartbeat / `http.Flusher` plumbing. Track active connection count and surface it in `host_stats` | L | ✅ Done in part — SSE replay, heartbeat, overflow handling, and EventSource auth fallback are implemented; connection-count host stats remain future polish |
| 4.2.11 | Frontend: `web/src/hooks/useEventStream.js` — opens `EventSource`, handles reconnect with `Last-Event-ID`, falls back to polling on 410 / network error for 30s, exposes connection state. Replace polling on Dashboard and VMList with the hook; add a small "live" indicator | M | Reconnect uses exponential backoff; tests in `tests/web/` with mock SSE server |
| 4.2.12 | Frontend: "Activity" tab on VMDetail showing reverse-chronological event timeline filtered by `vm_id`, infinite scroll via `until=<id>` | M | ✅ Done — `web/src/pages/VMDetail.jsx` adds an Activity tab that embeds the timeline with `vm_id` pre-filtered. Pagination is page-based (Prev/Next + per-page selector); `until=<id>` infinite scroll deferred until 4.2.10 ships |
| 4.2.13 | Frontend: top-level "Activity" page with filter chips (type / severity / source) and date-range picker, deep links via query params | M | ✅ Done — `web/src/pages/Activity.jsx` lists events with VM / source / severity / type filters; filters are mirrored to URL search params for deep links |
| 4.2.14 | CLI: `vmsmith events list [--vm <id>] [--type <t>] [--source <s>] [--severity <sev>] [--since <duration|id>] [--limit <n>]` (one-shot REST query) and `vmsmith events follow [--vm <id>] [--type <t>]` (SSE, prints events as they arrive, exits on Ctrl-C) | M | ✅ Done — `vmsmith events list` and `vmsmith events follow` are both shipped in `internal/cli/events.go`. `events follow` opens an SSE stream against `/api/v1/events/stream`, applies `--vm/--type/--source/--severity` filters client-side, reconnects with `Last-Event-ID` + `?since=` on transient errors, exits cleanly on Ctrl-C, and treats 401/410 as fatal so the user gets a clear error |
| 4.2.15 | Webhook subsystem: `webhooks` bbolt bucket; `POST/GET/DELETE /api/v1/webhooks` endpoints; HMAC-SHA256 signing; exponential backoff with jitter (1s/5s/30s/2m/10m); bounded queue (1000) with `queue_overflow` system event; SSRF protection via `daemon.webhooks.allowed_hosts` allowlist plus default deny-list of loopback / link-local / metadata / VM-NAT ranges; DNS resolution checked against deny-list before connect | L | New `internal/webhooks/` package; emits `webhook.delivery_failed` on giving up so the events stream itself reflects delivery health |
| 4.2.16 | Webhook UI: list / create / delete in Settings page; "send test event" button (synthesizes a `system.webhook_test` event for that webhook only); show last delivery status, response code, and most recent failure reason | M | |
| 4.2.17 | Tests: unit (`EventBus` fan-out / slow-drop / ID monotonicity, Bolt persister round-trip, retention sweep, webhook signing + SSRF deny), integration (HTTP `/events` filtering + pagination, SSE replay + heartbeat + 410 + shutdown frame, webhook end-to-end with mock receiver covering retry + final failure event), E2E (start a real VM and assert `vm.started` arrives on the live SSE stream) | L | `httptest` SSE harness; mock HMAC verification example included so the test doubles as documentation |
| 4.2.18 | Docs: new `docs/ARCHITECTURE.md` "Event System" section covering bus, persistence layout, SSE protocol (frame format + replay rules), webhook contract (payload shape + signature verification with a 6-line bash example) | S | Cross-link from CLAUDE.md and README |

#### 4.2.2 Open architectural questions

These are deliberately deferred — flag them in the PRs that touch them.

1. **Pre-shutdown drain semantics.** Does `BeginShutdown` stop emitting `app` events (rejecting state-changing requests already returns 503) or only stop accepting new HTTP requests? Recommendation: keep emitting for in-flight requests, then close the bus after the libvirt connection is closed so terminal `vm.stopped`/`daemon.shutdown` events are recorded.
2. **SSE auth for the embedded UI.** Once 3.1 auth is enabled, `EventSource` cannot send custom headers. The `?api_key=` fallback is acceptable for same-origin GUI but should be rate-limited and never written to the request log. Long-term: short-lived signed session cookie (move to 3.1.5).
3. **Webhook payload stability.** Once webhooks ship, the outbound JSON is part of the public contract. Reserve the right to add fields, but never rename or remove. `schema_version: 1` from day one.
4. **Multi-host (5.5) implications.** When events come from multiple hosts, IDs need either a central allocator or a per-host namespace (`{host_id}-{seq}`). Modeling `Event.ID` as a `string` from v1 keeps that option open even though v1 always emits stringified `uint64`.
5. **Cross-cutting actor capture.** 4.2.6's `PublishAppEvent` helper assumes auth middleware has already populated a request-scoped context value. If auth is disabled (`daemon.auth.enabled: false`), `Actor` should be `"anonymous"` — not blank — so audit queries can distinguish "no auth configured" from "system-emitted".

#### 4.2.3 Dependencies

- **3.1 (auth)** — must be in place before exposing webhook config (admin-only) and before the SSE auth fallback design is finalized. The events system itself does not block on auth, but webhook UX does.
- **3.4 (rate limiting)** — `?api_key=` SSE fallback should reuse the existing per-IP token bucket and add a separate stricter bucket for unauthenticated `/events/stream` connection attempts.
- **4.1 (metrics)** — share the in-process retention pattern. The active-SSE-connection gauge belongs in `host_stats`. Consider unifying `events_dropped_total` and `webhook_*` counters once a metrics package exists.
- **5.2 (schedules)** — `schedule.fired` and `schedule.failed` are `app`-source events; design 4.2.6's helper to be schedule-friendly (accept a synthetic actor like `"scheduler"`).

### 4.3 OpenAPI / Swagger Spec

API documentation is hand-written in ARCHITECTURE.md. Auto-generated spec enables client SDKs and API explorers.

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 4.3.1 | Add OpenAPI 3.0 annotations to all handlers (or generate from types) | L | ✅ Done — added hand-written `docs/openapi.yaml` covering the implemented `/api/v1` endpoints, shared schemas, pagination headers, and bearer-auth requirements |
| 4.3.2 | Serve Swagger UI at `/api/docs` | S | ✅ Done — added embedded docs handlers that serve Swagger UI at `/api/docs` and the OpenAPI schema at `/api/openapi.yaml` |
| 4.3.3 | Generate TypeScript API client from OpenAPI spec for frontend | M | ✅ Done — `web/src/api/generated/schema.d.ts` is generated from `docs/openapi.yaml`, `web/src/api/client.ts` now uses `openapi-fetch`, and `npm run generate:api` refreshes the frontend API types |

---

## Phase 5: Advanced Features (Week 10+)

Larger features for power users and advanced use cases.

### 5.1 VNC Console Access

VNC is already configured in domain XML (`internal/vm/domain.go:60` — `<graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>`) but not exposed. The work below adds a browser-based console without ever opening the VNC port to the network.

#### 5.1.0 Architectural overview

**Threat model.** The VNC server is bound to `127.0.0.1` on the host. There is no VNC password by default and the RFB protocol is not encrypted. Two non-negotiable invariants:

1. The VNC TCP port must **never** be reachable from outside the host. All access goes through the authenticated, TLS-terminated daemon.
2. A console session must be authorized to the specific VM, time-limited, and revocable.

**Proxy architecture.**
- Frontend opens `WebSocket /api/v1/vms/{id}/console?ticket=<one-time-token>` from the browser.
- Daemon's websocket handler validates the ticket, looks up the live VNC TCP port (`virsh domdisplay` or libvirt `Domain.GetXMLDesc` → parse `<graphics port='...'/>`), and pipes bytes between the websocket frame stream and `net.Dial("tcp", "127.0.0.1:<vnc_port>")`.
- Subprotocol negotiation: `Sec-WebSocket-Protocol: binary` — noVNC sends and expects raw RFB bytes inside binary websocket frames.
- Bidirectional copy uses two goroutines (`ws→tcp`, `tcp→ws`) joined on first close. Set both directions to a 30s idle write deadline; the noVNC client sends framebuffer-update requests on activity, so a long idle indicates a wedged connection.
- The handler holds a `Conn` reference in a per-VM "active console session" map so admin-initiated VM stop / delete can `Close()` the websocket cleanly (`event: console.session_terminated`).

**One-time ticket flow.** EventSource and WebSocket both have weak custom-header support in browsers, and embedding API keys in URLs leaks them into proxy logs. The flow:

1. Client `POST /api/v1/vms/{id}/console/ticket` with normal `Authorization: Bearer` auth → response `{ "ticket": "...", "expires_at": "...", "websocket_url": "wss://.../api/v1/vms/{id}/console?ticket=..." }`.
2. Tickets are 32 random bytes (base64url-encoded), single-use, valid for 60s, scoped to the VM ID and the issuing API key. Stored in an in-memory map (`map[string]ticket{vmID, expires, apiKey}`) under `sync.RWMutex` with a janitor goroutine sweeping every 30s.
3. Websocket handler consumes the ticket on connect (delete-on-read), validates VM ID match, then upgrades. After upgrade, the ticket is gone — refresh requires a new POST.

**VNC password handling (optional, per-VM).**
- New `vnc_password` field on `types.VM` (write-only via API: accepted on create/update, redacted on read; persisted in bbolt as bcrypt-hashed for storage and reversibly encrypted for the libvirt domain XML using a daemon-level key from `daemon.console.password_key`).
- When set, regenerate domain XML with `<graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1' passwd='<plain>'/>` and apply on next start.
- The proxy still requires the ticket. The VNC password is a defense-in-depth layer for the rare case where the daemon socket is mis-bound.
- The proxy does **not** transparently inject the password — the noVNC client prompts the user. (Auto-injection would defeat the second factor.)

**Frontend integration.**
- Bundle noVNC's `core/rfb.js` (~200KB minified) under `web/src/vendor/novnc/`. Pin a specific version; do not load from CDN.
- New `web/src/pages/VMConsole.jsx` opens at `/vms/{id}/console`. On mount: POST for ticket → instantiate `RFB(canvas, websocketUrl)` → handle `disconnect`/`credentialsrequired`/`securityfailure` events.
- Keyboard capture: noVNC handles this; toggle full-screen with a button (calls `rfb.focus()`); add a "send Ctrl-Alt-Del" button (`rfb.sendCtrlAltDel()`).
- Reconnect on transient failure: re-POST for a new ticket and re-instantiate `RFB`. After 3 failures within 30s, surface an error and stop auto-retrying.

**Serial console alternative.** `<console type='pty'>` already exists in domain XML. libvirt exposes the pty path via `Domain.OpenConsole()` which returns a libvirt `Stream`. Wrap that stream in a websocket the same way as VNC, but serve as `text` subprotocol (UTF-8). Frontend uses `xterm.js` (~150KB). The same ticket flow applies; tickets carry an `intent: "vnc" | "serial"` field so a single-purpose ticket can't be redirected.

**Resource limits.**
- `daemon.console.max_concurrent_sessions` (default 8): global cap to prevent a leak of file descriptors and goroutines. Excess returns HTTP 429 `console_limit_reached`.
- `daemon.console.max_session_seconds` (default 3600): hard idle timeout. After expiry the daemon sends a websocket close frame.
- `daemon.console.idle_timeout_seconds` (default 600): closes the session after 10 min of zero traffic in either direction.

**Observability.** Each session emits three `app`-source events (depends on 4.2): `console.session_started`, `console.session_ended` (with reason: `client_close` / `server_idle` / `vm_stopped` / `daemon_shutdown` / `error`), and `console.session_terminated` for admin-revoked sessions. Active session count is reported in `host_stats`.

**Security checklist (must hold for v1).**
- [ ] Ticket endpoint requires `Authorization: Bearer`.
- [ ] Ticket is single-use, ≤60s TTL, scoped to VM ID + API key.
- [ ] Websocket handler rejects requests without a valid ticket with HTTP 401.
- [ ] VNC port stays bound to `127.0.0.1` — verified by an integration test that asserts external connect refuses.
- [ ] No ticket appears in any access log (the middleware redacts `?ticket=`).
- [ ] Sessions are forcibly closed on VM stop, VM delete, daemon shutdown, and API-key revocation.
- [ ] `wss://` is required when TLS is configured (`ws://` rejected with 403 to avoid mixed-content downgrade).

#### 5.1.1 Task list

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 5.1.1 | Add `internal/console/` package: ticket store (in-memory map + janitor), `IssueTicket(vmID, apiKey)`, `ConsumeTicket(token, vmID) (apiKey, error)`. Unit tests for single-use, expiry, VM-scope mismatch | M | Pure Go; no libvirt deps |
| 5.1.2 | Add `POST /api/v1/vms/{id}/console/ticket` endpoint returning `{ticket, expires_at, websocket_url}`. Reuse `Authorization: Bearer` middleware. Validate VM exists and is in `running` state (refuse `stopped` with 409 `vm_not_running`) | S | Middleware order: auth → rate-limit → handler |
| 5.1.3 | Add `vm.Manager.GetConsoleEndpoint(ctx, id, intent) (host string, port int, err error)`: parses domain XML for `<graphics>` (VNC) or returns the pty path for serial. Implement on `LibvirtManager` and `MockManager` | M | Mock returns a synthetic listener for tests |
| 5.1.4 | Add `GET /api/v1/vms/{id}/console` websocket endpoint: validate ticket, dial VNC TCP socket, bidirectional copy with idle deadlines, register session in active map. Use `gorilla/websocket` (already pulled in indirectly? — confirm during 5.1.1) or `nhooyr.io/websocket`. Subprotocol `binary` | L | Handle: client close, server close, dial failure (502 `console_unreachable`), TLS-mismatch (403 `mixed_content_blocked`) |
| 5.1.5 | Wire active-session map into `vm.Manager.Stop`/`Delete` so admin actions force-close in-flight sessions. Emit `console.session_terminated` (depends on 4.2) | S | |
| 5.1.6 | Add daemon config: `daemon.console.max_concurrent_sessions` (8), `daemon.console.max_session_seconds` (3600), `daemon.console.idle_timeout_seconds` (600), `daemon.console.password_key` (random base64 secret). Document in `vmsmith.yaml.example` | S | `password_key` empty disables per-VM VNC passwords |
| 5.1.7 | Vendor noVNC under `web/src/vendor/novnc/` (pinned version, license header preserved). Add `web/src/pages/VMConsole.jsx` with ticket fetch, RFB instantiation, Ctrl-Alt-Del button, fullscreen toggle, status overlay | L | Add to router as `/vms/:id/console`; "Console" button on VMDetail opens in a new tab to give a clean keyboard capture surface |
| 5.1.8 | Add VNC password support: `vnc_password` field on `VMSpec`/`VMUpdateSpec`, redact-on-read in API responses, persist as bcrypt hash + reversible-encrypted blob (AES-GCM with `daemon.console.password_key`). Regenerate domain XML on next start with `passwd='...'`. Add unit tests for round-trip and redaction | M | Reject password on update if VM is running and require restart message |
| 5.1.9 | Serial console (`?intent=serial`): `vm.Manager.OpenSerialConsole(ctx, id) (io.ReadWriteCloser, error)` wrapping `Domain.OpenConsole`. Websocket handler uses `text` subprotocol. Bundle `xterm.js` and add a "Serial" tab next to "VNC" on the VMConsole page | M | Tickets carry `intent`; ticket for VNC cannot open serial and vice versa |
| 5.1.10 | Redact `?ticket=` from request middleware logs (extend the existing logging middleware to scrub the query param). Add a unit test asserting the ticket never appears in captured log output | S | |
| 5.1.11 | Tests: unit (ticket store concurrency + expiry, password encryption round-trip, redaction), integration (ticket → websocket happy path with a fake VNC echo server, ticket reuse rejected, expired ticket rejected, VM stop forces close, idle timeout), Playwright (open console page, see canvas mount, send Ctrl-Alt-Del) | L | Use `httptest.NewServer` + `gorilla/websocket` test helpers |
| 5.1.12 | Docs: new section in `docs/ARCHITECTURE.md` covering proxy design, ticket flow, security checklist; add operator note in `docs/PRODUCTION_DEPLOYMENT.md` about firewalling the host's loopback (no action needed if iptables doesn't touch `lo`) | S | |

#### 5.1.2 Open architectural questions

1. **Per-session vs per-VM concurrency.** Should two operators be able to view the same VM's console simultaneously? RFB supports it (read-only viewers); recommendation for v1: allow one read-write session and any number of read-only viewers, gated by `read_only: true` on the ticket. Defer to PR.
2. **TLS termination behind reverse proxy.** When 3.2 (TLS) is satisfied via nginx/Caddy in front of the daemon, the websocket arrives as `ws://` on the loopback. The handler should trust `X-Forwarded-Proto` only when the source IP matches a configured `daemon.trusted_proxies` CIDR list. Co-design with 3.2.4 docs.
3. **Clipboard sync.** noVNC supports clipboard via the `cuttext`/`bell` extensions. Cross-origin clipboard in browsers is restricted; document as "best effort" and disable by default behind `daemon.console.allow_clipboard`.
4. **Audit detail.** Should keystrokes / mouse events be logged? Privacy implications are significant; recommendation: log only session boundaries (start/end + bytes-in/out totals), never payloads. Make this an explicit non-feature in docs.

#### 5.1.3 Dependencies

- **3.1 (auth)** — required. The ticket endpoint and websocket auth assume `Authorization: Bearer` middleware is in place. Without auth, anyone on the network gets full console access.
- **3.2 (TLS)** — strongly recommended before exposing this beyond localhost. RFB inside `ws://` is plaintext.
- **4.2 (events)** — `console.session_*` events are emitted via the events bus; if 4.2 hasn't shipped, fall back to `logger.Info` and revisit.
- **3.4 (rate limiting)** — apply a stricter bucket on ticket issuance (e.g., 10/min per API key) to prevent enumeration of VM IDs through ticket POSTs.

### 5.2 Scheduled Operations

Automate routine tasks like snapshots, scheduled start/stop windows, and backups. The original task list glossed over several correctness questions (catch-up after restart, concurrent runs, missed window vs daylight-saving anomalies, action retries) that determine whether operators can actually trust the scheduler with production data.

#### 5.2.0 Architectural overview

**Core design.** A single in-process scheduler goroutine drives a list of `Schedule` records loaded from bbolt. Use `github.com/robfig/cron/v3` with the `WithSeconds` and `WithLocation` options for predictable spec parsing. The scheduler does **not** spawn a goroutine per schedule for execution — instead, every fire dispatches to a bounded worker pool so a stuck snapshot can't starve the rest.

**`Schedule` record (`pkg/types/schedule.go`):**

```go
type Schedule struct {
    ID             string         `json:"id"`             // sched-<unix-nano>
    Name           string         `json:"name"`
    VMID           string         `json:"vm_id"`          // empty = applies to all VMs (admin-only schedules)
    TagSelector    []string       `json:"tag_selector,omitempty"` // alternative to VMID; OR-of-tags match
    Action         string         `json:"action"`         // "snapshot" | "start" | "stop" | "restart"
    CronSpec       string         `json:"cron_spec"`      // 6-field with seconds: "0 30 2 * * *"
    Timezone       string         `json:"timezone"`       // IANA name; empty -> daemon's TZ
    Enabled        bool           `json:"enabled"`
    CatchUpPolicy  string         `json:"catch_up_policy"` // "skip" (default) | "run_once" | "run_all"
    MaxConcurrent  int            `json:"max_concurrent"`  // default 1; serializes overlapping fires per-schedule
    RetentionCount int            `json:"retention_count,omitempty"` // for snapshot action; 0 = use quota default
    Params         map[string]any `json:"params,omitempty"` // action-specific (e.g., snapshot name template)
    CreatedAt      time.Time      `json:"created_at"`
    UpdatedAt      time.Time      `json:"updated_at"`
    LastFiredAt    *time.Time     `json:"last_fired_at,omitempty"`
    LastResult     string         `json:"last_result,omitempty"` // "success" | "error: ..."
    NextFireAt     *time.Time     `json:"next_fire_at,omitempty"` // computed; cached for UI
}
```

**Run records (`pkg/types/schedule_run.go`)** — separate bucket so the schedule definition stays small:

```go
type ScheduleRun struct {
    ID         string    `json:"id"`           // run-<unix-nano>
    ScheduleID string    `json:"schedule_id"`
    VMID       string    `json:"vm_id"`        // resolved VM (tag selectors fan out)
    StartedAt  time.Time `json:"started_at"`
    FinishedAt time.Time `json:"finished_at,omitempty"`
    Status     string    `json:"status"`       // "running" | "success" | "error" | "skipped"
    Error      string    `json:"error,omitempty"`
    SkipReason string    `json:"skip_reason,omitempty"` // "vm_not_found" | "vm_already_stopped" | "concurrent_run" | "catch_up_skipped"
}
```

**bbolt layout:**
- `schedules` — key = schedule ID, value = JSON `Schedule`.
- `schedule_runs` — key = `{schedule_id}/{run_id_be}` (big-endian timestamp suffix). Compound key keeps a single forward cursor scan per-schedule history. Retention: per-schedule cap of `daemon.schedules.max_run_history` (default 200) trimmed in the same Update tx that writes a new run.
- `schedule_meta` — key = `last_tick`, value = RFC3339 timestamp of the most recent successful tick (used for catch-up after a restart).

**Catch-up semantics (the subtle part).** When the daemon restarts, some firings may have been missed. The scheduler reads `schedule_meta/last_tick` and computes which schedules would have fired between `last_tick` and `now()`. Three policies, configurable per schedule (`catch_up_policy`):

| Policy | Behavior | Use case |
|---|---|---|
| `skip` | Ignore all missed fires. Resume normal scheduling. | Default. Idempotent actions where a missed window doesn't matter (e.g., periodic snapshot, the next one will replace it). |
| `run_once` | If any fires were missed, run the action exactly once, then resume. | Backups: missing one is OK, but you want the system to know it's behind. |
| `run_all` | Replay every missed fire in chronological order, sequentially. | Auditable schedules where every interval matters (rare; warn the operator if window > 24h). |

`schedule_meta/last_tick` is updated every minute by the scheduler tick (whether or not any schedule fires). On startup, if it's missing, treat as `now()` (no catch-up — fresh install).

**Daylight-saving and timezone handling.**
- `Schedule.Timezone` is an IANA name (`America/New_York`). robfig/cron's `WithLocation` is per-scheduler, not per-entry, so we run **one cron instance per distinct timezone** and route each schedule to the right instance.
- For ambiguous local times during DST transition (e.g., 02:30 on a fall-back day occurring twice), document that we use the wall-clock time once — robfig's behavior matches Go's `time.ParseInLocation`. Surface this in the schedule edit UI with a warning when the spec touches 02:00–03:00.

**Concurrency control.**
- Per-schedule: `MaxConcurrent` (default 1). If a fire arrives while the previous run is in progress, write a `ScheduleRun{Status: "skipped", SkipReason: "concurrent_run"}` and emit a `schedule.fire_skipped` event (depends on 4.2).
- Global: `daemon.schedules.worker_pool_size` (default 4). The dispatcher queues fires onto the worker pool. Queue overflow drops with `skip_reason: "queue_full"` and emits a `system` event.

**Action handlers.** Pluggable via a small `ScheduleActionFunc` registry:

```go
type ActionFunc func(ctx context.Context, vmID string, params map[string]any) error
```

v1 actions:

| Action | Implementation |
|---|---|
| `snapshot` | `vm.Manager.CreateSnapshot(ctx, vmID, name)`. Snapshot name template defaults to `auto-{schedule_name}-{rfc3339}`. Honors `RetentionCount`: after creation, list snapshots whose names start with `auto-{schedule_name}-` and delete oldest until count ≤ retention. |
| `start` | `vm.Manager.Start(ctx, vmID)`. Skip if already running with `skip_reason: "vm_already_running"`. |
| `stop` | `vm.Manager.Stop(ctx, vmID)`. Skip if already stopped. |
| `restart` | Stop then Start with a 30s wait between. Single run record. |

**Tag-selector fan-out.** When `TagSelector` is set (and `VMID` is empty), the scheduler resolves the matching VM list at fire time — not at schedule-create time. Each matched VM gets its own `ScheduleRun` record. New VMs picked up automatically; deleted VMs simply produce zero runs.

**Failure & retry.**
- Per-action timeout from `daemon.schedules.action_timeout` (default 5m for snapshot, 30s for start/stop).
- On transient error, retry up to `daemon.schedules.max_retries` (default 2) with 30s/2m backoff. Persistent error is recorded as `Status: "error"` with the last error string.
- Retries do **not** create new `ScheduleRun` records — they update the existing one's `Error` field with each attempt's outcome (`attempt N: <err>; attempt N+1: <err>`).

**Permissions (depends on 3.1).** Schedule CRUD requires the same auth as VM mutation. Once 3.1.5 (RBAC) lands, schedules are admin-only. v1 treats any authenticated request as authorized.

**Observability.** Emits `schedule.created`, `schedule.deleted`, `schedule.updated`, `schedule.fired`, `schedule.fire_skipped`, `schedule.fire_succeeded`, `schedule.fire_failed`, `schedule.catch_up_replayed` (with N missed count). All `app`-source via 4.2's `PublishAppEvent` helper, with `Actor: "scheduler"` for fires.

#### 5.2.1 Task list

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 5.2.1 | Define `pkg/types/schedule.go` (`Schedule`) and `pkg/types/schedule_run.go` (`ScheduleRun`) per the schemas above | S | Pure types |
| 5.2.2 | Add `schedules`, `schedule_runs`, `schedule_meta` bbolt buckets; `Store.{Put,Get,List,Delete}Schedule`, `Store.AppendRun(scheduleID, run)` (with retention trim in the same tx), `Store.ListRuns(scheduleID, limit)`, `Store.GetLastTick`/`SetLastTick` | M | `schedule_runs` keys are `{schedule_id}/{ts_be}` for fast per-schedule scans |
| 5.2.3 | Create `internal/scheduler/` package: per-timezone `cron.Cron` instances, schedule registration/deregistration on CRUD, bounded worker pool, action registry. Unit tests for: spec parsing, timezone routing, max-concurrent skip, queue overflow | L | Use `robfig/cron/v3` with `WithSeconds` |
| 5.2.4 | Implement catch-up logic on daemon startup: compare `last_tick` to `now()`, fire each schedule per its `catch_up_policy`. Tick the meta key every 60s thereafter | M | Cap `run_all` replay at 100 missed fires per schedule with a warning log to prevent storms |
| 5.2.5 | Action registry with `snapshot`, `start`, `stop`, `restart` handlers. Snapshot honors `RetentionCount` and uses a name template scoped to the schedule. Per-action timeout + retry with backoff | M | Reject actions on non-existent/deleted VMs with `skip_reason: "vm_not_found"` |
| 5.2.6 | Tag-selector resolution at fire time: query `Store.ListVMs` filtered by tag set; fan out to one `ScheduleRun` per matched VM, all queued onto the same worker pool | S | |
| 5.2.7 | API: `POST /api/v1/schedules`, `GET /api/v1/schedules` (with `?vm_id=`, `?action=`, `?enabled=`), `GET /api/v1/schedules/{id}`, `PATCH /api/v1/schedules/{id}` (toggle enabled, update spec/retention), `DELETE /api/v1/schedules/{id}`, `GET /api/v1/schedules/{id}/runs` (paginated, reverse-chronological), `POST /api/v1/schedules/{id}/run-now` (synthesize a manual fire, recorded as `Actor: "<api-key-alias>"`) | M | Validate cron spec on create/update; reject invalid timezone with `invalid_timezone` |
| 5.2.8 | CLI: `vmsmith schedule create --vm <id|--tag <t>> --action <a> --cron "<spec>" --timezone <tz> --retention <n> --catch-up <skip|run_once|run_all>`, `schedule list [--vm <id>]`, `schedule show <id>` (definition + last 20 runs), `schedule edit <id>` (PATCH), `schedule delete <id>`, `schedule run-now <id>` | M | Cron spec validated client-side via `robfig/cron` parser; surface `next_fire_at` in `list` output |
| 5.2.9 | Frontend: `web/src/pages/Schedules.jsx` listing all schedules with enabled toggle, next-fire timestamp, last-result chip, "Run now" button. `web/src/components/ScheduleForm.jsx` for create/edit with cron-spec helper (preset chips: hourly, daily 02:00, weekly Sunday) | M | Plus a "Recent runs" expander on each row showing the last 5 runs |
| 5.2.10 | Frontend: schedule section on VMDetail page showing schedules whose `vm_id == this.id` or whose tag selector matches this VM's tags. "Add schedule" opens the form pre-filled with this VM | S | |
| 5.2.11 | Tests: unit (timezone routing across DST transitions using a fake clock, catch-up policies, max-concurrent skip, retention trim under concurrent appends, action retry/backoff), integration (CRUD endpoints, run-now synthesis, run history pagination), E2E (real schedule fires a snapshot on a real VM and the snapshot appears) | L | Use `clockwork.NewFakeClock` for time-travel; `cron.WithChain(cron.SkipIfStillRunning(...))` not enough — we need our own per-schedule mutex for clearer skip reasons |
| 5.2.12 | Docs: new `docs/SCHEDULES.md` covering cron-spec syntax (note the 6-field with-seconds form), timezone/DST rules, catch-up policies with worked examples, retention semantics. Cross-link from CLAUDE.md | S | |
| 5.2.13 | (Already done) Snapshot retention auto-delete | S | ✅ Done — `daemon.quotas.max_snapshots_per_vm` auto-deletes the oldest snapshots. The scheduler's `RetentionCount` is independent and scoped to *auto-named* snapshots from the same schedule, so manual snapshots are not affected |

#### 5.2.2 Open architectural questions

1. **Distributed schedules (future 5.5).** When multi-host lands, two daemons must not both fire the same schedule. Either elect a leader (use bbolt-on-shared-storage advisory locks, fragile), or namespace schedules per-host (`Schedule.HostID`). Recommendation: namespace per-host now (`HostID` empty = "this daemon's host"), defer leader election.
2. **Idempotency keys for actions.** If `run_all` catch-up replays 30 missed `snapshot` fires after a 30-day outage, we'd create 30 snapshots and immediately retention-trim 28. Better: pass a `IdempotencyKey: "<schedule_id>:<scheduled_time_unix>"` to the action; snapshot handler skips if a snapshot with that key already exists. Defer to follow-up; v1 just trims.
3. **Pause window override.** Operators want "don't run any schedule between 09:00–17:00 on weekdays" without editing every cron spec. Add `daemon.schedules.maintenance_window` later.
4. **Cron-spec backward compat.** robfig/cron supports both 5-field and 6-field forms via constructor options. Pick one (6-field, seconds-required) and document it as the only accepted form to avoid 5-vs-6 confusion. Reject 5-field input with a clear error pointing to the 6-field equivalent.

#### 5.2.3 Dependencies

- **3.1 (auth)** — required before exposing schedule CRUD; "run-now" can fire arbitrary VM operations.
- **4.2 (events)** — schedule lifecycle and fire events flow through the events bus. `Actor: "scheduler"` distinguishes them from operator-initiated runs in the audit log.
- **3.5 (quotas)** — snapshot retention from 3.5.1 stacks with 5.2's per-schedule `RetentionCount`. Document that the lower of the two wins.
- **5.5 (multi-host)** — informs the `HostID` namespacing question above. Add the field now even though it's unused in v1.

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
| 5.4.1 | Add `?page=&per_page=` query params to `GET /vms`, `GET /images`, `GET /logs` | M | ✅ Done — list/log endpoints now accept `page` + `per_page` (with `limit` kept as an alias on logs) and return `X-Total-Count` for the full matching result set |
| 5.4.2 | Add `?status=running&sort=created_at&order=desc` filtering to `GET /vms` | M | ✅ Done — `GET /api/v1/vms` now supports case-insensitive `status=<state>` filtering and composes with the existing `tag=<tag>` filter |
| 5.4.3 | Update frontend tables to support server-side pagination | M | ✅ Done — VM list, image list, and log viewer now use backend pagination metadata/controls instead of loading unbounded frontend tables |
| 5.4.4 | Add `--limit` and `--offset` flags to CLI list commands | S | ✅ Done — `vmsmith vm list` and `vmsmith image list` now support local `--limit` / `--offset` pagination with CLI test coverage and updated docs |

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
| 6.2.1 | Create DEB package build (for Ubuntu/Debian) | M | ✅ Done — `make deb` now stages the release binary, `/etc/vmsmith/config.yaml`, and `vmsmith.service` into `bin/packages/*.deb` via `scripts/build-deb.sh` |
| 6.2.2 | Create RPM package build (for Rocky/RHEL/Fedora) | M | ✅ Done — added `make rpm` + `scripts/build-rpm.sh` to produce an RPM that packages the linux/amd64 release binary, systemd unit, and default config for Rocky/RHEL/Fedora installs |
| 6.2.3 | Create container image for VMSmith daemon (requires privileged mode for libvirt) | M | ✅ Done — added multi-stage `Dockerfile`, `scripts/docker-entrypoint.sh`, `.dockerignore`, and `docs/CONTAINER.md` for privileged local/lab usage |
| 6.2.4 | Add installation script (`curl -sSL https://... \| sh`) | S | ✅ Done — added `scripts/install.sh` to download the published Linux amd64 release asset and install it to `/usr/local/bin/vmsmith`, plus README/production docs and a local smoke test |

### 6.3 Documentation Expansion

| # | Task | Effort | Notes |
|---|------|--------|-------|
| 6.3.1 | Write production deployment guide (systemd, TLS, reverse proxy, firewall rules) | M | ✅ Done — `docs/PRODUCTION_DEPLOYMENT.md` covers systemd, TLS via reverse proxy, firewall rules, logging, backups, and upgrade guidance |
| 6.3.2 | Write networking deep-dive (NAT vs macvtap vs bridge, when to use each, troubleshooting) | M | ✅ Done — added `docs/NETWORKING.md` covering mode selection, tradeoffs, examples, and troubleshooting |
| 6.3.3 | Add example automation scripts (bash/python) for common workflows | S | ✅ Done — added `examples/` with bash and Python API automation examples for common create/wait/port-forward flows |
| 6.3.4 | Create short video/GIF demos for README | S | |

---

## Summary: Suggested Next Priority Order

With the initial platform hardening work mostly done, the next highest-value roadmap items are:

| Priority | Area | Key Tasks | Why |
|----------|------|-----------|-----|
| **P0** | VM Resource Metrics | 4.1.5 – 4.1.8, 4.1.10 – 4.1.11 | REST stats + CLI are in place, but live streaming, charts, dashboard rollups, and docs polish still remain |
| **P1** | Events | 4.2.7 – 4.2.11 | Core event API/UI shipped; remaining work is deeper system-event coverage, connection observability, and broader live updates |
| **P1** | VM Cloning | 2.1.2, 2.1.7 | Main clone flows ship today, but the libvirt implementation and deeper integration/E2E coverage remain the last notable gaps |
| **P1** | OpenAPI Tooling | 4.3.1 – 4.3.3 | Spec, Swagger UI, and typed frontend client are in place; remaining work is maintenance and follow-on SDK ergonomics rather than first delivery |
| **P2** | Console Access | 5.1.1 – 5.1.4 | High user value, but larger implementation surface |
| **P2** | Scheduled Operations | 5.2.1 – 5.2.6 | Useful automation once observability and lifecycle features are in place |
| **P3** | OVA Import/Export | 5.3.1 – 5.3.3 | Helpful interoperability feature, but less urgent than core ops gaps |
| **P3** | Multi-Host Management | 5.5.1 – 5.5.4 | Still a long-term architecture track rather than near-term delivery |

---

## Notes

- Effort estimates assume familiarity with the codebase. First-time contributors should add ~50% buffer.
- Phases are not strictly sequential — items from Phase 2 can begin as soon as Phase 1 CI is in place.
- Each task should be a single PR where possible. Larger tasks (L/XL) may need multiple PRs.
- All new features should include: API endpoint, CLI command, frontend UI, tests, and CLAUDE.md update.
