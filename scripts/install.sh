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

mkdir -p "$BIN_DIR"
install -m 0755 "$TMPBIN" "$BIN_DIR/vmsmith"

log "Installed vmsmith to $BIN_DIR/vmsmith"
log "Run 'vmsmith version' to verify the install."
