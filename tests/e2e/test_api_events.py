"""E2E tests for the live events SSE stream against a real VM."""

import concurrent.futures

import pytest

from helpers import api_post, delete_vm_api, wait_for_event, wait_for_vm_state


@pytest.mark.api
class TestAPIEventStream:
    """Exercise the live SSE stream against the real daemon/libvirt stack."""

    def test_starting_a_real_vm_emits_vm_started(self, rocky_image, ssh_pubkey):
        """Create a real VM, restart it under an SSE subscription, and observe vm.started."""
        vm_ids: list[str] = []

        def cleanup() -> None:
            for vm_id in reversed(vm_ids):
                delete_vm_api(vm_id)

        try:
            resp = api_post(
                "/vms",
                json={
                    "name": "e2e-events-stream",
                    "image": rocky_image,
                    "cpus": 2,
                    "ram_mb": 2048,
                    "disk_gb": 20,
                    "ssh_pub_key": ssh_pubkey,
                },
            )
            assert resp.status_code == 201, f"Create failed: {resp.text}"

            vm = resp.json()
            vm_ids.append(vm["id"])
            wait_for_vm_state(vm["id"], "running", timeout=180)

            resp = api_post(f"/vms/{vm['id']}/stop")
            assert resp.status_code == 200, f"Stop failed: {resp.text}"
            wait_for_vm_state(vm["id"], "stopped", timeout=90)

            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
                wait_future = pool.submit(
                    wait_for_event,
                    lambda event: event.get("type") == "vm.started" and event.get("vm_id") == vm["id"],
                    {"type": "vm.started", "vm_id": vm["id"]},
                    180,
                )

                resp = api_post(f"/vms/{vm['id']}/start")
                assert resp.status_code == 200, f"Start failed: {resp.text}"

                event = wait_future.result(timeout=200)
                assert event["type"] == "vm.started"
                assert event["vm_id"] == vm["id"]
                assert event["source"] == "libvirt"
        finally:
            cleanup()
