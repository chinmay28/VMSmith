"""E2E tests for VM lifecycle via the vmsmith CLI.

Covers:
  1. Create a VM from Rocky image, verify IP, reachability, and SSH
  2. Snapshot, restore, export image, create VM from exported image
"""

import pytest

from helpers import (
    SSH_USER,
    delete_vm_cli,
    parse_create_output,
    parse_table_output,
    parse_vm_info,
    ping_host,
    run_cli,
    ssh_run,
    wait_for_ssh,
    wait_for_vm_ip,
)


@pytest.mark.cli
class TestCLIVMLifecycle:
    """Test VM create → IP → SSH → snapshot → restore → image → re-create."""

    # ------------------------------------------------------------------
    # Step 1: Create VM, verify IP, reachability, SSH
    # ------------------------------------------------------------------

    def test_create_vm_and_verify(self, rocky_image, ssh_pubkey, cli_vm_cleanup):
        """Create a Rocky VM, wait for IP, ping it, and SSH into it."""
        # Create
        result = run_cli(
            "vm", "create", "e2e-cli-rocky",
            "--image", rocky_image,
            "--cpus", "2",
            "--ram", "2048",
            "--disk", "20",
            "--ssh-key", ssh_pubkey,
        )
        info = parse_create_output(result.stdout)
        vm_id = info["id"]
        cli_vm_cleanup.append(vm_id)

        assert vm_id.startswith("vm-"), f"Unexpected VM ID: {vm_id}"
        assert info["name"] == "e2e-cli-rocky"

        # With static IP pre-assignment, the IP should appear immediately in the
        # create output — no polling required.  Fall back to polling for the
        # rare case where the DHCP range is exhausted and dynamic assignment is used.
        if info.get("ip"):
            ip = info["ip"]
        else:
            ip = wait_for_vm_ip(vm_id, source="cli")
        assert ip, "VM did not get an IP"

        # Verify reachability via ping
        assert ping_host(ip), f"VM IP {ip} is not reachable"

        # Verify SSH access
        wait_for_ssh(ip)
        hostname = ssh_run(ip, "hostname").strip()
        assert hostname, "SSH command returned empty hostname"

        # Verify VM info command
        info_result = run_cli("vm", "info", vm_id)
        vm_info = parse_vm_info(info_result.stdout)
        assert vm_info["name"] == "e2e-cli-rocky"
        assert vm_info["state"] == "running"
        assert vm_info["ip"] == ip

        # Store for use in subsequent tests (via class attribute)
        TestCLIVMLifecycle._vm_id = vm_id
        TestCLIVMLifecycle._vm_ip = ip

    # ------------------------------------------------------------------
    # Step 1b: Verify VM appears in list
    # ------------------------------------------------------------------

    def test_vm_appears_in_list(self):
        """The created VM should appear in `vmsmith vm list`."""
        vm_id = getattr(TestCLIVMLifecycle, "_vm_id", None)
        if not vm_id:
            pytest.skip("No VM created in previous test")

        result = run_cli("vm", "list")
        rows = parse_table_output(result.stdout)
        ids = [r.get("ID", "") for r in rows]
        assert vm_id in ids, f"VM {vm_id} not found in list output"

    # ------------------------------------------------------------------
    # Step 2: Snapshot, modify, restore, verify
    # ------------------------------------------------------------------

    def test_snapshot_and_restore(self):
        """Create a snapshot, make a change, restore, and verify the change is reverted."""
        vm_id = getattr(TestCLIVMLifecycle, "_vm_id", None)
        ip = getattr(TestCLIVMLifecycle, "_vm_ip", None)
        if not vm_id or not ip:
            pytest.skip("No VM from previous test")

        # Create snapshot
        run_cli("snapshot", "create", vm_id, "--name", "pre-change")

        # Verify snapshot in list
        result = run_cli("snapshot", "list", vm_id)
        assert "pre-change" in result.stdout

        # Make a change inside the VM
        ssh_run(ip, "echo 'e2e-marker-file' | sudo tee /tmp/e2e_marker")
        marker = ssh_run(ip, "cat /tmp/e2e_marker").strip()
        assert marker == "e2e-marker-file"

        # Restore snapshot (VM must be stopped first for some hypervisors)
        run_cli("snapshot", "restore", vm_id, "--name", "pre-change")

        # Wait for VM to be accessible again
        wait_for_ssh(ip)

        # Verify the change is reverted
        check = run_cli("vm", "info", vm_id)
        # The marker file should not exist after restore
        result = ssh_run(ip, "test -f /tmp/e2e_marker && echo exists || echo missing").strip()
        assert result == "missing", "Snapshot restore did not revert changes"

    # ------------------------------------------------------------------
    # Step 2b: Export image from VM
    # ------------------------------------------------------------------

    def test_export_image_from_vm(self):
        """Export the VM as a reusable image."""
        vm_id = getattr(TestCLIVMLifecycle, "_vm_id", None)
        if not vm_id:
            pytest.skip("No VM from previous test")

        # Stop VM before exporting (best practice)
        run_cli("vm", "stop", vm_id)

        # Export image
        result = run_cli("image", "create", vm_id, "--name", "e2e-rocky-export")
        assert "Image created" in result.stdout or "e2e-rocky-export" in result.stdout

        # Verify image appears in list
        result = run_cli("image", "list")
        assert "e2e-rocky-export" in result.stdout

        TestCLIVMLifecycle._exported_image = "e2e-rocky-export"

    # ------------------------------------------------------------------
    # Step 2c: Create VM from exported image and verify
    # ------------------------------------------------------------------

    def test_create_vm_from_exported_image(self, ssh_pubkey, cli_vm_cleanup):
        """Create a new VM from the exported image and verify it boots and is SSH-able."""
        exported = getattr(TestCLIVMLifecycle, "_exported_image", None)
        if not exported:
            pytest.skip("No exported image from previous test")

        # Find image path from image list
        result = run_cli("image", "list")
        rows = parse_table_output(result.stdout)
        image_row = None
        for r in rows:
            if r.get("NAME", "") == exported:
                image_row = r
                break
        assert image_row, f"Exported image {exported} not found in list"

        # Create VM from exported image (use image name)
        result = run_cli(
            "vm", "create", "e2e-cli-from-export",
            "--image", exported,
            "--cpus", "2",
            "--ram", "2048",
            "--disk", "20",
            "--ssh-key", ssh_pubkey,
        )
        info = parse_create_output(result.stdout)
        vm_id = info["id"]
        cli_vm_cleanup.append(vm_id)

        # Wait for IP and SSH
        ip = wait_for_vm_ip(vm_id, source="cli")
        assert ip, "VM from export did not get an IP"
        assert ping_host(ip), f"VM from export at {ip} not reachable"
        wait_for_ssh(ip)

        hostname = ssh_run(ip, "hostname").strip()
        assert hostname, "SSH to VM from export failed"

    # ------------------------------------------------------------------
    # Cleanup: delete snapshot and original VM
    # ------------------------------------------------------------------

    def test_cleanup_snapshot(self):
        """Delete the test snapshot."""
        vm_id = getattr(TestCLIVMLifecycle, "_vm_id", None)
        if vm_id:
            run_cli("snapshot", "delete", vm_id, "--name", "pre-change", check=False)

    def test_cleanup_original_vm(self):
        """Delete the original test VM."""
        vm_id = getattr(TestCLIVMLifecycle, "_vm_id", None)
        if vm_id:
            delete_vm_cli(vm_id)

    def test_cleanup_exported_image(self):
        """Delete the exported image."""
        # Find image ID
        result = run_cli("image", "list", check=False)
        rows = parse_table_output(result.stdout)
        for r in rows:
            if r.get("NAME", "") == "e2e-rocky-export":
                run_cli("image", "delete", r["ID"], check=False)
                break
