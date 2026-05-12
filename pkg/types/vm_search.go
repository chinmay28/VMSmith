package types

import "strings"

// VMMatchesSearch reports whether the given VM matches the (already
// normalised, lowercase) search query.  The match is a case-insensitive
// substring scan over the VM's name, description, and tags — the fields an
// operator would type into a "find this machine" box.  An empty query matches
// everything; callers should short-circuit before calling.
//
// Matching against ID is intentionally excluded — IDs are opaque
// `vm-<unix-nano>` strings that no operator types from memory; including them
// produces noisy false positives when the substring happens to look numeric.
func VMMatchesSearch(vm *VM, query string) bool {
	if vm == nil {
		return false
	}
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(vm.Name), query) {
		return true
	}
	if vm.Description != "" && strings.Contains(strings.ToLower(vm.Description), query) {
		return true
	}
	for _, tag := range vm.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}
