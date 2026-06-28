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
	SnapshotSortID          = "id"
	SnapshotSortName        = "name"
	SnapshotSortCreatedAt   = "created_at"
	SnapshotSortDescription = "description"
)

// IsValidSnapshotSort reports whether s is one of the whitelisted snapshot
// list sort fields. Parsers normalise (TrimSpace + ToLower) before lookup so
// `DESCRIPTION` or ` description ` return false here — the API and CLI both
// surface that as a 400 instead of silently falling back to id-asc.
func IsValidSnapshotSort(s string) bool {
	switch s {
	case SnapshotSortID, SnapshotSortName,
		SnapshotSortCreatedAt, SnapshotSortDescription:
		return true
	}
	return false
}

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
		case SnapshotSortDescription:
			// Case-insensitive compare on `description` so operators can
			// paste a description verbatim — descriptions are free-form
			// text the operator chose, and matching `Pre upgrade` against
			// `pre upgrade` should land in the same bucket. Mirrors the
			// case-insensitive haystack in the existing `?search=` filter
			// on the snapshot list, so the same description-based query
			// surface is filtered (substring) and sorted (alphabetical)
			// on the same semantics. Snapshots with an empty description
			// (the common case — most snapshots get no description) sink
			// to the tail of asc / head of desc, mirroring the nil-trailing
			// semantics on every other nullable string sort axis (image,
			// gpu, actor, last_fired_at, last_delivery_at) and the image
			// (5.4.118) / template (5.4.119) / VM (5.4.120) `description`
			// axes one resource over.
			aiD, ajD := strings.ToLower(ai.Description), strings.ToLower(aj.Description)
			switch {
			case aiD == "" && ajD == "":
				less = ai.Name < aj.Name
			case aiD == "":
				less = false
			case ajD == "":
				less = true
			case aiD != ajD:
				less = aiD < ajD
			default:
				less = ai.Name < aj.Name
			}
		default: // SnapshotSortID (== Name within a VM scope)
			less = ai.Name < aj.Name
		}
		if desc {
			return !less
		}
		return less
	})
}
