"""E2E test for the scheduled-operations subsystem against a real VM
(roadmap 5.2.11, final tier).

Covers: a real schedule with a seconds-granularity cron fires a snapshot
action on a live QEMU VM; the snapshot appears via the snapshot API, and the
run history records a success attributed to the scheduler.
"""

import time

import pytest

from helpers import api_delete, api_get, api_post, wait_for_vm_state


@pytest.mark.api
@pytest.mark.schedules
class TestScheduleFiresSnapshotOnRealVM:
    def test_schedule_snapshot_fires_and_appears(
        self, rocky_image, ssh_pubkey, api_vm_cleanup
    ):
        spec = {
            "name": "e2e-sched-snap",
            "image": rocky_image,
            "cpus": 1,
            "ram_mb": 1024,
            "disk_gb": 10,
            "ssh_pub_key": ssh_pubkey,
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201, f"Create failed: {resp.text}"
        vm_id = resp.json()["id"]
        api_vm_cleanup.append(vm_id)
        wait_for_vm_state(vm_id, "running")

        # Fire every 15 seconds (6-field cron with seconds).
        sched_spec = {
            "name": "e2e-snap-every-15s",
            "vm_id": vm_id,
            "action": "snapshot",
            "cron_spec": "*/15 * * * * *",
            "enabled": True,
            "retention_count": 3,
        }
        resp = api_post("/schedules", json=sched_spec)
        assert resp.status_code in (200, 201), f"Schedule create failed: {resp.text}"
        sched_id = resp.json()["id"]

        try:
            # Wait up to 90s for at least one successful run.
            deadline = time.time() + 90
            success_run = None
            while time.time() < deadline and success_run is None:
                resp = api_get(f"/schedules/{sched_id}/runs?page=1&per_page=20")
                assert resp.status_code == 200, f"Runs failed: {resp.text}"
                for run in resp.json():
                    if run["status"] == "success":
                        success_run = run
                        break
                if success_run is None:
                    time.sleep(3)
            assert success_run is not None, "schedule never recorded a successful run"
            assert success_run["vm_id"] == vm_id
            assert success_run["actor"] == "scheduler"

            # The auto-snapshot naming contract: auto-<schedule-name>-*.
            resp = api_get(f"/vms/{vm_id}/snapshots")
            assert resp.status_code == 200, f"Snapshots failed: {resp.text}"
            names = [s["name"] for s in resp.json()]
            auto = [n for n in names if n.startswith("auto-e2e-snap-every-15s-")]
            assert auto, f"no auto snapshot found in {names}"
        finally:
            api_delete(f"/schedules/{sched_id}")
