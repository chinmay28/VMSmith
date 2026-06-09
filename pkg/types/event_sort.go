package types

import (
	"sort"
	"strings"
)

// Event list sort fields. Whitelisted at the API/CLI surface so callers
// can't silently fall through to a different ordering.
const (
	EventSortID         = "id"
	EventSortOccurredAt = "occurred_at"
	EventSortType       = "type"
	EventSortSource     = "source"
	EventSortSeverity   = "severity"
	EventSortActor      = "actor"
)

// IsValidEventSort reports whether s is an accepted event list sort field.
// Used by the API and CLI parsers to reject unknown values uniformly so the
// whitelist lives in one place.
func IsValidEventSort(s string) bool {
	switch s {
	case EventSortID, EventSortOccurredAt, EventSortType,
		EventSortSource, EventSortSeverity, EventSortActor:
		return true
	}
	return false
}

// SortEvents sorts the given events in place by the requested field and
// order. All comparators tiebreak on `id` so pagination over repeated
// requests is deterministic.
//
// `type`, `source`, and `severity` match case-insensitively. The default
// ordering used by `GET /api/v1/events` is `occurred_at`-desc (newest
// first); the comparator below preserves that behaviour exactly when
// callers ask for the same shape (`?sort=occurred_at&order=desc`).
//
// Unknown sort/order values silently fall back to id-asc; surface
// validation errors at the parsing layer (see
// `internal/api.parseEventSort`).
func SortEvents(events []*Event, sortField, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(events, func(i, j int) bool {
		ai, aj := events[i], events[j]
		var less bool
		switch sortField {
		case EventSortOccurredAt:
			ti, tj := ai.OccurredAt, aj.OccurredAt
			if ti.IsZero() {
				ti = ai.CreatedAt
			}
			if tj.IsZero() {
				tj = aj.CreatedAt
			}
			if !ti.Equal(tj) {
				less = ti.Before(tj)
				break
			}
			less = ai.ID < aj.ID
		case EventSortType:
			ti, tj := strings.ToLower(ai.Type), strings.ToLower(aj.Type)
			if ti != tj {
				less = ti < tj
				break
			}
			less = ai.ID < aj.ID
		case EventSortSource:
			si, sj := strings.ToLower(ai.Source), strings.ToLower(aj.Source)
			if si != sj {
				less = si < sj
				break
			}
			less = ai.ID < aj.ID
		case EventSortSeverity:
			si, sj := strings.ToLower(ai.Severity), strings.ToLower(aj.Severity)
			if si != sj {
				less = si < sj
				break
			}
			less = ai.ID < aj.ID
		case EventSortActor:
			// Case-sensitive comparison mirrors the case-sensitive
			// `?actor=` exact-match filter contract — operators
			// reference actor identifiers verbatim (e.g. `system`,
			// `app`, `ops-alice`). Empty actors sort to the tail of
			// asc / head of desc so legacy events written before the
			// actor attribution sweep don't crowd the head of a
			// freshly-sorted timeline; mirrors the nil-trailing
			// semantics on the VM list `ip` axis and the schedule
			// `last_fired_at` / `next_fire_at` axes.
			ax, bx := ai.Actor, aj.Actor
			switch {
			case ax == "" && bx == "":
				less = ai.ID < aj.ID
			case ax == "":
				less = false
			case bx == "":
				less = true
			case ax != bx:
				less = ax < bx
			default:
				less = ai.ID < aj.ID
			}
		default: // EventSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
