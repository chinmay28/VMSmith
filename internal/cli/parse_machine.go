package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// cliVMMatchesMachineFilter mirrors the API's vmMatchesMachineFilter for
// the CLI: resolution defers to VMSpec.ResolvedMachine so a VM whose stored
// machine is empty matches the daemon-wide default (types.DefaultMachine).
func cliVMMatchesMachineFilter(spec types.VMSpec, filter string) bool {
	return spec.ResolvedMachine() == filter
}

// parseCLIMachine mirrors the API's parseMachineFilter contract for the
// `--machine` flag on `vmsmith vm list`. Empty disables; whitespace-trimmed
// (case preserved — libvirt machine names are case-sensitive); garbage
// failing the alphabet check returns an error so the operator sees the typo
// before the daemon is contacted.
func parseCLIMachine(raw, flag string) (string, bool, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false, nil
	}
	if !types.IsValidMachineType(v) {
		return "", false, fmt.Errorf(
			"invalid %s %q: must match the libvirt machine-type alphabet [A-Za-z0-9._-]+",
			flag, raw,
		)
	}
	return v, true, nil
}
