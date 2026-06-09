package types

import (
	"sort"
	"strings"
)

// Port forward list sort fields. Whitelisted at the API/CLI surface so callers
// can't silently fall through to a different ordering.
const (
	PortForwardSortID          = "id"
	PortForwardSortHostPort    = "host_port"
	PortForwardSortGuestPort   = "guest_port"
	PortForwardSortProtocol    = "protocol"
	PortForwardSortDescription = "description"
	PortForwardSortGuestIP     = "guest_ip"
)

// IsValidPortForwardSort reports whether s is an accepted port-forward list
// sort field. Used by the API and CLI parsers to reject unknown values
// uniformly, mirroring IsValidVMSort.
func IsValidPortForwardSort(s string) bool {
	switch s {
	case PortForwardSortID,
		PortForwardSortHostPort,
		PortForwardSortGuestPort,
		PortForwardSortProtocol,
		PortForwardSortDescription,
		PortForwardSortGuestIP:
		return true
	}
	return false
}

// SortPortForwards sorts the given port forwards in place by the requested
// field and order. All comparators tiebreak on `id` so pagination — when it
// arrives at this endpoint — is deterministic. The list is scoped to a single
// VM at the handler, so the id tiebreak amounts to a host-port tiebreak today
// (PortForward.ID is `{vmID}/{hostPort}`), but encoding the rule against `id`
// keeps the matcher correct if the ID scheme changes later.
//
// Unknown sort/order values silently fall back to id-asc; surface validation
// errors at the parsing layer (see `internal/api.parsePortForwardSort`).
func SortPortForwards(pfs []*PortForward, sortField, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(pfs, func(i, j int) bool {
		ai, aj := pfs[i], pfs[j]
		var less bool
		switch sortField {
		case PortForwardSortHostPort:
			if ai.HostPort != aj.HostPort {
				less = ai.HostPort < aj.HostPort
				break
			}
			less = ai.ID < aj.ID
		case PortForwardSortGuestPort:
			if ai.GuestPort != aj.GuestPort {
				less = ai.GuestPort < aj.GuestPort
				break
			}
			less = ai.ID < aj.ID
		case PortForwardSortProtocol:
			pi, pj := string(ai.Protocol), string(aj.Protocol)
			if pi != pj {
				less = pi < pj
				break
			}
			less = ai.ID < aj.ID
		case PortForwardSortDescription:
			di, dj := strings.ToLower(ai.Description), strings.ToLower(aj.Description)
			if di != dj {
				less = di < dj
				break
			}
			less = ai.ID < aj.ID
		case PortForwardSortGuestIP:
			// Numeric IP comparison (compareVMIP is package-private but
			// general-purpose: both args are strings, no VM type involved).
			// Empty / unparseable guest_ip values sink to the tail of asc
			// and the head of desc, mirroring the nil-trailing semantics
			// on the VM `ip` sort axis (5.4.85) and the schedule
			// `last_fired_at` / `next_fire_at` sort axes.
			cmp := compareVMIP(ai.GuestIP, aj.GuestIP)
			if cmp != 0 {
				less = cmp < 0
				break
			}
			less = ai.ID < aj.ID
		default: // PortForwardSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
