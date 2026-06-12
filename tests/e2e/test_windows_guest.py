"""E2E test for Windows guest support (roadmap 5.6.16).

Gated behind --windows-image / VMSMITH_WINDOWS_IMAGE — the whole module is
skipped when unset (via the windows_image fixture). Windows first boots run
cloudbase-init and are slow, so the IP wait defaults to 900s
(--windows-ip-timeout / VMSMITH_WINDOWS_IP_TIMEOUT).

Covers:
  1. Create a Windows VM (os_type=windows, explicit admin_password)
  2. Wait for the DHCP IP, then verify RDP (TCP 3389) is reachable
  3. Optional non-fatal SSH check when the image ships OpenSSH Server
"""

import pytest

from helpers import (
    api_post,
    ssh_run,
    wait_for_ssh,
    wait_for_tcp_port,
    wait_for_vm_ip,
)

RDP_PORT = 3389
RDP_TIMEOUT = 600
# Explicit password so the daemon must not auto-generate one (5.6.17).
ADMIN_PASSWORD = "E2eWinPassw0rd!"


@pytest.mark.windows
@pytest.mark.api
@pytest.mark.timeout(1800)
class TestWindowsGuest:
    """Create a Windows VM and verify it boots far enough to serve RDP."""

    def test_create_windows_vm_and_verify_rdp(
        self, windows_image, windows_ip_timeout, ssh_pubkey, api_vm_cleanup
    ):
        spec = {
            "name": "e2e-win-guest",
            "image": windows_image,
            "cpus": 2,
            "ram_mb": 4096,  # Windows guests require >= 2048
            "disk_gb": 40,   # and >= 32
            "os_type": "windows",
            "admin_password": ADMIN_PASSWORD,
            "ssh_pub_key": ssh_pubkey,
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201, f"Windows create failed: {resp.text}"
        vm = resp.json()
        vm_id = vm["id"]
        api_vm_cleanup.append(vm_id)

        assert vm_id.startswith("vm-")
        assert vm["spec"]["os_type"] == "windows"
        # Explicit admin_password ⇒ nothing auto-generated...
        assert not vm.get("generated_admin_password"), (
            "daemon auto-generated a password despite explicit admin_password"
        )
        # ...and the write-only password must be redacted from the stored spec.
        assert not vm["spec"].get("admin_password"), (
            "admin_password should be redacted from the returned VM record"
        )

        # Windows boots are slow — generous, configurable wait.
        ip = wait_for_vm_ip(vm_id, timeout=windows_ip_timeout, source="api")
        assert ip, "Windows VM did not get an IP"

        # RDP reachability is the "boot completed" signal for a Windows guest.
        wait_for_tcp_port(ip, RDP_PORT, timeout=RDP_TIMEOUT)

        # Optional SSH check — only meaningful when the image ships OpenSSH
        # Server; the roadmap treats this as best-effort, so never fail on it.
        try:
            wait_for_ssh(ip, timeout=60)
            hostname = ssh_run(ip, "hostname").strip()
            assert hostname
        except Exception as exc:
            print(f"optional Windows SSH check skipped (non-fatal): {exc}")
