"""E2E test for scheduled operations against a live daemon (roadmap 5.2.11).

Covers:
  1. Create a VM, then a snapshot schedule firing every 15 seconds
  2. Poll the run history until a status=success run is recorded
  3. Assert the auto-snapshot (auto-<schedule-name>-<UTC ts>) exists on the VM

Skips cleanly when the schedules subsystem is disabled (503 schedules_disabled).
"""

import time

import pytest

from helpers import (
    POLL_INTERVAL,
    api_delete,
    api_get,
    api_post,
    wait_for_vm_state,
)

RUN_TIMEOUT = 120
SCHEDULE_NAME = "e2e-sched-snap"
# Scheduler snapshot action names: auto-<sanitized schedule name>-<UTC timestamp>
AUTO_PREFIX = f"auto-{SCHEDULE_NAME}-"


@pytest.mark.schedules
@pytest.mark.api
class TestAPISchedules:
    """Verify a snapshot schedule fires and creates an auto-snapshot."""

    def test_snapshot_schedule_fires(self, rocky_image, ssh_pubkey, api_vm_cleanup):
        # Target VM
        spec = {
            "name": "e2e-api-sched",
            "image": rocky_image,
            "cpus": 2,
            "ram_mb": 2048,
            "disk_gb": 20,
            "ssh_pub_key": ssh_pubkey,
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201, f"Create failed: {resp.text}"
        vm_id = resp.json()["id"]
        api_vm_cleanup.append(vm_id)
        wait_for_vm_state(vm_id, "running", timeout=120)

        # 6-field cron with seconds: fire every 15 seconds.
        body = {
            "name": SCHEDULE_NAME,
            "vm_id": vm_id,
            "action": "snapshot",
            "cron_spec": "*/15 * * * * *",
            "retention_count": 3,
        }
        resp = api_post("/schedules", json=body)
        if resp.status_code == 503 and "schedules_disabled" in resp.text:
            pytest.skip("schedules subsystem disabled on daemon (schedules_disabled)")
        assert resp.status_code == 201, f"Schedule create failed: {resp.text}"
        sched = resp.json()
        sched_id = sched["id"]
        assert sched_id.startswith("sched-")
        assert sched["action"] == "snapshot"
        assert sched["cron_spec"] == "*/15 * * * * *"
        assert sched["enabled"] is True

        try:
            # Poll run history until a successful fire is recorded.
            success = None
            deadline = time.time() + RUN_TIMEOUT
            while time.time() < deadline and success is None:
                resp = api_get(f"/schedules/{sched_id}/runs")
                assert resp.status_code == 200, f"Runs list failed: {resp.text}"
                for run in resp.json():
                    if run["status"] == "success":
                        success = run
                        break
                if success is None:
                    time.sleep(POLL_INTERVAL)

            assert success, f"No successful run for {sched_id} within {RUN_TIMEOUT}s"
            assert success["schedule_id"] == sched_id
            assert success["vm_id"] == vm_id
            assert success.get("finished_at"), "Successful run should carry finished_at"

            # The fire must have produced an auto-snapshot on the VM.
            resp = api_get(f"/vms/{vm_id}/snapshots")
            assert resp.status_code == 200
            auto = [s["name"] for s in resp.json() if s["name"].startswith(AUTO_PREFIX)]
            assert auto, f"No snapshot with prefix {AUTO_PREFIX!r} after successful run"
        finally:
            api_delete(f"/schedules/{sched_id}")
