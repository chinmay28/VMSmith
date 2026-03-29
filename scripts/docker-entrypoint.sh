#!/usr/bin/env bash
set -euo pipefail

mkdir -p /var/run/libvirt /var/lib/vmsmith/vms /var/lib/vmsmith/images /var/log/libvirt

if [[ "${1:-}" == "daemon" ]]; then
  if [[ ! -S /var/run/libvirt/libvirt-sock ]]; then
    mkdir -p /var/run/libvirt
    libvirtd --daemon
    for _ in $(seq 1 30); do
      if [[ -S /var/run/libvirt/libvirt-sock ]]; then
        break
      fi
      sleep 1
    done
  fi
fi

exec /usr/local/bin/vmsmith "$@"
