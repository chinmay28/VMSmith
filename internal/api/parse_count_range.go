package api

import (
	"strconv"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseCountRangeParam parses an optional non-negative integer query parameter
// (e.g. ?min_cpus= / ?max_cpus=) and returns (value, set, apiErr).
//
//   - Empty or whitespace-only inputs return (0, false, nil) so the caller can
//     short-circuit the predicate without distinguishing "no filter" from
//     "match all".
//   - A valid base-10 non-negative integer returns (parsed, true, nil).
//   - Anything else (non-numeric or negative) returns a typed `*types.APIError`
//     with code `invalid_<name>` so the handler can pass it through
//     `writeAPIError`.
//
// Mirrors parseSizeRangeParam's contract but carries count semantics (no byte
// unit in the message), so it composes with whole-number resource fields like
// the VM's `spec.cpus`.
func parseCountRangeParam(raw, name string) (int, bool, *types.APIError) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < 0 {
		return 0, false, types.NewAPIError("invalid_"+name, name+" must be a non-negative integer")
	}
	return parsed, true, nil
}

// countInRange returns true when v falls inside the optional [min, max]
// inclusive window. Both endpoints are inclusive — an operator asking for
// "VMs with 4 to 8 vCPUs" expects the boundary counts to match. When neither
// bound is set the predicate is a no-op (returns true).
func countInRange(v int, min int, minSet bool, max int, maxSet bool) bool {
	if minSet && v < min {
		return false
	}
	if maxSet && v > max {
		return false
	}
	return true
}
