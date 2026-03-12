# =============================================================================
# Stage 1: frontend — build the React SPA
# =============================================================================
FROM node:20-slim AS frontend

WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build
# Output: /app/web/dist  (picked up by go:embed in internal/web/embed.go)


# =============================================================================
# Stage 2: builder — compile the Go binary with CGO (libvirt bindings)
# =============================================================================
FROM golang:1.22-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libc6-dev \
    libvirt-dev \
    pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Cache Go module downloads before copying source
COPY go.mod go.sum ./
RUN go mod download

# Embed the frontend build before compiling Go (internal/web/embed.go needs it)
COPY --from=frontend /app/internal/web/dist ./internal/web/dist

# Copy the rest of the source
COPY . .

RUN CGO_ENABLED=1 go build \
    -ldflags "-s -w" \
    -o /bin/vmsmith \
    ./cmd/vmsmith


# =============================================================================
# Stage 3: test — run the full Go test suite (no real libvirtd needed)
# =============================================================================
FROM builder AS test

# qemu-img is shelled out by storage/image.go; tests that exercise that code
# path need the binary present to avoid "exec: not found" failures.
RUN apt-get update && apt-get install -y --no-install-recommends \
    qemu-utils \
    && rm -rf /var/lib/apt/lists/*

CMD ["go", "test", "-v", "-race", "./..."]


# =============================================================================
# Stage 4: runtime — lean image with QEMU/KVM + libvirt
# =============================================================================
FROM debian:bookworm-slim AS runtime

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    # Hypervisor
    qemu-kvm \
    qemu-utils \
    # VM management
    libvirt-daemon-system \
    libvirt-clients \
    virtlogd \
    # Cloud-init ISO generation (genisoimage is called by lifecycle.go)
    genisoimage \
    # Networking
    iptables \
    bridge-utils \
    iproute2 \
    dnsmasq \
    # TLS / certs
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Required directories matching config.go DefaultConfig() paths
RUN mkdir -p \
    /var/lib/vmsmith/images \
    /var/lib/vmsmith/vms \
    /root/.vmsmith \
    /etc/vmsmith \
    /var/run/libvirt \
    /var/log/libvirt/qemu

COPY --from=builder /bin/vmsmith /usr/local/bin/vmsmith
COPY scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 8080

# Persist VM disk images, base images, and the bbolt metadata DB
VOLUME ["/var/lib/vmsmith", "/root/.vmsmith"]

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
# Default: start the daemon (entrypoint prepends service startup before exec)
CMD ["vmsmith", "daemon", "start"]
