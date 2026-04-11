#!/usr/bin/env sh
set -eu

REPO_OWNER="chinmay28"
REPO_NAME="VMSmith"
DEFAULT_BIN_DIR="/usr/local/bin"

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
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

[ "$OS" = "Linux" ] || fail "unsupported OS: $OS (Linux only)"

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
