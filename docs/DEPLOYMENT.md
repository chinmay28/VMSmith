# Production Deployment Guide

This guide covers a practical production-style VMSmith deployment on a single Linux host with:

- systemd to keep the daemon running
- a dedicated config file under `/etc/vmsmith/`
- a reverse proxy for stable external access
- basic firewall guidance
- operational checks for logs, restarts, and upgrades

It assumes Ubuntu 22.04+ or Rocky Linux 8+ with libvirt and QEMU/KVM available.

---

## 1. Install host dependencies

Use the provided bootstrap script for your distro:

```bash
# Ubuntu / Debian
sudo bash scripts/install-deps-ubuntu.sh

# Rocky / RHEL
sudo bash scripts/install-deps-rocky.sh
```

Verify the host is ready:

```bash
ls -l /dev/kvm
sudo systemctl status libvirtd --no-pager
virsh -c qemu:///system list --all
```

If `/dev/kvm` is missing or `libvirtd` is not active, fix that before continuing.

---

## 2. Build or install the binary

From source:

```bash
make deps
make build
```

The resulting binary is written to:

```bash
./bin/vmsmith
```

Install it somewhere stable for systemd, for example:

```bash
sudo install -D -m 0755 ./bin/vmsmith /usr/local/bin/vmsmith
```

---

## 3. Create runtime directories

Create the default storage paths and configuration directory:

```bash
sudo mkdir -p /etc/vmsmith
sudo mkdir -p /var/lib/vmsmith/images
sudo mkdir -p /var/lib/vmsmith/vms
```

Set permissions so libvirt/QEMU can read the disk/image paths:

```bash
sudo chown -R "$(whoami):$(whoami)" /var/lib/vmsmith
sudo chmod -R 755 /var/lib/vmsmith
```

If you run the daemon as a dedicated service user instead of your own account, adjust ownership accordingly.

---

## 4. Create the production config file

Start from the example file:

```bash
sudo cp vmsmith.yaml.example /etc/vmsmith/config.yaml
```

Recommended baseline:

```yaml
daemon:
  listen: "127.0.0.1:8080"
  pid_file: "/run/vmsmith/vmsmith.pid"
  log_file: "/var/log/vmsmith/vmsmith.log"

libvirt:
  uri: "qemu:///system"

storage:
  images_dir: "/var/lib/vmsmith/images"
  base_dir: "/var/lib/vmsmith/vms"
  db_path: "/var/lib/vmsmith/vmsmith.db"

network:
  name: "vmsmith-net"
  subnet: "192.168.100.0/24"
  dhcp_start: "192.168.100.10"
  dhcp_end: "192.168.100.254"

defaults:
  cpus: 2
  ram_mb: 2048
  disk_gb: 20
  ssh_user: ubuntu
```

### Why bind to `127.0.0.1`?

For production, the safest default is to keep VMSmith private on localhost and publish it through a reverse proxy. That gives you:

- a single stable public port (`80`/`443`)
- optional TLS termination
- a clean place to add auth later
- less accidental exposure of the raw API

If you intentionally want direct LAN access without a proxy, change `daemon.listen` to a concrete interface such as `192.168.1.10:8080` and restrict it with your firewall.

---

## 5. Smoke-test the daemon manually first

Before introducing systemd, verify the config works:

```bash
sudo /usr/local/bin/vmsmith daemon start --config /etc/vmsmith/config.yaml
```

In another shell:

```bash
curl http://127.0.0.1:8080/api/v1/vms
```

You should get JSON back, even if it is just an empty list.

Stop the manual daemon once this check passes.

---

## 6. Install a systemd service

Create `/etc/systemd/system/vmsmith.service`:

```ini
[Unit]
Description=VMSmith daemon
After=network-online.target libvirtd.service
Wants=network-online.target libvirtd.service

[Service]
Type=simple
User=root
Group=root
RuntimeDirectory=vmsmith
ExecStart=/usr/local/bin/vmsmith daemon start --config /etc/vmsmith/config.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

> Running as `root` is the simplest deployment path when using `qemu:///system`, libvirt networking, and iptables-based port forwarding. If you later support a dedicated service user cleanly, you can tighten this.

Enable and start it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now vmsmith
```

Check health:

```bash
sudo systemctl status vmsmith --no-pager
journalctl -u vmsmith -n 100 --no-pager
```

---

## 7. Put a reverse proxy in front

VMSmith serves both the web UI and API on the same port, so a basic reverse proxy config is enough.

### Option A — nginx

Example `/etc/nginx/sites-available/vmsmith.conf`:

```nginx
server {
    listen 80;
    server_name vmsmith.example.com;

    client_max_body_size 10G;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Enable it:

```bash
sudo ln -s /etc/nginx/sites-available/vmsmith.conf /etc/nginx/sites-enabled/vmsmith.conf
sudo nginx -t
sudo systemctl reload nginx
```

For HTTPS, add certificates with your usual Certbot or manually-managed TLS flow.

### Option B — Caddy

Example `Caddyfile`:

```caddy
vmsmith.example.com {
    reverse_proxy 127.0.0.1:8080
    request_body {
        max_size 10GB
    }
}
```

Caddy is a nice fit if you want automatic HTTPS with minimal setup.

### Upload-size note

Image uploads can be large. Make sure your reverse proxy body-size limits are higher than your expected qcow2 upload sizes.

---

## 8. Firewall guidance

A simple production stance is:

- expose only `22`, `80`, and `443` externally
- keep the raw VMSmith daemon port (`8080`) private
- allow libvirt/NAT traffic locally on the host

Example with `ufw`:

```bash
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw deny 8080/tcp
sudo ufw enable
```

If you intentionally expose VMSmith directly on a LAN port, allow only that subnet instead of the whole internet.

---

## 9. Logs and operational checks

You can inspect the daemon in three places:

### systemd / journal

```bash
journalctl -u vmsmith -f
```

### structured log file

If `daemon.log_file` is configured:

```bash
tail -f /var/log/vmsmith/vmsmith.log
```

### HTTP logs endpoint

```bash
curl "http://127.0.0.1:8080/api/v1/logs?level=info&limit=50"
```

Useful recurring checks:

```bash
sudo systemctl status vmsmith --no-pager
virsh -c qemu:///system net-list --all
virsh -c qemu:///system list --all
curl -f http://127.0.0.1:8080/api/v1/vms >/dev/null && echo ok
```

---

## 10. Upgrades

A straightforward upgrade flow:

```bash
git pull
make build
sudo install -D -m 0755 ./bin/vmsmith /usr/local/bin/vmsmith
sudo systemctl restart vmsmith
```

Then confirm the daemon came back cleanly:

```bash
sudo systemctl status vmsmith --no-pager
journalctl -u vmsmith -n 50 --no-pager
```

If you changed config structure or storage behavior in a new release, review the release notes before restarting production.

---

## 11. Backups

At minimum, back up:

- `/etc/vmsmith/config.yaml`
- `/var/lib/vmsmith/vmsmith.db`
- `/var/lib/vmsmith/images/`
- `/var/lib/vmsmith/vms/` if you want full VM-disk recovery

A config-only backup is not enough if you care about restoring existing VMs and imported images.

---

## 12. Recommended production checklist

- [ ] `libvirtd` is active and healthy
- [ ] `vmsmith` runs under systemd
- [ ] daemon binds to localhost or a restricted interface
- [ ] reverse proxy is configured for external access
- [ ] firewall blocks unintended direct API exposure
- [ ] upload body limits are large enough for qcow2 files
- [ ] storage directories live on a filesystem with enough free space
- [ ] config and VM/image data are backed up
- [ ] logs are easy to inspect during incidents

---

## 13. Current limitations to keep in mind

This guide improves deployment hygiene, but it does **not** replace missing product features that are still on the roadmap:

- API authentication is not yet built in
- native TLS in the daemon is not yet built in
- packaged systemd installation is not yet automated

Until those land, the safest approach is:

1. run VMSmith behind a reverse proxy
2. avoid exposing the raw daemon directly to the public internet
3. keep host firewall rules tight
