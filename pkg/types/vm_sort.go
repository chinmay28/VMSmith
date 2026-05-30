package types

import (
	"sort"
	"strings"
)

// VM list sort fields. Whitelisted at the API/CLI surface so callers can't
// silently fall through to a different ordering.
const (
	VMSortID        = "id"
	VMSortName      = "name"
	VMSortCreatedAt = "created_at"
	VMSortState     = "state"
	VMSortCPUs      = "cpus"
	VMSortRAMMB     = "ram_mb"
	VMSortDiskGB    = "disk_gb"

	SortOrderAsc  = "asc"
	SortOrderDesc = "desc"
)

// SortVMs sorts the given VMs in place by the requested field and order.
// All comparators tiebreak on `id` so pagination is deterministic across
// backends — `LibvirtManager` iterates bbolt key order (which is by ID),
// but `MockManager` iterates a Go map, so without a tiebreak equal-key
// elements would shuffle between requests.
//
// Unknown sort/order values silently fall back to id-asc; surface validation
// errors at the parsing layer (see `internal/api.parseVMSort`).
func SortVMs(vms []*VM, sortField, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(vms, func(i, j int) bool {
		ai, aj := vms[i], vms[j]
		var less bool
		switch sortField {
		case VMSortName:
			ni, nj := strings.ToLower(ai.Name), strings.ToLower(aj.Name)
			if ni != nj {
				less = ni < nj
				break
			}
			less = ai.ID < aj.ID
		case VMSortCreatedAt:
			if !ai.CreatedAt.Equal(aj.CreatedAt) {
				less = ai.CreatedAt.Before(aj.CreatedAt)
				break
			}
			less = ai.ID < aj.ID
		case VMSortState:
			si, sj := string(ai.State), string(aj.State)
			if si != sj {
				less = si < sj
				break
			}
			less = ai.ID < aj.ID
		case VMSortCPUs:
			if ai.Spec.CPUs != aj.Spec.CPUs {
				less = ai.Spec.CPUs < aj.Spec.CPUs
				break
			}
			less = ai.ID < aj.ID
		case VMSortRAMMB:
			if ai.Spec.RAMMB != aj.Spec.RAMMB {
				less = ai.Spec.RAMMB < aj.Spec.RAMMB
				break
			}
			less = ai.ID < aj.ID
		case VMSortDiskGB:
			if ai.Spec.DiskGB != aj.Spec.DiskGB {
				less = ai.Spec.DiskGB < aj.Spec.DiskGB
				break
			}
			less = ai.ID < aj.ID
		default: // VMSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
