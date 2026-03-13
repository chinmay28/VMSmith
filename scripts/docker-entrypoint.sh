#!/usr/bin/env bash
# docker-entrypoint.sh — start libvirt services, configure the network stack,
# then exec the requested command (default: bash interactive shell).
set -euo pipefail

# ---------------------------------------------------------------------------
# 1. Start virtlogd if available — libvirt's log daemon.  On Debian bookworm
#    virtlogd ships inside libvirt-daemon so the binary is always present;
#    the guard is kept for forward compatibility.
# ---------------------------------------------------------------------------
if command -v virtlogd &>/dev/null; then
    virtlogd --daemon 2>/dev/null || true
fi

# ---------------------------------------------------------------------------
# 2. Start libvirtd
# ---------------------------------------------------------------------------
libvirtd --daemon

# ---------------------------------------------------------------------------
# 3. Wait for the libvirt Unix socket to appear (max 30 s).
#    daemon.go calls libvirt.NewConnect("qemu:///system") which uses this
#    socket; starting vmsmith before it is ready causes a connection error.
# ---------------------------------------------------------------------------
SOCKET=/var/run/libvirt/libvirt.sock
for i in $(seq 1 30); do
    if [ -S "$SOCKET" ]; then
        break
    fi
    echo "vmsmith: waiting for libvirtd socket... ($i/30)"
    sleep 1
done

if [ ! -S "$SOCKET" ]; then
    echo "ERROR: libvirtd socket never appeared at $SOCKET" >&2
    echo "       Check 'journalctl -u libvirtd' or container logs for details." >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# 4. Enable IPv4 forwarding — required for libvirt NAT networking.
#    network/nat.go creates a NAT network that routes VM traffic through
#    the host interface; without forwarding the VMs have no external route.
# ---------------------------------------------------------------------------
echo 1 > /proc/sys/net/ipv4/ip_forward

# ---------------------------------------------------------------------------
# 5. Ensure storage directories exist (handles first-run with empty volumes).
#    config.go EnsureDirs() does the same at startup, but doing it here
#    guarantees correct permissions before vmsmith launches.
# ---------------------------------------------------------------------------
mkdir -p \
    /var/lib/vmsmith/images \
    /var/lib/vmsmith/vms \
    /root/.vmsmith

# ---------------------------------------------------------------------------
# 6. Hand off to the requested command.
# ---------------------------------------------------------------------------
exec "$@"
