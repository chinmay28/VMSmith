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
	SortSnapshots(snaps, "no_such_field", SortOrderAsc)
	got := snapNames(snaps)
	want := []string{"alpha", "bravo", "charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

// ============================================================
// `description` sort axis (5.4.121)
// ============================================================

func TestSortSnapshots_ByDescription_AscCaseInsensitive(t *testing.T) {
	// Mixed-case descriptions sort case-insensitively so `Pre upgrade`
	// and `pre upgrade` collate as identical. Mirrors the case-insensitive
	// haystack in the `?search=` filter — the same description-based
	// query surface is filtered (substring) and sorted (alphabetical)
	// on the same semantics.
	snaps := []*Snapshot{
		{Name: "snap-1", Description: "Pre upgrade"},
		{Name: "snap-2", Description: "audit"},
		{Name: "snap-3", Description: "pre upgrade"},
	}
	SortSnapshots(snaps, SnapshotSortDescription, SortOrderAsc)
	// asc: `audit` < `pre upgrade` (case-folded). The two `pre upgrade`
	// entries tiebreak on name ascending (snap-1 before snap-3).
	want := []string{"snap-2", "snap-1", "snap-3"}
	for i, s := range snaps {
		if s.Name != want[i] {
			t.Errorf("idx %d: name=%q description=%q, want %q", i, s.Name, s.Description, want[i])
		}
	}
}

func TestSortSnapshots_ByDescription_EmptyTrailsInAsc(t *testing.T) {
	// Snapshots with no description sink to the tail in ascending order —
	// operators looking for "which snapshots have a description" want them
	// at the head of asc, not buried among the unset majority. Mirrors the
	// image (5.4.118) / template (5.4.119) / VM (5.4.120) `description`
	// axes one resource over.
	snaps := []*Snapshot{
		{Name: "snap-1", Description: ""},
		{Name: "snap-2", Description: "z"},
		{Name: "snap-3", Description: "a"},
	}
	SortSnapshots(snaps, SnapshotSortDescription, SortOrderAsc)
	want := []string{"snap-3", "snap-2", "snap-1"}
	for i, s := range snaps {
		if s.Name != want[i] {
			t.Errorf("idx %d: name=%q description=%q, want %q", i, s.Name, s.Description, want[i])
		}
	}
}

func TestSortSnapshots_ByDescription_EmptyHeadsInDesc(t *testing.T) {
	snaps := []*Snapshot{
		{Name: "snap-1", Description: "a"},
		{Name: "snap-2", Description: ""},
		{Name: "snap-3", Description: ""},
	}
	SortSnapshots(snaps, SnapshotSortDescription, SortOrderDesc)
	// Empty heads in desc. The two empty-description entries tiebreak on
	// name — and because the outer desc-wrapper inverts the tiebreak,
	// snap-3 heads snap-2, then snap-1 (the only concrete description)
	// trails. Matches the image / template / VM `description` axes.
	want := []string{"snap-3", "snap-2", "snap-1"}
	for i, s := range snaps {
		if s.Name != want[i] {
			t.Errorf("idx %d: name=%q description=%q, want %q", i, s.Name, s.Description, want[i])
		}
	}
}

func TestSortSnapshots_ByDescription_TiebreaksOnName(t *testing.T) {
	snaps := []*Snapshot{
		{Name: "snap-c", Description: "same"},
		{Name: "snap-a", Description: "same"},
		{Name: "snap-b", Description: "same"},
	}
	SortSnapshots(snaps, SnapshotSortDescription, SortOrderAsc)
	want := []string{"snap-a", "snap-b", "snap-c"}
	for i, s := range snaps {
		if s.Name != want[i] {
			t.Errorf("idx %d: name=%q, want %q", i, s.Name, want[i])
		}
	}
}

func TestIsValidSnapshotSort_AcceptsDescription(t *testing.T) {
	if !IsValidSnapshotSort(SnapshotSortDescription) {
		t.Fatalf("IsValidSnapshotSort(%q) = false, want true", SnapshotSortDescription)
	}
	for _, axis := range []string{"DESCRIPTION", "Description", " description "} {
		if IsValidSnapshotSort(axis) {
			t.Errorf("IsValidSnapshotSort(%q) = true, want false (parser must normalise before lookup)", axis)
		}
	}
}

func TestIsValidSnapshotSort_AcceptsExistingAxes(t *testing.T) {
	for _, axis := range []string{
		SnapshotSortID,
		SnapshotSortName,
		SnapshotSortCreatedAt,
		SnapshotSortDescription,
	} {
		if !IsValidSnapshotSort(axis) {
			t.Errorf("IsValidSnapshotSort(%q) = false, want true", axis)
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
