"""Shared pytest fixtures for VMSmith E2E tests.

Required environment variables:
    VMSMITH_ROCKY_IMAGE   Path to a Rocky Linux qcow2 image
    VMSMITH_SSH_PRIVATE_KEY  Path to SSH private key (default: ~/.ssh/id_rsa)

Optional:
    VMSMITH_BIN           Path to vmsmith binary (default: "vmsmith")
    VMSMITH_API           Daemon base URL (default: http://localhost:8080)
    VMSMITH_SSH_USER      SSH username for Rocky image (default: "rocky")
    VMSMITH_HOST_IFACE    Host interface for multi-NIC tests (e.g. "eth1")
    VMSMITH_HOST_IFACE2   Second host interface for multi-NIC tests (e.g. "eth2")
"""

import os
import subprocess
import tempfile
import textwrap

import pytest

from helpers import (
    ROCKY_IMAGE,
    SSH_PRIVATE_KEY,
    VMSMITH_API,
    VMSMITH_BIN,
    api_delete,
    api_post,
    delete_vm_api,
    delete_vm_cli,
    run_cli,
)


def pytest_collection_modifyitems(config, items):
    """Order tests so lifecycle runs before networking."""
    pass


# ---------------------------------------------------------------------------
# Precondition checks
# ---------------------------------------------------------------------------

def pytest_configure(config):
    """Validate that required env vars and prerequisites are present."""
    config.addinivalue_line("markers", "cli: CLI-based E2E tests")
    config.addinivalue_line("markers", "api: REST API-based E2E tests")
    config.addinivalue_line("markers", "gui: GUI/Playwright E2E tests")
    config.addinivalue_line("markers", "networking: multi-NIC networking tests")
    config.addinivalue_line("markers", "portforward: port forwarding tests")


@pytest.fixture(scope="session", autouse=True)
def check_prerequisites():
    """Verify the vmsmith binary and Rocky image exist."""
    # Check binary
    result = subprocess.run([VMSMITH_BIN, "--help"], capture_output=True, timeout=10)
    assert result.returncode == 0, f"vmsmith binary not found or broken at {VMSMITH_BIN}"

    # Check image
    assert ROCKY_IMAGE, (
        "VMSMITH_ROCKY_IMAGE env var must be set to a Rocky Linux qcow2 image path"
    )
    assert os.path.isfile(ROCKY_IMAGE), f"Rocky image not found: {ROCKY_IMAGE}"

    # Check SSH key
    assert os.path.isfile(SSH_PRIVATE_KEY), f"SSH key not found: {SSH_PRIVATE_KEY}"


@pytest.fixture(scope="session")
def rocky_image():
    """Return the path to the Rocky Linux qcow2 image."""
    return ROCKY_IMAGE


@pytest.fixture(scope="session")
def ssh_pubkey():
    """Return the SSH public key content."""
    pubkey_path = SSH_PRIVATE_KEY + ".pub"
    if os.path.isfile(pubkey_path):
        with open(pubkey_path) as f:
            return f.read().strip()
    # Try to derive it
    result = subprocess.run(
        ["ssh-keygen", "-y", "-f", SSH_PRIVATE_KEY],
        capture_output=True, text=True, timeout=10,
    )
    assert result.returncode == 0, f"Cannot read SSH public key: {result.stderr}"
    return result.stdout.strip()


@pytest.fixture(scope="session")
def host_interface():
    """Return the host interface name for multi-NIC tests (from VMSMITH_HOST_IFACE)."""
    iface = os.environ.get("VMSMITH_HOST_IFACE", "")
    if not iface:
        pytest.skip("VMSMITH_HOST_IFACE not set — skipping multi-NIC tests")
    return iface


@pytest.fixture(scope="session")
def host_interface2():
    """Return a second host interface for multi-NIC tests (from VMSMITH_HOST_IFACE2)."""
    iface = os.environ.get("VMSMITH_HOST_IFACE2", "")
    if not iface:
        pytest.skip("VMSMITH_HOST_IFACE2 not set — skipping dual-NIC tests")
    return iface


@pytest.fixture(scope="session")
def api_base_url():
    """Return the daemon API base URL."""
    return VMSMITH_API


# ---------------------------------------------------------------------------
# VM tracking for cleanup
# ---------------------------------------------------------------------------

@pytest.fixture
def cli_vm_cleanup():
    """Track VM IDs created during a test and delete them on teardown (CLI)."""
    created = []
    yield created
    for vm_id in reversed(created):
        delete_vm_cli(vm_id)


@pytest.fixture
def api_vm_cleanup():
    """Track VM IDs created during a test and delete them on teardown (API)."""
    created = []
    yield created
    for vm_id in reversed(created):
        delete_vm_api(vm_id)


# ---------------------------------------------------------------------------
# Port forward cleanup
# ---------------------------------------------------------------------------

@pytest.fixture
def cli_port_cleanup():
    """Track port-forward IDs created during a test and remove them on teardown."""
    created = []  # list of (vm_id, pf_id)
    yield created
    for pf_id in reversed(created):
        run_cli("port", "remove", pf_id, check=False)


@pytest.fixture
def api_port_cleanup():
    """Track port-forward IDs for API cleanup."""
    created = []  # list of (vm_id, pf_id)
    yield created
    for vm_id, pf_id in reversed(created):
        api_delete(f"/vms/{vm_id}/ports/{pf_id}")
