#!/usr/bin/env sh
set -eu

REPO_OWNER="chinmay28"
REPO_NAME="VMSmith"
DEFAULT_BIN_DIR="/usr/local/bin"

log() {
  printf '%s\n' "$*"
}

warn() {
  printf 'warning: %s\n' "$*" >&2
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

sudo_run() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    fail "this step needs root; re-run as root or install sudo"
  fi
}

detect_os() {
  OS_ID=""
  OS_ID_LIKE=""
  if [ -r /etc/os-release ]; then
    # Read in subshells so /etc/os-release variables (ID, VERSION, NAME, ...)
    # do not leak into this script's scope and clobber our own VERSION etc.
    OS_ID=$(. /etc/os-release 2>/dev/null; printf '%s' "${ID:-}")
    OS_ID_LIKE=$(. /etc/os-release 2>/dev/null; printf '%s' "${ID_LIKE:-}")
  fi
}

pkg_family() {
  case "$OS_ID" in
    ubuntu|debian) printf 'apt\n'; return ;;
    rocky|rhel|centos|almalinux|fedora) printf 'dnf\n'; return ;;
  esac
  case " $OS_ID_LIKE " in
    *debian*|*ubuntu*) printf 'apt\n'; return ;;
    *rhel*|*fedora*|*centos*) printf 'dnf\n'; return ;;
  esac
  printf 'unknown\n'
}

install_deps_apt() {
  log "Detected Ubuntu/Debian (${OS_ID:-unknown}); installing runtime dependencies via apt-get..."
  sudo_run env DEBIAN_FRONTEND=noninteractive apt-get update
  sudo_run env DEBIAN_FRONTEND=noninteractive apt-get install -y \
    qemu-kvm \
    qemu-utils \
    libvirt-daemon-system \
    libvirt-clients \
    genisoimage \
    iptables \
    bridge-utils
}

install_deps_dnf() {
  log "Detected Rocky/RHEL (${OS_ID:-unknown}); installing runtime dependencies via dnf..."
  if command -v dnf >/dev/null 2>&1; then
    PM="dnf"
  elif command -v yum >/dev/null 2>&1; then
    PM="yum"
  else
    fail "neither dnf nor yum found; cannot install dependencies"
  fi
  sudo_run "$PM" install -y \
    qemu-kvm \
    qemu-img \
    libvirt \
    libvirt-client \
    genisoimage \
    iptables-nft
}

enable_libvirtd() {
  if command -v systemctl >/dev/null 2>&1; then
    sudo_run systemctl enable --now libvirtd || \
      warn "could not enable libvirtd; start it manually with 'systemctl enable --now libvirtd'"
  else
    warn "systemctl not found; start libvirtd manually for vmsmith to function"
  fi
}

install_deps() {
  detect_os
  family="$(pkg_family)"
  case "$family" in
    apt) install_deps_apt ;;
    dnf) install_deps_dnf ;;
    *)
      warn "unrecognized distro (ID=${OS_ID:-?}, ID_LIKE=${OS_ID_LIKE:-?}); skipping dependency install"
      warn "install qemu-kvm, libvirt, genisoimage and iptables manually before running vmsmith"
      return 0
      ;;
  esac
  enable_libvirtd
}

uname_s() {
  if [ -n "${UNAME_S:-}" ]; then
    printf '%s\n' "$UNAME_S"
  else
    uname -s
  fi
}

uname_m() {
  if [ -n "${UNAME_M:-}" ]; then
    printf '%s\n' "$UNAME_M"
  else
    uname -m
  fi
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

install_bin() {
  target="$1"

  if install -m 0755 "$TMPBIN" "$target" 2>/dev/null; then
    return 0
  fi

  if command -v sudo >/dev/null 2>&1; then
    sudo install -m 0755 "$TMPBIN" "$target"
    return 0
  fi

  fail "could not write to ${BIN_DIR}; re-run as root or install sudo"
}

need_cmd uname
need_cmd mktemp
need_cmd curl
need_cmd chmod
need_cmd install

OS="$(uname_s)"
ARCH_RAW="$(uname_m)"
BIN_DIR="${BIN_DIR:-$DEFAULT_BIN_DIR}"
VERSION="${VERSION:-latest}"
INSTALL_DEPS="${INSTALL_DEPS:-yes}"

[ "$OS" = "Linux" ] || fail "unsupported OS: $OS (Linux only)"

case "$INSTALL_DEPS" in
  1|y|yes|true|on)
    install_deps
    ;;
  0|n|no|false|off|skip)
    log "Skipping dependency install (INSTALL_DEPS=$INSTALL_DEPS)."
    ;;
  *)
    fail "invalid INSTALL_DEPS value: $INSTALL_DEPS (use yes/no)"
    ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64)
    ARCH="amd64"
    ;;
  *)
    fail "unsupported architecture: $ARCH_RAW (supported: x86_64/amd64)"
    ;;
esac

if [ "$VERSION" = "latest" ]; then
  RELEASE_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/latest/download/vmsmith-linux-${ARCH}"
else
  RELEASE_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${VERSION}/vmsmith-linux-${ARCH}"
fi

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM
TMPBIN="$TMPDIR/vmsmith"

log "Downloading ${REPO_NAME} ${VERSION} (${ARCH})..."
curl -fsSL "$RELEASE_URL" -o "$TMPBIN"
chmod 0755 "$TMPBIN"

if ! mkdir -p "$BIN_DIR" 2>/dev/null; then
  if command -v sudo >/dev/null 2>&1; then
    sudo mkdir -p "$BIN_DIR"
  else
    fail "could not create ${BIN_DIR}; re-run as root or install sudo"
  fi
fi

install_bin "$BIN_DIR/vmsmith"

log "Installed vmsmith to $BIN_DIR/vmsmith"
log "Run 'vmsmith version' to verify the install."
