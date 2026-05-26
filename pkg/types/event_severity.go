package types

import "strings"

// eventSeverityRanks ranks event severities for floor/ceiling comparisons:
// info < warn < error. Used by the ?min_severity= filter on the event list
// and stream so operators can ask for "warn and above" without enumerating
// each level — mirrors the severity-floor semantics the logs `level` filter
// already ships.
var eventSeverityRanks = map[string]int{
	EventSeverityInfo:  0,
	EventSeverityWarn:  1,
	EventSeverityError: 2,
}

// EventSeverityRank returns the rank of a severity string (matched
// case-insensitively after trimming) and whether it is a recognised severity.
// Unknown severities return (0, false) so callers can validate operator input.
func EventSeverityRank(severity string) (int, bool) {
	r, ok := eventSeverityRanks[strings.ToLower(strings.TrimSpace(severity))]
	return r, ok
}

// EventMeetsMinSeverity reports whether evt's severity is at or above the
// floor named by minSeverity. An empty or unrecognised minSeverity disables
// the floor (returns true) so the predicate is a no-op unless the caller has
// validated the value. Events whose own severity is unrecognised/empty are
// treated as info (rank 0) so they are only dropped when the floor is above
// info. A nil event never matches.
func EventMeetsMinSeverity(evt *Event, minSeverity string) bool {
	if evt == nil {
		return false
	}
	minRank, ok := EventSeverityRank(minSeverity)
	if !ok {
		return true
	}
	r, ok := EventSeverityRank(evt.Severity)
	if !ok {
		r = 0
	}
	return r >= minRank
}
