package types

import (
	"bytes"
	"net"
	"sort"
	"strings"
)

// VM list sort fields. Whitelisted at the API/CLI surface so callers can't
// silently fall through to a different ordering.
const (
	VMSortID          = "id"
	VMSortName        = "name"
	VMSortCreatedAt   = "created_at"
	VMSortState       = "state"
	VMSortCPUs        = "cpus"
	VMSortRAMMB       = "ram_mb"
	VMSortDiskGB      = "disk_gb"
	VMSortIP          = "ip"
	VMSortImage       = "image"
	VMSortDefaultUser = "default_user"

	SortOrderAsc  = "asc"
	SortOrderDesc = "desc"
)

// IsValidVMSort reports whether s is an accepted VM list sort field. Used by
// the API and CLI parsers to reject unknown values uniformly.
func IsValidVMSort(s string) bool {
	switch s {
	case VMSortID, VMSortName, VMSortCreatedAt, VMSortState,
		VMSortCPUs, VMSortRAMMB, VMSortDiskGB, VMSortIP,
		VMSortImage, VMSortDefaultUser:
		return true
	}
	return false
}

// resolveDefaultUser collapses an empty stored `spec.default_user` to "root",
// mirroring the runtime semantics in `internal/vm/lifecycle.go` and the
// `?default_user=` filter contract (5.4.23). Exposed at the type layer so the
// VM list sort axis (5.4.91) and the future template/list filter parity all
// share a single source of truth.
func resolveDefaultUser(s string) string {
	if s == "" {
		return "root"
	}
	return s
}

// compareVMIP returns -1/0/+1 comparing two VM runtime IPs numerically.
// IPs that fail to parse (empty or garbage) sort after any concrete address —
// stopped or DHCP-pending VMs sink to the tail of an ascending list, mirroring
// the nil-trailing semantics on time-valued sort axes (last_fired_at,
// next_fire_at). The compare is byte-wise on the canonical 16-byte form so
// IPv4 dotted-quads order numerically (10.0.0.2 < 10.0.0.10) instead of
// lexicographically.
func compareVMIP(a, b string) int {
	ai, bi := net.ParseIP(a), net.ParseIP(b)
	switch {
	case ai == nil && bi == nil:
		return 0
	case ai == nil:
		return 1
	case bi == nil:
		return -1
	}
	return bytes.Compare(ai.To16(), bi.To16())
}

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
		case VMSortIP:
			cmp := compareVMIP(ai.IP, aj.IP)
			if cmp != 0 {
				less = cmp < 0
				break
			}
			less = ai.ID < aj.ID
		case VMSortImage:
			// Case-insensitive compare mirrors the case-insensitive
			// `?image=` exact-match filter contract (5.4.22) so the
			// filter and sort agree on the same column. VMs with an
			// empty `spec.image` sink to the tail of asc / head of
			// desc — mirrors the nil-trailing semantics on every
			// other nullable sort axis (ip, guest_ip, last_fired_at,
			// last_delivery_at, actor).
			aiImg, ajImg := strings.ToLower(ai.Spec.Image), strings.ToLower(aj.Spec.Image)
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
		case VMSortDefaultUser:
			// Case-insensitive compare mirrors the case-insensitive
			// `?default_user=` exact-match filter (5.4.23). Diverges
			// from the nil-trailing convention on `ip` / `image` /
			// `actor` because this column has a documented default:
			// `lifecycle.go` runs the VM as root when
			// `spec.default_user` is empty, and the filter contract
			// is "an empty `spec.default_user` is treated as `root`"
			// — so empty VMs collate with explicit-root VMs rather
			// than sinking to the tail. The resolveDefaultUser
			// helper lives at the type layer so the filter and sort
			// share a single source of truth.
			aiU := strings.ToLower(resolveDefaultUser(ai.Spec.DefaultUser))
			ajU := strings.ToLower(resolveDefaultUser(aj.Spec.DefaultUser))
			if aiU != ajU {
				less = aiU < ajU
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
