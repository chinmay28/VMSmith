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
	ImageSortSourceVM  = "source_vm"
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
		case ImageSortSourceVM:
			// Case-insensitive compare mirrors the case-insensitive
			// `?source_vm=` exact-match filter contract so the filter
			// and sort agree on the same column. Images with an empty
			// `source_vm` (uploaded images, never exported from a VM)
			// sink to the tail of asc / head of desc — mirrors the
			// nil-trailing semantics on every other nullable sort axis
			// (ip, guest_ip, image, last_fired_at, last_delivery_at,
			// actor).
			aiSrc, ajSrc := strings.ToLower(ai.SourceVM), strings.ToLower(aj.SourceVM)
			switch {
			case aiSrc == "" && ajSrc == "":
				less = ai.ID < aj.ID
			case aiSrc == "":
				less = false
			case ajSrc == "":
				less = true
			case aiSrc != ajSrc:
				less = aiSrc < ajSrc
			default:
				less = ai.ID < aj.ID
			}
		default: // ImageSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
