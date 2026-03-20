"""E2E tests for VM lifecycle via the REST API.

Covers:
  1. Create a VM from Rocky image, verify IP, reachability, and SSH
  2. Snapshot, restore, export image, create VM from exported image
"""

import pytest

from helpers import (
    SSH_USER,
    api_delete,
    api_get,
    api_post,
    delete_vm_api,
    ping_host,
    ssh_run,
    wait_for_ssh,
    wait_for_vm_ip,
    wait_for_vm_state,
)


@pytest.mark.api
class TestAPIVMLifecycle:
    """Test VM lifecycle through the REST API."""

    # ------------------------------------------------------------------
    # Step 1: Create VM, verify IP, reachability, SSH
    # ------------------------------------------------------------------

    def test_create_vm_and_verify(self, rocky_image, ssh_pubkey, api_vm_cleanup):
        """POST /vms to create a Rocky VM, wait for IP, ping, SSH."""
        spec = {
            "name": "e2e-api-rocky",
            "image": rocky_image,
            "cpus": 2,
            "ram_mb": 2048,
            "disk_gb": 20,
            "ssh_pub_key": ssh_pubkey,
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201, f"Create failed: {resp.text}"

        vm = resp.json()
        vm_id = vm["id"]
        api_vm_cleanup.append(vm_id)

        assert vm_id.startswith("vm-")
        assert vm["name"] == "e2e-api-rocky"
        assert vm["state"] in ("running", "creating")

        # Wait for IP
        ip = wait_for_vm_ip(vm_id, source="api")
        assert ip, "VM did not get an IP"

        # Verify via GET
        resp = api_get(f"/vms/{vm_id}")
        assert resp.status_code == 200
        assert resp.json()["ip"] == ip

        # Ping
        assert ping_host(ip), f"VM IP {ip} not reachable"

        # SSH
        wait_for_ssh(ip)
        hostname = ssh_run(ip, "hostname").strip()
        assert hostname, "SSH returned empty hostname"

        TestAPIVMLifecycle._vm_id = vm_id
        TestAPIVMLifecycle._vm_ip = ip

    # ------------------------------------------------------------------
    # Step 1b: VM appears in list
    # ------------------------------------------------------------------

    def test_vm_in_list(self):
        """GET /vms should include the created VM."""
        vm_id = getattr(TestAPIVMLifecycle, "_vm_id", None)
        if not vm_id:
            pytest.skip("No VM created")

        resp = api_get("/vms")
        assert resp.status_code == 200
        vms = resp.json()
        ids = [v["id"] for v in vms]
        assert vm_id in ids

    # ------------------------------------------------------------------
    # Step 2: Snapshot, modify, restore, verify
    # ------------------------------------------------------------------

    def test_snapshot_and_restore(self):
        """Create snapshot via API, modify VM, restore, verify revert."""
        vm_id = getattr(TestAPIVMLifecycle, "_vm_id", None)
        ip = getattr(TestAPIVMLifecycle, "_vm_ip", None)
        if not vm_id or not ip:
            pytest.skip("No VM from previous test")

        # Create snapshot
        resp = api_post(f"/vms/{vm_id}/snapshots", json={"name": "api-pre-change"})
        assert resp.status_code == 201, f"Snapshot create failed: {resp.text}"
        snap = resp.json()
        assert snap["name"] == "api-pre-change"

        # List snapshots
        resp = api_get(f"/vms/{vm_id}/snapshots")
        assert resp.status_code == 200
        names = [s["name"] for s in resp.json()]
        assert "api-pre-change" in names

        # Make a change
        ssh_run(ip, "echo 'api-marker' | sudo tee /tmp/api_marker")
        assert ssh_run(ip, "cat /tmp/api_marker").strip() == "api-marker"

        # Restore snapshot
        resp = api_post(f"/vms/{vm_id}/snapshots/api-pre-change/restore")
        assert resp.status_code == 200

        # Wait for SSH again
        wait_for_ssh(ip)

        # Verify revert
        result = ssh_run(ip, "test -f /tmp/api_marker && echo exists || echo missing").strip()
        assert result == "missing", "Snapshot restore did not revert"

    # ------------------------------------------------------------------
    # Step 2b: Export image
    # ------------------------------------------------------------------

    def test_export_image(self):
        """POST /images to create an image from the VM."""
        vm_id = getattr(TestAPIVMLifecycle, "_vm_id", None)
        if not vm_id:
            pytest.skip("No VM")

        # Stop VM first
        resp = api_post(f"/vms/{vm_id}/stop")
        assert resp.status_code == 200
        wait_for_vm_state(vm_id, "stopped")

        # Create image
        resp = api_post("/images", json={"vm_id": vm_id, "name": "e2e-api-export"})
        assert resp.status_code == 201, f"Image create failed: {resp.text}"
        img = resp.json()
        assert img["name"] == "e2e-api-export"

        # Verify in list
        resp = api_get("/images")
        assert resp.status_code == 200
        names = [i["name"] for i in resp.json()]
        assert "e2e-api-export" in names

        TestAPIVMLifecycle._image_id = img["id"]
        TestAPIVMLifecycle._image_name = img["name"]

    # ------------------------------------------------------------------
    # Step 2c: Create VM from exported image
    # ------------------------------------------------------------------

    def test_create_from_exported_image(self, ssh_pubkey, api_vm_cleanup):
        """Create a new VM from the exported image and verify boot + SSH."""
        image_name = getattr(TestAPIVMLifecycle, "_image_name", None)
        if not image_name:
            pytest.skip("No exported image")

        spec = {
            "name": "e2e-api-from-export",
            "image": image_name,
            "cpus": 2,
            "ram_mb": 2048,
            "disk_gb": 20,
            "ssh_pub_key": ssh_pubkey,
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201, f"Create from export failed: {resp.text}"

        vm = resp.json()
        vm_id = vm["id"]
        api_vm_cleanup.append(vm_id)

        ip = wait_for_vm_ip(vm_id, source="api")
        assert ip
        assert ping_host(ip)
        wait_for_ssh(ip)

        hostname = ssh_run(ip, "hostname").strip()
        assert hostname, "SSH to VM from exported image failed"

    # ------------------------------------------------------------------
    # Step 2d: Stop and start via API
    # ------------------------------------------------------------------

    def test_stop_and_start_vm(self):
        """POST /vms/{id}/stop and /start, verify state transitions."""
        vm_id = getattr(TestAPIVMLifecycle, "_vm_id", None)
        if not vm_id:
            pytest.skip("No VM")

        # Start if stopped (from image export test)
        resp = api_post(f"/vms/{vm_id}/start")
        assert resp.status_code == 200
        wait_for_vm_state(vm_id, "running")

        resp = api_get(f"/vms/{vm_id}")
        assert resp.json()["state"] == "running"

        # Stop
        resp = api_post(f"/vms/{vm_id}/stop")
        assert resp.status_code == 200
        wait_for_vm_state(vm_id, "stopped")

        resp = api_get(f"/vms/{vm_id}")
        assert resp.json()["state"] == "stopped"

    # ------------------------------------------------------------------
    # Cleanup
    # ------------------------------------------------------------------

    def test_cleanup(self):
        """Delete test resources."""
        vm_id = getattr(TestAPIVMLifecycle, "_vm_id", None)
        image_id = getattr(TestAPIVMLifecycle, "_image_id", None)

        if vm_id:
            api_delete(f"/vms/{vm_id}/snapshots/api-pre-change")
            delete_vm_api(vm_id)

        if image_id:
            api_delete(f"/images/{image_id}")
