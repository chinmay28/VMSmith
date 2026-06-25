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

// ============================================================
// `image` sort axis (5.4.88)
// ============================================================

func TestSortVMs_ByImage_AscEmptyTrailing(t *testing.T) {
	// Concrete images sort case-insensitively; the empty-image VM sinks to
	// the tail in ascending order, mirroring the nil-trailing contract on
	// every other nullable sort axis (ip / guest_ip / last_fired_at / actor).
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{Image: "rocky9.qcow2"}},
		{ID: "vm-2", Spec: VMSpec{Image: ""}},
		{ID: "vm-3", Spec: VMSpec{Image: "alpine.qcow2"}},
	}
	SortVMs(vms, VMSortImage, SortOrderAsc)
	want := []string{"vm-3", "vm-1", "vm-2"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q image=%q, want %q", i, v.ID, v.Spec.Image, want[i])
		}
	}
}

func TestSortVMs_ByImage_DescEmptyLeading(t *testing.T) {
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{Image: "rocky9.qcow2"}},
		{ID: "vm-2", Spec: VMSpec{Image: ""}},
		{ID: "vm-3", Spec: VMSpec{Image: "alpine.qcow2"}},
	}
	SortVMs(vms, VMSortImage, SortOrderDesc)
	// Empty leads in descending; concrete images then sort reverse-alphabetic.
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q image=%q, want %q", i, v.ID, v.Spec.Image, want[i])
		}
	}
}

func TestSortVMs_ByImage_CaseInsensitive(t *testing.T) {
	// Mixed-case base image names — `Rocky9.qcow2` and `rocky9.qcow2`
	// must collate as identical so the sort agrees with the
	// case-insensitive `?image=` filter (5.4.22). Without lowering, the
	// uppercase `R` (0x52) would sort before lowercase `a` (0x61) and split
	// the rocky cohort apart, breaking the cohort-discovery operator query.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{Image: "Rocky9.qcow2"}},
		{ID: "vm-2", Spec: VMSpec{Image: "alpine.qcow2"}},
		{ID: "vm-3", Spec: VMSpec{Image: "rocky9.qcow2"}},
	}
	SortVMs(vms, VMSortImage, SortOrderAsc)
	// asc: alpine < rocky9 (case-folded). The two rocky9 entries tiebreak
	// on id ascending (vm-1 before vm-3).
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q image=%q, want %q", i, v.ID, v.Spec.Image, want[i])
		}
	}
}

func TestSortVMs_ByImage_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Spec: VMSpec{Image: "rocky9.qcow2"}},
		{ID: "vm-1", Spec: VMSpec{Image: "rocky9.qcow2"}},
		{ID: "vm-2", Spec: VMSpec{Image: "rocky9.qcow2"}},
	}
	SortVMs(vms, VMSortImage, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByImage_AllEmpty_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, VMSortImage, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestIsValidVMSort_AcceptsImage(t *testing.T) {
	if !IsValidVMSort(VMSortImage) {
		t.Fatalf("IsValidVMSort(%q) = false, want true", VMSortImage)
	}
	// Case sensitivity at the parse layer: the API/CLI lower-case the value
	// before calling IsValidVMSort, so `Image` is rejected at this layer.
	if IsValidVMSort("Image") {
		t.Errorf("IsValidVMSort(%q) = true, want false", "Image")
	}
}

// ============================================================
// `default_user` sort axis (5.4.91)
// ============================================================

func TestSortVMs_ByDefaultUser_AscEmptyResolvesToRoot(t *testing.T) {
	// Diverges from the nil-trailing convention on `ip` / `image` / `actor`
	// because `default_user` has a documented default. Empty stored values
	// resolve to "root" so they collate with explicit-root VMs in the
	// alphabetic ordering (`a` < `e` < `r`). The empty-stored row sorts
	// alongside vm-2 (explicit `root`), not at the tail.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{DefaultUser: "ec2-user"}},
		{ID: "vm-2", Spec: VMSpec{DefaultUser: "root"}},
		{ID: "vm-3", Spec: VMSpec{DefaultUser: ""}}, // resolves to "root"
		{ID: "vm-4", Spec: VMSpec{DefaultUser: "admin"}},
	}
	SortVMs(vms, VMSortDefaultUser, SortOrderAsc)
	// asc: admin < ec2-user < root; vm-2 and vm-3 both resolve to "root"
	// and tiebreak on id ascending so vm-2 precedes vm-3.
	want := []string{"vm-4", "vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q default_user=%q, want %q", i, v.ID, v.Spec.DefaultUser, want[i])
		}
	}
}

func TestSortVMs_ByDefaultUser_DescEmptyResolvesToRoot(t *testing.T) {
	// Desc order also resolves empty to "root", so the empty row sits in
	// the `r` bucket reversed (vm-3 trails vm-2 in desc tiebreak inversion).
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{DefaultUser: "ec2-user"}},
		{ID: "vm-2", Spec: VMSpec{DefaultUser: "root"}},
		{ID: "vm-3", Spec: VMSpec{DefaultUser: ""}}, // resolves to "root"
		{ID: "vm-4", Spec: VMSpec{DefaultUser: "admin"}},
	}
	SortVMs(vms, VMSortDefaultUser, SortOrderDesc)
	// desc reverses the entire compare result, so vm-3 leads the root
	// cohort (tiebreak on id flips too).
	want := []string{"vm-3", "vm-2", "vm-1", "vm-4"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q default_user=%q, want %q", i, v.ID, v.Spec.DefaultUser, want[i])
		}
	}
}

func TestSortVMs_ByDefaultUser_CaseInsensitive(t *testing.T) {
	// `ROOT` and `root` collate as identical so the sort agrees with the
	// case-insensitive `?default_user=` filter (5.4.23). Without lowering,
	// the uppercase `R` (0x52) would sort before lowercase `a` (0x61) and
	// split the root cohort apart.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{DefaultUser: "ROOT"}},
		{ID: "vm-2", Spec: VMSpec{DefaultUser: "admin"}},
		{ID: "vm-3", Spec: VMSpec{DefaultUser: "root"}},
	}
	SortVMs(vms, VMSortDefaultUser, SortOrderAsc)
	// asc: admin < root (case-folded). Equal-user cohort tiebreaks on id
	// ascending so vm-1 ("ROOT") precedes vm-3 ("root").
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q default_user=%q, want %q", i, v.ID, v.Spec.DefaultUser, want[i])
		}
	}
}

func TestSortVMs_ByDefaultUser_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Spec: VMSpec{DefaultUser: "ops-alice"}},
		{ID: "vm-1", Spec: VMSpec{DefaultUser: "ops-alice"}},
		{ID: "vm-2", Spec: VMSpec{DefaultUser: "ops-alice"}},
	}
	SortVMs(vms, VMSortDefaultUser, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByDefaultUser_AllEmpty_TiebreaksOnID(t *testing.T) {
	// All-empty VMs all resolve to "root" so they tiebreak on id.
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, VMSortDefaultUser, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestIsValidVMSort_AcceptsDefaultUser(t *testing.T) {
	if !IsValidVMSort(VMSortDefaultUser) {
		t.Fatalf("IsValidVMSort(%q) = false, want true", VMSortDefaultUser)
	}
	if IsValidVMSort("Default_User") {
		t.Errorf("IsValidVMSort(%q) = true, want false", "Default_User")
	}
}

// ============================================================
// `gpu` sort axis (5.7.13)
// ============================================================

func TestSortVMs_ByGPU_AscEmptyTrailing(t *testing.T) {
	// Concrete GPUs sort lexicographically on the canonical long form;
	// VMs with no requested GPUs sink to the tail in ascending order,
	// mirroring the nil-trailing contract on every other nullable sort
	// axis (ip / guest_ip / image / last_fired_at / actor).
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{GPUs: []string{"0000:02:00.0"}}},
		{ID: "vm-2", Spec: VMSpec{}},
		{ID: "vm-3", Spec: VMSpec{GPUs: []string{"0000:01:00.0"}}},
	}
	SortVMs(vms, VMSortGPU, SortOrderAsc)
	want := []string{"vm-3", "vm-1", "vm-2"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q gpus=%v, want %q", i, v.ID, v.Spec.GPUs, want[i])
		}
	}
}

func TestSortVMs_ByGPU_DescEmptyLeading(t *testing.T) {
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{GPUs: []string{"0000:02:00.0"}}},
		{ID: "vm-2", Spec: VMSpec{}},
		{ID: "vm-3", Spec: VMSpec{GPUs: []string{"0000:01:00.0"}}},
	}
	SortVMs(vms, VMSortGPU, SortOrderDesc)
	// Empty leads in descending; concrete GPUs then sort reverse-lexicographic.
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q gpus=%v, want %q", i, v.ID, v.Spec.GPUs, want[i])
		}
	}
}

func TestSortVMs_ByGPU_NormalisesShortForm(t *testing.T) {
	// A VM persisted with the short PCI form ("01:00.0") must collate
	// identically to one persisted with the long form ("0000:01:00.0") so
	// the sort agrees with the same-form alphabet contract on `?gpu=`
	// (5.7.9) and `vmsmith vm create --gpu`. Without normalisation,
	// lexicographic compare on the raw string would sort the short form
	// after every concrete long-form entry, breaking cohort discovery.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{GPUs: []string{"0000:02:00.0"}}},
		{ID: "vm-2", Spec: VMSpec{GPUs: []string{"01:00.0"}}}, // short form
		{ID: "vm-3", Spec: VMSpec{GPUs: []string{"0000:03:00.0"}}},
	}
	SortVMs(vms, VMSortGPU, SortOrderAsc)
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q gpus=%v, want %q", i, v.ID, v.Spec.GPUs, want[i])
		}
	}
}

func TestSortVMs_ByGPU_MultiGPUUsesSmallestSlot(t *testing.T) {
	// A multi-GPU VM is positioned by its lexicographically-smallest GPU
	// (the operator's "primary slot"). vm-1 has [02, 04] so its smallest
	// is 02; vm-2 has [01, 05] so its smallest is 01 and surfaces first
	// in asc; vm-3 has only [03] so it lands between.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{GPUs: []string{"0000:02:00.0", "0000:04:00.0"}}},
		{ID: "vm-2", Spec: VMSpec{GPUs: []string{"0000:01:00.0", "0000:05:00.0"}}},
		{ID: "vm-3", Spec: VMSpec{GPUs: []string{"0000:03:00.0"}}},
	}
	SortVMs(vms, VMSortGPU, SortOrderAsc)
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q gpus=%v, want %q", i, v.ID, v.Spec.GPUs, want[i])
		}
	}
}

func TestSortVMs_ByGPU_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Spec: VMSpec{GPUs: []string{"0000:01:00.0"}}},
		{ID: "vm-1", Spec: VMSpec{GPUs: []string{"0000:01:00.0"}}},
		{ID: "vm-2", Spec: VMSpec{GPUs: []string{"0000:01:00.0"}}},
	}
	SortVMs(vms, VMSortGPU, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByGPU_AllEmpty_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, VMSortGPU, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestIsValidVMSort_AcceptsGPU(t *testing.T) {
	if !IsValidVMSort(VMSortGPU) {
		t.Fatalf("IsValidVMSort(%q) = false, want true", VMSortGPU)
	}
	for _, axis := range []string{"GPU", "Gpu", "gpus", " gpu "} {
		if IsValidVMSort(axis) {
			t.Errorf("IsValidVMSort(%q) = true, want false (parser must normalise before lookup)", axis)
		}
	}
}

// ============================================================
// `os_type` sort axis (5.4.100)
// ============================================================

func TestSortVMs_ByOSType_AscEmptyResolvesToLinux(t *testing.T) {
	// Empty `os_type` resolves to "linux" via VMSpec.ResolvedOSType so the
	// unset VM collates with the explicit-linux VM in alphabetical order
	// rather than sinking to the tail. Diverges from the nil-trailing
	// image-sort contract because `os_type` has a documented default —
	// same rationale as the `default_user` axis (5.4.91) collapsing empty
	// to "root".
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{OSType: OSTypeWindows}},
		{ID: "vm-2", Spec: VMSpec{OSType: OSTypeLinux}},
		{ID: "vm-3", Spec: VMSpec{OSType: ""}}, // resolves to "linux"
		{ID: "vm-4", Spec: VMSpec{OSType: OSTypeWindows}},
	}
	SortVMs(vms, VMSortOSType, SortOrderAsc)
	// asc: linux < windows; vm-2 and vm-3 both resolve to "linux" and
	// tiebreak on id ascending so vm-2 precedes vm-3.
	want := []string{"vm-2", "vm-3", "vm-1", "vm-4"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q os_type=%q, want %q", i, v.ID, v.Spec.OSType, want[i])
		}
	}
}

func TestSortVMs_ByOSType_DescEmptyResolvesToLinux(t *testing.T) {
	// Desc reverses the entire compare result so windows VMs head the
	// list. Empty VMs still resolve to linux (the documented default),
	// so they sit in the linux bucket reversed (id-desc tiebreak).
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{OSType: OSTypeWindows}},
		{ID: "vm-2", Spec: VMSpec{OSType: OSTypeLinux}},
		{ID: "vm-3", Spec: VMSpec{OSType: ""}}, // resolves to "linux"
		{ID: "vm-4", Spec: VMSpec{OSType: OSTypeWindows}},
	}
	SortVMs(vms, VMSortOSType, SortOrderDesc)
	want := []string{"vm-4", "vm-1", "vm-3", "vm-2"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q os_type=%q, want %q", i, v.ID, v.Spec.OSType, want[i])
		}
	}
}

func TestSortVMs_ByOSType_CaseInsensitive(t *testing.T) {
	// `WINDOWS` and `windows` must collate as identical so the sort
	// agrees with the case-insensitive `?os_type=` filter (5.6.8). The
	// comparator lowers the resolved value before compare.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{OSType: "WINDOWS"}},
		{ID: "vm-2", Spec: VMSpec{OSType: "linux"}},
		{ID: "vm-3", Spec: VMSpec{OSType: "windows"}},
	}
	SortVMs(vms, VMSortOSType, SortOrderAsc)
	// asc: linux < windows. Equal-os cohort tiebreaks on id ascending
	// so vm-1 ("WINDOWS") precedes vm-3 ("windows").
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q os_type=%q, want %q", i, v.ID, v.Spec.OSType, want[i])
		}
	}
}

func TestSortVMs_ByOSType_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Spec: VMSpec{OSType: OSTypeWindows}},
		{ID: "vm-1", Spec: VMSpec{OSType: OSTypeWindows}},
		{ID: "vm-2", Spec: VMSpec{OSType: OSTypeWindows}},
	}
	SortVMs(vms, VMSortOSType, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByOSType_AllEmpty_TiebreaksOnID(t *testing.T) {
	// All-empty VMs all resolve to "linux" so they tiebreak on id.
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, VMSortOSType, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestIsValidVMSort_AcceptsOSType(t *testing.T) {
	if !IsValidVMSort(VMSortOSType) {
		t.Fatalf("IsValidVMSort(%q) = false, want true", VMSortOSType)
	}
	for _, axis := range []string{"OS_TYPE", "Os_Type", "ostype", " os_type "} {
		if IsValidVMSort(axis) {
			t.Errorf("IsValidVMSort(%q) = true, want false (parser must normalise before lookup)", axis)
		}
	}
}

// 5.4.101 — case-insensitive `firmware` sort axis with empty→bios resolution.

func TestSortVMs_ByFirmware_AscEmptyResolvesToBIOS(t *testing.T) {
	// Empty `firmware` resolves to "bios" via resolveFirmware so the unset
	// VM collates with the explicit-bios VM in alphabetical order rather
	// than sinking to the tail. Diverges from the nil-trailing image-sort
	// contract because `firmware` has a documented default — same rationale
	// as the `os_type` axis (5.4.100) collapsing empty to "linux" and the
	// `default_user` axis (5.4.91) collapsing empty to "root".
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{Firmware: FirmwareUEFI}},
		{ID: "vm-2", Spec: VMSpec{Firmware: FirmwareBIOS}},
		{ID: "vm-3", Spec: VMSpec{Firmware: ""}}, // resolves to "bios"
		{ID: "vm-4", Spec: VMSpec{Firmware: FirmwareOVMF}},
	}
	SortVMs(vms, VMSortFirmware, SortOrderAsc)
	// asc: bios < ovmf < uefi; vm-2 and vm-3 both resolve to "bios" and
	// tiebreak on id ascending so vm-2 precedes vm-3.
	want := []string{"vm-2", "vm-3", "vm-4", "vm-1"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q firmware=%q, want %q", i, v.ID, v.Spec.Firmware, want[i])
		}
	}
}

func TestSortVMs_ByFirmware_DescEmptyResolvesToBIOS(t *testing.T) {
	// Desc reverses the entire compare result so uefi VMs head the list,
	// then ovmf, then the bios bucket (with the empty-stored vm-3 leading
	// vm-2 because the id tiebreak also inverts).
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{Firmware: FirmwareUEFI}},
		{ID: "vm-2", Spec: VMSpec{Firmware: FirmwareBIOS}},
		{ID: "vm-3", Spec: VMSpec{Firmware: ""}}, // resolves to "bios"
		{ID: "vm-4", Spec: VMSpec{Firmware: FirmwareOVMF}},
	}
	SortVMs(vms, VMSortFirmware, SortOrderDesc)
	want := []string{"vm-1", "vm-4", "vm-3", "vm-2"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q firmware=%q, want %q", i, v.ID, v.Spec.Firmware, want[i])
		}
	}
}

func TestSortVMs_ByFirmware_CaseInsensitive(t *testing.T) {
	// `UEFI` and `uefi` must collate as identical so the sort agrees with
	// the case-insensitive `?firmware=` filter (5.4.68). The resolver
	// lowers + trims the stored value before compare.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{Firmware: "UEFI"}},
		{ID: "vm-2", Spec: VMSpec{Firmware: "bios"}},
		{ID: "vm-3", Spec: VMSpec{Firmware: "uefi"}},
		{ID: "vm-4", Spec: VMSpec{Firmware: " OVMF "}}, // whitespace + uppercase
	}
	SortVMs(vms, VMSortFirmware, SortOrderAsc)
	// asc: bios < ovmf < uefi. Equal-firmware cohort tiebreaks on id
	// ascending so vm-1 ("UEFI") precedes vm-3 ("uefi").
	want := []string{"vm-2", "vm-4", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q firmware=%q, want %q", i, v.ID, v.Spec.Firmware, want[i])
		}
	}
}

func TestSortVMs_ByFirmware_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Spec: VMSpec{Firmware: FirmwareUEFI}},
		{ID: "vm-1", Spec: VMSpec{Firmware: FirmwareUEFI}},
		{ID: "vm-2", Spec: VMSpec{Firmware: FirmwareUEFI}},
	}
	SortVMs(vms, VMSortFirmware, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByFirmware_AllEmpty_TiebreaksOnID(t *testing.T) {
	// All-empty VMs all resolve to "bios" so they tiebreak on id.
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, VMSortFirmware, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestIsValidVMSort_AcceptsFirmware(t *testing.T) {
	if !IsValidVMSort(VMSortFirmware) {
		t.Fatalf("IsValidVMSort(%q) = false, want true", VMSortFirmware)
	}
	for _, axis := range []string{"FIRMWARE", "Firmware", "firm", " firmware "} {
		if IsValidVMSort(axis) {
			t.Errorf("IsValidVMSort(%q) = true, want false (parser must normalise before lookup)", axis)
		}
	}
}

// 5.4.103 — case-insensitive `os_variant` sort axis with nil-trailing semantics.

func TestSortVMs_ByOSVariant_AscEmptyTrailing(t *testing.T) {
	// Unlike os_type (5.4.100) and firmware (5.4.101), os_variant has no
	// documented default — Linux VMs and VMs whose operator never specified
	// an edition genuinely have no value. Empty VMs sink to the tail in
	// ascending order, mirroring the nil-trailing semantics on image (5.4.88)
	// / gpu (5.7.13) / ip (5.4.85) / actor (5.4.87) rather than collapsing
	// to a default like os_type/firmware do.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{OSVariant: "windows-server-2022"}},
		{ID: "vm-2", Spec: VMSpec{OSVariant: ""}},
		{ID: "vm-3", Spec: VMSpec{OSVariant: "windows-10"}},
		{ID: "vm-4", Spec: VMSpec{OSVariant: "windows-11"}},
	}
	SortVMs(vms, VMSortOSVariant, SortOrderAsc)
	want := []string{"vm-3", "vm-4", "vm-1", "vm-2"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q os_variant=%q, want %q", i, v.ID, v.Spec.OSVariant, want[i])
		}
	}
}

func TestSortVMs_ByOSVariant_DescEmptyLeading(t *testing.T) {
	// Desc reverses the entire compare so empty VMs head the list (nil-leading
	// in desc is the symmetric counterpart to nil-trailing in asc), then
	// windows-server-2022, windows-11, windows-10.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{OSVariant: "windows-server-2022"}},
		{ID: "vm-2", Spec: VMSpec{OSVariant: ""}},
		{ID: "vm-3", Spec: VMSpec{OSVariant: "windows-10"}},
		{ID: "vm-4", Spec: VMSpec{OSVariant: "windows-11"}},
	}
	SortVMs(vms, VMSortOSVariant, SortOrderDesc)
	want := []string{"vm-2", "vm-1", "vm-4", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q os_variant=%q, want %q", i, v.ID, v.Spec.OSVariant, want[i])
		}
	}
}

func TestSortVMs_ByOSVariant_CaseInsensitive(t *testing.T) {
	// `Windows-11` and `windows-11` must collate identically so the sort
	// agrees with the case-insensitive `?os_variant=` filter (5.4.66). The
	// comparator lowercases + trims the stored value before compare.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{OSVariant: "WINDOWS-11"}},
		{ID: "vm-2", Spec: VMSpec{OSVariant: "windows-10"}},
		{ID: "vm-3", Spec: VMSpec{OSVariant: "windows-11"}},
		{ID: "vm-4", Spec: VMSpec{OSVariant: " Windows-10 "}}, // whitespace + uppercase
	}
	SortVMs(vms, VMSortOSVariant, SortOrderAsc)
	// asc: windows-10 < windows-11. Equal-edition cohort tiebreaks on id
	// ascending so vm-2 ("windows-10") precedes vm-4 (" Windows-10 ").
	want := []string{"vm-2", "vm-4", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q os_variant=%q, want %q", i, v.ID, v.Spec.OSVariant, want[i])
		}
	}
}

func TestSortVMs_ByOSVariant_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Spec: VMSpec{OSVariant: "windows-11"}},
		{ID: "vm-1", Spec: VMSpec{OSVariant: "windows-11"}},
		{ID: "vm-2", Spec: VMSpec{OSVariant: "windows-11"}},
	}
	SortVMs(vms, VMSortOSVariant, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByOSVariant_AllEmpty_TiebreaksOnID(t *testing.T) {
	// All-empty VMs all fall into the nil bucket so they tiebreak on id.
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, VMSortOSVariant, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestIsValidVMSort_AcceptsOSVariant(t *testing.T) {
	if !IsValidVMSort(VMSortOSVariant) {
		t.Fatalf("IsValidVMSort(%q) = false, want true", VMSortOSVariant)
	}
	for _, axis := range []string{"OS_VARIANT", "Os_Variant", "osvariant", " os_variant "} {
		if IsValidVMSort(axis) {
			t.Errorf("IsValidVMSort(%q) = true, want false (parser must normalise before lookup)", axis)
		}
	}
}

// 5.4.104 — case-insensitive `disk_bus` sort axis with OS-family-aware default.

func TestSortVMs_ByDiskBus_AscResolvesOSFamilyDefault(t *testing.T) {
	// Empty `disk_bus` resolves to the OS-family default via ResolvedDiskBus —
	// `virtio` for Linux, `sata` for Windows. The empty Linux VM collates
	// with explicit-virtio Linux VMs and the empty Windows VM collates with
	// explicit-sata Windows VMs in alphabetical order rather than sinking
	// to the tail. Diverges from the nil-trailing image-sort contract
	// because `disk_bus` has a documented OS-family-aware default — same
	// rationale as the `firmware` axis (5.4.101) collapsing empty to "bios"
	// and the `os_type` axis (5.4.100) collapsing empty to "linux".
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{DiskBus: DiskBusVirtio}},
		{ID: "vm-2", Spec: VMSpec{DiskBus: DiskBusSATA}},
		{ID: "vm-3", Spec: VMSpec{}},                                       // Linux empty → virtio
		{ID: "vm-4", Spec: VMSpec{OSType: OSTypeWindows}},                  // Windows empty → sata
		{ID: "vm-5", Spec: VMSpec{OSType: OSTypeWindows, DiskBus: "sata"}}, // explicit sata
	}
	SortVMs(vms, VMSortDiskBus, SortOrderAsc)
	// asc: sata < virtio. The sata cohort (vm-2, vm-4, vm-5) tiebreaks on
	// id ascending; the virtio cohort (vm-1, vm-3) tiebreaks on id.
	want := []string{"vm-2", "vm-4", "vm-5", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q disk_bus=%q os_type=%q, want %q",
				i, v.ID, v.Spec.DiskBus, v.Spec.OSType, want[i])
		}
	}
}

func TestSortVMs_ByDiskBus_DescResolvesOSFamilyDefault(t *testing.T) {
	// Desc reverses the entire compare result so virtio VMs head the list,
	// then sata. The id tiebreak also inverts so within the virtio cohort
	// vm-3 (empty Linux → virtio) leads vm-1; within the sata cohort vm-5
	// leads vm-4 leads vm-2.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{DiskBus: DiskBusVirtio}},
		{ID: "vm-2", Spec: VMSpec{DiskBus: DiskBusSATA}},
		{ID: "vm-3", Spec: VMSpec{}},                                       // Linux empty → virtio
		{ID: "vm-4", Spec: VMSpec{OSType: OSTypeWindows}},                  // Windows empty → sata
		{ID: "vm-5", Spec: VMSpec{OSType: OSTypeWindows, DiskBus: "sata"}}, // explicit sata
	}
	SortVMs(vms, VMSortDiskBus, SortOrderDesc)
	want := []string{"vm-3", "vm-1", "vm-5", "vm-4", "vm-2"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q disk_bus=%q os_type=%q, want %q",
				i, v.ID, v.Spec.DiskBus, v.Spec.OSType, want[i])
		}
	}
}

func TestSortVMs_ByDiskBus_CaseInsensitive(t *testing.T) {
	// `VIRTIO` and `virtio` must collate as identical so the sort agrees
	// with the case-insensitive `?disk_bus=` filter contract. ResolvedDiskBus
	// already lowers + trims the stored value before fallback, and the sort
	// comparator lowers the result again to guarantee an identical compare.
	vms := []*VM{
		{ID: "vm-1", Spec: VMSpec{DiskBus: "VIRTIO"}},
		{ID: "vm-2", Spec: VMSpec{DiskBus: "sata"}},
		{ID: "vm-3", Spec: VMSpec{DiskBus: "virtio"}},
		{ID: "vm-4", Spec: VMSpec{DiskBus: " SATA "}}, // whitespace + uppercase
	}
	SortVMs(vms, VMSortDiskBus, SortOrderAsc)
	// asc: sata < virtio. Equal-bus cohort tiebreaks on id ascending so
	// vm-2 precedes vm-4 in the sata bucket and vm-1 precedes vm-3 in the
	// virtio bucket.
	want := []string{"vm-2", "vm-4", "vm-1", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q disk_bus=%q, want %q", i, v.ID, v.Spec.DiskBus, want[i])
		}
	}
}

func TestSortVMs_ByDiskBus_TiebreaksOnID(t *testing.T) {
	vms := []*VM{
		{ID: "vm-3", Spec: VMSpec{DiskBus: DiskBusVirtio}},
		{ID: "vm-1", Spec: VMSpec{DiskBus: DiskBusVirtio}},
		{ID: "vm-2", Spec: VMSpec{DiskBus: DiskBusVirtio}},
	}
	SortVMs(vms, VMSortDiskBus, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortVMs_ByDiskBus_AllEmptyLinux_TiebreaksOnID(t *testing.T) {
	// All-empty Linux VMs all resolve to "virtio" so they tiebreak on id.
	// Mirrors TestSortVMs_ByFirmware_AllEmpty_TiebreaksOnID for the
	// OS-family-default flavor: the OS family is also empty (Linux is the
	// default) so every VM resolves identically.
	vms := []*VM{
		{ID: "vm-3"},
		{ID: "vm-1"},
		{ID: "vm-2"},
	}
	SortVMs(vms, VMSortDiskBus, SortOrderAsc)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, v := range vms {
		if v.ID != want[i] {
			t.Errorf("idx %d: id=%q, want %q", i, v.ID, want[i])
		}
	}
}

func TestIsValidVMSort_AcceptsDiskBus(t *testing.T) {
	if !IsValidVMSort(VMSortDiskBus) {
		t.Fatalf("IsValidVMSort(%q) = false, want true", VMSortDiskBus)
	}
	for _, axis := range []string{"DISK_BUS", "Disk_Bus", "diskbus", " disk_bus "} {
		if IsValidVMSort(axis) {
			t.Errorf("IsValidVMSort(%q) = true, want false (parser must normalise before lookup)", axis)
		}
	}
}
