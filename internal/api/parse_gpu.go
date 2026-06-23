package api

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseGPUFilter parses the optional `?gpu=<pci-addr>` query parameter used
// by `GET /vms` (5.7.9). The filter is an "any-of" exact-match against the
// VM's `spec.gpus[]`, normalised to the long PCI form so the short
// (`01:00.0`) and long (`0000:01:00.0`) forms round-trip identically to a
// single canonical key — operators who paste straight from `lspci` (short
// form) match the same VMs as operators who paste from `vmsmith host gpus`
// (long form), without having to remember which form the daemon stored.
//
// Contract:
//
//   - empty value disables the filter
//   - whitespace is trimmed
//   - both long (`0000:01:00.0`) and short (`01:00.0`) forms accepted
//   - invalid PCI addresses return 400 `invalid_gpu`, matching the
//     create-time contract on `VMSpec.GPUs` so the filter alphabet stays
//     1:1 with the create alphabet
//
// Closes the operator query *"which VM has 0000:01:00.0 assigned right
// now?"* that today requires scanning every VM's `spec.gpus`. VMs with no
// assigned GPUs drop out whenever the filter is set, mirroring the
// empty-stored-excludes contract on `?nat_static_ip=` / `?nat_gateway=` /
// `?ip=`.
func parseGPUFilter(raw string) (string, bool, *types.APIError) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, nil
	}
	if !types.IsValidPCIAddress(trimmed) {
		return "", false, types.NewAPIError("invalid_gpu",
			fmt.Sprintf("gpu %q must be a PCI address like 0000:01:00.0 or 01:00.0", raw))
	}
	return types.NormalizePCIAddress(trimmed), true, nil
}

// vmMatchesGPUFilter reports whether any of the VM's assigned GPUs equals
// the (already-normalised) filter value. The VM's stored addresses are
// normalised at compare time so a VM persisted with the short form still
// matches a long-form query and vice versa — both forms canonicalise to the
// same key, matching the contract documented on parseGPUFilter.
func vmMatchesGPUFilter(vm *types.VM, filter string) bool {
	for _, g := range vm.Spec.GPUs {
		if types.NormalizePCIAddress(g) == filter {
			return true
		}
	}
	return false
}
