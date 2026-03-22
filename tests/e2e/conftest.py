"""Shared pytest fixtures for VMSmith E2E tests.

Configuration is resolved in order: pytest CLI option → environment variable → default.

CLI options (run ``pytest --help`` to see all):
    --rocky-image PATH       Rocky Linux qcow2 image path
    --ssh-key PATH           SSH private key
    --ssh-user USER          SSH username (default: root)
    --vmsmith-bin PATH       vmsmith binary path (default: vmsmith)
    --vmsmith-api URL        Daemon API base URL (default: http://localhost:8080)
    --host-iface NAME        Host interface for multi-NIC tests
    --host-iface2 NAME       Second host interface for dual-NIC tests
    --ip-timeout SECS        Timeout waiting for VM IP (default: 120)
    --ssh-timeout SECS       Timeout waiting for SSH (default: 180)

Equivalent env vars: VMSMITH_ROCKY_IMAGE, VMSMITH_SSH_PRIVATE_KEY, VMSMITH_SSH_USER,
VMSMITH_BIN, VMSMITH_API, VMSMITH_HOST_IFACE, VMSMITH_HOST_IFACE2,
VMSMITH_IP_TIMEOUT, VMSMITH_SSH_TIMEOUT.
"""

import os
import subprocess

import pytest

import helpers
from helpers import (
    api_delete,
    api_post,
    delete_vm_api,
    delete_vm_cli,
    run_cli,
)


# ---------------------------------------------------------------------------
# pytest CLI options
# ---------------------------------------------------------------------------

def pytest_addoption(parser):
    """Register custom CLI options for E2E test configuration."""
    g = parser.getgroup("vmsmith", "VMSmith E2E test options")
    g.addoption(
        "--rocky-image",
        default=os.environ.get("VMSMITH_ROCKY_IMAGE", ""),
        help="Path to Rocky Linux qcow2 image (env: VMSMITH_ROCKY_IMAGE)",
    )
    g.addoption(
        "--ssh-key",
        default=os.environ.get(
            "VMSMITH_SSH_PRIVATE_KEY", os.path.expanduser("~/.ssh/id_rsa")
        ),
        help="Path to SSH private key (env: VMSMITH_SSH_PRIVATE_KEY, default: ~/.ssh/id_rsa)",
    )
    g.addoption(
        "--ssh-user",
        default=os.environ.get("VMSMITH_SSH_USER", "root"),
        help="SSH username for VM access (env: VMSMITH_SSH_USER, default: root)",
    )
    g.addoption(
        "--vmsmith-bin",
        default=os.environ.get("VMSMITH_BIN", "vmsmith"),
        help="Path to vmsmith binary (env: VMSMITH_BIN, default: vmsmith)",
    )
    g.addoption(
        "--vmsmith-api",
        default=os.environ.get("VMSMITH_API", "http://localhost:8080"),
        help="Daemon API base URL (env: VMSMITH_API, default: http://localhost:8080)",
    )
    g.addoption(
        "--host-iface",
        default=os.environ.get("VMSMITH_HOST_IFACE", ""),
        help="Host interface for multi-NIC tests (env: VMSMITH_HOST_IFACE)",
    )
    g.addoption(
        "--host-iface2",
        default=os.environ.get("VMSMITH_HOST_IFACE2", ""),
        help="Second host interface for dual-NIC tests (env: VMSMITH_HOST_IFACE2)",
    )
    g.addoption(
        "--ip-timeout",
        default=int(os.environ.get("VMSMITH_IP_TIMEOUT", "120")),
        type=int,
        help="Seconds to wait for VM IP assignment (env: VMSMITH_IP_TIMEOUT, default: 120)",
    )
    g.addoption(
        "--ssh-timeout",
        default=int(os.environ.get("VMSMITH_SSH_TIMEOUT", "180")),
        type=int,
        help="Seconds to wait for SSH readiness (env: VMSMITH_SSH_TIMEOUT, default: 180)",
    )


# ---------------------------------------------------------------------------
# Apply CLI options to the helpers module
# ---------------------------------------------------------------------------

def pytest_configure(config):
    """Register markers and propagate CLI options into helpers module globals."""
    config.addinivalue_line("markers", "cli: CLI-based E2E tests")
    config.addinivalue_line("markers", "api: REST API-based E2E tests")
    config.addinivalue_line("markers", "gui: GUI/Playwright E2E tests")
    config.addinivalue_line("markers", "networking: multi-NIC networking tests")
    config.addinivalue_line("markers", "portforward: port forwarding tests")

    # Propagate CLI options into helpers module-level config.
    # getoption() returns None when pytest_addoption hasn't run yet (e.g. during
    # collection from other conftest files), so guard with hasattr/try.
    try:
        helpers.VMSMITH_BIN = config.getoption("--vmsmith-bin") or helpers.VMSMITH_BIN
        helpers.VMSMITH_API = config.getoption("--vmsmith-api") or helpers.VMSMITH_API
        helpers.ROCKY_IMAGE = config.getoption("--rocky-image") or helpers.ROCKY_IMAGE
        helpers.SSH_PRIVATE_KEY = config.getoption("--ssh-key") or helpers.SSH_PRIVATE_KEY
        helpers.SSH_USER = config.getoption("--ssh-user") or helpers.SSH_USER
        helpers.VM_IP_TIMEOUT = config.getoption("--ip-timeout") or helpers.VM_IP_TIMEOUT
        helpers.VM_SSH_TIMEOUT = config.getoption("--ssh-timeout") or helpers.VM_SSH_TIMEOUT
    except (ValueError, AttributeError):
        pass


def pytest_collection_modifyitems(config, items):
    """Order tests so lifecycle runs before networking."""
    pass


# ---------------------------------------------------------------------------
# Precondition checks
# ---------------------------------------------------------------------------

@pytest.fixture(scope="session", autouse=True)
def check_prerequisites():
    """Verify the vmsmith binary and Rocky image exist."""
    # Check binary
    result = subprocess.run(
        [helpers.VMSMITH_BIN, "--help"], capture_output=True, timeout=10
    )
    assert result.returncode == 0, (
        f"vmsmith binary not found or broken at {helpers.VMSMITH_BIN}"
    )

    # Check image
    assert helpers.ROCKY_IMAGE, (
        "Rocky image path required. Set --rocky-image or VMSMITH_ROCKY_IMAGE env var."
    )
    assert os.path.isfile(helpers.ROCKY_IMAGE), (
        f"Rocky image not found: {helpers.ROCKY_IMAGE}"
    )

    # Check SSH key
    assert os.path.isfile(helpers.SSH_PRIVATE_KEY), (
        f"SSH key not found: {helpers.SSH_PRIVATE_KEY}. "
        "Set --ssh-key or VMSMITH_SSH_PRIVATE_KEY env var."
    )


@pytest.fixture(scope="session")
def rocky_image():
    """Return the path to the Rocky Linux qcow2 image."""
    return helpers.ROCKY_IMAGE


@pytest.fixture(scope="session")
def ssh_pubkey():
    """Return the SSH public key content."""
    pubkey_path = helpers.SSH_PRIVATE_KEY + ".pub"
    if os.path.isfile(pubkey_path):
        with open(pubkey_path) as f:
            return f.read().strip()
    # Try to derive it
    result = subprocess.run(
        ["ssh-keygen", "-y", "-f", helpers.SSH_PRIVATE_KEY],
        capture_output=True, text=True, timeout=10,
    )
    assert result.returncode == 0, f"Cannot read SSH public key: {result.stderr}"
    return result.stdout.strip()


@pytest.fixture(scope="session")
def host_interface(request):
    """Return the host interface name for multi-NIC tests."""
    iface = request.config.getoption("--host-iface")
    if not iface:
        pytest.skip("--host-iface not set — skipping multi-NIC tests")
    return iface


@pytest.fixture(scope="session")
def host_interface2(request):
    """Return a second host interface for multi-NIC tests."""
    iface = request.config.getoption("--host-iface2")
    if not iface:
        pytest.skip("--host-iface2 not set — skipping dual-NIC tests")
    return iface


@pytest.fixture(scope="session")
def api_base_url():
    """Return the daemon API base URL."""
    return helpers.VMSMITH_API


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
    created = []  # list of pf_id strings
    yield created
    for pf_id in reversed(created):
        run_cli("port", "remove", pf_id, check=False)


@pytest.fixture
def api_port_cleanup():
    """Track port-forward IDs for API cleanup."""
    created = []  # list of (vm_id, pf_id) tuples
    yield created
    for vm_id, pf_id in reversed(created):
        api_delete(f"/vms/{vm_id}/ports/{pf_id}")
