package api

import (
	"strconv"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parsePortRangeParam parses an optional non-negative port-number query
// parameter (e.g. ?min_host_port= / ?max_host_port=) and returns
// (value, set, apiErr).
//
//   - Empty or whitespace-only inputs return (0, false, nil) so the caller can
//     short-circuit the predicate without distinguishing "no filter" from
//     "match all".
//   - A valid base-10 non-negative integer returns (parsed, true, nil).
//   - Anything else (non-numeric or negative) returns a typed `*types.APIError`
//     with code `invalid_<name>` so the handler can pass it through
//     `writeAPIError`.
//
// Mirrors the `parseSizeRangeParam` contract (5.4.40). Like the size-range
// filter there is no upper-bound validation: a value above the 65535 TCP/UDP
// ceiling simply matches nothing, keeping the wire contract identical to the
// established numeric-range filters.
func parsePortRangeParam(raw, name string) (int, bool, *types.APIError) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < 0 {
		return 0, false, types.NewAPIError("invalid_"+name, name+" must be a non-negative integer port number")
	}
	return parsed, true, nil
}

// portInRange returns true when port falls inside the optional [min, max]
// window. Both endpoints are inclusive — operators reading "forwards listening
// between 8000 and 8999" expect the boundary ports to match. When neither
// bound is set the predicate is a no-op (returns true).
func portInRange(port int, min int, minSet bool, max int, maxSet bool) bool {
	if minSet && port < min {
		return false
	}
	if maxSet && port > max {
		return false
	}
	return true
}
