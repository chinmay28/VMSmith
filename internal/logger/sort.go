package logger

import (
	"sort"
	"strings"
)

// Log list sort fields. Whitelisted at the API/CLI surface so callers can't
// silently fall through to a different ordering.
const (
	EntrySortTimestamp = "timestamp"
	EntrySortLevel     = "level"
	EntrySortSource    = "source"
	EntrySortVMID      = "vm_id"

	EntrySortOrderAsc  = "asc"
	EntrySortOrderDesc = "desc"
)

// levelRank maps a level name to a comparable rank so `sort=level` produces
// a meaningful order (debug < info < warn < error) — alphabetical sort
// would put `debug` and `error` next to each other, which is the opposite
// of operator intent (triaging severity).
func levelRank(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn", "warning":
		return 2
	case "error":
		return 3
	default:
		return -1
	}
}

// SortEntries sorts the given log entries in place by the requested field
// and order.  All comparators tiebreak on `timestamp` then `source` so
// pagination over repeated requests is deterministic — the ring buffer
// iterates in insertion order which is not stable under filtering.
//
// `level` matches by severity rank (debug < info < warn < error), not
// alphabetically.  `source` matches case-insensitively.  `vm_id` matches
// case-sensitively (VM IDs are opaque `vm-<unix-nano>` strings); entries
// with no `vm_id` field sink to the tail of `asc` and the head of `desc`,
// mirroring the nil-trailing semantics on the events `vm_id` sort axis
// and every other nullable sort axis (ip, guest_ip, last_fired_at,
// last_delivery_at).
//
// Unknown sort/order values silently fall back to timestamp-asc (the
// legacy oldest-first contract); surface validation errors at the parsing
// layer (see `internal/api.parseLogSort`).
func SortEntries(entries []Entry, sortField, order string) {
	desc := order == EntrySortOrderDesc
	vmIDOf := func(e Entry) string {
		if e.Fields == nil {
			return ""
		}
		return e.Fields["vm_id"]
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ai, aj := entries[i], entries[j]
		var less bool
		switch sortField {
		case EntrySortLevel:
			ri, rj := levelRank(ai.Level), levelRank(aj.Level)
			if ri != rj {
				less = ri < rj
				break
			}
			if !ai.Timestamp.Equal(aj.Timestamp) {
				less = ai.Timestamp.Before(aj.Timestamp)
				break
			}
			less = strings.ToLower(ai.Source) < strings.ToLower(aj.Source)
		case EntrySortSource:
			si, sj := strings.ToLower(ai.Source), strings.ToLower(aj.Source)
			if si != sj {
				less = si < sj
				break
			}
			if !ai.Timestamp.Equal(aj.Timestamp) {
				less = ai.Timestamp.Before(aj.Timestamp)
				break
			}
			less = ai.Level < aj.Level
		case EntrySortVMID:
			// Case-sensitive comparison mirrors the case-sensitive
			// `?vm_id=` exact-match filter contract — VM IDs are
			// opaque `vm-<unix-nano>` strings operators reference
			// verbatim.  Entries with no `vm_id` field sort to the
			// tail of asc / head of desc, mirroring the nil-trailing
			// semantics on the events `vm_id` sort axis and every
			// other nullable sort axis.
			vx, wx := vmIDOf(ai), vmIDOf(aj)
			switch {
			case vx == "" && wx == "":
				if !ai.Timestamp.Equal(aj.Timestamp) {
					less = ai.Timestamp.Before(aj.Timestamp)
					break
				}
				less = strings.ToLower(ai.Source) < strings.ToLower(aj.Source)
			case vx == "":
				less = false
			case wx == "":
				less = true
			case vx != wx:
				less = vx < wx
			default:
				if !ai.Timestamp.Equal(aj.Timestamp) {
					less = ai.Timestamp.Before(aj.Timestamp)
					break
				}
				less = strings.ToLower(ai.Source) < strings.ToLower(aj.Source)
			}
		default: // EntrySortTimestamp (legacy default)
			if !ai.Timestamp.Equal(aj.Timestamp) {
				less = ai.Timestamp.Before(aj.Timestamp)
				break
			}
			less = strings.ToLower(ai.Source) < strings.ToLower(aj.Source)
		}
		if desc {
			return !less
		}
		return less
	})
}
