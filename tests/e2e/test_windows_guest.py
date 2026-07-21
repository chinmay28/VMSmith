"""Windows guest E2E tier (roadmap 5.6.16).

Gated behind ``--windows-image`` / ``VMSMITH_WINDOWS_IMAGE`` the same way the
Linux tier is gated behind ``--rocky-image``. Provisions a prepared Windows
eval image (cloudbase-init capable, see docs/WINDOWS_GUESTS.md), waits for
the DHCP IP, and verifies RDP (3389) reachability. When
``--windows-ssh-user`` is provided (image with OpenSSH server baked in) an
optional SSH login is verified too.
"""

import socket
import time

import pytest

import helpers
from helpers import api_post, ssh_run, wait_for_ssh, wait_for_vm_ip, wait_for_vm_state


def _wait_for_tcp(ip, port, timeout):
    deadline = time.time() + timeout
    last_err = None
    while time.time() < deadline:
        try:
            with socket.create_connection((ip, port), timeout=5):
                return
        except OSError as err:
            last_err = err
            time.sleep(5)
    raise AssertionError(f"tcp {ip}:{port} never became reachable: {last_err}")


@pytest.mark.api
@pytest.mark.windows
class TestWindowsGuestBoots:
    def test_windows_vm_gets_ip_and_rdp(
        self, windows_image, ssh_pubkey, api_vm_cleanup, request
    ):
        spec = {
            "name": "e2e-win-guest",
            "image": windows_image,
            "cpus": 2,
            "ram_mb": 4096,
            "disk_gb": 64,
            "os_type": "windows",
            "ssh_pub_key": ssh_pubkey,
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201, f"Create failed: {resp.text}"
        body = resp.json()
        vm_id = body["id"]
        api_vm_cleanup.append(vm_id)

        # 5.6.17: an omitted admin_password must surface a one-time generated
        # password on the create response (and never again).
        assert body.get("generated_admin_password"), (
            "expected generated_admin_password on Windows create response"
        )

        wait_for_vm_state(vm_id, "running")

        # Windows first boot + cloudbase-init is slow; allow a generous
        # multiple of the Linux IP timeout.
        ip = wait_for_vm_ip(vm_id, timeout=max(helpers.VM_IP_TIMEOUT, 600))

        # RDP reachability is the Windows-tier health signal.
        _wait_for_tcp(ip, 3389, timeout=max(helpers.VM_SSH_TIMEOUT, 600))

        # Optional SSH check for images with OpenSSH Server enabled.
        ssh_user = request.config.getoption("--windows-ssh-user")
        if ssh_user:
            wait_for_ssh(ip, user=ssh_user, timeout=max(helpers.VM_SSH_TIMEOUT, 600))
            out = ssh_run(ip, "cmd /c ver", user=ssh_user)
            assert "Windows" in out, f"unexpected `ver` output: {out}"
