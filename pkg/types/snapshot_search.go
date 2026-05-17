package types

import "strings"

// SnapshotMatchesSearch reports whether the given snapshot has a substring
// match against the (already lower-cased, trimmed) query. The haystack
// covers the snapshot's name, description, and tags — the three fields an
// operator would type into a "find this snapshot" box. An empty query
// matches every non-nil snapshot; a nil snapshot never matches.
//
// Matching against ID and VMID is intentionally excluded:
//   - ID is the libvirt path `<vmID>/<name>` and is therefore redundant with
//     the name match plus the VM scoping the request already lives under
//     (`GET /api/v1/vms/{vmID}/snapshots`).
//   - VMID is the URL scope, not a useful operator query.
//
// Mirrors the contract of VMMatchesSearch (2.2.13), ImageMatchesSearch
// (5.4.9), and EventMatchesSearch (4.2.20): callers must lower-case + trim
// the needle before invoking (the API/CLI handlers do).
func SnapshotMatchesSearch(snap *Snapshot, query string) bool {
	if snap == nil {
		return false
	}
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(snap.Name), query) {
		return true
	}
	if snap.Description != "" && strings.Contains(strings.ToLower(snap.Description), query) {
		return true
	}
	for _, tag := range snap.Tags {
		if tag == "" {
			continue
		}
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}
