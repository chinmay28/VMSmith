package logger

import "strings"

// EntryMatchesSearch reports whether the given log entry has a case-insensitive
// substring match against the (already lower-cased, trimmed) query. The
// haystack covers the entry's message, source, level, and every value in the
// structured fields map — the surfaces an operator would type into a "find
// this log line" box. An empty query matches every entry.
//
// Field *keys* are intentionally excluded from the haystack: keys are a
// short, repeating vocabulary (`vm_id`, `error`, `method`, ...) and including
// them produces noisy matches against operator-supplied values that happen to
// share a substring with a key name. Timestamps are also excluded — operators
// use the existing `since` filter for time scoping.
//
// Mirrors the contract of VMMatchesSearch (2.2.13), ImageMatchesSearch
// (5.4.9), SnapshotMatchesSearch (5.4.10), PortForwardMatchesSearch (5.4.11),
// TemplateMatchesSearch (5.4.12), and EventMatchesSearch (4.2.20): callers
// must lower-case + trim the needle before invoking (the API handler does).
func EntryMatchesSearch(e Entry, query string) bool {
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(e.Message), query) {
		return true
	}
	if e.Source != "" && strings.Contains(strings.ToLower(e.Source), query) {
		return true
	}
	if e.Level != "" && strings.Contains(strings.ToLower(e.Level), query) {
		return true
	}
	for _, v := range e.Fields {
		if strings.Contains(strings.ToLower(v), query) {
			return true
		}
	}
	return false
}
