package types

import (
	"sort"
	"strings"
)

// Snapshot list sort fields. Whitelisted at the API/CLI surface so callers
// can't silently fall through to a different ordering. Mirrors the VM-list
// (`VMSortID` / `VMSortName` / `VMSortCreatedAt`) and image-list contracts.
//
// `id` is the default; within a single VM the snapshot ID is `<vmID>/<name>`
// so id-asc is identical to name-asc — kept as a sort field so the API
// surface matches `GET /vms` and `GET /images`.
const (
	SnapshotSortID        = "id"
	SnapshotSortName      = "name"
	SnapshotSortCreatedAt = "created_at"
)

// SortSnapshots sorts the given snapshots in place by the requested field and
// order. All comparators tiebreak on `name` so pagination is deterministic
// across backends — same contract `SortVMs` / `SortImages` lock in (within a
// single VM, snapshot names are the unique key; the constructed `ID` field is
// just `<vmID>/<name>`).
//
// Unknown sort/order values silently fall back to name-asc; surface validation
// errors at the parsing layer (see `internal/api.parseSnapshotSort`).
func SortSnapshots(snaps []*Snapshot, sortField, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(snaps, func(i, j int) bool {
		ai, aj := snaps[i], snaps[j]
		var less bool
		switch sortField {
		case SnapshotSortName:
			ni, nj := strings.ToLower(ai.Name), strings.ToLower(aj.Name)
			if ni != nj {
				less = ni < nj
				break
			}
			less = ai.Name < aj.Name
		case SnapshotSortCreatedAt:
			if !ai.CreatedAt.Equal(aj.CreatedAt) {
				less = ai.CreatedAt.Before(aj.CreatedAt)
				break
			}
			less = ai.Name < aj.Name
		default: // SnapshotSortID (== Name within a VM scope)
			less = ai.Name < aj.Name
		}
		if desc {
			return !less
		}
		return less
	})
}
