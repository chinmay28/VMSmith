package api

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseFirmwareFilter parses the optional `?firmware=<bios|uefi|ovmf>` query
// parameter used by `GET /vms`. Mirrors the parseOSTypeFilter contract: empty
// disables; a recognised value (case-insensitive, whitespace-trimmed) returns
// the canonical lowercased form; anything else returns a 400 with the stable
// `invalid_firmware` code so the CLI / GUI can surface the typo.
//
// Semantics intentionally match the create-path validation in
// validateDeviceOverrides — the same three-value vocabulary,
// case-insensitive + trim-then-compare. uefi and ovmf are preserved as
// separate filter values (they map to the same libvirt firmware='efi'
// attribute at render time, but the operator's stored choice survives the
// round-trip so the filter exposes it back).
//
// The match semantics live with the handler, not here: an empty stored
// firmware resolves to BIOS (the SeaBIOS default), so `?firmware=bios`
// matches both stored "bios" and stored "" — mirroring the way
// `?os_type=linux` matches empty-stored VMs via the documented Linux default.
func parseFirmwareFilter(raw string) (string, bool, *types.APIError) {
	normalised := strings.ToLower(strings.TrimSpace(raw))
	if normalised == "" {
		return "", false, nil
	}
	switch normalised {
	case types.FirmwareBIOS, types.FirmwareUEFI, types.FirmwareOVMF:
		return normalised, true, nil
	default:
		return "", false, types.NewAPIError(
			"invalid_firmware",
			fmt.Sprintf("firmware must be one of: %s, %s, %s",
				types.FirmwareBIOS, types.FirmwareUEFI, types.FirmwareOVMF),
		)
	}
}

// vmMatchesFirmwareFilter reports whether the VM matches the requested
// firmware bucket. `bios` (and the documented empty-stored default) match
// `?firmware=bios`; `uefi` and `ovmf` strict-match their stored value. The
// helper centralises the empty-means-bios semantics so the handler loop stays
// a single-line predicate call.
func vmMatchesFirmwareFilter(spec types.VMSpec, filter string) bool {
	stored := strings.ToLower(strings.TrimSpace(spec.Firmware))
	if filter == types.FirmwareBIOS {
		return stored == "" || stored == types.FirmwareBIOS
	}
	return stored == filter
}
