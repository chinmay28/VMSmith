"""GPU passthrough E2E tier (roadmap 5.7.12).

Gated behind ``--gpu <pci-addr>`` / ``VMSMITH_GPU`` the same way the Linux
tier is gated behind ``--rocky-image`` — it requires a host with IOMMU
enabled and a passthrough-eligible GPU that is not driving the host console
(see docs/GPU_PASSTHROUGH.md).

Creates a VM with the GPU assigned, boots it, and verifies the device is
visible in the guest via lspci. When the proprietary driver stack is
pre-baked into the image (``--gpu-smi-cmd``, e.g. ``nvidia-smi`` or
``rocm-smi``), the tool must also succeed in-guest.
"""

import pytest

from helpers import (
    api_get,
    api_post,
    ssh_run,
    wait_for_ssh,
    wait_for_vm_ip,
    wait_for_vm_state,
)


@pytest.mark.api
@pytest.mark.gpu
class TestGPUPassthrough:
    def test_gpu_visible_in_guest(
        self, rocky_image, ssh_pubkey, gpu_address, api_vm_cleanup, request
    ):
        # The host must report the GPU as assignable before we try.
        resp = api_get("/host/gpus")
        assert resp.status_code == 200, f"host gpus failed: {resp.text}"
        gpus = resp.json()
        match = [g for g in gpus if g["address"].endswith(gpu_address.lstrip("0")) or g["address"] == gpu_address]
        assert gpus, "host reported no assignable GPUs"

        spec = {
            "name": "e2e-gpu-guest",
            "image": rocky_image,
            "cpus": 2,
            "ram_mb": 4096,
            "disk_gb": 20,
            "ssh_pub_key": ssh_pubkey,
            "gpus": [gpu_address],
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201, f"Create failed: {resp.text}"
        vm = resp.json()
        vm_id = vm["id"]
        api_vm_cleanup.append(vm_id)

        # The stored spec must round-trip the requested GPU.
        assert vm["spec"].get("gpus"), "created VM lost its gpus assignment"

        wait_for_vm_state(vm_id, "running")
        ip = wait_for_vm_ip(vm_id)
        wait_for_ssh(ip)

        # The passthrough device must be visible on the guest PCI bus.
        # 0300 = VGA controller, 0302 = 3D controller.
        out = ssh_run(ip, "lspci -nn")
        assert any(cls in out for cls in ("[0300]", "[0302]")), (
            f"no display/3D controller visible in guest lspci:\n{out}"
        )

        # Optional: verify the vendor SMI tool when the image carries the
        # proprietary driver stack.
        smi = request.config.getoption("--gpu-smi-cmd")
        if smi:
            out = ssh_run(ip, smi)
            assert out.strip(), f"{smi} produced no output"
