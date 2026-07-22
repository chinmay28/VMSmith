# Multi-Host Management (roadmap 5.5)

VMSmith can manage QEMU/KVM virtual machines across **multiple physical
hosts** from a single daemon. This document records the architecture
decision (5.5.1) and the operator contract for the shipped v1
(5.5.2–5.5.4).

## Architecture decision (5.5.1)

Two architectures were considered:

| | **A: Central coordinator + remote libvirt URIs** *(chosen)* | B: Per-host agents |
|---|---|---|
| Topology | One `vmsmith daemon` holds a libvirt connection per host (`qemu+ssh://` / `qemu+tls://`) | A `vmsmith-agent` on every host, coordinator speaks a custom RPC |
| New moving parts | None — libvirt's remote transport is battle-tested | Agent binary, its lifecycle, auth, upgrade skew |
| Metadata | Single bbolt store on the coordinator | Distributed or replicated store needed |
| Failure mode | Host unreachable ⇒ its VMs show stored state, ops on them fail cleanly | Agent down ⇒ same, plus agent-health machinery |
| Scale ceiling | Tens of hosts (each URI is one long-lived connection) | Hundreds+ |
| Effort | Fits the existing `vm.Manager` seam | XL, new subsystem |

**Decision: A.** VMSmith targets single-operator/small-team deployments
(tens of hosts at most), where libvirt's own remote transport removes the
need for any new agent, auth scheme, or wire protocol. The coordinator
pattern also drops cleanly into the existing `vm.Manager` seam: a
`MultiHostManager` routes each call to a per-host `LibvirtManager`.
Architecture B remains the escalation path if the host count ever
outgrows connection-per-host.

### How routing works

- Every host gets its own `LibvirtManager` (its own libvirt connection);
  the implicit **`local`** host uses `libvirt.uri`.
- **Placement is decided at create time**: `VMSpec.Host` (`--host` CLI
  flag) selects the target host, defaulting to `local`. The chosen host is
  stamped into the stored spec, and every subsequent lifecycle call
  (start/stop/snapshot/console/…) routes to that host's manager. Unknown
  host names are rejected with 400 `invalid_host`.
- `List` fans out across hosts; each host's connection enriches the live
  state of the VMs placed on it.
- The daemon fails fast at startup if any configured host is unreachable —
  a misconfigured fleet should surface immediately, not at first placement.

### v1 constraints (explicitly out of scope)

- **Shared storage is assumed.** `storage.images_dir` and
  `storage.base_dir` must be mounted at the same paths on every host
  (NFS or equivalent). The coordinator runs `qemu-img` locally when
  creating disks; with shared storage the resulting files are visible to
  the remote QEMU. This is the standard libvirt multi-host pattern.
- **No live migration.** Placement is fixed post-create. Move a VM by
  exporting (`vm export`, OVA) and importing on the other host, or by
  snapshot → image → create.
- **Networking is per-host.** The `vmsmith-net` NAT network and DHCP
  reservations are created on each host via its own connection; NAT IPs
  are only meaningful on the VM's host. Port forwards (iptables) apply on
  the coordinator host only — forwarding to VMs on remote hosts requires
  host-level routing outside VMSmith's scope in v1.
- **Metrics/console** use the VM's host connection transparently; the
  console proxy dials the remote VNC through the libvirt URI's transport
  only when the graphics listen address is reachable from the
  coordinator. Bind remote-host VNC to an address the coordinator can
  reach, or tunnel it.

## Configuration (5.5.2)

```yaml
libvirt:
  uri: "qemu:///system"          # the implicit "local" host

hosts:                            # additional hosts (empty = single-host mode)
  - name: hv2
    uri: "qemu+ssh://root@hv2.example.com/system"
    description: "rack 2, GPU box"
  - name: hv3
    uri: "qemu+tls://hv3.example.com/system"
```

Rules: names must be unique and non-empty; `local` is reserved; every
entry needs a URI. Validation runs at daemon startup.

For `qemu+ssh://`, install the daemon user's SSH key on the remote host;
for `qemu+tls://`, set up libvirt's TLS certificates. Test with
`virsh -c <uri> list` before adding a host.

## Placing VMs (5.5.3)

```bash
vmsmith vm create web-1 --image rocky9 --host hv2
```

```jsonc
POST /api/v1/vms
{ "name": "web-1", "image": "rocky9", "host": "hv2" }
```

The VM's host is visible in `spec.host` on every VM response (empty =
`local`). Unknown names → 400 `invalid_host`.

## Host overview (5.5.4)

```bash
vmsmith host list           # NAME URI DEFAULT REACHABLE VMS CPUS RAM_MB DISK_GB GPUS
vmsmith host list --json
```

`GET /api/v1/hosts` returns one row per host — the implicit `local` host
first — with per-host **allocation** (VM count, vCPUs, RAM, disk, GPUs of
the VMs placed there) plus a live `reachable` probe when the daemon runs
in multi-host mode. The GUI Dashboard renders the same data as a Hosts
table whenever more than one host is configured.

Live utilisation (CPU %, IO rates) remains per-VM via `/vms/{id}/stats`;
the hosts view is about placement capacity.
