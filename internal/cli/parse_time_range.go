package cli

import (
	"fmt"
	"strings"
	"time"
)

// parseCLITimeRange parses an optional RFC3339 / RFC3339Nano timestamp CLI
// flag (e.g. --since / --until) and returns (value, set, err).
//
//   - Empty or whitespace-only inputs return ({}, false, nil) so the caller
//     can short-circuit the predicate without distinguishing "no filter" from
//     "match all".
//   - Anything else returns a usage error that name-checks the offending
//     flag for the operator.
//
// Accepts both RFC3339Nano and RFC3339 to mirror the API-side
// `parseTimeRangeParam` contract — the CLI predicate must produce identical
// matching to the over-HTTP path.
func parseCLITimeRange(raw, flag string) (time.Time, bool, error) {
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
	return time.Time{}, false, fmt.Errorf("invalid %s %q: must be a valid RFC3339 timestamp (e.g. 2026-05-01T00:00:00Z)", flag, v)
}

// snapshotInCLITimeRange is the CLI-side mirror of `snapshotInTimeRange`
// from `internal/api/parse_time_range.go`. Both endpoints are inclusive;
// a zero CreatedAt is filtered out whenever any bound is set.
func snapshotInCLITimeRange(t time.Time, since time.Time, sinceSet bool, until time.Time, untilSet bool) bool {
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
