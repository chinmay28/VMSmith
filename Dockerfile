# syntax=docker/dockerfile:1.7

FROM node:22-bookworm AS web-build
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24-bookworm AS go-build
WORKDIR /src
RUN apt-get update && apt-get install -y --no-install-recommends \
    libvirt-dev \
    pkg-config \
    && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-build /src/web/dist ./internal/web/dist
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o /out/vmsmith ./cmd/vmsmith

FROM ubuntu:24.04 AS runtime
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    genisoimage \
    iptables \
    libvirt-clients \
    libvirt-daemon-system \
    qemu-kvm \
    qemu-utils \
    tini \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go-build /out/vmsmith /usr/local/bin/vmsmith
COPY scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY vmsmith.service /etc/vmsmith/vmsmith.service.example
COPY vmsmith.yaml.example /etc/vmsmith/config.yaml
RUN chmod 0755 /usr/local/bin/vmsmith /usr/local/bin/docker-entrypoint.sh \
    && mkdir -p /var/lib/vmsmith/vms /var/lib/vmsmith/images /var/lib/libvirt /var/log/libvirt \
    && sed -i 's|db_path:.*|db_path: "/var/lib/vmsmith/vmsmith.db"|' /etc/vmsmith/config.yaml

EXPOSE 8080
VOLUME ["/var/lib/vmsmith", "/var/lib/libvirt"]
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
CMD ["daemon", "start", "--config", "/etc/vmsmith/config.yaml"]
