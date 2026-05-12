package types

import (
	"sort"
	"strings"
)

// Image list sort fields. Whitelisted at the API/CLI surface so callers can't
// silently fall through to a different ordering. Mirrors the VM-list contract
// (`VMSortID` / `VMSortName` / `VMSortCreatedAt`) plus `size` for "biggest
// images first" — common when freeing host disk space.
const (
	ImageSortID        = "id"
	ImageSortName      = "name"
	ImageSortSize      = "size"
	ImageSortCreatedAt = "created_at"
)

// SortImages sorts the given images in place by the requested field and order.
// All comparators tiebreak on `id` so pagination is deterministic across
// backends — same contract as `SortVMs`.
//
// Unknown sort/order values silently fall back to id-asc; surface validation
// errors at the parsing layer (see `internal/api.parseImageSort`).
func SortImages(imgs []*Image, sortField, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(imgs, func(i, j int) bool {
		ai, aj := imgs[i], imgs[j]
		var less bool
		switch sortField {
		case ImageSortName:
			ni, nj := strings.ToLower(ai.Name), strings.ToLower(aj.Name)
			if ni != nj {
				less = ni < nj
				break
			}
			less = ai.ID < aj.ID
		case ImageSortSize:
			if ai.SizeBytes != aj.SizeBytes {
				less = ai.SizeBytes < aj.SizeBytes
				break
			}
			less = ai.ID < aj.ID
		case ImageSortCreatedAt:
			if !ai.CreatedAt.Equal(aj.CreatedAt) {
				less = ai.CreatedAt.Before(aj.CreatedAt)
				break
			}
			less = ai.ID < aj.ID
		default: // ImageSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
