#!/usr/bin/env python3
"""Create a VM via the VMSmith REST API and wait for it to appear with an IP."""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


def request_json(method: str, url: str, payload: dict | None = None) -> dict:
    data = None
    headers = {}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"

    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=15) as resp:
        body = resp.read().decode("utf-8")
        return json.loads(body) if body else {}


def wait_for_ip(api_base: str, vm_id: str, timeout: int, interval: float) -> dict:
    deadline = time.time() + timeout
    last_vm = {}
    while time.time() < deadline:
        last_vm = request_json("GET", f"{api_base}/vms/{urllib.parse.quote(vm_id)}")
        if last_vm.get("ip"):
            return last_vm
        time.sleep(interval)
    return last_vm


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api", default="http://localhost:8080/api/v1", help="Base API URL")
    parser.add_argument("--name", required=True, help="VM name")
    parser.add_argument("--image", required=True, help="Image name or path")
    parser.add_argument("--cpus", type=int, default=2, help="vCPU count")
    parser.add_argument("--ram-mb", type=int, default=2048, help="RAM in MiB")
    parser.add_argument("--disk-gb", type=int, default=20, help="Disk size in GiB")
    parser.add_argument("--ssh-pub-key", default="", help="SSH public key to inject")
    parser.add_argument("--default-user", default="", help="Optional named default user")
    parser.add_argument("--wait-timeout", type=int, default=60, help="Seconds to wait for an IP")
    parser.add_argument("--wait-interval", type=float, default=2.0, help="Polling interval in seconds")
    args = parser.parse_args()

    payload = {
        "name": args.name,
        "image": args.image,
        "cpus": args.cpus,
        "ram_mb": args.ram_mb,
        "disk_gb": args.disk_gb,
        "ssh_pub_key": args.ssh_pub_key,
    }
    if args.default_user:
        payload["default_user"] = args.default_user

    try:
        created = request_json("POST", f"{args.api}/vms", payload)
        vm_id = created["id"]
        print(f"Created VM: {vm_id}")

        vm = created if created.get("ip") else wait_for_ip(args.api, vm_id, args.wait_timeout, args.wait_interval)
        print(json.dumps(vm, indent=2, sort_keys=True))
        if not vm.get("ip"):
            print("warning: VM was created but no IP was observed before timeout", file=sys.stderr)
            return 2
        return 0
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        print(f"HTTP error {exc.code}: {body}", file=sys.stderr)
        return 1
    except urllib.error.URLError as exc:
        print(f"Request failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
