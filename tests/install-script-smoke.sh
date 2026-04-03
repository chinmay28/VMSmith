#!/usr/bin/env sh
set -eu

REPO_ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
SCRIPT="${REPO_ROOT}/scripts/install.sh"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM

FAKE_BIN="${TMPDIR}/bin"
TARGET_BIN_DIR="${TMPDIR}/target"
mkdir -p "$FAKE_BIN" "$TARGET_BIN_DIR"

cat >"${FAKE_BIN}/curl" <<'EOF'
#!/usr/bin/env sh
set -eu
OUT=""
URL=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      OUT="$2"
      shift 2
      ;;
    -*)
      shift
      ;;
    *)
      URL="$1"
      shift
      ;;
  esac
done
[ -n "$OUT" ]
[ -n "$URL" ]
[ -n "${FAKE_CURL_LOG:-}" ]
printf '%s' "$URL" >"$FAKE_CURL_LOG"
printf '#!/usr/bin/env sh\necho fake-vmsmith\n' >"$OUT"
EOF
chmod 0755 "${FAKE_BIN}/curl"

cat >"${FAKE_BIN}/install" <<'EOF'
#!/usr/bin/env sh
set -eu
MODE=""
if [ "$1" = "-m" ]; then
  MODE="$2"
  shift 2
fi
SRC="$1"
DST="$2"
cp "$SRC" "$DST"
chmod "$MODE" "$DST"
EOF
chmod 0755 "${FAKE_BIN}/install"

CURL_LOG="${TMPDIR}/curl-url.log"

PATH="${FAKE_BIN}:$PATH" \
  BIN_DIR="$TARGET_BIN_DIR" \
  VERSION="v1.2.3" \
  UNAME_S="Linux" \
  UNAME_M="x86_64" \
  FAKE_CURL_LOG="$CURL_LOG" \
  "$SCRIPT"

EXPECTED_URL="https://github.com/chinmay28/VMSmith/releases/download/v1.2.3/vmsmith-linux-amd64"
ACTUAL_URL="$(cat "$CURL_LOG")"
[ "$ACTUAL_URL" = "$EXPECTED_URL" ]
[ -x "${TARGET_BIN_DIR}/vmsmith" ]
"${TARGET_BIN_DIR}/vmsmith" | grep -q 'fake-vmsmith'

printf '%s\n' 'install script smoke test passed'
