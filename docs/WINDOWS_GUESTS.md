# Windows Guest Support

VMSmith runs Windows guests — both workstation (Windows 10/11) and server
(Windows Server 2019/2022/2025) — alongside Linux VMs. Windows guests reuse the
same overlay-disk, NAT network, DHCP reservation, snapshot, and lifecycle
machinery as Linux. The only differences are the libvirt domain tuning and the
first-boot provisioning datasource, both selected by a single `os_type` field.

> Scope: "2020 version and up" — Windows 10, Windows 11, Windows Server 2019,
> Windows Server 2022, and Windows Server 2025.

---

## TL;DR

```bash
# Prepare a Windows base image (see "Preparing a base image" below) named e.g.
# /var/lib/vmsmith/images/win2022.qcow2

vmsmith vm create win-01 \
  --image win2022.qcow2 \
  --os windows \
  --os-variant windows-server-2022 \
  --cpus 4 --ram 4096 --disk 64 \
  --admin-password 'S3cret!Pass' \
  --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"
```

The VM gets a reserved NAT IP via DHCP (same as Linux), Remote Desktop is
enabled, and — if you passed `--ssh-key` — the Windows OpenSSH server is
installed and your key authorised for administrators.

---

## How it differs from Linux guests

`os_type: windows` (CLI `--os windows`, JSON `"os_type": "windows"`) flips the
guest into the Windows profile:

| Aspect | Linux (default) | Windows |
|---|---|---|
| System disk bus | virtio (`vda`) | SATA (`sda`) — boots without extra storage drivers |
| NIC model | virtio | `e1000e` — native Windows driver, works out of the box |
| RTC clock | `utc` | `localtime` — Windows expects the RTC in local time |
| CPU enlightenments | none | Hyper-V (`relaxed`, `vapic`, `spinlocks`, `vpindex`, `synic`, `stimer`, `frequencies`) + `hypervclock` timer |
| Input | — | USB tablet (usable VNC mouse tracking) |
| Video | libvirt default | QXL |
| Provisioning | cloud-init NoCloud | cloudbase-init NoCloud |
| Extra cdrom | — | virtio-win driver ISO (when configured) |
| Resource floor | RAM ≥ 128 MB, disk ≥ 1 GB | RAM ≥ 2048 MB, disk ≥ 32 GB |

Everything else — the `cidata`-labelled provisioning ISO, the MAC-based DHCP
reservation that pins a stable IP, snapshots, clone, start/stop/reboot/suspend,
quotas, the IP monitor — is identical to the Linux path.

### Performance: switching to virtio later

SATA + e1000e are chosen so a *fresh* Windows image boots and reaches the
network with zero driver work. For better throughput, install the paravirtual
drivers from the attached virtio-win ISO inside the guest and then have an
administrator switch the disk/NIC models to virtio. Configure the ISO via
`storage.virtio_win_iso` (see below).

---

## Configuration: the virtio-win driver ISO

```yaml
storage:
  # Optional. Attached as an extra cdrom to Windows guests so the in-guest
  # installer can load paravirtual storage/network/balloon drivers.
  virtio_win_iso: "/var/lib/vmsmith/images/virtio-win.iso"
```

If left empty, VMSmith auto-probes the conventional RHEL/Fedora location
`/usr/share/virtio-win/virtio-win.iso` (provided by the `virtio-win` package).
If no ISO is found the guest simply boots without it — SATA + e1000e still work.

Download the signed ISO from the upstream Fedora project:
<https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso>

---

## Preparing a base image

Just as Linux base images must be cloud-init-ready GenericCloud images, Windows
base images must be prepared so VMSmith's first-boot datasource takes effect:

1. **Install Windows** into a qcow2 disk (use the virtio-win ISO during install
   if you want a virtio system disk; otherwise SATA install is fine).
2. **Install [cloudbase-init](https://cloudbase.it/cloudbase-init/)** and
   configure its NoCloud / ConfigDrive metadata service. cloudbase-init is the
   Windows analogue of cloud-init; it reads the same `cidata`-labelled ISO that
   VMSmith generates.
3. *(Optional)* Install the virtio-win drivers so you can switch to virtio.
4. **Sysprep + generalize** so each clone gets a fresh SID/hostname:
   `C:\Windows\System32\Sysprep\sysprep.exe /generalize /oobe /shutdown
   /unattend:Unattend.xml` (cloudbase-init ships an `Unattend.xml` that re-arms
   itself on next boot).
5. Save the resulting qcow2 as `<name>.qcow2` in `storage.images_dir` (the
   `.qcow2` extension is required — see the AppArmor note in the main docs).

Cloudbase publishes ready-made evaluation qcow2 images that already include
cloudbase-init and virtio drivers, which are a convenient starting point.

---

## What VMSmith injects at first boot

For a Windows guest VMSmith writes a NoCloud datasource ISO (volume label
`cidata`) containing:

- **`meta-data`** — `instance-id`, `local-hostname` (the VM name), and
  `admin_pass` (when `--admin-password` is set). cloudbase-init renames the
  computer and sets the Administrator password from these.
- **`user-data`** — a `#ps1_sysnative` PowerShell script (cloudbase-init's
  `UserDataPlugin` executes it) that:
  - sets the local Administrator password (idempotent),
  - enables Remote Desktop and opens the firewall group,
  - when `--ssh-key` is supplied, installs/enables the Windows OpenSSH server
    and writes the key to `%ProgramData%\ssh\administrators_authorized_keys`.

Provide your own datasource instead with `--cloud-init <file>` (CLI) /
`cloud_init_file` (API) — the file becomes the `user-data` verbatim, so you can
ship any cloudbase-init-compatible payload (cloud-config, PowerShell, or a
multipart bundle).

> **Security note.** `admin_password` is *write-only*: VMSmith bakes it into the
> provisioning ISO and then redacts it from the stored bbolt record and every
> API response, so it never lingers in the metadata store.

---

## Accessing a Windows guest

- **RDP** — always enabled by the injected script. Connect to the VM's NAT IP
  (shown by `vmsmith vm create` / `vmsmith vm list`). Use a port-forward
  (`vmsmith port add <id> --host-port 13389 --guest-port 3389`) to reach it
  from outside the host.
- **SSH** — available when you passed `--ssh-key`; log in as the local
  administrator over the Windows OpenSSH server.
- **VNC console** — the QXL display + USB tablet make the graphical console
  usable via the standard console-ticket flow.

---

## API / CLI reference

```jsonc
// POST /api/v1/vms
{
  "name": "win-01",
  "image": "win2022.qcow2",
  "os_type": "windows",                 // "linux" (default) | "windows"
  "os_variant": "windows-server-2022",  // optional, advisory
  "admin_password": "S3cret!Pass",      // write-only, redacted after create
  "cpus": 4,
  "ram_mb": 4096,                        // ≥ 2048 for windows
  "disk_gb": 64,                         // ≥ 32 for windows
  "ssh_pub_key": "ssh-ed25519 AAAA..."
}
```

```
vmsmith vm create <name> --image <img> --os windows \
  [--os-variant windows-10|windows-11|windows-server-2019|windows-server-2022|windows-server-2025] \
  [--admin-password <pw>] [--ssh-key <pubkey>] [--cpus N --ram MB --disk GB]
```

Validation errors: `invalid_os_type` (not `linux`/`windows`),
`invalid_os_variant` (unknown variant), and `invalid_spec` for the Windows
RAM/disk floors — all HTTP 400.
