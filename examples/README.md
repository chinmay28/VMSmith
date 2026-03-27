# Automation Examples

These examples show small, reusable VMSmith automation flows without requiring any third-party Python packages.

## Files

- `create-and-forward.sh` — create a VM through the REST API, then add an SSH port forward
- `create_vm.py` — create a VM through the REST API and poll until the VM appears with an IP

## Assumptions

- The VMSmith daemon is reachable at `http://localhost:8080` unless overridden
- `curl` is available for the shell example
- Python 3.9+ is available for the Python example
- You already uploaded or imported a qcow2 image and know its image name

## Quick start

### Bash example

```bash
chmod +x examples/create-and-forward.sh
VMSMITH_IMAGE=ubuntu-22.04 \
VMSMITH_VM_NAME=web01 \
VMSMITH_HOST_PORT=2222 \
./examples/create-and-forward.sh
```

### Python example

```bash
python3 examples/create_vm.py \
  --name web01 \
  --image ubuntu-22.04 \
  --ssh-pub-key "$(cat ~/.ssh/id_ed25519.pub)"
```

## Notes

- Both examples print the VM ID returned by the API so you can script follow-up actions.
- The bash example keeps the workflow simple and depends only on `curl` and Python's built-in JSON parser.
- The Python example uses only the standard library (`argparse`, `json`, `urllib`).
- These are intended as starting points; adapt them to your own naming, networking, and image conventions.
