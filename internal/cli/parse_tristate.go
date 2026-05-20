package cli

import (
	"fmt"
	"strings"
)

// parseCLITristateBool parses a CLI tristate-bool flag: empty / whitespace
// disables the filter; "true"/"false" (or "1"/"0") set it; anything else
// returns a usage error referencing flagName. Mirrors the daemon-side
// `parseTristateBoolParam` so the wire form and CLI form are symmetric.
func parseCLITristateBool(raw, flagName string) (bool, bool, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return false, false, nil
	}
	switch trimmed {
	case "true", "1":
		return true, true, nil
	case "false", "0":
		return false, true, nil
	}
	return false, false, fmt.Errorf("invalid %s %q: must be 'true' or 'false'", flagName, raw)
}
