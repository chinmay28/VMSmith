package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// cliVMMatchesDiskBusFilter mirrors the API's vmMatchesDiskBusFilter
// helper for the CLI: resolution defers to VMSpec.ResolvedDiskBus so a VM
// whose stored disk_bus is empty matches its OS-family default (virtio for
// Linux, sata for Windows).
func cliVMMatchesDiskBusFilter(spec types.VMSpec, filter string) bool {
	return spec.ResolvedDiskBus() == filter
}

// parseCLIDiskBus mirrors the API's parseDiskBusFilter contract for the
// `--disk-bus` flag on `vmsmith vm list`. Empty disables; a recognised value
// (case-insensitive, whitespace-trimmed) returns the canonical lowercased
// form; anything else returns an error so the operator sees the typo before
// the daemon is contacted.
func parseCLIDiskBus(raw, flag string) (string, bool, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false, nil
	}
	switch v {
	case types.DiskBusVirtio, types.DiskBusSATA:
		return v, true, nil
	default:
		return "", false, fmt.Errorf(
			"invalid %s %q: must be one of %s, %s",
			flag, raw, types.DiskBusVirtio, types.DiskBusSATA,
		)
	}
}
