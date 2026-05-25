package api

import (
	"strconv"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseSizeRangeParam parses an optional non-negative byte-count query
// parameter (e.g. ?min_size= / ?max_size=) and returns (value, set, apiErr).
//
//   - Empty or whitespace-only inputs return (0, false, nil) so the caller can
//     short-circuit the predicate without distinguishing "no filter" from
//     "match all".
//   - A valid base-10 non-negative integer returns (parsed, true, nil).
//   - Anything else (non-numeric, negative, or out-of-range for int64) returns
//     a typed `*types.APIError` with code `invalid_<name>` so the handler can
//     pass it through `writeAPIError`.
//
// Bytes are the only accepted unit — operators compose human-friendly sizes on
// the client (e.g. the CLI / GUI), keeping the wire contract unambiguous.
func parseSizeRangeParam(raw, name string) (int64, bool, *types.APIError) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false, nil
	}
	parsed, err := strconv.ParseInt(v, 10, 64)
	if err != nil || parsed < 0 {
		return 0, false, types.NewAPIError("invalid_"+name, name+" must be a non-negative integer number of bytes")
	}
	return parsed, true, nil
}

// imageInSizeRange returns true when size falls inside the optional
// [min, max] window. Both endpoints are inclusive — operators reading
// "images between 1 GiB and 2 GiB" expect the boundary sizes to match.
// When neither bound is set the predicate is a no-op (returns true).
//
// Unlike the time-range predicate there is no zero-value exclusion: a
// zero-byte size is a legitimate, fully-known value, so it matches whenever it
// falls within the requested window.
func imageInSizeRange(size int64, min int64, minSet bool, max int64, maxSet bool) bool {
	if minSet && size < min {
		return false
	}
	if maxSet && size > max {
		return false
	}
	return true
}
