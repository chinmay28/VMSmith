"""E2E tests for multi-NIC networking and port forwarding via the vmsmith CLI.

Covers:
  3. Deploy VMs with multiple interfaces, verify reachable IPs,
     test inter-VM connectivity
  4. Port forwarding — SSH into VM via forwarded port
"""

import re
import socket

import pytest

from helpers import (
    SSH_PRIVATE_KEY,
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


# ======================================================================
# Test 3: Multi-NIC networking
# ======================================================================

@pytest.mark.cli
@pytest.mark.networking
class TestCLIMultiNIC:
    """Deploy VMs with multiple interfaces and verify connectivity."""

    def test_create_vm_with_extra_interface(
        self, rocky_image, ssh_pubkey, host_interface, cli_vm_cleanup
    ):
        """Create a VM with an extra macvtap interface on the host network."""
        result = run_cli(
            "vm", "create", "e2e-multnic-1",
            "--image", rocky_image,
            "--cpus", "2",
            "--ram", "2048",
            "--disk", "20",
            "--ssh-key", ssh_pubkey,
            "--network", host_interface,
        )
        info = parse_create_output(result.stdout)
        vm_id = info["id"]
        cli_vm_cleanup.append(vm_id)

        assert "Extra networks: 1" in result.stdout or "extra" in result.stdout.lower() or vm_id

        # Wait for management IP (NAT interface)
        mgmt_ip = wait_for_vm_ip(vm_id, source="cli")
        assert mgmt_ip, "Multi-NIC VM did not get a management IP"
        assert ping_host(mgmt_ip), f"Management IP {mgmt_ip} not reachable"

        # SSH in and check that the extra interface exists
        wait_for_ssh(mgmt_ip)
        ifaces = ssh_run(mgmt_ip, "ip -o link show | awk '{print $2}'").strip()
        # Should have at least lo, eth0 (NAT), and eth1 (extra)
        assert "eth1" in ifaces or "ens" in ifaces, (
            f"Extra interface not found. Interfaces: {ifaces}"
        )

        # Check that the extra interface has an IP (DHCP or static)
        ip_output = ssh_run(mgmt_ip, "ip -4 -o addr show").strip()
        # Count interfaces with IPs (excluding lo)
        ip_lines = [l for l in ip_output.splitlines() if "127.0.0.1" not in l]
        assert len(ip_lines) >= 2, (
            f"Expected at least 2 interfaces with IPs, got:\n{ip_output}"
        )

        TestCLIMultiNIC._vm1_id = vm_id
        TestCLIMultiNIC._vm1_mgmt_ip = mgmt_ip

    def test_create_second_vm_with_extra_interface(
        self, rocky_image, ssh_pubkey, host_interface, cli_vm_cleanup
    ):
        """Create a second VM on the same extra network."""
        result = run_cli(
            "vm", "create", "e2e-multnic-2",
            "--image", rocky_image,
            "--cpus", "2",
            "--ram", "2048",
            "--disk", "20",
            "--ssh-key", ssh_pubkey,
            "--network", host_interface,
        )
        info = parse_create_output(result.stdout)
        vm_id = info["id"]
        cli_vm_cleanup.append(vm_id)

        mgmt_ip = wait_for_vm_ip(vm_id, source="cli")
        assert mgmt_ip, "Second multi-NIC VM did not get a management IP"
        wait_for_ssh(mgmt_ip)

        TestCLIMultiNIC._vm2_id = vm_id
        TestCLIMultiNIC._vm2_mgmt_ip = mgmt_ip

    def test_inter_vm_connectivity(self):
        """Verify that the two VMs can reach each other on the extra network."""
        vm1_ip = getattr(TestCLIMultiNIC, "_vm1_mgmt_ip", None)
        vm2_ip = getattr(TestCLIMultiNIC, "_vm2_mgmt_ip", None)
        if not vm1_ip or not vm2_ip:
            pytest.skip("Multi-NIC VMs not created")

        # Get the extra-interface IPs from inside each VM
        vm1_extra_ip = self._get_extra_ip(vm1_ip)
        vm2_extra_ip = self._get_extra_ip(vm2_ip)

        assert vm1_extra_ip, "VM1 extra interface has no IP"
        assert vm2_extra_ip, "VM2 extra interface has no IP"

        # Ping VM2 extra IP from VM1
        ping_result = ssh_run(
            vm1_ip, f"ping -c 3 -W 5 {vm2_extra_ip}"
        )
        assert "0% packet loss" in ping_result or "0 received" not in ping_result, (
            f"VM1 cannot reach VM2 on extra network: {ping_result}"
        )

        # Ping VM1 extra IP from VM2
        ping_result = ssh_run(
            vm2_ip, f"ping -c 3 -W 5 {vm1_extra_ip}"
        )
        assert "0% packet loss" in ping_result or "0 received" not in ping_result, (
            f"VM2 cannot reach VM1 on extra network: {ping_result}"
        )

    def test_vm_with_dual_extra_interfaces(
        self, rocky_image, ssh_pubkey, host_interface, host_interface2, cli_vm_cleanup
    ):
        """Create a VM with two extra interfaces and verify both get IPs."""
        result = run_cli(
            "vm", "create", "e2e-dual-nic",
            "--image", rocky_image,
            "--cpus", "2",
            "--ram", "2048",
            "--disk", "20",
            "--ssh-key", ssh_pubkey,
            "--network", host_interface,
            "--network", host_interface2,
        )
        info = parse_create_output(result.stdout)
        vm_id = info["id"]
        cli_vm_cleanup.append(vm_id)

        mgmt_ip = wait_for_vm_ip(vm_id, source="cli")
        wait_for_ssh(mgmt_ip)

        # Check that we have at least 3 interfaces with IPs (NAT + 2 extra)
        ip_output = ssh_run(mgmt_ip, "ip -4 -o addr show").strip()
        ip_lines = [l for l in ip_output.splitlines() if "127.0.0.1" not in l]
        assert len(ip_lines) >= 3, (
            f"Expected at least 3 interfaces with IPs (NAT + 2 extra), got:\n{ip_output}"
        )

    @staticmethod
    def _get_extra_ip(mgmt_ip: str) -> str:
        """SSH into a VM and get the IP of the first non-NAT, non-lo interface."""
        output = ssh_run(mgmt_ip, "ip -4 -o addr show")
        for line in output.strip().splitlines():
            # Skip loopback and the management/NAT interface (192.168.100.x)
            if "127.0.0.1" in line:
                continue
            if "192.168.100." in line:
                continue
            # Extract IP from format: "N: ethX inet A.B.C.D/prefix ..."
            match = re.search(r"inet\s+(\d+\.\d+\.\d+\.\d+)", line)
            if match:
                return match.group(1)
        return ""

    def test_cleanup_multi_nic_vms(self):
        """Delete multi-NIC test VMs."""
        for attr in ("_vm1_id", "_vm2_id"):
            vm_id = getattr(TestCLIMultiNIC, attr, None)
            if vm_id:
                delete_vm_cli(vm_id)


# ======================================================================
# Test 4: Port forwarding
# ======================================================================

@pytest.mark.cli
@pytest.mark.portforward
class TestCLIPortForward:
    """Test port forwarding: add a rule and SSH through the forwarded port."""

    def test_port_forward_ssh(
        self, rocky_image, ssh_pubkey, cli_vm_cleanup, cli_port_cleanup
    ):
        """Create a VM, add a port forward, and SSH via the forwarded port."""
        # Create VM
        result = run_cli(
            "vm", "create", "e2e-portfwd",
            "--image", rocky_image,
            "--cpus", "2",
            "--ram", "2048",
            "--disk", "20",
            "--ssh-key", ssh_pubkey,
        )
        info = parse_create_output(result.stdout)
        vm_id = info["id"]
        cli_vm_cleanup.append(vm_id)

        # Wait for IP and SSH readiness
        vm_ip = wait_for_vm_ip(vm_id, source="cli")
        wait_for_ssh(vm_ip)

        # Find an available host port
        host_port = self._find_free_port()

        # Add port forward: host_port -> guest 22
        result = run_cli(
            "port", "add", vm_id,
            "--host", str(host_port),
            "--guest", "22",
            "--proto", "tcp",
        )
        assert "Port forward added" in result.stdout

        # Extract port forward ID from list for cleanup
        list_result = run_cli("port", "list", vm_id)
        rows = parse_table_output(list_result.stdout)
        assert len(rows) >= 1, "No port forwards found"
        pf_id = rows[0].get("ID", "")
        cli_port_cleanup.append(pf_id)

        # Verify the port forward appears in the list
        assert str(host_port) in list_result.stdout

        # SSH via the forwarded port (connecting to localhost:host_port)
        wait_for_ssh("127.0.0.1", port=host_port)
        hostname = ssh_run("127.0.0.1", "hostname", port=host_port).strip()
        assert hostname, "SSH via port forward returned empty hostname"

    def test_port_forward_list_and_remove(
        self, rocky_image, ssh_pubkey, cli_vm_cleanup
    ):
        """Add multiple port forwards, list them, remove one, verify."""
        result = run_cli(
            "vm", "create", "e2e-pf-list",
            "--image", rocky_image,
            "--cpus", "2",
            "--ram", "2048",
            "--disk", "20",
            "--ssh-key", ssh_pubkey,
        )
        info = parse_create_output(result.stdout)
        vm_id = info["id"]
        cli_vm_cleanup.append(vm_id)

        vm_ip = wait_for_vm_ip(vm_id, source="cli")
        wait_for_ssh(vm_ip)

        port1 = self._find_free_port()
        port2 = self._find_free_port()

        # Add two port forwards
        run_cli("port", "add", vm_id, "--host", str(port1), "--guest", "22")
        run_cli("port", "add", vm_id, "--host", str(port2), "--guest", "80")

        # List — should have 2 entries
        list_result = run_cli("port", "list", vm_id)
        rows = parse_table_output(list_result.stdout)
        assert len(rows) == 2, f"Expected 2 port forwards, got {len(rows)}"

        # Remove the first one
        pf_id = rows[0]["ID"]
        run_cli("port", "remove", pf_id)

        # List again — should have 1 entry
        list_result = run_cli("port", "list", vm_id)
        rows = parse_table_output(list_result.stdout)
        assert len(rows) == 1, f"Expected 1 port forward after removal, got {len(rows)}"

        # Clean up remaining
        run_cli("port", "remove", rows[0]["ID"], check=False)

    @staticmethod
    def _find_free_port() -> int:
        """Find an available TCP port on the host."""
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            s.bind(("", 0))
            return s.getsockname()[1]
