"""E2E tests for the live events SSE stream via the REST API.

Covers:
  1. Start a real stopped VM and verify the live /events/stream feed emits
     the corresponding vm.started event for that VM.
"""

import json
import time

import pytest
import requests

from helpers import api_get, api_post, api_url, wait_for_vm_state


def _read_sse_event(response, timeout=90):
    """Read one SSE event frame from a streaming requests response."""
    deadline = time.time() + timeout
    event = {"id": "", "event": "", "data": []}

    for raw_line in response.iter_lines(chunk_size=1, decode_unicode=True):
        if time.time() > deadline:
            raise TimeoutError(f"Timed out waiting for SSE event within {timeout}s")
        if raw_line is None:
            continue
        line = raw_line.strip("\r")
        if line == "":
            if event["id"] or event["event"] or event["data"]:
                return {
                    "id": event["id"],
                    "event": event["event"],
                    "data": "\n".join(event["data"]),
                }
            continue
        if line.startswith(":"):
            continue
        if line.startswith("id:"):
            event["id"] = line[3:].strip()
        elif line.startswith("event:"):
            event["event"] = line[6:].strip()
        elif line.startswith("data:"):
            event["data"].append(line[5:].lstrip())

    raise RuntimeError("SSE stream closed before an event frame was received")


@pytest.mark.api
class TestAPIEventStream:
    """Exercise the live SSE event feed against a real daemon."""

    def test_start_vm_emits_live_started_event(self, rocky_image, ssh_pubkey, api_vm_cleanup):
        """Create a VM, stop it, then verify /events/stream emits vm.started on restart."""
        spec = {
            "name": "e2e-api-events-rocky",
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

        wait_for_vm_state(vm_id, "running")

        resp = api_post(f"/vms/{vm_id}/stop")
        assert resp.status_code == 200, f"Stop failed: {resp.text}"
        wait_for_vm_state(vm_id, "stopped")

        # Snapshot the current event sequence so the SSE replay window starts
        # exactly after the events that created/stopped the VM.
        resp = api_get("/events?page=1&per_page=1")
        assert resp.status_code == 200, f"List events failed: {resp.text}"
        events = resp.json()
        since = events[0]["id"] if events else "0"

        with requests.get(
            api_url(f"/events/stream?vm_id={vm_id}&type=vm.started&since={since}"),
            stream=True,
            timeout=(10, 120),
            headers={"Accept": "text/event-stream"},
        ) as stream_resp:
            assert stream_resp.status_code == 200, f"Stream failed: {stream_resp.text}"
            assert stream_resp.headers["Content-Type"].startswith("text/event-stream")

            start_resp = api_post(f"/vms/{vm_id}/start")
            assert start_resp.status_code == 200, f"Start failed: {start_resp.text}"

            event = _read_sse_event(stream_resp, timeout=120)
            assert event["event"] == "vm.started", f"Unexpected event: {event}"
            payload = json.loads(event["data"])
            assert payload["vm_id"] == vm_id
            assert payload["type"] == "vm.started"
            assert payload["source"] == "libvirt"

        wait_for_vm_state(vm_id, "running")
