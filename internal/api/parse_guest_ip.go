package api

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseGuestIPFilter parses the optional `?guest_ip=<addr>` query parameter
// used by `GET /vms/{vmID}/ports`. The contract mirrors the existing
// exact-match filters on this endpoint (`?vm_id=` on `GET /events`, the
// `?actor=` / `?resource_id=` event filters):
//
//   - empty value disables the filter
//   - whitespace is trimmed
//   - case-insensitive — IPv4 dotted-quads are case-irrelevant, but IPv6
//     literals can mix `::FFFF` / `::ffff` and operators routinely paste
//     either form. Normalising to lowercase keeps the filter robust without
//     pulling in a full `net/netip` round-trip (this matches the search
//     haystack's lowercase semantics on the same field).
//
// Unlike the closed-vocabulary peers (`?protocol=` / `?nic_model=`) there is
// no validation rejection — guest_ip is a free-form value that operators
// paste verbatim and any garbage simply matches no rules.
func parseGuestIPFilter(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	return v, true
}

// portMatchesGuestIPFilter reports whether the port forward's GuestIP matches
// the requested filter value. Comparison is case-insensitive to mirror the
// parser; the rule's GuestIP is stored verbatim, so we lowercase it inside
// the comparator rather than mutating the stored value.
func portMatchesGuestIPFilter(pf *types.PortForward, filter string) bool {
	return strings.ToLower(strings.TrimSpace(pf.GuestIP)) == filter
}
