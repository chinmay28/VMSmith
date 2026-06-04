package api

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseMachineFilter parses the optional `?machine=<machine-type>` query
// parameter used by `GET /vms`. Unlike the closed-vocabulary
// firmware/disk_bus/nic_model filters this one is free-form (any value
// matching the pc-q35-style alphabet `[A-Za-z0-9._-]+`) so the contract
// mirrors `?timezone=` instead: case-sensitive (libvirt machine names like
// "pc-q35-6.2" / "q35" / "virt-7.2" are case-sensitive at the QEMU layer),
// whitespace-trimmed, empty disables. Garbage that fails the alphabet check
// returns 400 `invalid_machine` so a typo surfaces before the filter is
// silently no-op'd.
//
// Match semantics live with the handler, not here: an empty stored machine
// resolves to types.DefaultMachine via VMSpec.ResolvedMachine, so
// `?machine=pc-q35-6.2` matches both stored "pc-q35-6.2" AND VMs with no
// override. Mirrors the `?firmware=bios` empty-defaults-to-SeaBIOS contract.
func parseMachineFilter(raw string) (string, bool, *types.APIError) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false, nil
	}
	if !types.IsValidMachineType(v) {
		return "", false, types.NewAPIError(
			"invalid_machine",
			"machine must match the libvirt machine-type alphabet [A-Za-z0-9._-]+",
		)
	}
	return v, true, nil
}

// vmMatchesMachineFilter reports whether the VM matches the requested
// machine bucket. Resolution defers to VMSpec.ResolvedMachine so a VM whose
// stored machine is empty matches the daemon-wide default. The helper
// centralises the empty-means-default semantics so the handler loop stays
// a single-line predicate call.
func vmMatchesMachineFilter(spec types.VMSpec, filter string) bool {
	return spec.ResolvedMachine() == filter
}
