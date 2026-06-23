package api

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseGPUFilter parses the optional `?gpu=<pci-addr>` query parameter used by
// `GET /vms` (5.7.9). Matches against the VM's requested passthrough GPUs
// (`spec.gpus`, normalised to the canonical long form via
// `types.ResolvedGPUs`). Closes the operator query *"which VM has
// 0000:01:00.0 assigned?"* — the natural follow-up after the 5.7 GPU
// passthrough feature shipped.
//
// Contract:
//
//   - empty value disables the filter
//   - whitespace is trimmed
//   - both the long form (`0000:01:00.0`) and the short form (`01:00.0`) are
//     accepted; the value is normalised to the long form before matching, so
//     a VM stored with the short form still surfaces when queried by the long
//     form (and vice versa) — mirrors the alphabet semantics in
//     `vmsmith vm create --gpu` and `vmsmith host gpus`
//   - garbage that fails `IsValidPCIAddress` returns 400 `invalid_gpu`,
//     mirroring the create-path validation contract (5.7.4) so a typo
//     surfaces before the filter is silently no-op'd
//
// VMs with no requested GPUs drop out whenever the filter is set, mirroring
// the empty-stored-excludes contract on `?nat_static_ip=` / `?nat_gateway=` /
// `?ip=`.
func parseGPUFilter(raw string) (string, bool, *types.APIError) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false, nil
	}
	if !types.IsValidPCIAddress(v) {
		return "", false, types.NewAPIError(
			"invalid_gpu",
			"gpu must be a PCI address in domain:bus:slot.function form (long '0000:01:00.0' or short '01:00.0')",
		)
	}
	return types.NormalizePCIAddress(v), true, nil
}

// vmMatchesGPUFilter reports whether any of the VM's requested passthrough
// GPUs equals filter (which is already the canonical long form, as produced
// by parseGPUFilter). Matching uses `VMSpec.ResolvedGPUs`, so stored
// short-form addresses round-trip cleanly and duplicates / invalid stored
// entries are ignored.
func vmMatchesGPUFilter(spec types.VMSpec, filter string) bool {
	for _, g := range spec.ResolvedGPUs() {
		if g == filter {
			return true
		}
	}
	return false
}
