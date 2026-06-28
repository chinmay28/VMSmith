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
	VMSortGPU         = "gpu"
	VMSortOSType      = "os_type"
	VMSortFirmware    = "firmware"
	VMSortOSVariant   = "os_variant"
	VMSortDiskBus     = "disk_bus"
	VMSortNICModel    = "nic_model"
	VMSortMachine     = "machine"
	VMSortClockOffset = "clock_offset"
	VMSortAutoStart   = "auto_start"
	VMSortLocked      = "locked"
	VMSortNatStaticIP = "nat_static_ip"
	VMSortNatGateway  = "nat_gateway"
	VMSortDescription = "description"

	SortOrderAsc  = "asc"
	SortOrderDesc = "desc"
)

// IsValidVMSort reports whether s is an accepted VM list sort field. Used by
// the API and CLI parsers to reject unknown values uniformly.
func IsValidVMSort(s string) bool {
	switch s {
	case VMSortID, VMSortName, VMSortCreatedAt, VMSortState,
		VMSortCPUs, VMSortRAMMB, VMSortDiskGB, VMSortIP,
		VMSortImage, VMSortDefaultUser, VMSortGPU, VMSortOSType,
		VMSortFirmware, VMSortOSVariant, VMSortDiskBus, VMSortNICModel,
		VMSortMachine, VMSortClockOffset, VMSortAutoStart,
		VMSortLocked, VMSortNatStaticIP, VMSortNatGateway,
		VMSortDescription:
		return true
	}
	return false
}

// natStaticIPSortKey strips the optional CIDR suffix from a stored
// `spec.nat_static_ip` so the numeric IP comparator (compareVMIP) sees a
// bare address. Matches the matching contract on the `?nat_static_ip=`
// filter (5.4.79) which accepts the filter as either the full CIDR OR
// just the IP portion — so the sort uses the IP portion as the sort key
// to keep filter and sort in agreement on the same column.
func natStaticIPSortKey(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "/"); i >= 0 {
		return s[:i]
	}
	return s
}

// resolveFirmware collapses an empty stored `spec.firmware` to "bios" (the
// SeaBIOS default), mirroring the `?firmware=bios` empty-means-bios filter
// contract on `GET /vms`. uefi and ovmf are preserved as-is (they map to the
// same libvirt `firmware='efi'` attribute at render time but the operator's
// chosen alias survives the round-trip — same as the filter contract). Used by
// the VM list firmware sort axis (5.4.101) so the same documented-default
// semantics that the filter exposes are honoured on the ordering path.
func resolveFirmware(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return FirmwareBIOS
	}
	return v
}

// smallestGPU returns the lexicographically-smallest canonical PCI address
// among the VM's requested passthrough GPUs, or "" when none are assigned.
// Used by the gpu sort axis so a multi-GPU VM has a deterministic position
// (the smallest slot wins). Short-form entries are normalised to the long
// form before comparison so a VM persisted with "01:00.0" sorts identically
// to one persisted with "0000:01:00.0" — matches the alphabet contract on
// `?gpu=` (5.7.9) and `vmsmith vm create --gpu`.
func smallestGPU(s VMSpec) string {
	gpus := s.ResolvedGPUs()
	if len(gpus) == 0 {
		return ""
	}
	min := gpus[0]
	for _, g := range gpus[1:] {
		if g < min {
			min = g
		}
	}
	return min
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
		case VMSortGPU:
			// Lexicographic sort on the VM's smallest assigned GPU PCI
			// address (canonical long form via NormalizePCIAddress, so
			// "01:00.0" collates identically to "0000:01:00.0").
			// Symmetric sort counterpart to the `?gpu=` filter (5.7.9)
			// so the same passthrough cohort can be both filtered and
			// sorted on the same column. VMs with no requested GPUs
			// sink to the tail in asc / head in desc, mirroring the
			// nil-trailing semantics on every other nullable axis (ip,
			// guest_ip, image, last_fired_at, last_delivery_at, actor).
			aiG, ajG := smallestGPU(ai.Spec), smallestGPU(aj.Spec)
			switch {
			case aiG == "" && ajG == "":
				less = ai.ID < aj.ID
			case aiG == "":
				less = false
			case ajG == "":
				less = true
			case aiG != ajG:
				less = aiG < ajG
			default:
				less = ai.ID < aj.ID
			}
		case VMSortOSType:
			// Case-insensitive compare on the VM's *effective* OS family
			// via ResolvedOSType (5.4.100). Symmetric sort counterpart to
			// the case-insensitive `?os_type=` exact-match filter (5.6.8)
			// so the same OS-family cohort can be both filtered and
			// sorted on the same column. Diverges from the nil-trailing
			// convention on `ip` / `image` / `actor` because this column
			// has a documented default — an empty stored `spec.os_type`
			// resolves to `linux` (mirrors VMSpec.ResolvedOSType and the
			// `?os_type=linux` empty-means-linux filter contract) so
			// empty VMs collate with explicit-linux VMs rather than
			// sinking to the tail. The closed-and-total classification
			// guarantees every VM resolves to exactly one of `linux` <
			// `windows`, mirroring the `default_user` documented-default
			// rationale (5.4.91).
			aiOS := strings.ToLower(string(ai.Spec.ResolvedOSType()))
			ajOS := strings.ToLower(string(aj.Spec.ResolvedOSType()))
			if aiOS != ajOS {
				less = aiOS < ajOS
				break
			}
			less = ai.ID < aj.ID
		case VMSortOSVariant:
			// Case-insensitive compare on the VM's `spec.os_variant` field
			// (5.4.103). Symmetric sort counterpart to the case-insensitive
			// `?os_variant=` exact-match filter (5.4.66) so the same Windows
			// edition cohort can be both filtered and sorted on the same
			// column. Unlike `os_type` (5.4.100) and `firmware` (5.4.101),
			// `os_variant` has NO documented default — an empty stored value
			// means "operator did not specify an edition", typically because
			// the VM is a Linux guest (where the field is genuinely absent /
			// not applicable). So empty VMs sink to the tail of asc / head of
			// desc, mirroring the nil-trailing semantics on `image` /
			// `default_user` (template) / `actor` / `ip` rather than collapsing
			// to a default like `os_type` does. Alphabetical Windows edition
			// ordering: windows-10 < windows-11 < windows-server-2019 <
			// windows-server-2022 < windows-server-2025.
			aiV, ajV := strings.ToLower(strings.TrimSpace(ai.Spec.OSVariant)), strings.ToLower(strings.TrimSpace(aj.Spec.OSVariant))
			switch {
			case aiV == "" && ajV == "":
				less = ai.ID < aj.ID
			case aiV == "":
				less = false
			case ajV == "":
				less = true
			case aiV != ajV:
				less = aiV < ajV
			default:
				less = ai.ID < aj.ID
			}
		case VMSortFirmware:
			// Case-insensitive compare on the VM's effective firmware via
			// resolveFirmware (5.4.101). Symmetric sort counterpart to the
			// case-insensitive `?firmware=` exact-match filter (5.4.68) so
			// the same firmware cohort can be both filtered and sorted on
			// the same column. Alphabetical: bios < ovmf < uefi. Diverges
			// from the nil-trailing convention on `ip` / `image` / `gpu`
			// because this column has a documented default — an empty
			// stored `spec.firmware` resolves to `bios` (mirrors the
			// `?firmware=bios` empty-means-bios filter contract and the
			// SeaBIOS default surfaced through ResolvedFirmwareAttr) so
			// empty VMs collate with explicit-bios VMs rather than sinking
			// to the tail. Same documented-default rationale as the
			// `os_type` axis (5.4.100) collapsing empty to `linux` and the
			// `default_user` axis (5.4.91) collapsing empty to `root`. The
			// closed-and-total classification guarantees every VM resolves
			// to exactly one of the three values.
			aiFW := resolveFirmware(ai.Spec.Firmware)
			ajFW := resolveFirmware(aj.Spec.Firmware)
			if aiFW != ajFW {
				less = aiFW < ajFW
				break
			}
			less = ai.ID < aj.ID
		case VMSortDiskBus:
			// Case-insensitive compare on the VM's *effective* system-disk
			// bus via VMSpec.ResolvedDiskBus (5.4.104). Symmetric sort
			// counterpart to the case-insensitive `?disk_bus=` exact-match
			// filter on the same column so the same disk-bus cohort can be
			// both filtered and sorted on the same column. Alphabetical:
			// sata < virtio. Diverges from the nil-trailing convention on
			// `ip` / `image` / `gpu` because this column has a documented
			// default — an empty stored `spec.disk_bus` resolves to the
			// OS-family default (`virtio` for Linux, `sata` for Windows)
			// via ResolvedDiskBus so empty VMs collate with explicit-bus
			// VMs of the same OS family rather than sinking to the tail.
			// Mirrors the `?disk_bus=` filter contract that already treats
			// an empty stored bus as the OS-family default. The
			// closed-and-total classification guarantees every VM resolves
			// to exactly one of the two values. An explicit `spec.disk_bus`
			// always wins over the OS-family default, so a Windows guest
			// flipped to virtio after the operator installs the virtio-blk
			// drivers in-guest via 5.6.12 collates with the virtio cohort
			// rather than the sata cohort.
			aiDB := strings.ToLower(ai.Spec.ResolvedDiskBus())
			ajDB := strings.ToLower(aj.Spec.ResolvedDiskBus())
			if aiDB != ajDB {
				less = aiDB < ajDB
				break
			}
			less = ai.ID < aj.ID
		case VMSortNICModel:
			// Case-insensitive compare on the VM's *effective* NIC model via
			// VMSpec.ResolvedNICModel (5.4.105). Symmetric sort counterpart to
			// the case-insensitive `?nic_model=` exact-match filter on the
			// same column so the same NIC-model cohort can be both filtered
			// and sorted on the same column. Alphabetical: e1000e < virtio.
			// Diverges from the nil-trailing convention on `ip` / `image` /
			// `gpu` because this column has a documented OS-family-aware
			// default — an empty stored `spec.nic_model` resolves to `virtio`
			// for Linux guests and `e1000e` for Windows guests (mirrors the
			// `?nic_model=virtio` empty-defaults-to-OS-family filter contract
			// and the runtime semantics in `lifecycle.go`) so empty VMs
			// collate with explicit-model VMs of the same OS family rather
			// than sinking to the tail. Same OS-family-aware default
			// rationale as the `disk_bus` axis (5.4.104). An explicit
			// `spec.nic_model` always wins over the OS-family default, so a
			// Windows guest flipped to virtio after the operator installs the
			// virtio-net drivers in-guest via the 5.6.12 switch-to-virtio
			// helper collates with the virtio cohort rather than the e1000e
			// cohort.
			aiNM := strings.ToLower(ai.Spec.ResolvedNICModel())
			ajNM := strings.ToLower(aj.Spec.ResolvedNICModel())
			if aiNM != ajNM {
				less = aiNM < ajNM
				break
			}
			less = ai.ID < aj.ID
		case VMSortMachine:
			// Case-sensitive compare on the VM's *effective* libvirt machine
			// type via VMSpec.ResolvedMachine (5.4.107). Symmetric sort
			// counterpart to the case-sensitive `?machine=` exact-match
			// filter (5.4.69) on the same column so the same machine-type
			// cohort can be both filtered and sorted on the same column.
			// Diverges from the nil-trailing convention on `ip` / `image` /
			// `gpu` because this column has a documented default — an empty
			// stored `spec.machine` resolves to `DefaultMachine`
			// (`pc-q35-6.2`) via ResolvedMachine so empty VMs collate with
			// explicit-default VMs rather than sinking to the tail. Mirrors
			// the `?machine=pc-q35-6.2` empty-defaults-to-default filter
			// contract and the SeaBIOS-style documented-default rationale
			// of the `firmware` axis (5.4.101), though unlike the
			// closed-and-total enums on `disk_bus` / `nic_model` /
			// `clock_offset`, machine is a free-form bounded-alphabet
			// value (`[A-Za-z0-9._-]+`) so case-sensitive ordering
			// preserves the operator's chosen casing — libvirt machine
			// names like `pc-q35-6.2`, `q35`, and `virt-7.2` are
			// case-sensitive at the QEMU layer, mirroring the
			// case-sensitive filter contract on the same column.
			aiM := ai.Spec.ResolvedMachine()
			ajM := aj.Spec.ResolvedMachine()
			if aiM != ajM {
				less = aiM < ajM
				break
			}
			less = ai.ID < aj.ID
		case VMSortClockOffset:
			// Case-insensitive compare on the VM's *effective* clock offset
			// via VMSpec.ResolvedClockOffset (5.4.106). Symmetric sort
			// counterpart to the case-insensitive `?clock_offset=` exact-match
			// filter on the same column so the same clock-offset cohort can be
			// both filtered and sorted on the same column. Alphabetical:
			// localtime < utc. Diverges from the nil-trailing convention on
			// `ip` / `image` / `gpu` because this column has a documented
			// OS-family-aware default — an empty stored `spec.clock_offset`
			// resolves to the OS-family default (`utc` for Linux, `localtime`
			// for Windows) via ResolvedClockOffset so empty VMs collate with
			// explicit-offset VMs of the same OS family rather than sinking
			// to the tail. Mirrors the `?clock_offset=` filter contract that
			// already treats an empty stored offset as the OS-family default.
			// Same documented-default rationale as the `disk_bus` axis
			// (5.4.104) and the `nic_model` axis (5.4.105), though the
			// resolved value depends on the VM's OS family rather than being
			// a constant — the closed-and-total classification (`utc` /
			// `localtime`) guarantees every VM resolves to exactly one of
			// the two values. An explicit `spec.clock_offset` always wins
			// over the OS-family default, so a Windows guest pinned to utc
			// for an NTP-synced fleet collates with the utc cohort rather
			// than the localtime cohort.
			aiCO := strings.ToLower(ai.Spec.ResolvedClockOffset())
			ajCO := strings.ToLower(aj.Spec.ResolvedClockOffset())
			if aiCO != ajCO {
				less = aiCO < ajCO
				break
			}
			less = ai.ID < aj.ID
		case VMSortAutoStart:
			// Boolean compare on `spec.auto_start`. The symmetric sort
			// counterpart to the tristate `?auto_start=true|false`
			// exact-match filter on the same column so the same
			// auto-start cohort can be both filtered and sorted on the
			// same column. Asc collation: false < true (disabled cohort
			// at the head, enabled at the tail); desc inverts so the
			// auto-starting VMs operators actually care about at boot
			// surface first. The column is a non-nullable boolean — the
			// JSON tag is `json:"auto_start"` without `omitempty` so an
			// absent payload key is treated as the zero value (false).
			// Closed-and-total: every VM resolves to exactly one of the
			// two values, so there is no nil-trailing bucket — same
			// rationale as the `state` axis on the closed `running` /
			// `stopped` / `paused` enum and unlike the nullable string
			// axes `ip` / `image` / `guest_ip` / `actor` that sink to
			// the tail when empty.
			if ai.Spec.AutoStart != aj.Spec.AutoStart {
				less = !ai.Spec.AutoStart && aj.Spec.AutoStart
				break
			}
			less = ai.ID < aj.ID
		case VMSortLocked:
			// Boolean compare on `spec.locked` (delete-protection).
			// The symmetric sort counterpart to the tristate
			// `?locked=true|false` exact-match filter on the same
			// column so the same delete-protection cohort can be both
			// filtered and sorted on the same column. Asc collation:
			// false < true — unlocked cohort heads the list, the
			// locked cohort (operators routinely audit before a fleet
			// rebuild) sinks to the tail; desc surfaces the locked
			// cohort first, the natural ordering for safety review.
			// Closed-and-total: `VMSpec.Locked` is `json:"locked"`
			// without `omitempty` so a missing wire key resolves to
			// the zero value (false) and every VM belongs to exactly
			// one of the two buckets. Same rationale as the
			// `auto_start` axis (5.4.108) and the `state` axis on the
			// closed running/stopped/paused enum — no nil-trailing
			// bucket, unlike the nullable string axes `ip` / `image`
			// / `gpu` that sink empty stored values to the tail.
			if ai.Spec.Locked != aj.Spec.Locked {
				less = !ai.Spec.Locked && aj.Spec.Locked
				break
			}
			less = ai.ID < aj.ID
		case VMSortNatStaticIP:
			// Numeric IP compare on the IP portion of `spec.nat_static_ip`.
			// The symmetric sort counterpart to the `?nat_static_ip=`
			// exact-match filter (5.4.79) so the same configured-static
			// cohort can be both filtered and sorted on the same column.
			// The stored value is a CIDR (e.g. `192.168.100.10/24`) but
			// natStaticIPSortKey strips the suffix so the comparator
			// sees a bare address that net.ParseIP can lift — mirrors
			// the `?nat_static_ip=` filter contract which accepts the
			// filter as either the full CIDR OR just the IP portion.
			// Compared byte-wise on the canonical 16-byte To16() form
			// via compareVMIP so `192.168.100.2` sorts before
			// `192.168.100.10` instead of lexicographically. VMs with
			// an empty or unparseable `nat_static_ip` (DHCP-assigned)
			// sink to the tail of asc / head of desc, mirroring the
			// nil-trailing semantics on the `ip` sort axis and the
			// empty-stored-excludes contract on the `?nat_static_ip=`
			// filter. Same nil-trailing rationale as the nullable
			// string axes `ip` / `image` / `guest_ip` / `actor`,
			// unlike the documented-default boolean axes `auto_start`
			// / `locked` that have no nil-trailing bucket.
			cmp := compareVMIP(natStaticIPSortKey(ai.Spec.NatStaticIP), natStaticIPSortKey(aj.Spec.NatStaticIP))
			if cmp != 0 {
				less = cmp < 0
				break
			}
			less = ai.ID < aj.ID
		case VMSortNatGateway:
			// Numeric IP compare on `spec.nat_gateway` (the configured
			// NAT gateway, stored as a bare IP — no CIDR dual-form
			// like nat_static_ip). Symmetric sort counterpart to the
			// `?nat_gateway=` exact-match filter (5.4.80) so the same
			// gateway cohort can be both filtered and sorted on the
			// same column. Compared byte-wise on the canonical
			// 16-byte To16() form via compareVMIP so `192.168.100.2`
			// sorts before `192.168.100.10` instead of
			// lexicographically — same numeric IP comparator as the
			// `ip` (5.4.85) and `nat_static_ip` (5.4.110) sort axes.
			// VMs with an empty or unparseable `nat_gateway` (no
			// explicit gateway override) sink to the tail of asc /
			// head of desc, mirroring the nil-trailing semantics on
			// the `ip` / `nat_static_ip` sort axes and the
			// empty-stored-excludes contract on the `?nat_gateway=`
			// filter — same nil-trailing rationale as the nullable
			// string axes `ip` / `image` / `gpu` / `actor`, unlike
			// the closed-and-total boolean axes `auto_start` /
			// `locked` that have no nil-trailing bucket.
			cmp := compareVMIP(ai.Spec.NatGateway, aj.Spec.NatGateway)
			if cmp != 0 {
				less = cmp < 0
				break
			}
			less = ai.ID < aj.ID
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
		case VMSortDescription:
			// Case-insensitive compare on `spec.description` so operators
			// can paste a description verbatim — descriptions are free-form
			// text the operator chose, and matching `Web Prod` against
			// `web prod` should land in the same bucket. Mirrors the
			// case-insensitive haystack in the existing `?search=` filter
			// on the VM list, so the same description-based query surface
			// is filtered (substring) and sorted (alphabetical) on the
			// same case-insensitive semantics. VMs with an empty
			// `spec.description` (the common case — most VMs get no
			// description) sink to the tail of asc / head of desc,
			// mirroring the nil-trailing semantics on every other nullable
			// string sort axis (ip, image, gpu, actor, last_fired_at,
			// last_delivery_at) rather than collapsing to a default like
			// the documented-default axes (os_type → linux, firmware →
			// bios, default_user → root) — there is no documented default
			// for description because the field is genuinely "operator did
			// not bother to write one". Same axis rationale as the image
			// list `description` axis (5.4.118) and the template list
			// `description` axis (5.4.119) one resource over.
			aiD, ajD := strings.ToLower(ai.Spec.Description), strings.ToLower(aj.Spec.Description)
			switch {
			case aiD == "" && ajD == "":
				less = ai.ID < aj.ID
			case aiD == "":
				less = false
			case ajD == "":
				less = true
			case aiD != ajD:
				less = aiD < ajD
			default:
				less = ai.ID < aj.ID
			}
		default: // VMSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
