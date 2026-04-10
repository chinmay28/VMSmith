#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
BUILD_DIR=${BUILD_DIR:-"$ROOT_DIR/bin"}
PACKAGE_NAME=${PACKAGE_NAME:-vmsmith}
ARCH=${ARCH:-amd64}
MAINTAINER=${MAINTAINER:-"VMSmith Maintainers <maintainers@vmsmith.dev>"}
DESCRIPTION=${DESCRIPTION:-"VMSmith CLI, API daemon, and embedded web UI for QEMU/KVM VM management"}
VERSION_INPUT=${VERSION:-$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)}
DEB_VERSION=$(printf '%s' "$VERSION_INPUT" | sed 's/[^A-Za-z0-9.+:~-]/-/g; s/-/+/g')
case "$DEB_VERSION" in
  [0-9]*) ;;
  *) DEB_VERSION="0~$DEB_VERSION" ;;
esac
DIST_BINARY=${DIST_BINARY:-"$BUILD_DIR/vmsmith-linux-amd64"}
FALLBACK_BINARY=${FALLBACK_BINARY:-"$BUILD_DIR/vmsmith"}
OUTPUT_DIR=${OUTPUT_DIR:-"$BUILD_DIR/packages"}
STAGE_DIR=$(mktemp -d)
trap 'rm -rf "$STAGE_DIR"' EXIT

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "dpkg-deb is required to build Debian packages" >&2
  exit 1
fi

BINARY_PATH=
if [ -f "$DIST_BINARY" ]; then
  BINARY_PATH="$DIST_BINARY"
elif [ -f "$FALLBACK_BINARY" ]; then
  BINARY_PATH="$FALLBACK_BINARY"
else
  echo "Missing build artifact. Expected $DIST_BINARY (preferred) or $FALLBACK_BINARY." >&2
  echo "Run 'make dist' or 'make build-go' first." >&2
  exit 1
fi

mkdir -p \
  "$STAGE_DIR/DEBIAN" \
  "$STAGE_DIR/usr/local/bin" \
  "$STAGE_DIR/etc/vmsmith" \
  "$STAGE_DIR/lib/systemd/system" \
  "$STAGE_DIR/usr/share/doc/$PACKAGE_NAME"

install -m 0755 "$BINARY_PATH" "$STAGE_DIR/usr/local/bin/vmsmith"
install -m 0644 "$ROOT_DIR/vmsmith.service" "$STAGE_DIR/lib/systemd/system/vmsmith.service"
install -m 0644 "$ROOT_DIR/vmsmith.yaml.example" "$STAGE_DIR/etc/vmsmith/config.yaml"
install -m 0644 "$ROOT_DIR/README.md" "$STAGE_DIR/usr/share/doc/$PACKAGE_NAME/README.md"
install -m 0644 "$ROOT_DIR/LICENSE" "$STAGE_DIR/usr/share/doc/$PACKAGE_NAME/LICENSE"

cat > "$STAGE_DIR/DEBIAN/control" <<EOF
Package: $PACKAGE_NAME
Version: $DEB_VERSION
Section: admin
Priority: optional
Architecture: $ARCH
Maintainer: $MAINTAINER
Depends: systemd, qemu-kvm, qemu-utils, libvirt-daemon-system, libvirt-clients, genisoimage, iptables, bridge-utils
Description: $DESCRIPTION
 VMSmith provides a single binary that bundles a CLI, HTTP API daemon,
 and embedded web UI for provisioning and managing QEMU/KVM virtual machines.
EOF

cat > "$STAGE_DIR/DEBIAN/conffiles" <<EOF
/etc/vmsmith/config.yaml
EOF

cat > "$STAGE_DIR/DEBIAN/postinst" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

mkdir -p /var/lib/vmsmith/vms /var/lib/vmsmith/images /var/log/vmsmith
chmod 0755 /var/lib/vmsmith /var/lib/vmsmith/vms /var/lib/vmsmith/images /var/log/vmsmith

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi
EOF
chmod 0755 "$STAGE_DIR/DEBIAN/postinst"

mkdir -p "$OUTPUT_DIR"
PACKAGE_PATH="$OUTPUT_DIR/${PACKAGE_NAME}_${DEB_VERSION}_${ARCH}.deb"
dpkg-deb --build "$STAGE_DIR" "$PACKAGE_PATH" >/dev/null

echo "Built Debian package: $PACKAGE_PATH"
