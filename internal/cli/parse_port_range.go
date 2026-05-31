package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// parseCLIPortRange parses an optional non-negative port-number CLI flag
// (e.g. --min-host-port / --max-host-port) and returns (value, set, err).
//
//   - Empty or whitespace-only inputs return (0, false, nil) so the caller can
//     short-circuit the predicate without distinguishing "no filter" from
//     "match all".
//   - Anything else (non-numeric or negative) returns a usage error that
//     name-checks the offending flag for the operator.
//
// Mirrors the API-side `parsePortRangeParam` contract so the CLI predicate
// produces identical matching to the over-HTTP path.
func parseCLIPortRange(raw, flag string) (int, bool, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < 0 {
		return 0, false, fmt.Errorf("invalid %s %q: must be a non-negative integer port number", flag, v)
	}
	return parsed, true, nil
}

// portInCLIRange is the CLI-side mirror of `portInRange` from
// `internal/api/parse_port_range.go`. Both endpoints are inclusive.
func portInCLIRange(port int, min int, minSet bool, max int, maxSet bool) bool {
	if minSet && port < min {
		return false
	}
	if maxSet && port > max {
		return false
	}
	return true
}
