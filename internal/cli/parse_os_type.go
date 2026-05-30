package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseCLIOSType mirrors the API's parseOSTypeFilter contract for the
// `--os-type` flag on `vmsmith vm list` and `vmsmith template list`.
//
//   - Empty / whitespace-only input returns ("", false, nil) so the caller
//     can short-circuit the filter.
//   - A case-insensitive `linux` or `windows` returns the canonical
//     lowercased OSType with set=true.
//   - Anything else returns an error so the operator gets a clear "you
//     mistyped this" message instead of a silently empty filter.
func parseCLIOSType(raw, flag string) (types.OSType, bool, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false, nil
	}
	switch types.OSType(v) {
	case types.OSTypeLinux, types.OSTypeWindows:
		return types.OSType(v), true, nil
	default:
		return "", false, fmt.Errorf("invalid %s %q: must be 'linux' or 'windows'", flag, raw)
	}
}
