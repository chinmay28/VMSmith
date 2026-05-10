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
