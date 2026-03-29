#!/usr/bin/env bash
set -euo pipefail

API_BASE="${VMSMITH_API:-http://localhost:8080/api/v1}"
VM_NAME="${VMSMITH_VM_NAME:-example-vm}"
IMAGE="${VMSMITH_IMAGE:-ubuntu-22.04}"
CPUS="${VMSMITH_CPUS:-2}"
RAM_MB="${VMSMITH_RAM_MB:-2048}"
DISK_GB="${VMSMITH_DISK_GB:-20}"
HOST_PORT="${VMSMITH_HOST_PORT:-2222}"
GUEST_PORT="${VMSMITH_GUEST_PORT:-22}"
PROTOCOL="${VMSMITH_PROTOCOL:-tcp}"
SSH_PUB_KEY="${VMSMITH_SSH_PUB_KEY:-}"

export VM_NAME IMAGE CPUS RAM_MB DISK_GB HOST_PORT GUEST_PORT PROTOCOL SSH_PUB_KEY

if [[ -z "$SSH_PUB_KEY" ]]; then
  echo "error: set VMSMITH_SSH_PUB_KEY to a public key string" >&2
  exit 1
fi

create_payload=$(python3 - <<'PY'
import json
import os

payload = {
    "name": os.environ["VM_NAME"],
    "image": os.environ["IMAGE"],
    "cpus": int(os.environ["CPUS"]),
    "ram_mb": int(os.environ["RAM_MB"]),
    "disk_gb": int(os.environ["DISK_GB"]),
    "ssh_pub_key": os.environ["SSH_PUB_KEY"],
}
print(json.dumps(payload))
PY
)

create_response=$(curl -fsS \
  -H 'Content-Type: application/json' \
  -d "$create_payload" \
  "$API_BASE/vms")

vm_id=$(printf '%s' "$create_response" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
vm_ip=$(printf '%s' "$create_response" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("ip", ""))')

echo "Created VM: $vm_id"
if [[ -n "$vm_ip" ]]; then
  echo "Assigned IP: $vm_ip"
fi

port_payload=$(python3 - <<'PY'
import json
import os

payload = {
    "host_port": int(os.environ["HOST_PORT"]),
    "guest_port": int(os.environ["GUEST_PORT"]),
    "protocol": os.environ["PROTOCOL"],
}
print(json.dumps(payload))
PY
)

curl -fsS \
  -H 'Content-Type: application/json' \
  -d "$port_payload" \
  "$API_BASE/vms/$vm_id/ports" >/dev/null

echo "Added port forward: host $HOST_PORT -> guest $GUEST_PORT/$PROTOCOL"
echo "Suggested SSH command: ssh -p $HOST_PORT root@<vmsmith-host>"
