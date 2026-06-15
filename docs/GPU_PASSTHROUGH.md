# GPU Passthrough (VFIO)

VM Smith can pass a host NVIDIA (or AMD/Intel) GPU through to a VM so the guest
gets direct, near-native access to the hardware — useful for CUDA workloads,
ML training, transcoding, or gaming VMs. This uses the standard QEMU/KVM
**VFIO** mechanism: the host detaches the GPU from its kernel driver and binds
it to `vfio-pci`, then libvirt assigns it to the guest as a `<hostdev>`.

This guide covers the one-time host setup and the per-VM workflow. It was
validated against an NVIDIA RTX 4080 on a Linux host.

---

## 1. Host prerequisites (one-time)

GPU passthrough requires hardware + firmware + kernel support. Do this once per
host.

### 1.1 Enable IOMMU in firmware and kernel

1. In your motherboard BIOS/UEFI, enable **VT-d** (Intel) or **AMD-Vi / IOMMU**
   (AMD), and enable **Above 4G Decoding** / **Resizable BAR** (recommended for
   modern large-VRAM cards like the RTX 4080).
2. Add the IOMMU flag to the kernel command line:
   - Intel: `intel_iommu=on iommu=pt`
   - AMD: `amd_iommu=on iommu=pt`

   Edit `/etc/default/grub`, append to `GRUB_CMDLINE_LINUX_DEFAULT`, then:

   ```bash
   sudo update-grub        # Debian/Ubuntu
   sudo grub2-mkconfig -o /boot/grub2/grub.cfg   # Rocky/RHEL
   sudo reboot
   ```

3. After reboot, confirm IOMMU is active:

   ```bash
   dmesg | grep -i -e DMAR -e IOMMU
   ls /sys/kernel/iommu_groups/    # should list numbered groups
   ```

### 1.2 Inspect the GPU and its IOMMU group

Ask the daemon what it sees:

```bash
vmsmith host gpus
```

```
ADDRESS       VENDOR  DEVICE  DRIVER  IOMMU  GROUP
0000:01:00.0  NVIDIA  0x2704  nvidia  15     0000:01:00.0 0000:01:00.1
```

The `GROUP` column lists every device in the GPU's IOMMU group — here the GPU
(`01:00.0`) and its HDMI-audio function (`01:00.1`). **VFIO assigns a whole
IOMMU group at once**, so vmsmith automatically attaches every device in the
group when you request the GPU; you only need to pass the GPU's own address.

If the group also contains unrelated devices (a common problem on consumer
boards where the GPU shares a group with a USB/SATA controller), you cannot
cleanly pass it through without an ACS-override patched kernel. A clean group
(GPU + its own audio) is what you want.

### 1.3 Free the GPU from the host

`managed='yes'` (what vmsmith emits) lets libvirt rebind the device from
`nvidia` to `vfio-pci` automatically at VM start — **but only if the GPU is not
in use by the host**. For a reliable setup, use a host where:

- the GPU is a secondary card not driving the host console/display, **or**
- the host runs headless, **or**
- you bind the GPU to `vfio-pci` at boot (blacklist `nouveau`/`nvidia` for that
  device, or use a `vfio-pci.ids=10de:2704,10de:22bb` kernel argument).

When the GPU is bound to `vfio-pci`, `vmsmith host gpus` shows `DRIVER vfio-pci`
and it is ready to assign.

> **Note:** vmsmith does not modify the host's driver bindings or kernel
> command line for you — that is a deliberate, host-wide change you make once.
> vmsmith only emits the libvirt `<hostdev managed='yes'>` element, which drives
> the per-VM-start rebind.

---

## 2. Create a VM with a GPU

Pass `--gpu` with the PCI address from `vmsmith host gpus` (repeat for multiple
GPUs). The long (`0000:01:00.0`) and short (`01:00.0`) forms are both accepted.

```bash
vmsmith vm create cuda-box \
  --image ubuntu-22.04.qcow2 \
  --cpus 8 --ram 16384 --disk 100 \
  --firmware uefi \
  --gpu 0000:01:00.0
```

- **`--firmware uefi` is strongly recommended** for GPU passthrough (and
  required for cards with large BARs / Resizable BAR, and for Windows 11
  guests). vmsmith already defaults to the `q35` machine type, which is the
  other half of the modern passthrough baseline.
- vmsmith expands `01:00.0` to the full IOMMU group (`01:00.0` + `01:00.1`) and
  emits one `<hostdev>` per device.

REST equivalent:

```bash
curl -X POST http://localhost:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"cuda-box","image":"ubuntu-22.04.qcow2","cpus":8,
       "ram_mb":16384,"disk_gb":100,"firmware":"uefi",
       "gpus":["0000:01:00.0"]}'
```

The create response and `vmsmith vm create` output echo the attached GPUs.

### 2.1 From the web GUI

In the **Machines** page, click **New VM** → **Advanced** tab. The **GPU
Passthrough** section lists every GPU the daemon discovered (the same data as
`vmsmith host gpus`), each with its vendor, PCI address, bound driver, and IOMMU
group. Tick the GPU(s) to pass through and create the VM as usual. A green
`vfio-pci` chip means the device is ready; an amber chip (e.g. `nvidia`) warns
that libvirt will rebind it at start, which only works if the GPU isn't driving
the host console. Recommended: also set **Firmware → uefi** in the Device
Tuning section. The VM detail page shows a **GPU Passthrough** card listing the
assigned addresses.

---

## 3. Inside the guest

After the VM boots, install the vendor driver in the guest:

- **Linux guest, NVIDIA:** install the NVIDIA driver + CUDA toolkit from your
  distro or NVIDIA's `.run` installer, then `nvidia-smi` should list the card.
- **Windows guest, NVIDIA:** install the standard GeForce/Studio or datacenter
  driver. (For Windows guests, also see `docs/WINDOWS_GUESTS.md`.)

The PCI device appears in the guest as a normal GPU (`lspci | grep -i nvidia`).

---

## 4. Lifecycle notes

- The GPU is owned by the guest for the lifetime of the VM. With
  `managed='yes'`, libvirt reattaches the device to the host driver when the VM
  stops, so the same GPU can be reused by another VM (or the host) afterwards.
- The GPU set is fixed at create time — there is no live add/remove. Recreate
  the VM to change the assignment. Clones intentionally start without any GPU
  assignments so they do not inherit passthrough devices that are already bound
  to the source VM.
- A GPU can only be assigned to one running VM at a time. Starting a second VM
  that wants the same GPU fails at libvirt with a device-in-use error.

---

## 5. Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `vmsmith host gpus` shows no GPUs | IOMMU not enabled, or running on a host with no discrete GPU. Check `ls /sys/kernel/iommu_groups/`. |
| VM start fails: "Device or resource busy" | The GPU is still bound to the host driver and in use (driving the console). Bind it to `vfio-pci` at boot, or use a headless/secondary GPU. |
| GPU shows in guest but driver errors (NVIDIA Code 43, historically) | Modern NVIDIA drivers no longer reject KVM, so this is rare on the RTX 40-series. Ensure `--firmware uefi` and a recent driver. |
| IOMMU group contains unrelated devices | Consumer-board grouping limitation. A clean group (GPU + its audio) is required without an ACS-override kernel. |
| Large-VRAM card fails to start | Enable **Above 4G Decoding** / **Resizable BAR** in firmware and use `--firmware uefi`. |
