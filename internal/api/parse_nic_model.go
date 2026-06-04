package api

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseNICModelFilter parses the optional `?nic_model=<virtio|e1000e>` query
// parameter used by `GET /vms`. Mirrors the parseDiskBusFilter contract:
// empty disables; a recognised value (case-insensitive, whitespace-trimmed)
// returns the canonical lowercased form; anything else returns a 400 with
// the stable `invalid_nic_model` code so the CLI / GUI can surface the typo.
//
// Semantics intentionally match the create-path validation in
// validateDeviceOverrides — the same two-value vocabulary, case-insensitive
// + trim-then-compare.
//
// Match semantics live with the handler, not here: an empty stored nic_model
// resolves to the OS-family default (virtio for Linux, e1000e for Windows)
// via VMSpec.ResolvedNICModel, so `?nic_model=virtio` matches both stored
// "virtio" AND Linux VMs with no override; `?nic_model=e1000e` matches stored
// "e1000e" AND Windows VMs with no override. This mirrors how `?disk_bus=virtio`
// matches empty-stored Linux VMs via the documented virtio default (5.4.69).
func parseNICModelFilter(raw string) (string, bool, *types.APIError) {
	normalised := strings.ToLower(strings.TrimSpace(raw))
	if normalised == "" {
		return "", false, nil
	}
	switch normalised {
	case types.NICModelVirtio, types.NICModelE1000e:
		return normalised, true, nil
	default:
		return "", false, types.NewAPIError(
			"invalid_nic_model",
			fmt.Sprintf("nic_model must be one of: %s, %s",
				types.NICModelVirtio, types.NICModelE1000e),
		)
	}
}

// vmMatchesNICModelFilter reports whether the VM matches the requested
// nic_model bucket. Resolution defers to VMSpec.ResolvedNICModel so a VM
// whose stored nic_model is empty matches its OS-family default — Linux VMs
// match `?nic_model=virtio`, Windows VMs match `?nic_model=e1000e`. The helper
// centralises the empty-means-default semantics so the handler loop stays
// a single-line predicate call.
func vmMatchesNICModelFilter(spec types.VMSpec, filter string) bool {
	return spec.ResolvedNICModel() == filter
}
