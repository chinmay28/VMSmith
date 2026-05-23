package types

import "strings"

// VMMatchesNetwork reports whether the given VM is attached to a network whose
// name equals the (already normalised, lowercase) query. The match is a
// case-insensitive exact-match (any-of) over the names of the VM's additional
// network attachments (spec.networks[].name) — the user-friendly labels an
// operator assigns ("data-net", "storage-net"). An empty query matches
// everything; callers should short-circuit before calling.
//
// The implicit primary NAT network is intentionally NOT matched: it is not
// represented in spec.networks (it is the default every VM carries), so a
// `?network=` query only ever scopes to the explicitly-attached extra
// networks operators name and group by.
func VMMatchesNetwork(vm *VM, network string) bool {
	if vm == nil {
		return false
	}
	if network == "" {
		return true
	}
	for _, attachment := range vm.Spec.Networks {
		if strings.EqualFold(attachment.Name, network) {
			return true
		}
	}
	return false
}
