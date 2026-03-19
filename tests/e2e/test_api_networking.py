"""E2E tests for multi-NIC networking and port forwarding via the REST API.

Covers:
  3. Deploy VMs with multiple interfaces, verify reachable IPs,
     test inter-VM connectivity
  4. Port forwarding — SSH into VM via forwarded port
"""

import re
import socket

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
)


# ======================================================================
# Test 3: Multi-NIC networking via API
# ======================================================================

@pytest.mark.api
@pytest.mark.networking
class TestAPIMultiNIC:
    """Deploy VMs with multiple interfaces via REST API and verify connectivity."""

    def test_create_vm_with_extra_interface(
        self, rocky_image, ssh_pubkey, host_interface, api_vm_cleanup
    ):
        """POST /vms with a network attachment."""
        spec = {
            "name": "e2e-api-multnic-1",
            "image": rocky_image,
            "cpus": 2,
            "ram_mb": 2048,
            "disk_gb": 20,
            "ssh_pub_key": ssh_pubkey,
            "networks": [
                {
                    "name": host_interface,
                    "mode": "macvtap",
                    "host_interface": host_interface,
                }
            ],
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201, f"Create failed: {resp.text}"

        vm = resp.json()
        vm_id = vm["id"]
        api_vm_cleanup.append(vm_id)

        # Verify spec echoed back with networks
        assert len(vm["spec"].get("networks", [])) == 1

        # Wait for management IP
        mgmt_ip = wait_for_vm_ip(vm_id, source="api")
        assert mgmt_ip
        assert ping_host(mgmt_ip)

        wait_for_ssh(mgmt_ip)

        # Check extra interface inside VM
        ifaces = ssh_run(mgmt_ip, "ip -o link show | awk '{print $2}'").strip()
        assert "eth1" in ifaces or "ens" in ifaces, f"Extra interface missing: {ifaces}"

        # Verify extra interface has an IP
        ip_output = ssh_run(mgmt_ip, "ip -4 -o addr show").strip()
        ip_lines = [l for l in ip_output.splitlines() if "127.0.0.1" not in l]
        assert len(ip_lines) >= 2, f"Expected >=2 IPs:\n{ip_output}"

        TestAPIMultiNIC._vm1_id = vm_id
        TestAPIMultiNIC._vm1_mgmt_ip = mgmt_ip

    def test_create_second_vm_and_verify_connectivity(
        self, rocky_image, ssh_pubkey, host_interface, api_vm_cleanup
    ):
        """Create second VM on same network and test inter-VM ping."""
        vm1_ip = getattr(TestAPIMultiNIC, "_vm1_mgmt_ip", None)
        if not vm1_ip:
            pytest.skip("First VM not created")

        spec = {
            "name": "e2e-api-multnic-2",
            "image": rocky_image,
            "cpus": 2,
            "ram_mb": 2048,
            "disk_gb": 20,
            "ssh_pub_key": ssh_pubkey,
            "networks": [
                {
                    "name": host_interface,
                    "mode": "macvtap",
                    "host_interface": host_interface,
                }
            ],
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201

        vm = resp.json()
        vm_id = vm["id"]
        api_vm_cleanup.append(vm_id)

        mgmt_ip = wait_for_vm_ip(vm_id, source="api")
        wait_for_ssh(mgmt_ip)

        TestAPIMultiNIC._vm2_id = vm_id
        TestAPIMultiNIC._vm2_mgmt_ip = mgmt_ip

        # Get extra IPs
        vm1_extra = self._get_extra_ip(vm1_ip)
        vm2_extra = self._get_extra_ip(mgmt_ip)

        assert vm1_extra, "VM1 extra interface has no IP"
        assert vm2_extra, "VM2 extra interface has no IP"

        # Cross-ping on extra network
        result = ssh_run(vm1_ip, f"ping -c 3 -W 5 {vm2_extra}")
        assert "0% packet loss" in result, f"VM1→VM2 ping failed: {result}"

        result = ssh_run(mgmt_ip, f"ping -c 3 -W 5 {vm1_extra}")
        assert "0% packet loss" in result, f"VM2→VM1 ping failed: {result}"

    def test_host_interfaces_endpoint(self):
        """GET /host/interfaces should return available host NICs."""
        resp = api_get("/host/interfaces")
        assert resp.status_code == 200
        ifaces = resp.json()
        assert isinstance(ifaces, list)
        assert len(ifaces) >= 1, "No host interfaces returned"
        # Each should have name and is_up
        for iface in ifaces:
            assert "name" in iface
            assert "is_up" in iface

    def test_cleanup(self):
        """Delete multi-NIC VMs."""
        for attr in ("_vm1_id", "_vm2_id"):
            vm_id = getattr(TestAPIMultiNIC, attr, None)
            if vm_id:
                delete_vm_api(vm_id)

    @staticmethod
    def _get_extra_ip(mgmt_ip: str) -> str:
        """Get the IP of the first non-NAT, non-lo interface via SSH."""
        output = ssh_run(mgmt_ip, "ip -4 -o addr show")
        for line in output.strip().splitlines():
            if "127.0.0.1" in line or "192.168.100." in line:
                continue
            match = re.search(r"inet\s+(\d+\.\d+\.\d+\.\d+)", line)
            if match:
                return match.group(1)
        return ""


# ======================================================================
# Test 4: Port forwarding via API
# ======================================================================

@pytest.mark.api
@pytest.mark.portforward
class TestAPIPortForward:
    """Test port forwarding through the REST API."""

    def test_port_forward_ssh(self, rocky_image, ssh_pubkey, api_vm_cleanup, api_port_cleanup):
        """Create VM, add port forward via API, SSH through forwarded port."""
        # Create VM
        spec = {
            "name": "e2e-api-portfwd",
            "image": rocky_image,
            "cpus": 2,
            "ram_mb": 2048,
            "disk_gb": 20,
            "ssh_pub_key": ssh_pubkey,
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201

        vm = resp.json()
        vm_id = vm["id"]
        api_vm_cleanup.append(vm_id)

        vm_ip = wait_for_vm_ip(vm_id, source="api")
        wait_for_ssh(vm_ip)

        # Find free port
        host_port = self._find_free_port()

        # Add port forward
        resp = api_post(f"/vms/{vm_id}/ports", json={
            "host_port": host_port,
            "guest_port": 22,
            "protocol": "tcp",
        })
        assert resp.status_code == 201, f"Port forward failed: {resp.text}"

        pf = resp.json()
        pf_id = pf["id"]
        api_port_cleanup.append((vm_id, pf_id))

        assert pf["host_port"] == host_port
        assert pf["guest_port"] == 22
        assert pf["protocol"] == "tcp"

        # List port forwards
        resp = api_get(f"/vms/{vm_id}/ports")
        assert resp.status_code == 200
        ports = resp.json()
        assert len(ports) >= 1
        assert any(p["id"] == pf_id for p in ports)

        # SSH via forwarded port
        wait_for_ssh("127.0.0.1", port=host_port)
        hostname = ssh_run("127.0.0.1", "hostname", port=host_port).strip()
        assert hostname, "SSH via port forward returned empty hostname"

    def test_port_forward_crud(self, rocky_image, ssh_pubkey, api_vm_cleanup):
        """Add, list, and remove port forwards via API."""
        spec = {
            "name": "e2e-api-pf-crud",
            "image": rocky_image,
            "cpus": 2,
            "ram_mb": 2048,
            "disk_gb": 20,
            "ssh_pub_key": ssh_pubkey,
        }
        resp = api_post("/vms", json=spec)
        assert resp.status_code == 201
        vm_id = resp.json()["id"]
        api_vm_cleanup.append(vm_id)

        wait_for_vm_ip(vm_id, source="api")

        port1 = self._find_free_port()
        port2 = self._find_free_port()

        # Add two port forwards
        r1 = api_post(f"/vms/{vm_id}/ports", json={
            "host_port": port1, "guest_port": 22, "protocol": "tcp",
        })
        r2 = api_post(f"/vms/{vm_id}/ports", json={
            "host_port": port2, "guest_port": 80, "protocol": "tcp",
        })
        assert r1.status_code == 201
        assert r2.status_code == 201

        pf1_id = r1.json()["id"]
        pf2_id = r2.json()["id"]

        # List — should have 2
        resp = api_get(f"/vms/{vm_id}/ports")
        assert len(resp.json()) == 2

        # Remove first
        resp = api_delete(f"/vms/{vm_id}/ports/{pf1_id}")
        assert resp.status_code == 204

        # List — should have 1
        resp = api_get(f"/vms/{vm_id}/ports")
        ports = resp.json()
        assert len(ports) == 1
        assert ports[0]["id"] == pf2_id

        # Remove second
        api_delete(f"/vms/{vm_id}/ports/{pf2_id}")

    @staticmethod
    def _find_free_port() -> int:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            s.bind(("", 0))
            return s.getsockname()[1]
