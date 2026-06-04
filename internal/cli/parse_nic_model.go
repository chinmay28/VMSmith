package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// cliVMMatchesNICModelFilter mirrors the API's vmMatchesNICModelFilter helper
// for the CLI. Resolution defers to VMSpec.ResolvedNICModel so a VM whose
// stored nic_model is empty matches its OS-family default — Linux VMs match
// `virtio`, Windows VMs match `e1000e`.
func cliVMMatchesNICModelFilter(spec types.VMSpec, filter string) bool {
	return spec.ResolvedNICModel() == filter
}

// parseCLINICModel mirrors the API's parseNICModelFilter contract for the
// `--nic-model` flag on `vmsmith vm list`. Empty disables; a recognised
// value (case-insensitive, whitespace-trimmed) returns the canonical
// lowercased form; anything else returns an error so the operator sees the
// typo before the daemon is contacted.
func parseCLINICModel(raw, flag string) (string, bool, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false, nil
	}
	switch v {
	case types.NICModelVirtio, types.NICModelE1000e:
		return v, true, nil
	default:
		return "", false, fmt.Errorf(
			"invalid %s %q: must be one of %s, %s",
			flag, raw, types.NICModelVirtio, types.NICModelE1000e,
		)
	}
}
