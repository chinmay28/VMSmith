"""E2E tests for the per-VM metrics endpoint (roadmap 4.1.10).

Covers:
  1. Create a Rocky VM, generate CPU + network load over SSH
  2. Poll GET /vms/{id}/stats until non-zero cpu_percent and net counters appear

Skips cleanly when the daemon has metrics collection disabled
(503 metrics_disabled).
"""

import time

import pytest

from helpers import (
    POLL_INTERVAL,
    api_get,
    api_post,
    ssh_run,
    wait_for_ssh,
    wait_for_vm_ip,
)

# The collector samples every 10s by default and needs two samples to compute
# a rate, so give it a few cycles to observe the generated load.
STATS_TIMEOUT = 180


@pytest.mark.metrics
@pytest.mark.api
@pytest.mark.timeout(900)
class TestAPIVMMetrics:
    """Verify per-VM stats report real CPU and network activity."""

    def test_stats_report_cpu_and_net_load(self, rocky_image, ssh_pubkey, api_vm_cleanup):
        """Create VM, drive load, assert non-zero cpu_percent + net bps samples."""
        spec = {
            "name": "e2e-api-metrics",
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

        ip = wait_for_vm_ip(vm_id, source="api")
        assert ip, "VM did not get an IP"
        wait_for_ssh(ip)

        # Baseline snapshot — also detects the metrics-disabled daemon config.
        resp = api_get(f"/vms/{vm_id}/stats")
        if resp.status_code == 503:
            pytest.skip("metrics collection disabled on daemon (metrics_disabled)")
        assert resp.status_code == 200, f"Stats failed: {resp.text}"
        snap = resp.json()
        assert snap["vm_id"] == vm_id
        assert snap["state"] == "running"
        assert isinstance(snap["history"], list)
        assert snap["interval_seconds"] > 0

        # CPU load: background busy loop (dd ships on Rocky GenericCloud).
        ssh_run(ip, "nohup sh -c 'dd if=/dev/zero of=/dev/null' >/dev/null 2>&1 & echo ok")

        try:
            saw_cpu = False
            saw_net = False
            deadline = time.time() + STATS_TIMEOUT
            while time.time() < deadline:
                # Network load: stream zeroes back over SSH each pass so the
                # guest NIC counters keep moving while we poll.
                if not saw_net:
                    ssh_run(ip, "dd if=/dev/zero bs=1M count=16 2>/dev/null")

                resp = api_get(f"/vms/{vm_id}/stats")
                assert resp.status_code == 200, f"Stats failed: {resp.text}"
                snap = resp.json()

                samples = list(snap["history"])
                if snap.get("current"):
                    samples.append(snap["current"])
                for sample in samples:
                    if (sample.get("cpu_percent") or 0) > 0:
                        saw_cpu = True
                    if (sample.get("net_rx_bps") or 0) > 0 or (sample.get("net_tx_bps") or 0) > 0:
                        saw_net = True

                if saw_cpu and saw_net:
                    break
                time.sleep(POLL_INTERVAL)

            assert saw_cpu, f"No non-zero cpu_percent sample within {STATS_TIMEOUT}s"
            assert saw_net, f"No non-zero net_rx_bps/net_tx_bps sample within {STATS_TIMEOUT}s"
        finally:
            # Stop the busy loop; best-effort — the VM is deleted on teardown anyway.
            try:
                ssh_run(ip, "pkill -f 'dd if=/dev/zero' || true")
            except Exception:
                pass
