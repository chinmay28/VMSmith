package types

import (
	"testing"
	"time"
)

func snapNames(snaps []*Snapshot) []string {
	out := make([]string, len(snaps))
	for i, v := range snaps {
		out[i] = v.Name
	}
	return out
}

func TestSortSnapshots_ByID_Asc_OrdersByName(t *testing.T) {
	// Within a single VM scope the snapshot ID is `<vmID>/<name>`, so id-asc
	// is functionally name-asc. The contract assertion belongs to the test
	// suite so a future refactor that changes the ID scheme can't silently
	// reorder paginated responses.
	snaps := []*Snapshot{
		{ID: "vm-1/charlie", Name: "charlie"},
		{ID: "vm-1/alpha", Name: "alpha"},
		{ID: "vm-1/bravo", Name: "bravo"},
	}
	SortSnapshots(snaps, SnapshotSortID, SortOrderAsc)
	got := snapNames(snaps)
	want := []string{"alpha", "bravo", "charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortSnapshots_ByName_CaseInsensitive(t *testing.T) {
	snaps := []*Snapshot{
		{Name: "Charlie"},
		{Name: "alpha"},
		{Name: "Bravo"},
	}
	SortSnapshots(snaps, SnapshotSortName, SortOrderAsc)
	got := snapNames(snaps)
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortSnapshots_ByCreatedAt_DescTiebreaksOnName(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	snaps := []*Snapshot{
		{Name: "bravo", CreatedAt: t0},
		{Name: "alpha", CreatedAt: t0}, // same timestamp
		{Name: "charlie", CreatedAt: t0.Add(time.Hour)},
	}
	SortSnapshots(snaps, SnapshotSortCreatedAt, SortOrderDesc)
	// Newest first; equal-timestamp pair flips on the desc wrapper too because
	// the wrapper inverts the entire compare result.
	got := snapNames(snaps)
	want := []string{"charlie", "bravo", "alpha"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortSnapshots_ByCreatedAt_AscTiebreaksOnName(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	snaps := []*Snapshot{
		{Name: "bravo", CreatedAt: t0},
		{Name: "alpha", CreatedAt: t0}, // same timestamp tiebreaks alpha < bravo
		{Name: "charlie", CreatedAt: t0.Add(time.Hour)},
	}
	SortSnapshots(snaps, SnapshotSortCreatedAt, SortOrderAsc)
	got := snapNames(snaps)
	want := []string{"alpha", "bravo", "charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortSnapshots_UnknownFieldFallsBackToName(t *testing.T) {
	snaps := []*Snapshot{
		{Name: "charlie"},
		{Name: "alpha"},
		{Name: "bravo"},
	}
	SortSnapshots(snaps, "description", SortOrderAsc)
	got := snapNames(snaps)
	want := []string{"alpha", "bravo", "charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortSnapshots_StablePagination(t *testing.T) {
	// Repeated sorts on equal-key data must produce the same order so page-2
	// of a paginated query matches page-1's continuation.
	make := func() []*Snapshot {
		t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		return []*Snapshot{
			{Name: "snap-c", CreatedAt: t0},
			{Name: "snap-a", CreatedAt: t0},
			{Name: "snap-d", CreatedAt: t0},
			{Name: "snap-b", CreatedAt: t0},
		}
	}
	a := make()
	b := make()
	SortSnapshots(a, SnapshotSortCreatedAt, SortOrderAsc)
	SortSnapshots(b, SnapshotSortCreatedAt, SortOrderAsc)
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Fatalf("idx %d: a=%q b=%q — equal-timestamp tie not deterministic", i, a[i].Name, b[i].Name)
		}
	}
	// Verify the tiebreak is the documented name-asc order.
	wantOrder := []string{"snap-a", "snap-b", "snap-c", "snap-d"}
	got := snapNames(a)
	for i, n := range got {
		if n != wantOrder[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, wantOrder[i], got)
		}
	}
}
