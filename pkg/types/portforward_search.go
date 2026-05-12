package types

import (
	"strconv"
	"strings"
)

// PortForwardMatchesSearch reports whether the given port-forward has a
// substring match against the (already lower-cased, trimmed) query. The
// haystack covers the operator-useful fields: description, protocol, the
// host port, the guest port, and the guest IP. The rule ID (`{vmID}/{host}`)
// and vm_id are intentionally excluded — they're the URL scope, redundant
// with the host port match, or noisy numeric needles.
//
// An empty query matches every non-nil port forward; a nil rule never
// matches.
//
// Mirrors the contract of VMMatchesSearch (2.2.13), ImageMatchesSearch
// (5.4.9), SnapshotMatchesSearch (5.4.10), and EventMatchesSearch
// (4.2.20): callers must lower-case + trim the needle before invoking
// (the API/CLI handlers do).
func PortForwardMatchesSearch(pf *PortForward, query string) bool {
	if pf == nil {
		return false
	}
	if query == "" {
		return true
	}
	if pf.Description != "" && strings.Contains(strings.ToLower(pf.Description), query) {
		return true
	}
	if strings.Contains(strings.ToLower(string(pf.Protocol)), query) {
		return true
	}
	if strings.Contains(strconv.Itoa(pf.HostPort), query) {
		return true
	}
	if strings.Contains(strconv.Itoa(pf.GuestPort), query) {
		return true
	}
	if pf.GuestIP != "" && strings.Contains(strings.ToLower(pf.GuestIP), query) {
		return true
	}
	return false
}
