# Contributing to VMSmith

Thanks for helping improve VMSmith.

This guide keeps contributions predictable and reviewable without adding a lot of process overhead.

## Prerequisites

For local development on Linux, install:

- Go 1.22+
- Node.js 22+
- libvirt development packages (`libvirt-dev` / `libvirt-devel`)
- QEMU/KVM + libvirt daemon for integration work

Bootstrap a machine with the included helper scripts:

```bash
# Ubuntu / Debian
sudo bash scripts/install-deps-ubuntu.sh

# Rocky / RHEL
sudo bash scripts/install-deps-rocky.sh
```

## Local setup

```bash
git clone <your-fork-or-repo-url>
cd VMSmith
make deps
make web-install
make install-githooks
```

Common development loops:

```bash
# Full production-style build (frontend + backend)
make build

# Faster backend-only iteration
make build-go

# Run backend + frontend together
make dev

# Or run them separately
make dev-api
make dev-web
```

- `make dev` starts both processes and stops them together on Ctrl-C
- `make dev-api` starts the daemon on `:8080`
- `make dev-web` starts the Vite frontend on `:3000` and proxies `/api` to `:8080`
- `make install-githooks` enables the versioned `.githooks/pre-commit` hook for this clone

The pre-commit hook runs `make fmt && make lint` before each commit so simple formatting and lint issues are caught locally.

## Testing before opening a PR

Run the smallest useful set for the code you touched.

### Backend changes

```bash
make fmt
make test
```

If `golangci-lint` is installed locally, also run:

```bash
make lint
```

### Frontend changes

```bash
make web
make test-web
```

### Changes that affect real VM workflows

These require a Linux host with virtualization enabled, libvirt running, and suitable test images:

```bash
make test-integration
make test-e2e
```

If you cannot run a required test locally, mention that clearly in the PR description.

## Pull request expectations

Please keep PRs focused:

- One logical change per PR where practical
- Include tests or explain why tests are not practical
- Update docs when behavior, flags, APIs, or workflows change
- Prefer follow-up issues/PRs over bundling unrelated cleanup

Suggested PR checklist:

- [ ] Code builds locally
- [ ] Relevant tests pass locally
- [ ] Docs/README updated if needed
- [ ] Screenshots or CLI examples included for UI/UX changes
- [ ] Any skipped checks are called out in the PR body

## Commit style

A strict convention is not required, but these are preferred:

- `feat: add ...`
- `fix: handle ...`
- `docs: update ...`
- `test: cover ...`
- `chore: tidy ...`

Write commit messages that explain the user-facing or maintenance impact, not just the implementation detail.

## Scope guidance

Good first contributions in this repo include:

- validation and error-message improvements
- API and CLI test coverage
- small frontend usability fixes
- documentation and developer-experience improvements

Larger features should ideally reference the roadmap in `docs/ROADMAP.md` so maintainers can align on scope before substantial work begins.
