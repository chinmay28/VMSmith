package types

import (
	"testing"
	"time"
)

func names(vms []*VM) []string {
	out := make([]string, len(vms))
	for i, v := range vms {
		out[i] = v.Name
	}
	return out
}

func TestSortVMs_ByID_Asc(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Name: "c"},
		{ID: "vm-1", Name: "a"},
		{ID: "vm-2", Name: "b"},
	}
	SortVMs(vms, VMSortID, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByName_CaseInsensitive(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Name: "Charlie"},
		{ID: "vm-1", Name: "alpha"},
		{ID: "vm-2", Name: "Bravo"},
	}
	SortVMs(vms, VMSortName, SortOrderAsc)
	got := names(vms)
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortVMs_ByCreatedAt_DescTiebreaksOnID(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	vms := []*VM{
		{ID: "vm-2", Name: "b", CreatedAt: t0},
		{ID: "vm-1", Name: "a", CreatedAt: t0}, // same timestamp
		{ID: "vm-3", Name: "c", CreatedAt: t0.Add(time.Hour)},
	}
	SortVMs(vms, VMSortCreatedAt, SortOrderDesc)
	// Newest first; equal-time pair should reverse on tiebreak too
	// because the descending wrapper inverts the entire compare result.
	got := names(vms)
	want := []string{"c", "b", "a"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortVMs_ByState_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Name: "c", State: VMStateStopped},
		{ID: "vm-2", Name: "b", State: VMStateRunning},
		{ID: "vm-1", Name: "a", State: VMStateRunning},
	}
	SortVMs(vms, VMSortState, SortOrderAsc)
	// "running" < "stopped" lexicographically
	if vms[0].ID != "vm-1" || vms[1].ID != "vm-2" || vms[2].ID != "vm-3" {
		t.Errorf("got %q,%q,%q want vm-1,vm-2,vm-3", vms[0].ID, vms[1].ID, vms[2].ID)
	}
}

func TestSortVMs_ByCPUs_AscTiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-2", Name: "b", Spec: VMSpec{CPUs: 4}},
		{ID: "vm-1", Name: "a", Spec: VMSpec{CPUs: 4}}, // tie with vm-2
		{ID: "vm-3", Name: "c", Spec: VMSpec{CPUs: 1}},
		{ID: "vm-4", Name: "d", Spec: VMSpec{CPUs: 8}},
	}
	SortVMs(vms, VMSortCPUs, SortOrderAsc)
	got := []string{vms[0].ID, vms[1].ID, vms[2].ID, vms[3].ID}
	want := []string{"vm-3", "vm-1", "vm-2", "vm-4"} // 1 < 4(vm-1<vm-2) < 8
	for i, id := range got {
		if id != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, id, want[i], got)
		}
	}
}

func TestSortVMs_ByCPUs_Desc(t *testing.T) {
	vms := []*VM{
		{ID: "vm-1", Name: "a", Spec: VMSpec{CPUs: 1}},
		{ID: "vm-2", Name: "b", Spec: VMSpec{CPUs: 4}},
		{ID: "vm-3", Name: "c", Spec: VMSpec{CPUs: 8}},
	}
	SortVMs(vms, VMSortCPUs, SortOrderDesc)
	got := []string{vms[0].ID, vms[1].ID, vms[2].ID}
	want := []string{"vm-3", "vm-2", "vm-1"}
	for i, id := range got {
		if id != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, id, want[i])
		}
	}
}

func TestSortVMs_ByRAMMB_AscTiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-2", Name: "b", Spec: VMSpec{RAMMB: 2048}},
		{ID: "vm-1", Name: "a", Spec: VMSpec{RAMMB: 2048}}, // tie with vm-2
		{ID: "vm-3", Name: "c", Spec: VMSpec{RAMMB: 1024}},
	}
	SortVMs(vms, VMSortRAMMB, SortOrderAsc)
	if vms[0].ID != "vm-3" || vms[1].ID != "vm-1" || vms[2].ID != "vm-2" {
		t.Errorf("ram_mb asc with tie wrong: got %q,%q,%q want vm-3,vm-1,vm-2", vms[0].ID, vms[1].ID, vms[2].ID)
	}
}

func TestSortVMs_ByDiskGB_DescTiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-1", Name: "a", Spec: VMSpec{DiskGB: 100}},
		{ID: "vm-2", Name: "b", Spec: VMSpec{DiskGB: 100}}, // tie with vm-1
		{ID: "vm-3", Name: "c", Spec: VMSpec{DiskGB: 20}},
	}
	SortVMs(vms, VMSortDiskGB, SortOrderDesc)
	// Largest disk first; equal-disk pair should reverse on tiebreak too
	// because the descending wrapper inverts the entire compare result.
	if vms[0].ID != "vm-2" || vms[1].ID != "vm-1" || vms[2].ID != "vm-3" {
		t.Errorf("disk_gb desc with tie wrong: got %q,%q,%q want vm-2,vm-1,vm-3", vms[0].ID, vms[1].ID, vms[2].ID)
	}
}

func TestSortVMs_UnknownFieldFallsBackToID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, "memory", SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_StablePagination(t *testing.T) {
	// Repeated sorts on equal-key data must produce the same order so
	// page-2 of a paginated query matches page-1's continuation. This
	// test does not directly call paginate — it asserts that two
	// independent SortVMs invocations on the same input agree.
	make := func() []*VM {
		return []*VM{
			{ID: "vm-3", Name: "shared"},
			{ID: "vm-1", Name: "shared"},
			{ID: "vm-4", Name: "shared"},
			{ID: "vm-2", Name: "shared"},
		}
	}
	a := make()
	b := make()
	SortVMs(a, VMSortName, SortOrderAsc)
	SortVMs(b, VMSortName, SortOrderAsc)
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("idx %d: a=%q b=%q — equal-name tie not deterministic", i, a[i].ID, b[i].ID)
		}
	}
}

func TestSortVMs_ByIP_Numeric_AscEmptyTrailing(t *testing.T) {
	// 192.168.100.10 must sort after 192.168.100.2 by numeric comparison —
	// a lexicographic compare would place "10" before "2" because "1" < "2".
	// Empty IPs (stopped or no-lease VMs) sink to the tail in ascending order
	// so a paginated by-IP view groups the live cohort first.
	vms := []*VM{
		{ID: "vm-1", IP: "192.168.100.10"},
		{ID: "vm-2", IP: ""},
		{ID: "vm-3", IP: "192.168.100.2"},
	}
	SortVMs(vms, VMSortIP, SortOrderAsc)
	want := []string{"vm-3", "vm-1", "vm-2"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q ip=%q, want %q", i, v.ID, v.IP, want[i])
		}
	}
}

func TestSortVMs_ByIP_DescEmptyLeading(t *testing.T) {
	vms := []*VM{
		{ID: "vm-1", IP: "192.168.100.10"},
		{ID: "vm-2", IP: ""},
		{ID: "vm-3", IP: "192.168.100.2"},
	}
	SortVMs(vms, VMSortIP, SortOrderDesc)
	// Empty leads in descending; concrete addresses follow newest-numeric-first.
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q ip=%q, want %q", i, v.ID, v.IP, want[i])
		}
	}
}

func TestSortVMs_ByIP_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", IP: "10.0.0.1"},
		{ID: "vm-1", IP: "10.0.0.1"},
		{ID: "vm-2", IP: "10.0.0.1"},
	}
	SortVMs(vms, VMSortIP, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByIP_AllEmpty_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, VMSortIP, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByIP_IPv4AndIPv6Interleave(t *testing.T) {
	// IPv4 in canonical 16-byte form has a leading run of zero bytes followed
	// by 0x00 0x00 0xff 0xff, so an IPv4-mapped address sorts numerically
	// against arbitrary IPv6 literals. Asserts that the compare is bytes.Compare
	// on To16() rather than a string compare.
	vms := []*VM{
		{ID: "vm-1", IP: "fe80::1"},
		{ID: "vm-2", IP: "192.168.100.5"},
		{ID: "vm-3", IP: "::1"},
	}
	SortVMs(vms, VMSortIP, SortOrderAsc)
	want := []string{"vm-3", "vm-2", "vm-1"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q ip=%q, want %q", i, v.ID, v.IP, want[i])
		}
	}
}

func TestSortVMs_ByIP_GarbageSortsAsEmpty(t *testing.T) {
	// Unparseable strings are treated like empty — they sink to the tail
	// in ascending order. Matches the contract on the runtime-IP filter
	// (5.4.81), which simply matches no VMs on garbage input.
	vms := []*VM{
		{ID: "vm-1", IP: "10.0.0.5"},
		{ID: "vm-2", IP: "not-an-ip"},
		{ID: "vm-3", IP: "10.0.0.1"},
	}
	SortVMs(vms, VMSortIP, SortOrderAsc)
	want := []string{"vm-3", "vm-1", "vm-2"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q ip=%q, want %q", i, v.ID, v.IP, want[i])
		}
	}
}

func TestIsValidVMSort_AcceptsIP(t *testing.T) {
	for _, axis := range []string{"id", "name", "created_at", "state", "cpus", "ram_mb", "disk_gb", "ip"} {
		if !IsValidVMSort(axis) {
			t.Errorf("IsValidVMSort(%q) = false, want true", axis)
		}
	}
	for _, axis := range []string{"", "memory", "IP", "address"} {
		if IsValidVMSort(axis) {
			t.Errorf("IsValidVMSort(%q) = true, want false", axis)
		}
	}
}
