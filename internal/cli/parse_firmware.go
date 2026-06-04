package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// cliVMMatchesFirmwareFilter mirrors the API's vmMatchesFirmwareFilter
// helper for the CLI: `bios` matches stored "" or "bios" (the SeaBIOS
// default); `uefi` and `ovmf` strict-match their stored value.
func cliVMMatchesFirmwareFilter(spec types.VMSpec, filter string) bool {
	stored := strings.ToLower(strings.TrimSpace(spec.Firmware))
	if filter == types.FirmwareBIOS {
		return stored == "" || stored == types.FirmwareBIOS
	}
	return stored == filter
}

// parseCLIFirmware mirrors the API's parseFirmwareFilter contract for the
// `--firmware` flag on `vmsmith vm list`. Empty disables; a recognised value
// (case-insensitive, whitespace-trimmed) returns the canonical lowercased
// form; anything else returns an error so the operator sees the typo before
// the daemon is contacted.
func parseCLIFirmware(raw, flag string) (string, bool, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false, nil
	}
	switch v {
	case types.FirmwareBIOS, types.FirmwareUEFI, types.FirmwareOVMF:
		return v, true, nil
	default:
		return "", false, fmt.Errorf(
			"invalid %s %q: must be one of %s, %s, %s",
			flag, raw, types.FirmwareBIOS, types.FirmwareUEFI, types.FirmwareOVMF,
		)
	}
}
