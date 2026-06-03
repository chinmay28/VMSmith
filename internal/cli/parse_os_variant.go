package cli

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseCLIOSVariant mirrors the API's parseOSVariantFilter contract for the
// `--os-variant` flag on `vmsmith vm list` (and any future CLI surfaces that
// gain the same filter).
//
//   - Empty / whitespace-only input returns ("", false, nil) so the caller
//     can short-circuit the filter.
//   - A case-insensitive value matching one of types.KnownWindowsVariants
//     returns the canonical lowercased variant string with set=true.
//   - Anything else returns an error so the operator gets a clear "you
//     mistyped this" message instead of a silently empty filter.
func parseCLIOSVariant(raw, flag string) (string, bool, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false, nil
	}
	if !types.IsKnownWindowsVariant(v) {
		return "", false, fmt.Errorf(
			"invalid %s %q: must be one of %s",
			flag, raw, strings.Join(types.KnownWindowsVariants, ", "),
		)
	}
	return v, true, nil
}
