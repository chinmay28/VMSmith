package types

import (
	"sort"
	"strings"
)

// Schedule run history sort fields. Whitelisted at the API/CLI surface so
// callers can't silently fall through to a different ordering.
const (
	ScheduleRunSortID         = "id"
	ScheduleRunSortStartedAt  = "started_at"
	ScheduleRunSortFinishedAt = "finished_at"
	ScheduleRunSortStatus     = "status"
	ScheduleRunSortDuration   = "duration"
	ScheduleRunSortVMID       = "vm_id"
)

// IsValidScheduleRunSort reports whether field is an accepted sort key.
func IsValidScheduleRunSort(field string) bool {
	switch field {
	case ScheduleRunSortID,
		ScheduleRunSortStartedAt,
		ScheduleRunSortFinishedAt,
		ScheduleRunSortStatus,
		ScheduleRunSortDuration,
		ScheduleRunSortVMID:
		return true
	default:
		return false
	}
}

// compareRunDuration orders two runs by their `finished_at - started_at`
// duration. Runs with a nil finished_at (still-running) have no known
// duration and sort after any concrete duration in ascending order (i.e.
// they sink to the tail), mirroring the nil-trailing semantics used by the
// finished_at axis. Returns -1 / 0 / 1.
func compareRunDuration(a, b *ScheduleRun) int {
	aNil := a.FinishedAt == nil
	bNil := b.FinishedAt == nil
	if aNil && bNil {
		return 0
	}
	if aNil {
		return 1
	}
	if bNil {
		return -1
	}
	da := a.FinishedAt.Sub(a.StartedAt)
	db := b.FinishedAt.Sub(b.StartedAt)
	if da < db {
		return -1
	}
	if da > db {
		return 1
	}
	return 0
}

// SortScheduleRuns sorts runs in place by the requested field and order.
// A nil finished_at sorts after any concrete time in ascending order so
// still-running runs sink to the tail (consistent with compareNullableTime's
// nil-trailing semantics). The duration axis treats still-running runs
// (nil finished_at) as unknown-duration and applies the same nil-trailing
// semantics. The vm_id axis is case-sensitive and sinks empty-vm_id runs
// to the tail of asc / head of desc, mirroring the events vm_id sort axis
// (5.4.93) and the logs vm_id sort axis (5.4.94). All comparators
// tiebreak on ID so paginated requests are deterministic. Unknown fields
// fall back to ID.
func SortScheduleRuns(runs []*ScheduleRun, field, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		var cmp int
		switch field {
		case ScheduleRunSortStartedAt:
			cmp = compareTime(a.StartedAt, b.StartedAt)
		case ScheduleRunSortFinishedAt:
			cmp = compareNullableTime(a.FinishedAt, b.FinishedAt)
		case ScheduleRunSortStatus:
			cmp = strings.Compare(string(a.Status), string(b.Status))
		case ScheduleRunSortDuration:
			cmp = compareRunDuration(a, b)
		case ScheduleRunSortVMID:
			// Case-sensitive comparison mirrors the case-sensitive
			// `?vm_id=` exact-match filter contract — VM IDs are
			// opaque `vm-<unix-nano>` strings operators reference
			// verbatim. Runs with an empty `vm_id` (skipped fires
			// recorded without a resolved target such as a
			// `queue_full` skip on an all-VMs schedule) sink to the
			// tail of asc / head of desc, mirroring the nil-trailing
			// semantics on the events `vm_id` sort axis (5.4.93),
			// the logs `vm_id` sort axis (5.4.94), and every other
			// nullable sort axis (ip, guest_ip, last_fired_at,
			// last_delivery_at, actor, resource_id, image,
			// default_user, gpu).
			switch {
			case a.VMID == "" && b.VMID == "":
				cmp = 0
			case a.VMID == "":
				cmp = 1
			case b.VMID == "":
				cmp = -1
			default:
				cmp = strings.Compare(a.VMID, b.VMID)
			}
		default:
			cmp = strings.Compare(a.ID, b.ID)
		}
		if cmp == 0 {
			cmp = strings.Compare(a.ID, b.ID)
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}
