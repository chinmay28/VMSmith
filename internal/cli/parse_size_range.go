package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// parseCLISizeRange parses an optional non-negative byte-count CLI flag
// (e.g. --min-size / --max-size) and returns (value, set, err).
//
//   - Empty or whitespace-only inputs return (0, false, nil) so the caller can
//     short-circuit the predicate without distinguishing "no filter" from
//     "match all".
//   - Anything else (non-numeric, negative, or out-of-range) returns a usage
//     error that name-checks the offending flag for the operator.
//
// Mirrors the API-side `parseSizeRangeParam` contract so the CLI predicate
// produces identical matching to the over-HTTP path.
func parseCLISizeRange(raw, flag string) (int64, bool, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false, nil
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	if err != nil || parsed < 0 {
		return 0, false, fmt.Errorf("invalid %s %q: must be a non-negative integer number of bytes", flag, v)
	}
	return parsed, true, nil
}

// imageInCLISizeRange is the CLI-side mirror of `imageInSizeRange` from
// `internal/api/parse_size_range.go`. Both endpoints are inclusive; there is
// no zero-value exclusion since a zero-byte size is a legitimate value.
func imageInCLISizeRange(size int64, min int64, minSet bool, max int64, maxSet bool) bool {
	if minSet && size < min {
		return false
	}
	if maxSet && size > max {
		return false
	}
	return true
}
