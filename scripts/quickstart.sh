#!/usr/bin/env bash
# quickstart.sh — One-command, end-to-end VMSmith installer for Ubuntu/Debian hosts.
#
# Installs system dependencies (qemu/kvm, libvirt, Go, Node), builds VMSmith from
# source at a chosen git ref, lays down a production config + data directories,
# and runs the daemon as a managed systemd service. Re-running performs a
# NON-DISRUPTIVE UPGRADE: the new binary is built before the old service is
# stopped, the metadata DB is snapshotted first, and a failed health check
# automatically rolls back to the previous binary + DB snapshot.
#
# Install the latest code on the main branch:
#
#   curl -fsSL https://raw.githubusercontent.com/chinmay28/VMSmith/main/scripts/quickstart.sh | sudo bash
#
# Pin a specific release tag (or any branch/commit) via VMSMITH_REF:
#
#   curl -fsSL https://raw.githubusercontent.com/chinmay28/VMSmith/main/scripts/quickstart.sh | sudo VMSMITH_REF=v1.0.0 bash
#
# Run it again any time to upgrade to the newest main (or a newer tag) in place
# without losing VMs, images, or metadata.
#
# Manage the service afterwards:
#
#   systemctl status vmsmith
#   journalctl -u vmsmith -f
#
# ---------------------------------------------------------------------------
# Configurable environment variables (all optional)
# ---------------------------------------------------------------------------
#   VMSMITH_REPO        Git URL to clone               (default: GitHub upstream)
#   VMSMITH_REF         Branch / tag / commit to build (default: main)
#   VMSMITH_PREFIX      Source checkout root           (default: /opt/vmsmith)
#   VMSMITH_DATA_DIR    Persistent data root           (default: /var/lib/vmsmith)
#   VMSMITH_CONFIG_DIR  Config directory               (default: /etc/vmsmith)
#   VMSMITH_BIN_DIR     Binary install directory       (default: /usr/local/bin)
#   VMSMITH_LISTEN      Daemon listen address          (default: 0.0.0.0:8080)
#   INSTALL_DEPS        auto | never                   (default: auto)
#   BACKUP_KEEP         Pre-upgrade snapshots to keep  (default: 10)
# ---------------------------------------------------------------------------

set -euo pipefail

VMSMITH_REPO="${VMSMITH_REPO:-https://github.com/chinmay28/VMSmith.git}"
VMSMITH_REF="${VMSMITH_REF:-main}"
VMSMITH_PREFIX="${VMSMITH_PREFIX:-/opt/vmsmith}"
VMSMITH_DATA_DIR="${VMSMITH_DATA_DIR:-/var/lib/vmsmith}"
VMSMITH_CONFIG_DIR="${VMSMITH_CONFIG_DIR:-/etc/vmsmith}"
VMSMITH_BIN_DIR="${VMSMITH_BIN_DIR:-/usr/local/bin}"
VMSMITH_LISTEN="${VMSMITH_LISTEN:-0.0.0.0:8080}"
INSTALL_DEPS="${INSTALL_DEPS:-auto}"
BACKUP_KEEP="${BACKUP_KEEP:-10}"

GO_VERSION="1.22.5"
NODE_MAJOR="22"

SRC_DIR="${VMSMITH_PREFIX}/src"
BACKUP_DIR="${VMSMITH_DATA_DIR}/backups"
LOG_DIR="/var/log/vmsmith"
DB_PATH="${VMSMITH_DATA_DIR}/state/vmsmith.db"
CONFIG_FILE="${VMSMITH_CONFIG_DIR}/config.yaml"
BIN_PATH="${VMSMITH_BIN_DIR}/vmsmith"
SERVICE_FILE="/etc/systemd/system/vmsmith.service"

# Health-check target: never poll a wildcard address.
PORT="${VMSMITH_LISTEN##*:}"
HEALTH_HOST="${VMSMITH_LISTEN%:*}"
case "$HEALTH_HOST" in
  ""|0.0.0.0|"[::]"|::) HEALTH_HOST="127.0.0.1" ;;
esac
HEALTH_URL="http://${HEALTH_HOST}:${PORT}/api/version"

# Upgrade-state, populated as we go so rollback knows what to undo.
IS_UPGRADE="no"
DB_SNAPSHOT=""
BIN_BACKUP=""

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------
if [ -t 1 ]; then
  C_BLUE="\033[1;34m"; C_GREEN="\033[1;32m"; C_YELLOW="\033[1;33m"; C_RED="\033[1;31m"; C_OFF="\033[0m"
else
  C_BLUE=""; C_GREEN=""; C_YELLOW=""; C_RED=""; C_OFF=""
fi

step() { printf "${C_BLUE}==>${C_OFF} %s\n" "$*"; }
info() { printf "    %s\n" "$*"; }
ok()   { printf "${C_GREEN}    ✓ %s${C_OFF}\n" "$*"; }
warn() { printf "${C_YELLOW}warning:${C_OFF} %s\n" "$*" >&2; }
fail() { printf "${C_RED}error:${C_OFF} %s\n" "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Step 1 — Preflight checks
# ---------------------------------------------------------------------------
preflight() {
  step "Checking prerequisites"

  [ "$(id -u)" -eq 0 ] || fail "must run as root (use sudo)"
  command -v systemctl >/dev/null 2>&1 || fail "systemd (systemctl) is required"

  if [ ! -r /etc/os-release ]; then
    warn "cannot read /etc/os-release; assuming a Debian/Ubuntu host"
    PKG="apt"
  else
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}${ID_LIKE:-}" in
      *debian*|*ubuntu*) PKG="apt" ;;
      *) PKG="unknown" ;;
    esac
    info "Detected ${PRETTY_NAME:-${ID:-unknown}}"
  fi

  if [ ! -e /dev/kvm ]; then
    warn "/dev/kvm not present — VMs will fall back to slow software emulation."
    warn "Enable nested/hardware virtualization on this host for usable performance."
  fi
  ok "Prerequisites satisfied"
}

# ---------------------------------------------------------------------------
# Step 2 — Dependencies
# ---------------------------------------------------------------------------
install_deps() {
  case "$INSTALL_DEPS" in
    never|no|0|off)
      step "Skipping dependency install (INSTALL_DEPS=$INSTALL_DEPS)"
      return 0 ;;
  esac

  if [ "$PKG" != "apt" ]; then
    warn "Automatic dependency install only supports apt (Ubuntu/Debian)."
    warn "Install qemu-kvm, libvirt(+dev), genisoimage, iptables, Go >=${GO_VERSION}, Node >=${NODE_MAJOR} manually,"
    warn "then re-run with INSTALL_DEPS=never."
    return 0
  fi

  step "Installing system dependencies via apt"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq \
    qemu-kvm qemu-utils \
    libvirt-daemon-system libvirt-clients libvirt-dev \
    genisoimage iptables bridge-utils \
    build-essential pkg-config git curl ca-certificates
  ok "Base packages installed"

  install_go
  install_node

  step "Enabling libvirtd"
  systemctl enable --now libvirtd >/dev/null 2>&1 || \
    warn "could not enable libvirtd; start it manually with 'systemctl enable --now libvirtd'"
  ok "libvirtd active"
}

go_ok() {
  command -v go >/dev/null 2>&1 || return 1
  go version 2>/dev/null | grep -qE 'go1\.(2[2-9]|[3-9][0-9])(\.[0-9]+)?' || return 1
}

install_go() {
  if go_ok; then
    info "Go $(go version | awk '{print $3}') already present (>= go${GO_VERSION})"
    return 0
  fi
  step "Installing Go ${GO_VERSION}"
  local tarball="/tmp/go${GO_VERSION}.linux-amd64.tar.gz"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o "$tarball"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tarball"
  rm -f "$tarball"
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  ok "Go $(/usr/local/go/bin/go version | awk '{print $3}') installed"
}

node_ok() {
  command -v node >/dev/null 2>&1 || return 1
  node --version 2>/dev/null | grep -qE "v(2[2-9]|[3-9][0-9])" || return 1
}

install_node() {
  if node_ok; then
    info "Node $(node --version) already present (>= v${NODE_MAJOR})"
    return 0
  fi
  step "Installing Node.js ${NODE_MAJOR}"
  curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - >/dev/null 2>&1
  apt-get install -y -qq nodejs
  ok "Node $(node --version) installed"
}

# Make freshly-installed Go available to this shell for the build step.
ensure_path() {
  if ! go_ok && [ -x /usr/local/go/bin/go ]; then
    export PATH="$PATH:/usr/local/go/bin"
  fi
}

# ---------------------------------------------------------------------------
# Step 3 — Source checkout / update
# ---------------------------------------------------------------------------
sync_source() {
  step "Fetching VMSmith source (ref: ${VMSMITH_REF})"

  # If invoked from inside an existing VMSmith checkout, build it in place.
  local here=""
  if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
    here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd || true)"
  fi
  if [ -n "$here" ] && grep -qs 'module github.com/vmsmith/vmsmith' "$here/go.mod" 2>/dev/null; then
    SRC_DIR="$here"
    info "Building from local checkout at ${SRC_DIR}"
    PREV_SHA="$(git -C "$SRC_DIR" rev-parse HEAD 2>/dev/null || echo unknown)"
    ok "Source ready (${PREV_SHA})"
    return 0
  fi

  command -v git >/dev/null 2>&1 || fail "git is required to fetch the source"
  mkdir -p "$VMSMITH_PREFIX"

  if [ -d "$SRC_DIR/.git" ]; then
    PREV_SHA="$(git -C "$SRC_DIR" rev-parse HEAD 2>/dev/null || echo unknown)"
    info "Updating existing checkout (was ${PREV_SHA})"
    git -C "$SRC_DIR" fetch --tags --force origin "$VMSMITH_REF"
    git -C "$SRC_DIR" checkout -f FETCH_HEAD
  else
    info "Cloning ${VMSMITH_REPO}"
    git clone --depth 1 --branch "$VMSMITH_REF" "$VMSMITH_REPO" "$SRC_DIR" 2>/dev/null \
      || git clone "$VMSMITH_REPO" "$SRC_DIR"
    git -C "$SRC_DIR" checkout -f "$VMSMITH_REF" 2>/dev/null || true
    PREV_SHA="unknown"
  fi
  ok "Source at $(git -C "$SRC_DIR" rev-parse --short HEAD 2>/dev/null || echo "$VMSMITH_REF")"
}

# ---------------------------------------------------------------------------
# Step 4 — Build (while the old service keeps running)
# ---------------------------------------------------------------------------
build() {
  step "Building VMSmith (this can take a few minutes)"
  ensure_path
  go_ok || fail "Go >= ${GO_VERSION} not found on PATH; install it or set INSTALL_DEPS=auto"

  ( cd "$SRC_DIR" && make deps && make build )
  [ -x "$SRC_DIR/bin/vmsmith" ] || fail "build did not produce $SRC_DIR/bin/vmsmith"
  ok "Built $("$SRC_DIR/bin/vmsmith" version 2>/dev/null | head -n1 || echo vmsmith)"
}

# ---------------------------------------------------------------------------
# Step 5 — Data directories + config (never clobbers existing data)
# ---------------------------------------------------------------------------
setup_data() {
  step "Preparing data directories"
  mkdir -p \
    "${VMSMITH_DATA_DIR}/vms" \
    "${VMSMITH_DATA_DIR}/images" \
    "${VMSMITH_DATA_DIR}/state" \
    "$BACKUP_DIR" \
    "$LOG_DIR" \
    "$VMSMITH_CONFIG_DIR"
  # World-executable so the libvirt-qemu user can traverse to VM disks.
  chmod 0755 "$VMSMITH_DATA_DIR" "${VMSMITH_DATA_DIR}/vms" "${VMSMITH_DATA_DIR}/images" "$LOG_DIR"
  chmod 0750 "${VMSMITH_DATA_DIR}/state" "$BACKUP_DIR"
  ok "Directories ready under ${VMSMITH_DATA_DIR}"

  if [ -f "$CONFIG_FILE" ]; then
    info "Keeping existing config at ${CONFIG_FILE}"
  else
    step "Writing default config to ${CONFIG_FILE}"
    write_config
    ok "Config written"
  fi
}

write_config() {
  cat > "$CONFIG_FILE" <<EOF
# VMSmith configuration — generated by scripts/quickstart.sh
# Safe to edit; quickstart.sh will not overwrite an existing file.
daemon:
  listen: "${VMSMITH_LISTEN}"
  pid_file: "/run/vmsmith.pid"
  log_file: "${LOG_DIR}/vmsmith.log"
  auth:
    enabled: false        # set true + add api_keys before exposing beyond localhost
    api_keys: []

libvirt:
  uri: "qemu:///system"

storage:
  images_dir: "${VMSMITH_DATA_DIR}/images"
  base_dir: "${VMSMITH_DATA_DIR}/vms"
  db_path: "${DB_PATH}"
  virtio_win_iso: ""      # set for Windows guests; see docs/WINDOWS_GUESTS.md

network:
  name: "vmsmith-net"
  subnet: "192.168.100.0/24"
  dhcp_start: "192.168.100.10"
  dhcp_end: "192.168.100.254"

defaults:
  cpus: 2
  ram_mb: 2048
  disk_gb: 20

quotas:
  max_vms: 0
  max_total_cpus: 0
  max_total_ram_mb: 0
  max_total_disk_gb: 0

schedules:
  enabled: true
EOF
}

# ---------------------------------------------------------------------------
# Step 6 — Snapshot before swapping (the heart of non-disruptive upgrades)
# ---------------------------------------------------------------------------
snapshot_state() {
  # Back up the current binary so we can roll back instantly.
  if [ -x "$BIN_PATH" ]; then
    IS_UPGRADE="yes"
    local ts; ts="$(date +%Y%m%d-%H%M%S)"
    BIN_BACKUP="${BACKUP_DIR}/vmsmith-bin-${ts}"
    cp -p "$BIN_PATH" "$BIN_BACKUP"
    info "Saved previous binary → ${BIN_BACKUP}"
  fi

  # Snapshot the metadata DB (+ its WAL sidecars) with the service stopped.
  if [ -f "$DB_PATH" ]; then
    step "Snapshotting metadata database"
    systemctl stop vmsmith 2>/dev/null || true
    local ts; ts="$(date +%Y%m%d-%H%M%S)"
    DB_SNAPSHOT="${BACKUP_DIR}/vmsmith-${ts}.db"
    cp -p "$DB_PATH" "$DB_SNAPSHOT"
    [ -f "${DB_PATH}-wal" ] && cp -p "${DB_PATH}-wal" "${DB_SNAPSHOT}-wal" || true
    [ -f "${DB_PATH}-shm" ] && cp -p "${DB_PATH}-shm" "${DB_SNAPSHOT}-shm" || true
    ok "Snapshot → ${DB_SNAPSHOT}"
    prune_backups
  fi
}

prune_backups() {
  # Keep only the newest $BACKUP_KEEP DB snapshots and binary backups.
  ls -1t "${BACKUP_DIR}"/vmsmith-*.db 2>/dev/null | tail -n +"$((BACKUP_KEEP + 1))" | while read -r old; do
    rm -f "$old" "${old}-wal" "${old}-shm"
  done
  ls -1t "${BACKUP_DIR}"/vmsmith-bin-* 2>/dev/null | tail -n +"$((BACKUP_KEEP + 1))" | xargs -r rm -f
}

# ---------------------------------------------------------------------------
# Step 7 — Install binary + systemd unit and (re)start
# ---------------------------------------------------------------------------
install_service() {
  step "Installing binary and systemd unit"
  install -m 0755 "$SRC_DIR/bin/vmsmith" "$BIN_PATH"
  info "Installed ${BIN_PATH}"

  cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=VMSmith daemon
Wants=network-online.target libvirtd.service
After=network-online.target libvirtd.service

[Service]
Type=simple
User=root
Group=root
Environment=HOME=/root
ExecStart=${BIN_PATH} daemon start --config ${CONFIG_FILE}
Restart=on-failure
RestartSec=5
RuntimeDirectory=vmsmith
RuntimeDirectoryMode=0755
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable vmsmith >/dev/null 2>&1 || true
  systemctl restart vmsmith
  ok "Service started"
}

# ---------------------------------------------------------------------------
# Step 8 — Health check, with automatic rollback on upgrade failure
# ---------------------------------------------------------------------------
health_check() {
  step "Waiting for VMSmith to become healthy (${HEALTH_URL})"
  local i
  for i in $(seq 1 30); do
    if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
      ok "Healthy after ${i}s"
      return 0
    fi
    sleep 1
  done
  return 1
}

rollback() {
  warn "New version failed its health check."
  if [ "$IS_UPGRADE" != "yes" ]; then
    warn "This was a fresh install — nothing to roll back to."
    warn "Inspect logs: journalctl -u vmsmith -n 100 --no-pager"
    return 1
  fi

  step "Rolling back to the previous version"
  systemctl stop vmsmith 2>/dev/null || true

  if [ -n "$BIN_BACKUP" ] && [ -x "$BIN_BACKUP" ]; then
    install -m 0755 "$BIN_BACKUP" "$BIN_PATH"
    info "Restored previous binary"
  fi
  if [ -n "$DB_SNAPSHOT" ] && [ -f "$DB_SNAPSHOT" ]; then
    cp -p "$DB_SNAPSHOT" "$DB_PATH"
    [ -f "${DB_SNAPSHOT}-wal" ] && cp -p "${DB_SNAPSHOT}-wal" "${DB_PATH}-wal" || rm -f "${DB_PATH}-wal"
    [ -f "${DB_SNAPSHOT}-shm" ] && cp -p "${DB_SNAPSHOT}-shm" "${DB_PATH}-shm" || rm -f "${DB_PATH}-shm"
    info "Restored database snapshot"
  fi

  systemctl restart vmsmith
  if health_check; then
    warn "Rolled back successfully to the previous working version."
    warn "The failed upgrade's DB snapshot is preserved at: ${DB_SNAPSHOT:-<none>}"
    return 0
  fi

  fail "Rollback also failed to become healthy. DB snapshot preserved at: ${DB_SNAPSHOT:-<none>}. Check: journalctl -u vmsmith -n 100 --no-pager"
}

summary() {
  echo
  printf "${C_GREEN}VMSmith is installed and running.${C_OFF}\n"
  echo
  info "Web UI / API : http://${HEALTH_HOST}:${PORT}"
  info "Config       : ${CONFIG_FILE}"
  info "Data         : ${VMSMITH_DATA_DIR}"
  info "Service      : systemctl status vmsmith"
  info "Logs         : journalctl -u vmsmith -f"
  echo
  info "Upgrade later by re-running this script (set VMSMITH_REF=<tag> to pin a release)."
  echo
  warn "Auth is disabled by default — enable daemon.auth before exposing beyond localhost."
}

main() {
  preflight
  install_deps
  sync_source
  build            # build new version BEFORE touching the running service
  setup_data
  snapshot_state   # back up binary + DB, stop old service
  install_service
  if health_check; then
    summary
  elif rollback; then
    summary
  else
    exit 1
  fi
}

main "$@"
