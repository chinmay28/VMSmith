"""E2E test for the live SSE event stream (roadmap 4.2.17).

Covers:
  1. Open GET /events/stream (server-side ?vm_id= filter, no replay cursor)
  2. Start a stopped VM via the API
  3. Assert a `vm.started` libvirt lifecycle event for that VM arrives live

A background reader thread parses ``id:``/``event:``/``data:`` frames into a
queue so the test can never block forever on the socket.
"""

import json
import queue
import threading
import time

import pytest
import requests

from helpers import api_post, api_url, wait_for_vm_state

EVENT_TIMEOUT = 90


def _read_sse_frames(resp, frames):
    """Parse SSE frames from a streaming response into the queue."""
    frame = {}
    try:
        for line in resp.iter_lines(decode_unicode=True):
            if line is None:
                continue
            if line == "":
                if frame:
                    frames.put(frame)
                frame = {}
                continue
            if line.startswith(":"):
                continue  # heartbeat comment
            field, _, value = line.partition(":")
            if value.startswith(" "):
                value = value[1:]
            frame[field] = value
    except Exception:
        pass  # socket closed during teardown


@pytest.mark.events
@pytest.mark.api
class TestAPIEventStream:
    """Verify VM lifecycle events are pushed live on the SSE stream."""

    def test_vm_started_event_arrives_on_stream(self, rocky_image, ssh_pubkey, api_vm_cleanup):
        # Create a VM and bring it to a clean stopped state first.
        spec = {
            "name": "e2e-api-events",
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
        resp = api_post(f"/vms/{vm_id}/stop")
        assert resp.status_code == 200
        wait_for_vm_state(vm_id, "stopped")

        # Open the live stream. Without Last-Event-ID / ?since= there is no
        # replay, so everything received is a live event. The server-side
        # vm_id filter drops cross-VM noise.
        stream = requests.get(
            api_url("/events/stream"),
            params={"vm_id": vm_id},
            headers={"Accept": "text/event-stream"},
            stream=True,
            timeout=(10, 60),  # heartbeats every 30s keep the read alive
        )
        assert stream.status_code == 200, f"Stream open failed: {stream.text}"
        assert stream.headers["Content-Type"].startswith("text/event-stream")

        frames = queue.Queue()
        reader = threading.Thread(target=_read_sse_frames, args=(stream, frames), daemon=True)
        reader.start()

        try:
            resp = api_post(f"/vms/{vm_id}/start")
            assert resp.status_code == 200, f"Start failed: {resp.text}"

            # The daemon emits vm.start_requested (source=app) from the
            # handler, then vm.started (source=libvirt) once the domain
            # actually transitions to running — assert on the latter.
            started = None
            deadline = time.time() + EVENT_TIMEOUT
            while time.time() < deadline and started is None:
                remaining = deadline - time.time()
                if remaining <= 0:
                    break
                try:
                    frame = frames.get(timeout=min(remaining, 5))
                except queue.Empty:
                    continue
                if frame.get("event") != "vm.started" or "data" not in frame:
                    continue
                evt = json.loads(frame["data"])
                if evt["type"] == "vm.started" and evt["vm_id"] == vm_id:
                    started = (frame, evt)

            assert started, f"No vm.started event for {vm_id} within {EVENT_TIMEOUT}s"
            frame, evt = started
            assert frame.get("id"), "SSE frame is missing its id: field"
            assert evt["source"] == "libvirt"
            assert evt["severity"] == "info"
        finally:
            stream.close()
            reader.join(timeout=10)
