# VMSmith Project Memory

Working directory: `/home/chinmay/Dropbox/projects/VMSmith`
Current branch: `firstPR` (base: `main`)

## What it is
CLI tool + REST API + embedded React GUI for provisioning and managing QEMU/KVM VMs on Linux.
Single static binary with embedded bbolt DB and embedded React SPA (via `go:embed`).

## Key architecture
- **Language:** Go 1.22, CGO_ENABLED=1 (libvirt C bindings via `libvirt.org/go/libvirt`)
- **Frontend:** React + Vite + Tailwind, built to `internal/web/dist/` (go:embed target)
- **Router:** Chi v5
- **DB:** bbolt (embedded, zero-config)
- **VM backend:** libvirt / QEMU/KVM
- **Network:** libvirt NAT (`vmsmith-net`, 192.168.100.0/24), iptables port forwarding
- **Config:** `~/.vmsmith/config.yaml` or `/etc/vmsmith/config.yaml`; falls back to compiled defaults

## Important file paths
| Path | Purpose |
|------|---------|
| `cmd/vmsmith/main.go` | Entrypoint → `cli.Execute()` |
| `internal/daemon/daemon.go` | HTTP server + libvirt connect + signal handling |
| `internal/config/config.go` | Config struct, DefaultConfig(), EnsureDirs() |
| `internal/vm/manager.go` | VMManager interface + libvirt impl |
| `internal/vm/mock_manager.go` | In-memory mock used by all tests |
| `internal/network/nat.go` | libvirt NAT network setup |
| `internal/network/portforward.go` | iptables rules |
| `internal/web/embed.go` | `go:embed dist/*` |
| `web/vite.config.js` | Vite outputs to `../internal/web/dist` (not `web/dist`!) |
| `vmsmith.yaml.example` | Config template |

## Runtime storage defaults (config.go)
- VM disks: `/var/lib/vmsmith/vms/`
- Images: `/var/lib/vmsmith/images/`
- DB: `~/.vmsmith/vmsmith.db`
- PID: `/var/run/vmsmith.pid`
- Libvirt URI: `qemu:///system`

## Build
```bash
make deps        # go mod tidy + download
make build       # frontend (Vite) + backend (Go CGO) → bin/vmsmith
make build-go    # backend only (skip frontend)
```
CGO is required — cannot cross-compile without matching libvirt-dev.

## Testing (151 tests, all use mocks — no real libvirt needed)
```bash
make test             # all Go tests (unit + integration)
make test-unit        # unit only
make test-integration # API integration (httptest + mock)
make test-web         # Playwright E2E (needs make test-web-deps first)
make docker-test      # all Go tests inside Docker (easiest, no deps needed)
```
Test files: `internal/*/\*_test.go`, `tests/web/`

## Docker support (added in commit 81f1de9)
4-stage Dockerfile: `frontend` → `builder` → `test` → `runtime` (debian:bookworm-slim)

Key gotcha: Vite outputs to `../internal/web/dist` relative to `web/`, so in Docker
the COPY is `--from=frontend /app/internal/web/dist ./internal/web/dist`.

```bash
make docker-build   # build runtime image
make docker-run     # docker compose up -d
make docker-stop    # docker compose down
make docker-logs    # tail logs
make docker-test    # build test stage + run all Go tests
make docker-shell   # exec bash in running container
```

Container requirements:
- `--device /dev/kvm` (host must have KVM enabled)
- `--cap-add NET_ADMIN NET_RAW SYS_ADMIN`
- `--security-opt apparmor:unconfined` (Ubuntu hosts)
- `--network host` (VM bridge/NAT networking must be in host network namespace)

Entrypoint: `scripts/docker-entrypoint.sh` — starts virtlogd + libvirtd, polls
for `/var/run/libvirt/libvirt.sock` (max 30s), enables IP forwarding, then `exec "$@"`.

Volumes: `vmsmith-data` → `/var/lib/vmsmith`, `vmsmith-db` → `/root/.vmsmith`

## Dev workflow
```bash
make dev-api   # Terminal 1: Go daemon on :8080
make dev-web   # Terminal 2: Vite dev server on :3000 (proxies /api → :8080)
```
