package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseCLIGPU mirrors the API contract for `?gpu=<pci-addr>` (see
// internal/api/parse_gpu.go). Empty disables; whitespace trimmed; both long
// (`0000:01:00.0`) and short (`01:00.0`) forms accepted and normalised to
// the long form so the comparison is canonical. An invalid PCI address
// returns a typed error that the caller surfaces to the operator before
// any HTTP round-trip — same alphabet as the create-time `--gpu` flag.
func parseCLIGPU(raw string) (string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, nil
	}
	if !types.IsValidPCIAddress(trimmed) {
		return "", false, fmt.Errorf("--gpu %q must be a PCI address like 0000:01:00.0 or 01:00.0", raw)
	}
	return types.NormalizePCIAddress(trimmed), true, nil
}

// cliVMMatchesGPUFilter mirrors `vmMatchesGPUFilter` on the API side. The
// filter value is already-normalised long form; stored values are
// normalised at compare time so a VM persisted with the short form still
// matches.
func cliVMMatchesGPUFilter(vm *types.VM, filter string) bool {
	for _, g := range vm.Spec.GPUs {
		if types.NormalizePCIAddress(g) == filter {
			return true
		}
	}
	return false
}
