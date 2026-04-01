# Production Deployment Guide

This guide shows one practical way to run VMSmith on a real Linux host:

- VMSmith daemon bound to localhost
- systemd managing process lifecycle
- nginx or Caddy terminating TLS
- firewall exposing only SSH and HTTPS

It is intentionally focused on **reverse proxy + TLS** as the recommended production setup. This avoids coupling deployment to VMSmith's future built-in HTTPS support and works today.

---

## 1. Recommended architecture

```text
Internet / LAN client
        |
      HTTPS :443
        |
   nginx or Caddy
        |
   http://127.0.0.1:8080
        |
      VMSmith daemon
```

Why this layout is preferable today:

- TLS certificates are managed by a mature reverse proxy
- VMSmith itself does not need to listen on a public interface
- you can add auth, IP allowlists, rate limiting, and access logging at the proxy layer
- certificate rotation and HTTPS redirects are simpler

> **Important:** If you expose VMSmith beyond localhost, enable `daemon.auth` and keep the daemon behind network-level controls (VPN, Tailscale, WireGuard, SSH tunnel, private subnet, IP allowlist, or similar). Reverse proxy TLS protects transport, but it does **not** by itself provide access control.

---

## 2. Host prerequisites

Use a host that already satisfies the normal VMSmith runtime requirements:

- Linux x86_64
- KVM available (`/dev/kvm` exists)
- libvirt installed and running
- qemu-kvm installed
- VMSmith binary built or installed

If you are building from source:

```bash
make deps
make build
```

The resulting binary is:

```bash
./bin/vmsmith
```

For the rest of this guide, we assume you install it to `/usr/local/bin/vmsmith`:

```bash
sudo install -m 0755 ./bin/vmsmith /usr/local/bin/vmsmith
```

---

## 3. Create persistent directories

Create storage directories in a location libvirt can access:

```bash
sudo mkdir -p /var/lib/vmsmith/vms
sudo mkdir -p /var/lib/vmsmith/images
sudo mkdir -p /var/lib/vmsmith/state
sudo chown -R root:root /var/lib/vmsmith
sudo chmod 0755 /var/lib/vmsmith /var/lib/vmsmith/vms /var/lib/vmsmith/images /var/lib/vmsmith/state
```

If you want a dedicated config directory:

```bash
sudo mkdir -p /etc/vmsmith
```

---

## 4. Create a production config

Create `/etc/vmsmith/config.yaml`:

```yaml
daemon:
  listen: "127.0.0.1:8080"
  pid_file: "/run/vmsmith.pid"
  log_file: "/var/log/vmsmith/vmsmith.log"

libvirt:
  uri: "qemu:///system"

storage:
  images_dir: "/var/lib/vmsmith/images"
  base_dir: "/var/lib/vmsmith/vms"
  db_path: "/var/lib/vmsmith/state/vmsmith.db"

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

Create the log directory:

```bash
sudo mkdir -p /var/log/vmsmith
sudo chmod 0755 /var/log/vmsmith
```

Binding to `127.0.0.1` is the key production choice here: only the reverse proxy should be publicly reachable.

---

## 5. Install a systemd unit

Create `/etc/systemd/system/vmsmith.service`:

```ini
[Unit]
Description=VMSmith daemon
Wants=network-online.target libvirtd.service
After=network-online.target libvirtd.service

[Service]
Type=simple
ExecStart=/usr/local/bin/vmsmith daemon start --config /etc/vmsmith/config.yaml
Restart=on-failure
RestartSec=5
User=root
Group=root
RuntimeDirectory=vmsmith
RuntimeDirectoryMode=0755
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

Then enable and start it:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now vmsmith
sudo systemctl status vmsmith
```

Check logs:

```bash
sudo journalctl -u vmsmith -f
```

Smoke-test the local API before adding a reverse proxy:

```bash
curl http://127.0.0.1:8080/api/v1/vms
```

You should also be able to open the web UI locally via `http://127.0.0.1:8080` on the host.

---

## 6. Reverse proxy with nginx

### Install nginx

```bash
# Ubuntu / Debian
sudo apt-get update
sudo apt-get install -y nginx

# Rocky / RHEL
sudo dnf install -y nginx
```

### Configure site

Create `/etc/nginx/sites-available/vmsmith.conf` on Debian/Ubuntu, or `/etc/nginx/conf.d/vmsmith.conf` on Rocky/RHEL:

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name vmsmith.example.com;

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

Enable it on Debian/Ubuntu:

```bash
sudo ln -sf /etc/nginx/sites-available/vmsmith.conf /etc/nginx/sites-enabled/vmsmith.conf
sudo rm -f /etc/nginx/sites-enabled/default
```

Validate and reload:

```bash
sudo nginx -t
sudo systemctl enable --now nginx
sudo systemctl reload nginx
```

At this point, plain HTTP should work on port 80.

### Add TLS with Let's Encrypt (Certbot)

```bash
# Ubuntu / Debian
sudo apt-get install -y certbot python3-certbot-nginx

# Rocky / RHEL
sudo dnf install -y certbot python3-certbot-nginx
```

Request and install the certificate:

```bash
sudo certbot --nginx -d vmsmith.example.com
```

Certbot will typically:

- obtain a certificate
- update the nginx config to listen on 443
- add an HTTP → HTTPS redirect
- install automated renewal timers

Afterwards, verify:

```bash
curl -I https://vmsmith.example.com
```

### Hardened nginx example

If you prefer to manage the TLS block yourself, a typical 443 config looks like this:

```nginx
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name vmsmith.example.com;

    ssl_certificate /etc/letsencrypt/live/vmsmith.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vmsmith.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

server {
    listen 80;
    listen [::]:80;
    server_name vmsmith.example.com;
    return 301 https://$host$request_uri;
}
```

---

## 7. Reverse proxy with Caddy

Caddy is a good fit if you want automatic HTTPS with minimal config.

### Install Caddy

See the official Caddy install docs for your distro, then create `/etc/caddy/Caddyfile` entry:

```caddy
vmsmith.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

That is enough for the common case. Caddy will:

- provision a certificate automatically
- renew it automatically
- redirect HTTP to HTTPS by default

Reload Caddy:

```bash
sudo systemctl reload caddy
```

Verify:

```bash
curl -I https://vmsmith.example.com
```

If you want to restrict by source IP or add basic auth at the proxy layer, Caddy can do that as well.

---

## 8. Firewall recommendations

Keep the daemon port private. Open only what you actually need.

### Simple rule of thumb

- allow `22/tcp` for SSH
- allow `80/tcp` only if you need ACME HTTP validation or HTTP redirect
- allow `443/tcp` for HTTPS
- do **not** expose `8080/tcp` publicly when using a reverse proxy

### UFW example

```bash
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw deny 8080/tcp
sudo ufw enable
sudo ufw status
```

### firewalld example

```bash
sudo firewall-cmd --permanent --add-service=ssh
sudo firewall-cmd --permanent --add-service=http
sudo firewall-cmd --permanent --add-service=https
sudo firewall-cmd --reload
```

If you had previously opened 8080, remove it.

---

## 9. Access control recommendations

Because VMSmith does not yet provide built-in auth, **do not** expose it broadly on the public internet unless you add another control layer.

Safer deployment patterns:

- publish only inside a private LAN
- expose only through Tailscale or WireGuard
- use a cloud firewall / security group with explicit source IP allowlists
- require an SSH tunnel for admin access
- add reverse-proxy authentication if appropriate for your environment

For example, an SSH tunnel keeps the service private while still allowing remote use:

```bash
ssh -L 8443:127.0.0.1:443 user@vmsmith-host
```

Then open:

```text
https://127.0.0.1:8443
```

---

## 10. Upgrade workflow

A simple upgrade procedure:

```bash
sudo systemctl stop vmsmith
sudo install -m 0755 ./bin/vmsmith /usr/local/bin/vmsmith
sudo systemctl start vmsmith
sudo systemctl status vmsmith
```

Watch logs after restart:

```bash
sudo journalctl -u vmsmith -n 100 --no-pager
```

If you change config or systemd unit content:

```bash
sudo systemctl daemon-reload
sudo systemctl restart vmsmith
```

---

## 11. Backup recommendations

Back up at least:

- `/etc/vmsmith/config.yaml`
- `/var/lib/vmsmith/state/vmsmith.db`
- `/var/lib/vmsmith/images/` if image library matters
- `/var/lib/vmsmith/vms/` if you want VM disk recovery from host backups

A lightweight config/database backup example:

```bash
sudo tar czf /root/vmsmith-backup-$(date +%F).tar.gz \
  /etc/vmsmith/config.yaml \
  /var/lib/vmsmith/state/vmsmith.db
```

Make sure backup cadence matches how valuable your VM metadata and disk images are.

---

## 12. Troubleshooting

### Reverse proxy returns 502 Bad Gateway

Usually means nginx/Caddy cannot reach VMSmith.

Check:

```bash
curl http://127.0.0.1:8080/api/v1/vms
sudo systemctl status vmsmith
sudo journalctl -u vmsmith -n 100 --no-pager
```

If localhost access fails, fix the daemon first before debugging TLS.

### HTTPS certificate issuance fails

Common causes:

- DNS for `vmsmith.example.com` does not point at the host
- port 80 or 443 is blocked by firewall/security group
- another service is already bound to those ports

Check:

```bash
sudo ss -ltnp | grep -E ':80|:443|:8080'
```

### Accidentally exposed port 8080 publicly

Confirm the daemon is bound only to localhost:

```bash
sudo ss -ltnp | grep 8080
```

Expected output should show `127.0.0.1:8080`, not `0.0.0.0:8080`.

### Web UI loads but API calls fail through proxy

Make sure proxy headers and `proxy_pass` target are correct, and verify the API directly:

```bash
curl http://127.0.0.1:8080/api/v1/vms
curl -I https://vmsmith.example.com
```

---

## 13. Minimal production checklist

- [ ] VMSmith runs under systemd
- [ ] daemon listens on `127.0.0.1:8080`, not a public interface
- [ ] reverse proxy handles HTTPS on `443`
- [ ] certificates renew automatically
- [ ] firewall exposes only required ports
- [ ] `8080/tcp` is not publicly reachable
- [ ] access is limited via private network, VPN, allowlist, or equivalent control
- [ ] backups cover config, DB, and important images/VM disks

---

## 14. Related docs

- [README.md](../README.md)
- [ARCHITECTURE.md](./ARCHITECTURE.md)
- [ROADMAP.md](./ROADMAP.md)
