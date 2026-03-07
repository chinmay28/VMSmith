#!/usr/bin/env bash
set -euo pipefail

echo "=== vmSmith: Installing dependencies on Rocky Linux ==="

dnf install -y \
    qemu-kvm \
    qemu-img \
    libvirt \
    libvirt-client \
    libvirt-devel \
    virt-install \
    genisoimage \
    iptables-nft \
    pkg-config \
    curl \
    git

# --- Go 1.22+ ---
if ! command -v go &>/dev/null || ! go version 2>/dev/null | grep -qE 'go1\.(2[2-9]|[3-9][0-9])'; then
    echo "Installing Go 1.22..."
    GOVERSION="1.22.5"
    curl -fsSL "https://go.dev/dl/go${GOVERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
    export PATH=$PATH:/usr/local/go/bin
    echo "Go $(go version) installed."
else
    echo "Go $(go version) already installed, skipping."
fi

# --- Node.js 18+ ---
if ! command -v node &>/dev/null || ! node --version 2>/dev/null | grep -qE 'v(1[89]|[2-9][0-9])'; then
    echo "Installing Node.js 18..."
    curl -fsSL https://rpm.nodesource.com/setup_18.x | bash -
    dnf install -y nodejs
    echo "Node.js $(node --version) installed."
else
    echo "Node.js $(node --version) already installed, skipping."
fi

# Enable and start libvirtd
systemctl enable --now libvirtd

# Add current user to libvirt group
if [ -n "${SUDO_USER:-}" ]; then
    usermod -aG libvirt "$SUDO_USER"
    usermod -aG kvm "$SUDO_USER"
    echo "NOTE: Log out and back in for group changes to take effect."
fi

echo ""
echo "=== Dependencies installed successfully ==="
echo ""
echo "Next steps:"
echo "  source /etc/profile.d/go.sh   # reload PATH (or open a new shell)"
echo "  make deps                      # download Go modules"
echo "  make build                     # compile vmsmith"
