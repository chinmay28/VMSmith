package api

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseDiskBusFilter parses the optional `?disk_bus=<virtio|sata>` query
// parameter used by `GET /vms`. Mirrors the parseFirmwareFilter contract:
// empty disables; a recognised value (case-insensitive, whitespace-trimmed)
// returns the canonical lowercased form; anything else returns a 400 with
// the stable `invalid_disk_bus` code so the CLI / GUI can surface the typo.
//
// Semantics intentionally match the create-path validation in
// validateDeviceOverrides — the same two-value vocabulary, case-insensitive
// + trim-then-compare.
//
// Match semantics live with the handler, not here: an empty stored disk_bus
// resolves to the OS-family default (virtio for Linux, sata for Windows) via
// VMSpec.ResolvedDiskBus, so `?disk_bus=virtio` matches both stored "virtio"
// AND Linux VMs with no override; `?disk_bus=sata` matches stored "sata" AND
// Windows VMs with no override. This mirrors how `?firmware=bios` matches
// empty-stored VMs via the documented BIOS default.
func parseDiskBusFilter(raw string) (string, bool, *types.APIError) {
	normalised := strings.ToLower(strings.TrimSpace(raw))
	if normalised == "" {
		return "", false, nil
	}
	switch normalised {
	case types.DiskBusVirtio, types.DiskBusSATA:
		return normalised, true, nil
	default:
		return "", false, types.NewAPIError(
			"invalid_disk_bus",
			fmt.Sprintf("disk_bus must be one of: %s, %s",
				types.DiskBusVirtio, types.DiskBusSATA),
		)
	}
}

// vmMatchesDiskBusFilter reports whether the VM matches the requested
// disk_bus bucket. Resolution defers to VMSpec.ResolvedDiskBus so a VM
// whose stored disk_bus is empty matches its OS-family default — Linux VMs
// match `?disk_bus=virtio`, Windows VMs match `?disk_bus=sata`. The helper
// centralises the empty-means-default semantics so the handler loop stays
// a single-line predicate call.
func vmMatchesDiskBusFilter(spec types.VMSpec, filter string) bool {
	return spec.ResolvedDiskBus() == filter
}
