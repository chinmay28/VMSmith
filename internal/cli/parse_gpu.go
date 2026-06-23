package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// cliVMMatchesGPUFilter mirrors the API's vmMatchesGPUFilter for the CLI:
// any-of exact match between the requested filter (canonical long form) and
// the VM's requested passthrough GPUs via VMSpec.ResolvedGPUs.
func cliVMMatchesGPUFilter(spec types.VMSpec, filter string) bool {
	for _, g := range spec.ResolvedGPUs() {
		if g == filter {
			return true
		}
	}
	return false
}

// parseCLIGPU mirrors the API's parseGPUFilter contract for the `--gpu` flag
// on `vmsmith vm list` (5.7.9). Empty disables; whitespace-trimmed; both the
// long form (`0000:01:00.0`) and short form (`01:00.0`) are accepted and
// normalised to the long form before matching, so stored short-form addresses
// still surface when queried by the long form. Garbage failing
// `IsValidPCIAddress` returns an error so the operator sees the typo before
// the daemon is contacted.
func parseCLIGPU(raw, flag string) (string, bool, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false, nil
	}
	if !types.IsValidPCIAddress(v) {
		return "", false, fmt.Errorf(
			"invalid %s %q: must be a PCI address in domain:bus:slot.function form (long '0000:01:00.0' or short '01:00.0')",
			flag, raw,
		)
	}
	return types.NormalizePCIAddress(v), true, nil
}
