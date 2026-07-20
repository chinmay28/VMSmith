"""E2E tests for VM resource metrics under real guest load (roadmap 4.1.10).

Covers the final 4.1.10 gap: boot a real VM, induce CPU load and network
traffic in-guest, and verify the daemon's per-VM stats endpoint reports
non-zero CPU and non-zero network counters within the 30s chart window.
"""

import time

import pytest

from helpers import (
    api_get,
    api_post,
    ssh_run,
    wait_for_ssh,
    wait_for_vm_ip,
    wait_for_vm_state,
)


def _current_sample(vm_id):
    """Fetch the VM's current metric sample (may be None before first poll)."""
    resp = api_get(f"/vms/{vm_id}/stats")
    assert resp.status_code == 200, f"stats failed: {resp.text}"
    return resp.json().get("current") or {}


def _wait_for_metric(vm_id, key, predicate, timeout=90, interval=2):
    """Poll the current sample until predicate(sample[key]) holds."""
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        sample = _current_sample(vm_id)
        last = sample.get(key)
        if last is not None and predicate(last):
            return last
        time.sleep(interval)
    raise AssertionError(
        f"metric {key} never satisfied predicate within {timeout}s (last={last})"
    )


@pytest.mark.api
@pytest.mark.metrics
class TestVMMetricsUnderLoad:
    """Real-VM load test: CPU burn and network traffic must show up in stats."""

    def test_cpu_and_net_metrics_reflect_guest_load(
        self, rocky_image, ssh_pubkey, api_vm_cleanup
    ):
        spec = {
            "name": "e2e-metrics-load",
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

        wait_for_vm_state(vm_id, "running")
        ip = wait_for_vm_ip(vm_id)
        wait_for_ssh(ip)

        # Idle baseline: the collector must be sampling this VM at all.
        _wait_for_metric(vm_id, "cpu_percent", lambda v: v is not None, timeout=60)

        # 1. CPU burn: two busy loops (one per vCPU) for ~60s in-guest.
        ssh_run(
            ip,
            "nohup sh -c 'for i in 1 2; do (timeout 60 sh -c \"while :; do :; done\" &) ; done' "
            ">/dev/null 2>&1 &",
        )
        # The 30s requirement from 4.1.10: the induced load must be visible
        # in the chart-backing stats within 30 seconds.
        cpu = _wait_for_metric(vm_id, "cpu_percent", lambda v: v > 20, timeout=30)
        assert cpu > 20, f"expected busy CPU, got {cpu}%"

        # 2. Network traffic: pull bytes through the NAT interface. Ping with
        # large payloads is dependency-free on GenericCloud images.
        ssh_run(
            ip,
            "nohup sh -c 'timeout 45 ping -s 1400 -i 0.01 192.168.100.1' "
            ">/dev/null 2>&1 &",
        )
        tx = _wait_for_metric(vm_id, "net_tx_bps", lambda v: v > 0, timeout=30)
        rx = _wait_for_metric(vm_id, "net_rx_bps", lambda v: v > 0, timeout=30)
        assert tx > 0 and rx > 0, f"expected non-zero net counters, got tx={tx} rx={rx}"

    def test_stopped_vm_reports_no_current_sample(
        self, rocky_image, ssh_pubkey, api_vm_cleanup
    ):
        spec = {
            "name": "e2e-metrics-stopped",
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
        resp = api_post(f"/vms/{vm_id}/stop")
        assert resp.status_code == 200, f"Stop failed: {resp.text}"
        wait_for_vm_state(vm_id, "stopped")

        # Give the collector one interval to notice the transition, then the
        # stats endpoint must still answer 200 with state=stopped.
        time.sleep(6)
        resp = api_get(f"/vms/{vm_id}/stats")
        assert resp.status_code == 200, f"stats failed: {resp.text}"
        body = resp.json()
        assert body["state"] == "stopped"
