package types

import "strings"

// EventStreamFilter captures the cross-cutting exact-match + substring
// predicate that GET /api/v1/events/stream applies server-side so SSE clients
// don't have to drop every non-matching event on the floor.
//
// Field semantics mirror the store-backed list filter at
// internal/store/bolt.go::ListEventsFiltered byte-for-byte so a client picking
// between /events and /events/stream sees the same membership:
//
//   - VMID / Type / Source / Severity / Actor / ResourceID: case-sensitive
//     exact-match. Empty disables the predicate.
//   - TypePrefix: case-insensitive prefix match against evt.Type. The caller
//     must pre-lowercase + trim the needle (the API/CLI handlers do).
//   - Search: case-insensitive substring match delegated to
//     EventMatchesSearch. The caller must pre-lowercase + trim the needle.
//
// Time-range / cursor predicates (?since=, ?until=) are not part of this
// struct — the SSE handler treats ?since= as a uint64 seq replay cursor
// (separately from the list endpoint's RFC3339 ?since=) and ?until= is
// meaningless on a live stream.
type EventStreamFilter struct {
	VMID       string
	Type       string
	Source     string
	Severity   string
	Actor      string
	ResourceID string
	TypePrefix string
	Search     string
}

// HasAny reports whether at least one predicate is active. The SSE handler
// uses this to short-circuit the no-filter fast path.
func (f EventStreamFilter) HasAny() bool {
	return f.VMID != "" || f.Type != "" || f.Source != "" || f.Severity != "" ||
		f.Actor != "" || f.ResourceID != "" || f.TypePrefix != "" || f.Search != ""
}

// EventMatchesStreamFilter reports whether evt satisfies every active field of
// f. A nil event never matches; an empty filter matches every non-nil event.
func EventMatchesStreamFilter(evt *Event, f EventStreamFilter) bool {
	if evt == nil {
		return false
	}
	if f.VMID != "" && evt.VMID != f.VMID {
		return false
	}
	if f.Type != "" && evt.Type != f.Type {
		return false
	}
	if f.Source != "" && evt.Source != f.Source {
		return false
	}
	if f.Severity != "" && evt.Severity != f.Severity {
		return false
	}
	if f.Actor != "" && evt.Actor != f.Actor {
		return false
	}
	if f.ResourceID != "" && evt.ResourceID != f.ResourceID {
		return false
	}
	if f.TypePrefix != "" && !strings.HasPrefix(strings.ToLower(evt.Type), f.TypePrefix) {
		return false
	}
	if f.Search != "" && !EventMatchesSearch(evt, f.Search) {
		return false
	}
	return true
}
