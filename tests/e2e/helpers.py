"""Shared helpers for E2E tests.

Configuration is resolved in order: pytest CLI option → environment variable → default.
Module-level variables below are set to env-var defaults at import time and then
overridden by conftest.py ``pytest_configure`` with any CLI option values.
"""

import os
import subprocess
import time

import paramiko
import requests


# ---------------------------------------------------------------------------
# Configuration — defaults from environment, overridden by conftest.py
# ---------------------------------------------------------------------------

VMSMITH_BIN: str = os.environ.get("VMSMITH_BIN", "vmsmith")
VMSMITH_API: str = os.environ.get("VMSMITH_API", "http://localhost:8080")
ROCKY_IMAGE: str = os.environ.get("VMSMITH_ROCKY_IMAGE", "")
SSH_PRIVATE_KEY: str = os.environ.get("VMSMITH_SSH_PRIVATE_KEY", os.path.expanduser("~/.ssh/id_rsa"))
SSH_USER: str = os.environ.get("VMSMITH_SSH_USER", "root")

# Timeouts (seconds)
VM_IP_TIMEOUT: int = int(os.environ.get("VMSMITH_IP_TIMEOUT", "120"))
VM_SSH_TIMEOUT: int = int(os.environ.get("VMSMITH_SSH_TIMEOUT", "180"))
POLL_INTERVAL: int = 5


# ---------------------------------------------------------------------------
# CLI helpers
# ---------------------------------------------------------------------------

def run_cli(*args: str, check: bool = True) -> subprocess.CompletedProcess:
    """Run the vmsmith CLI and return the CompletedProcess."""
    cmd = [VMSMITH_BIN] + list(args)
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
    if check and result.returncode != 0:
        raise RuntimeError(
            f"vmsmith {' '.join(args)} failed (rc={result.returncode}):\n"
            f"stdout: {result.stdout}\nstderr: {result.stderr}"
        )
    return result


def parse_create_output(stdout: str) -> dict:
    """Parse the output of `vmsmith vm create` into a dict with id, name, state, ip."""
    info = {}
    for line in stdout.strip().splitlines():
        line = line.strip()
        if line.startswith("ID:"):
            info["id"] = line.split(":", 1)[1].strip()
        elif line.startswith("Name:"):
            info["name"] = line.split(":", 1)[1].strip()
        elif line.startswith("State:"):
            info["state"] = line.split(":", 1)[1].strip()
        elif line.startswith("IP:"):
            info["ip"] = line.split(":", 1)[1].strip()
    return info


def parse_vm_info(stdout: str) -> dict:
    """Parse the output of `vmsmith vm info` into a dict."""
    info = {}
    for line in stdout.strip().splitlines():
        if ":" in line:
            key, _, val = line.partition(":")
            info[key.strip().lower().replace(" ", "_")] = val.strip()
    return info


def parse_table_output(stdout: str) -> list[dict]:
    """Parse tabwriter table output into a list of dicts.

    The first line is treated as headers.  Columns are split by 2+ spaces.
    """
    import re
    lines = [l for l in stdout.strip().splitlines() if l.strip()]
    if not lines:
        return []
    headers = re.split(r"\s{2,}", lines[0].strip())
    rows = []
    for line in lines[1:]:
        cols = re.split(r"\s{2,}", line.strip())
        row = {}
        for i, h in enumerate(headers):
            row[h] = cols[i] if i < len(cols) else ""
        rows.append(row)
    return rows


# ---------------------------------------------------------------------------
# API helpers
# ---------------------------------------------------------------------------

def api_url(path: str) -> str:
    """Build a full API URL."""
    return f"{VMSMITH_API}/api/v1{path}"


def api_get(path: str) -> requests.Response:
    return requests.get(api_url(path), timeout=30)


def api_post(path: str, json=None) -> requests.Response:
    return requests.post(api_url(path), json=json, timeout=60)


def api_delete(path: str) -> requests.Response:
    return requests.delete(api_url(path), timeout=30)


# ---------------------------------------------------------------------------
# Wait / polling helpers
# ---------------------------------------------------------------------------

def wait_for_vm_ip(vm_id: str, timeout: int = VM_IP_TIMEOUT, source: str = "api") -> str:
    """Poll until the VM has an IP address.  Returns the IP."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if source == "api":
            resp = api_get(f"/vms/{vm_id}")
            resp.raise_for_status()
            ip = resp.json().get("ip", "")
        else:
            result = run_cli("vm", "info", vm_id)
            info = parse_vm_info(result.stdout)
            ip = info.get("ip", "")
        if ip:
            return ip
        time.sleep(POLL_INTERVAL)
    raise TimeoutError(f"VM {vm_id} did not get an IP within {timeout}s")


def wait_for_vm_state(vm_id: str, desired: str, timeout: int = 60, source: str = "api") -> None:
    """Poll until the VM reaches the desired state."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if source == "api":
            resp = api_get(f"/vms/{vm_id}")
            resp.raise_for_status()
            state = resp.json().get("state", "")
        else:
            result = run_cli("vm", "info", vm_id)
            info = parse_vm_info(result.stdout)
            state = info.get("state", "")
        if state == desired:
            return
        time.sleep(POLL_INTERVAL)
    raise TimeoutError(f"VM {vm_id} did not reach state '{desired}' within {timeout}s")


def wait_for_ssh(ip: str, port: int = 22, timeout: int = VM_SSH_TIMEOUT, user: str = None) -> None:
    """Wait until SSH is reachable on the given IP/port."""
    if user is None:
        user = SSH_USER
    deadline = time.time() + timeout
    last_err = None
    while time.time() < deadline:
        try:
            client = paramiko.SSHClient()
            client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
            client.connect(
                ip, port=port, username=user,
                key_filename=SSH_PRIVATE_KEY,
                timeout=10, auth_timeout=10,
                banner_timeout=10,
            )
            client.close()
            return
        except Exception as e:
            last_err = e
            time.sleep(POLL_INTERVAL)
    raise TimeoutError(f"SSH not reachable on {ip}:{port} within {timeout}s: {last_err}")


def ssh_run(ip: str, command: str, port: int = 22, user: str = None) -> str:
    """Run a command over SSH and return stdout."""
    if user is None:
        user = SSH_USER
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(
        ip, port=port, username=user,
        key_filename=SSH_PRIVATE_KEY,
        timeout=10, auth_timeout=10,
    )
    try:
        _, stdout, stderr = client.exec_command(command, timeout=30)
        exit_code = stdout.channel.recv_exit_status()
        out = stdout.read().decode()
        err = stderr.read().decode()
        if exit_code != 0:
            raise RuntimeError(f"SSH command failed (rc={exit_code}): {err}")
        return out
    finally:
        client.close()


def ping_host(ip: str, count: int = 3, timeout: int = 10) -> bool:
    """Ping an IP and return True if reachable."""
    result = subprocess.run(
        ["ping", "-c", str(count), "-W", str(timeout), ip],
        capture_output=True, timeout=timeout + 5,
    )
    return result.returncode == 0


# ---------------------------------------------------------------------------
# Cleanup helpers
# ---------------------------------------------------------------------------

def delete_vm_cli(vm_id: str) -> None:
    """Delete a VM via CLI, ignoring errors."""
    run_cli("vm", "stop", vm_id, check=False)
    run_cli("vm", "delete", vm_id, check=False)


def delete_vm_api(vm_id: str) -> None:
    """Delete a VM via API, ignoring errors."""
    api_post(f"/vms/{vm_id}/stop")
    api_delete(f"/vms/{vm_id}")
