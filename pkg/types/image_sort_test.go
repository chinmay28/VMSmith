package types

import (
	"testing"
	"time"
)

func imageNames(imgs []*Image) []string {
	out := make([]string, len(imgs))
	for i, v := range imgs {
		out[i] = v.Name
	}
	return out
}

func TestSortImages_ByID_Asc(t *testing.T) {
	imgs := []*Image{
		{ID: "img-3", Name: "c"},
		{ID: "img-1", Name: "a"},
		{ID: "img-2", Name: "b"},
	}
	SortImages(imgs, ImageSortID, SortOrderAsc)
	want := []string{"img-1", "img-2", "img-3"}
	for i, v := range imgs {
		if v.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortImages_ByName_CaseInsensitive(t *testing.T) {
	imgs := []*Image{
		{ID: "img-3", Name: "Charlie"},
		{ID: "img-1", Name: "alpha"},
		{ID: "img-2", Name: "Bravo"},
	}
	SortImages(imgs, ImageSortName, SortOrderAsc)
	got := imageNames(imgs)
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortImages_BySize_DescTiebreaksOnID(t *testing.T) {
	imgs := []*Image{
		{ID: "img-2", Name: "b", SizeBytes: 2048},
		{ID: "img-1", Name: "a", SizeBytes: 2048}, // same size
		{ID: "img-3", Name: "c", SizeBytes: 8192},
	}
	SortImages(imgs, ImageSortSize, SortOrderDesc)
	// Biggest first; equal-size pair should reverse on tiebreak too because
	// the descending wrapper inverts the entire compare result.
	got := imageNames(imgs)
	want := []string{"c", "b", "a"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortImages_ByCreatedAt_DescTiebreaksOnID(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	imgs := []*Image{
		{ID: "img-2", Name: "b", CreatedAt: t0},
		{ID: "img-1", Name: "a", CreatedAt: t0}, // same timestamp
		{ID: "img-3", Name: "c", CreatedAt: t0.Add(time.Hour)},
	}
	SortImages(imgs, ImageSortCreatedAt, SortOrderDesc)
	got := imageNames(imgs)
	want := []string{"c", "b", "a"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortImages_UnknownFieldFallsBackToID(t *testing.T) {
	imgs := []*Image{
		{ID: "img-3"},
		{ID: "img-1"},
		{ID: "img-2"},
	}
	SortImages(imgs, "format", SortOrderAsc)
	want := []string{"img-1", "img-2", "img-3"}
	for i, v := range imgs {
		if v.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, v.ID, want[i])
		}
	}
}

func TestSortImages_StablePagination(t *testing.T) {
	// Repeated sorts on equal-key data must produce the same order so page-2
	// of a paginated query matches page-1's continuation.
	make := func() []*Image {
		return []*Image{
			{ID: "img-3", Name: "shared"},
			{ID: "img-1", Name: "shared"},
			{ID: "img-4", Name: "shared"},
			{ID: "img-2", Name: "shared"},
		}
	}
	a := make()
	b := make()
	SortImages(a, ImageSortName, SortOrderAsc)
	SortImages(b, ImageSortName, SortOrderAsc)
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("idx %d: a=%q b=%q — equal-name tie not deterministic", i, a[i].ID, b[i].ID)
		}
	}
}
