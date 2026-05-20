package api

import (
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseTimeRangeParam parses an optional RFC3339 timestamp query parameter
// (e.g. ?since= / ?until=) and returns (value, set, apiErr).
//
//   - Empty or whitespace-only inputs return ({}, false, nil) so the caller
//     can short-circuit the predicate without distinguishing "no filter" from
//     "match all".
//   - Valid RFC3339 / RFC3339Nano strings return (parsedTime, true, nil).
//   - Anything else returns a typed `*types.APIError` with code
//     `invalid_<name>` so the handler can pass it through `writeAPIError`.
//
// The helper accepts both fractional-second and second-precision forms by
// trying RFC3339Nano first and falling back to RFC3339, matching the existing
// `events` handler's contract.
func parseTimeRangeParam(raw, name string) (time.Time, bool, *types.APIError) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return time.Time{}, false, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return parsed, true, nil
	}
	if parsed, err := time.Parse(time.RFC3339, v); err == nil {
		return parsed, true, nil
	}
	return time.Time{}, false, types.NewAPIError("invalid_"+name, name+" must be a valid RFC3339 timestamp")
}

// snapshotInTimeRange returns true when t falls inside the optional
// [since, until] window. Both endpoints are inclusive — operators reading
// "snapshots from 2026-05-01 00:00:00 to 2026-05-01 23:59:59" would expect
// the boundary timestamps to match. When neither bound is set the predicate
// is a no-op (returns true).
//
// A zero `t` is treated as "unknown timestamp" and is filtered OUT whenever
// any range bound is set — operators looking for snapshots in a time window
// don't want unbounded entries silently included.
func snapshotInTimeRange(t time.Time, since time.Time, sinceSet bool, until time.Time, untilSet bool) bool {
	if !sinceSet && !untilSet {
		return true
	}
	if t.IsZero() {
		return false
	}
	if sinceSet && t.Before(since) {
		return false
	}
	if untilSet && t.After(until) {
		return false
	}
	return true
}
