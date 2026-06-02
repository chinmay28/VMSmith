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
)

// IsValidScheduleRunSort reports whether field is an accepted sort key.
func IsValidScheduleRunSort(field string) bool {
	switch field {
	case ScheduleRunSortID, ScheduleRunSortStartedAt, ScheduleRunSortFinishedAt, ScheduleRunSortStatus:
		return true
	default:
		return false
	}
}

// SortScheduleRuns sorts runs in place by the requested field and order.
// A nil finished_at sorts after any concrete time in ascending order so
// still-running runs sink to the tail (consistent with compareNextFire's
// nil-trailing semantics). All comparators tiebreak on ID so paginated
// requests are deterministic. Unknown fields fall back to ID.
func SortScheduleRuns(runs []*ScheduleRun, field, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		var cmp int
		switch field {
		case ScheduleRunSortStartedAt:
			cmp = compareTime(a.StartedAt, b.StartedAt)
		case ScheduleRunSortFinishedAt:
			cmp = compareNextFire(a.FinishedAt, b.FinishedAt)
		case ScheduleRunSortStatus:
			cmp = strings.Compare(string(a.Status), string(b.Status))
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
