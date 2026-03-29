# Networking Deep Dive

VMSmith always gives each VM a primary **NAT** interface on `vmsmith-net` and can optionally attach extra host-facing interfaces with `--network`.

This guide explains when to use **NAT**, **macvtap**, and **bridge** networking, what tradeoffs to expect, and how to troubleshoot common failures.

## Quick decision guide

- Use **NAT** when you want the simplest, safest default for most VMs.
- Use **macvtap** when the VM should get its **own address on your physical LAN** without first building a host bridge.
- Use **bridge** when you need **full host ↔ VM connectivity on that network** or already operate a Linux bridge such as `br0` / `br-data`.

## How VMSmith networking is structured

Every VM gets:

- **eth0 on the VMSmith NAT network**
  - default subnet: `192.168.100.0/24`
  - created and managed by libvirt as `vmsmith-net`
  - DHCP served by libvirt/dnsmasq

Optional extra interfaces are added with `--network` and appear in the guest as additional NICs (`eth1`, `eth2`, ... or distro-specific names).

Examples:

```bash
# NAT only
sudo ./bin/vmsmith vm create web01 --image ubuntu-22.04

# NAT + one extra LAN attachment using DHCP
sudo ./bin/vmsmith vm create db01 \
  --image ubuntu-22.04 \
  --network eth1

# NAT + bridge attachment on an existing Linux bridge
sudo ./bin/vmsmith vm create app01 \
  --image ubuntu-22.04 \
  --network eth1:mode=bridge,bridge=br-data
```

## NAT networking

### What it is

The built-in `vmsmith-net` libvirt network gives each VM a private address behind the host. Outbound traffic is NATed through the host, and inbound access is usually exposed with VMSmith port forwarding.

### When to use NAT

Use NAT when you want:

- a VM that works immediately with minimal host setup
- outbound internet/package-manager access from the guest
- isolation from the rest of the LAN by default
- predictable remote access through explicit `port add` rules

### Strengths

- zero extra network design required on most hosts
- easy to reason about and safer by default
- works well for development, CI, internal tools, and single-host labs
- avoids consuming another IP on the physical LAN

### Limitations

- guests are not directly reachable from the broader LAN unless you add port forwards
- the guest's NAT IP (`192.168.100.x` by default) is typically only directly useful from the host
- some protocols and service-discovery patterns work better with direct LAN attachment

### Typical workflow

```bash
sudo ./bin/vmsmith vm create web01 --image ubuntu-22.04
sudo ./bin/vmsmith port add web01 --host 2222 --guest 22
ssh -p 2222 root@<host-ip>
```

## macvtap networking

### What it is

macvtap attaches the VM directly to a physical host interface in a way that lets the VM appear as a separate machine on that upstream network.

In VMSmith, a `--network eth1` attachment defaults to **macvtap** mode unless you explicitly choose bridge mode.

### When to use macvtap

Use macvtap when you want:

- the VM to get its own DHCP lease or static IP on the real LAN/VLAN
- direct guest access from other machines on that network
- to avoid creating and managing a Linux bridge on the host

### Strengths

- simpler than bridge mode when you just want a VM on the LAN
- guest gets a first-class network identity
- works well for appliances, services, and test nodes that should look like separate machines

### Limitations

The biggest caveat: **the host usually cannot talk directly to the guest over that same macvtap network path**.

That means:

- other LAN devices may reach the VM
- the host itself may *not* be able to reach the VM on the macvtap interface
- you often still want the default NAT interface for host-side management or SSH fallback

This is why VMSmith keeps the NAT interface as the primary attachment even when you add extra interfaces.

### Example

```bash
# Extra interface on host NIC eth1 using DHCP from that LAN
sudo ./bin/vmsmith vm create db01 \
  --image ubuntu-22.04 \
  --network eth1

# Static address on that LAN
sudo ./bin/vmsmith vm create db01 \
  --image ubuntu-22.04 \
  --network eth1:ip=192.168.1.100/24,gw=192.168.1.1,name=data
```

## Bridge networking

### What it is

Bridge mode connects the VM NIC to an existing Linux bridge such as `br0` or `br-data`. The bridge is the Layer-2 segment; VMSmith attaches the guest to it.

### When to use bridge mode

Use bridge mode when you want:

- **host ↔ VM communication on that network**
- multiple VMs and the host on the same bridged segment
- to integrate with an existing Linux bridge / VLAN design
- behavior closer to traditional hypervisor bridge networking

### Strengths

- full peer-like connectivity between host and guest
- guest can be treated like another machine on the bridged network
- best fit for more advanced lab and production-style network topologies

### Limitations

- requires host bridge setup outside VMSmith
- bridge misconfiguration on the host can break connectivity for both host and guests
- slightly more operational complexity than NAT or macvtap

### Example

```bash
sudo ./bin/vmsmith vm create app01 \
  --image ubuntu-22.04 \
  --network eth1:mode=bridge,bridge=br-data
```

## Side-by-side comparison

| Mode | Best for | Reachable from LAN | Reachable from host | Extra host setup |
|---|---|---|---|---|
| NAT | simple defaults, local dev, safer isolation | only via port forwarding | yes | no |
| macvtap | direct LAN presence without building a bridge | yes | usually **no** on same path | no bridge required |
| bridge | full peer networking with host + LAN | yes | yes | yes, existing Linux bridge |

## Recommended patterns

### 1. Most users: NAT only

Use only the default NAT interface if you mainly need:

- package installs
- SSH from the host
- a small number of exposed services

### 2. Best practical hybrid: NAT + macvtap

This is often the sweet spot:

- **NAT** for dependable host-side management
- **macvtap** for guest presence on the physical network

That gives you a stable fallback path even if the LAN-facing interface has DHCP or routing issues.

### 3. Advanced deployments: NAT + bridge or bridge-focused layouts

Choose this when you already manage Linux bridges and need clean Layer-2 integration, host visibility, or more production-like network behavior.

## Guest IP assignment behavior

For extra interfaces, VMSmith writes cloud-init network config so the guest can bring up:

- DHCP interfaces
- static IP interfaces
- MAC-matched interface definitions that are more robust across distro naming differences

If an extra NIC appears in libvirt but not in the guest OS networking stack, cloud-init application or guest image support is the first thing to verify.

## Troubleshooting

### NAT network not found or not starting

Symptom:

```text
Error: creating VM: ensuring NAT network: Network not found
```

What to check:

- `libvirtd` is running
- the VMSmith daemon has permission to manage libvirt
- restarting the daemon recreates the NAT network if it was removed manually

Try:

```bash
sudo ./bin/vmsmith daemon status
sudo ./bin/vmsmith daemon start --port 8080
```

If dnsmasq is stuck from a previous unclean stop, see the README troubleshooting entry for stale `vmsmith-net` PID cleanup.

### Guest has NAT IP but service is unreachable from another machine

This is expected for NAT mode unless you expose a port.

Use:

```bash
sudo ./bin/vmsmith port add <vm-id> --host 8080 --guest 80
```

Then connect to the **host's** IP and forwarded port, not the guest's `192.168.100.x` address.

### Extra interface exists but has no IP in the guest

Check:

- the guest image supports cloud-init networking
- you are on a build with the DHCP extra-interface fix
- the guest saw and applied the generated network config

Useful checks inside the guest:

```bash
ip addr
ip route
journalctl -u cloud-init -b
```

### VM reachable from other LAN machines but not from the host

This strongly suggests **macvtap behavior**, not necessarily a bug.

If you need host ↔ VM connectivity on that same network, use one of these patterns:

- keep using the NAT interface for host-side SSH/management
- switch the extra interface to **bridge** mode on a real Linux bridge

### Bridge mode does not pass traffic

Check the host first:

- the configured bridge exists (`ip link show br-data`)
- the correct uplink is enslaved into the bridge
- VLAN / switch policy matches your expectations
- host firewall rules are not blocking bridged traffic

VMSmith can attach a NIC to a bridge, but it does not create or validate your full host bridge topology for you.

### Port forwarding works from host but not from other machines

Check:

- you connected to the **host IP**, not the guest NAT IP
- host firewall allows the forwarded TCP/UDP port
- the guest service is listening on the guest port
- the port forward exists in `vmsmith port list <vm-id>`

### Which mode should I pick?

Use this rule of thumb:

- want simple and safe → **NAT**
- want direct LAN presence and do not care about host↔guest on that path → **macvtap**
- want full host/LAN peer connectivity and already manage a bridge → **bridge**

## Related docs

- [README.md](../README.md)
- [Architecture](./ARCHITECTURE.md)
- [Roadmap](./ROADMAP.md)
