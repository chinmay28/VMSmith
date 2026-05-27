package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// parseCLICountRange parses an optional non-negative integer CLI flag
// (e.g. --min-cpus / --max-cpus) and returns (value, set, err).
//
//   - Empty or whitespace-only inputs return (0, false, nil) so the caller can
//     short-circuit the predicate without distinguishing "no filter" from
//     "match all".
//   - Anything else (non-numeric or negative) returns a usage error that
//     name-checks the offending flag for the operator.
//
// Mirrors the API-side parseCountRangeParam contract so the CLI predicate
// produces identical matching to the over-HTTP path.
func parseCLICountRange(raw, flag string) (int, bool, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < 0 {
		return 0, false, fmt.Errorf("invalid %s %q: must be a non-negative integer", flag, v)
	}
	return parsed, true, nil
}

// countInCLIRange is the CLI-side mirror of countInRange from
// internal/api/parse_count_range.go. Both endpoints are inclusive.
func countInCLIRange(v int, min int, minSet bool, max int, maxSet bool) bool {
	if minSet && v < min {
		return false
	}
	if maxSet && v > max {
		return false
	}
	return true
}
