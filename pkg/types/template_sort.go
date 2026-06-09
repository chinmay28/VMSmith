package types

import (
	"sort"
	"strings"
)

// Template list sort fields. Whitelisted at the API/CLI surface so callers
// can't silently fall through to a different ordering.
const (
	TemplateSortID        = "id"
	TemplateSortName      = "name"
	TemplateSortCreatedAt = "created_at"
	TemplateSortCPUs      = "cpus"
	TemplateSortRAMMB     = "ram_mb"
	TemplateSortDiskGB    = "disk_gb"
	TemplateSortImage     = "image"
)

// IsValidTemplateSort reports whether s is an accepted template list sort
// field. Used by the API and CLI parsers to reject unknown values uniformly.
func IsValidTemplateSort(s string) bool {
	switch s {
	case TemplateSortID, TemplateSortName, TemplateSortCreatedAt,
		TemplateSortCPUs, TemplateSortRAMMB, TemplateSortDiskGB,
		TemplateSortImage:
		return true
	}
	return false
}

// SortTemplates sorts the given templates in place by the requested field
// and order. All comparators tiebreak on `id` so paginated requests return
// the same set across backends — `storage.Manager.ListTemplates` iterates
// bbolt key order (which is by ID) but tests may seed templates with
// equal-timestamp inputs that would otherwise shuffle.
//
// Unknown sort/order values silently fall back to id-asc; surface validation
// errors at the parsing layer (see internal/api.parseTemplateSort).
func SortTemplates(templates []*VMTemplate, sortField, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(templates, func(i, j int) bool {
		ai, aj := templates[i], templates[j]
		var less bool
		switch sortField {
		case TemplateSortName:
			ni, nj := strings.ToLower(ai.Name), strings.ToLower(aj.Name)
			if ni != nj {
				less = ni < nj
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortCreatedAt:
			if !ai.CreatedAt.Equal(aj.CreatedAt) {
				less = ai.CreatedAt.Before(aj.CreatedAt)
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortCPUs:
			if ai.CPUs != aj.CPUs {
				less = ai.CPUs < aj.CPUs
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortRAMMB:
			if ai.RAMMB != aj.RAMMB {
				less = ai.RAMMB < aj.RAMMB
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortDiskGB:
			if ai.DiskGB != aj.DiskGB {
				less = ai.DiskGB < aj.DiskGB
				break
			}
			less = ai.ID < aj.ID
		case TemplateSortImage:
			// Case-insensitive compare mirrors the case-insensitive
			// `?image=` exact-match filter contract so the filter and sort
			// agree on the same column. Templates with an empty `Image`
			// sink to the tail of asc / head of desc — mirrors the
			// nil-trailing semantics on every other nullable sort axis
			// (ip, guest_ip, last_fired_at, last_delivery_at, actor) and
			// the VM list `image` axis (5.4.88).
			aiImg, ajImg := strings.ToLower(ai.Image), strings.ToLower(aj.Image)
			switch {
			case aiImg == "" && ajImg == "":
				less = ai.ID < aj.ID
			case aiImg == "":
				less = false
			case ajImg == "":
				less = true
			case aiImg != ajImg:
				less = aiImg < ajImg
			default:
				less = ai.ID < aj.ID
			}
		default: // TemplateSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
