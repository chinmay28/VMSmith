# Roadmap Pending-Tasks Report

**Date:** 2026-06-12
**Scope:** Disposition of every roadmap item that was still open before this
change set, what this change set completed, and — for the items that remain
open — exactly what is missing (details, dependencies, or decisions) before
they can be picked up.

---

## 1. Completed in this change set

| Item | What shipped |
|---|---|
| 5.1.8 | VNC password support: write-only `vnc_password` on `VMSpec`/`VMUpdateSpec`, persisted as bcrypt hash + AES-GCM blob keyed by `daemon.console.password_key`, baked into the domain XML `passwd=` attribute (XML-escaped), redact-on-read across create/get/list/clone/update responses, 409 `vm_running` on changes while running, CLI `--vnc-password` on `vm create` / `vm edit`, unit + integration tests. |
| 5.1.9 | Serial console: `?intent=vnc\|serial` on ticket issuance (intent-bound single-use tickets), `vm.Manager.OpenSerialConsole` wrapping libvirt `Domain.OpenConsole` (with a Mock loopback for tests), websocket proxy path on the `text` subprotocol, xterm.js "Serial" tab on the console page. |
| 5.1.7 | Browser VNC console: noVNC vendored under `web/src/vendor/novnc/` (pinned, license preserved), `VMConsole.jsx` with ticket fetch, RFB mount, Ctrl-Alt-Del, fullscreen, status overlay + reconnect, `/vms/:id/console` route, Console button on VMDetail. |
| 5.1.11 | Remaining slices: VNC-password unit tests (round-trip, wrong-key, redaction) and Playwright GUI specs for the console page (mock server). The websocket integration matrix was already in place. |
| 5.6.9 | UEFI Secure Boot + vTPM: `secure_boot` / `tpm` VMSpec flags (windows-11 ⇒ default on; secure boot additionally EFI-gated), `<firmware><feature/></firmware>` + SMM + `tpm-crb`/swtpm domain XML, host probes for `swtpm` and secboot OVMF builds with typed errors (`tpm_unavailable`, `secure_boot_unavailable`, `secure_boot_requires_uefi`) for explicit requests and warn-and-degrade for variant defaults, CLI flags, tests. |
| 4.1.10 (E2E slice) | `tests/e2e/test_api_metrics.py` — real VM under CPU + network load shows non-zero rates on `/vms/{id}/stats`. |
| 4.2.17 (E2E slice) | `tests/e2e/test_api_events_stream.py` — `vm.started` arrives on the live SSE stream when a real VM starts. |
| 5.2.11 (E2E slice) | `tests/e2e/test_api_schedules_e2e.py` — a real schedule fires a snapshot action and the `auto-*` snapshot appears. |
| 5.6.16 | `tests/e2e/test_windows_guest.py` — Windows guest boot + RDP (3389) reachability, gated behind `--windows-image` / `VMSMITH_WINDOWS_IMAGE` with a generous `--windows-ip-timeout`. |
| 2.1.2 | Verified already implemented (`LibvirtManager.Clone` in `internal/vm/lifecycle.go` builds the overlay via `createClonedDisk`, fresh MAC/DHCP reservation/cloud-init ISO, stopped-source guard). The roadmap row was stale; updated. |

**Note on the four E2E additions:** the tests are authored, registered
(markers `metrics`, `events`, `schedules`, `windows`; Makefile targets
`test-e2e-metrics|events|schedules|windows`) and syntax-checked, but they can
only *execute* against a host with QEMU/KVM, libvirt, a running daemon, and a
Rocky (resp. Windows) image. This container has none of those, so the roadmap
rows stay "shipped, pending first soak on real hardware".

---

## 2. Open items that cannot be completed from the codebase

### 1.1.6 — Branch protection rules for `main`
Pure GitHub repository configuration (Settings → Branches, or the
`PUT /repos/{owner}/{repo}/branches/main/protection` admin API). Requires
repo-admin permissions that CI agents and contributors do not have.
**Action for repo owner:** require the `Backend build and tests` and
`Frontend build and mock GUI tests` checks, require a PR review, disable
force pushes/deletions.

### 6.3.4 — Video/GIF demos for the README
Requires a human (or a display-capable environment) to record terminal/GUI
sessions. Suggested low-effort path: `vhs` (charmbracelet) tape scripts for
the CLI flows and a Playwright video capture for the GUI; both could be
checked in so the demos are reproducible. Blocked here: no display, no
running daemon.

---

## 3. Open items blocked on design decisions (need owner input)

### 3.1.5 — Role-based access control (`admin` / `operator` / `viewer`)
Explicitly marked "(Future) … optional follow-up" in the roadmap. The current
auth model is flat API keys (`auth.api_keys`). Open decisions before
implementation:
1. Key→role mapping shape in config (`api_keys: [{key, role}]` breaks the
   existing flat-list config; a parallel `auth.roles` map is
   backward-compatible).
2. Enforcement point: per-route middleware table in `router.go` (clean fit
   for chi) vs. per-handler checks.
3. Whether the websocket/console and SSE endpoints count as `operator` or
   `viewer` surfaces.
Estimated L once decided; no technical blockers.

### 5.3.1–5.3.3 — OVA/OVF import/export
Self-contained but large (L+L+M). Key design questions to settle first:
1. **Disk conversion location + space:** `qemu-img convert -O vmdk` doubles
   disk usage transiently; needs a scratch-dir config and disk-space guard
   (the image-upload path already has one to copy).
2. **OVF dialect:** VirtualBox and VMware disagree on hardware sections.
   Recommend targeting VMware-flavoured OVF 1.0 + SHA1 manifest, since
   that's what most appliances expect.
3. **Import mapping:** OVF CPU/RAM/disk map cleanly onto `VMSpec`, but
   network sections do not (vmsmith's NAT-first model). Recommend: ignore
   OVF networks, always attach `vmsmith-net`, and surface a warning.
4. **API shape:** export should be an async job (`POST /vms/{id}/export` →
   job id → download), since conversion of a 100 GB disk takes minutes; the
   codebase has no job framework yet — that is the real dependency.

### 5.5.1–5.5.4 — Multi-host management
The roadmap itself marks 5.5.1 "Architecture decision needed" (XL). The fork
in the road: central coordinator + per-host agents vs. one daemon fanning out
over remote libvirt URIs (`qemu+ssh://`). Everything else in the section
(hosts config, `--host` on create, host dashboard) is downstream of that
choice. Material constraints discovered in the code while implementing this
change set: bbolt is single-writer/embedded (a coordinator would need to own
all metadata or move to a shared store), port-forward iptables rules are
host-local, and the image store is a local directory (needs per-host streams
or shared storage). Recommend writing an ADR before any code.

### 5.6.13 — RDP console in the GUI
Needs a protocol-bridge decision: an HTML5 RDP client in the browser
(hyper-heavy; no maintained pure-JS RDP stack comparable to noVNC) vs. a
`guacd` (Apache Guacamole daemon) sidecar the websocket proxy speaks to.
`guacd` is the realistic option but adds a non-Go runtime dependency and a
packaging question (the daemon currently ships as a single binary + systemd
unit). The ticket/proxy plumbing from 5.1.x is reusable as-is (new intent
`rdp`, ticket-bound, same session caps), so the remaining work is ~all in the
bridge decision + deployment story.

---

## 4. Open items that are large but unblocked (ranked next picks)

### 5.6.11 — Unattended install from a raw Windows ISO (XL)
No design blockers, but it is the largest single remaining item:
`Autounattend.xml` generation (edition/locale/partitioning/RDP/WinRM/
cloudbase-init bootstrap), an `--install-iso` create path with an empty
system disk + cdrom boot order (today's model is overlay-from-base-image
everywhere — `createOverlayDisk` would need an empty-disk sibling and
`DomainParamsFromSpec` a boot-order override), and a "first boot completes
Setup" wait loop. Recommend splitting into: (a) empty-disk + boot-order
plumbing, (b) Autounattend generator + tests, (c) E2E behind the existing
`--windows-image`-style opt-in.

### 4.1.10 / 4.2.17 / 5.2.11 — first execution on real hardware
The E2E tests now exist (section 1). The remaining step is operational: run
`make test-e2e-metrics test-e2e-events test-e2e-schedules` on a KVM host with
a Rocky image and fix whatever timing flakes surface. Recommend wiring an
optional nightly job on a self-hosted runner.

---

## 5. Dependency graph of what's left

```
1.1.6  ──────────────  repo-admin only
6.3.4  ──────────────  human recording
3.1.5  ── ADR: role model ──► M-L implementation
5.3.x  ── ADR: async-job framework ──► export ──► import
5.5.x  ── ADR: coordinator vs remote-URI ──► everything else in 5.5
5.6.13 ── ADR: guacd sidecar packaging ──► reuse 5.1.x ticket/proxy
5.6.11 ── (a) empty-disk plumbing ─► (b) Autounattend ─► (c) E2E
E2E soak ─ needs KVM hardware/self-hosted runner
```
