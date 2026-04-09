#!/usr/bin/env sh
set -eu

REPO="${REPO:-chinmay28/VMSmith}"
VERSION="${VERSION:-latest}"
BIN_NAME="vmsmith"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"
ASSET_NAME="${ASSET_NAME:-vmsmith-linux-amd64}"
BASE_URL="https://github.com/${REPO}/releases"
CURL_BIN="${CURL_BIN:-curl}"
INSTALL_BIN="${INSTALL_BIN:-install}"
SUDO_BIN="${SUDO_BIN:-sudo}"

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

OS="${UNAME_S:-$(uname -s)}"
ARCH="${UNAME_M:-$(uname -m)}"

case "$OS" in
  Linux) ;;
  *) fail "this installer currently supports Linux only" ;;
esac

case "$ARCH" in
  x86_64|amd64) ;;
  *) fail "this installer currently supports x86_64/amd64 only" ;;
esac

need_cmd "$CURL_BIN"
need_cmd "$INSTALL_BIN"

if [ "$VERSION" = "latest" ]; then
  DOWNLOAD_URL="${BASE_URL}/latest/download/${ASSET_NAME}"
else
  DOWNLOAD_URL="${BASE_URL}/download/${VERSION}/${ASSET_NAME}"
fi

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM
TMPFILE="${TMPDIR}/${BIN_NAME}"

log "Downloading ${BIN_NAME} from ${DOWNLOAD_URL}"
"$CURL_BIN" -fsSL "$DOWNLOAD_URL" -o "$TMPFILE"
chmod 0755 "$TMPFILE"

log "Installing ${BIN_NAME} to ${BIN_DIR}/${BIN_NAME}"
if "$INSTALL_BIN" -m 0755 "$TMPFILE" "${BIN_DIR}/${BIN_NAME}" 2>/dev/null; then
  :
elif command -v "$SUDO_BIN" >/dev/null 2>&1; then
  "$SUDO_BIN" "$INSTALL_BIN" -m 0755 "$TMPFILE" "${BIN_DIR}/${BIN_NAME}"
else
  fail "could not write to ${BIN_DIR}; re-run as root or install sudo"
fi

log "Installed ${BIN_DIR}/${BIN_NAME}"
log "Run '${BIN_NAME} --help' to get started."
