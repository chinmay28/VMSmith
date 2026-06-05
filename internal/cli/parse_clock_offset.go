package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// cliVMMatchesClockOffsetFilter mirrors the API's vmMatchesClockOffsetFilter
// helper for the CLI. Resolution defers to VMSpec.ResolvedClockOffset so a VM
// whose stored clock_offset is empty matches its OS-family default — Linux
// VMs match `utc`, Windows VMs match `localtime`.
func cliVMMatchesClockOffsetFilter(spec types.VMSpec, filter string) bool {
	return spec.ResolvedClockOffset() == filter
}

// parseCLIClockOffset mirrors the API's parseClockOffsetFilter contract for
// the `--clock-offset` flag on `vmsmith vm list`. Empty disables; a
// recognised value (case-insensitive, whitespace-trimmed) returns the
// canonical lowercased form; anything else returns an error so the operator
// sees the typo before the daemon is contacted.
func parseCLIClockOffset(raw, flag string) (string, bool, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false, nil
	}
	switch v {
	case types.ClockOffsetUTC, types.ClockOffsetLocaltime:
		return v, true, nil
	default:
		return "", false, fmt.Errorf(
			"invalid %s %q: must be one of %s, %s",
			flag, raw, types.ClockOffsetUTC, types.ClockOffsetLocaltime,
		)
	}
}
