package types

import "strings"

// EventMatchesSearch reports whether evt has a substring match against the
// (already lower-cased) query. The haystack covers:
//
//   - Message, Type, Source, Severity, Actor, VMID, ResourceID
//   - every value in Attributes
//
// The numeric event ID is intentionally excluded — the bus assigns opaque
// stringified uint64 IDs and a substring like "1" would match every event
// created today. Callers must lower-case + trim the needle before invoking
// (the API/CLI handlers do).
//
// An empty query matches every non-nil event; a nil event never matches.
func EventMatchesSearch(evt *Event, query string) bool {
	if evt == nil {
		return false
	}
	if query == "" {
		return true
	}
	if substrFoldMatch(evt.Message, query) ||
		substrFoldMatch(evt.Type, query) ||
		substrFoldMatch(evt.Source, query) ||
		substrFoldMatch(evt.Severity, query) ||
		substrFoldMatch(evt.Actor, query) ||
		substrFoldMatch(evt.VMID, query) ||
		substrFoldMatch(evt.ResourceID, query) {
		return true
	}
	for _, v := range evt.Attributes {
		if substrFoldMatch(v, query) {
			return true
		}
	}
	return false
}

// substrFoldMatch returns true if needle (already lowered) is a substring of
// haystack after haystack is lowered.
func substrFoldMatch(haystack, needle string) bool {
	if haystack == "" {
		return false
	}
	return strings.Contains(strings.ToLower(haystack), needle)
}
