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
)

// IsValidScheduleRunSort reports whether field is an accepted sort key.
func IsValidScheduleRunSort(field string) bool {
	switch field {
	case ScheduleRunSortID, ScheduleRunSortStartedAt, ScheduleRunSortFinishedAt, ScheduleRunSortStatus, ScheduleRunSortDuration:
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
// semantics. All comparators tiebreak on ID so paginated requests are
// deterministic. Unknown fields fall back to ID.
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
