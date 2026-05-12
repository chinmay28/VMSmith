package types

import (
	"testing"
	"time"
)

func templateNames(templates []*VMTemplate) []string {
	out := make([]string, len(templates))
	for i, t := range templates {
		out[i] = t.Name
	}
	return out
}

func TestSortTemplates_ByID_Asc(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c"},
		{ID: "tpl-1", Name: "a"},
		{ID: "tpl-2", Name: "b"},
	}
	SortTemplates(templates, TemplateSortID, SortOrderAsc)
	want := []string{"tpl-1", "tpl-2", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, tpl.ID, want[i])
		}
	}
}

func TestSortTemplates_ByName_CaseInsensitive(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "Charlie"},
		{ID: "tpl-1", Name: "alpha"},
		{ID: "tpl-2", Name: "Bravo"},
	}
	SortTemplates(templates, TemplateSortName, SortOrderAsc)
	got := templateNames(templates)
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortTemplates_ByCreatedAt_DescTiebreaksOnID(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	templates := []*VMTemplate{
		{ID: "tpl-2", Name: "b", CreatedAt: t0},
		{ID: "tpl-1", Name: "a", CreatedAt: t0},
		{ID: "tpl-3", Name: "c", CreatedAt: t0.Add(time.Hour)},
	}
	SortTemplates(templates, TemplateSortCreatedAt, SortOrderDesc)
	// Newest first; equal-time pair reverses on tiebreak when descending.
	got := templateNames(templates)
	want := []string{"c", "b", "a"}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestSortTemplates_ByCreatedAt_AscTiebreaksOnID(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	templates := []*VMTemplate{
		{ID: "tpl-3", Name: "c", CreatedAt: t0.Add(time.Hour)},
		{ID: "tpl-2", Name: "b", CreatedAt: t0},
		{ID: "tpl-1", Name: "a", CreatedAt: t0},
	}
	SortTemplates(templates, TemplateSortCreatedAt, SortOrderAsc)
	if templates[0].ID != "tpl-1" || templates[1].ID != "tpl-2" || templates[2].ID != "tpl-3" {
		t.Errorf("got %q,%q,%q want tpl-1,tpl-2,tpl-3",
			templates[0].ID, templates[1].ID, templates[2].ID)
	}
}

func TestSortTemplates_UnknownFieldFallsBackToID(t *testing.T) {
	templates := []*VMTemplate{
		{ID: "tpl-3"},
		{ID: "tpl-1"},
		{ID: "tpl-2"},
	}
	SortTemplates(templates, "ram_mb", SortOrderAsc)
	want := []string{"tpl-1", "tpl-2", "tpl-3"}
	for i, tpl := range templates {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, tpl.ID, want[i])
		}
	}
}

func TestSortTemplates_StablePagination(t *testing.T) {
	// Two independent SortTemplates invocations on the same equal-name
	// input must agree so page-1 + page-2 fetches see a deterministic
	// total ordering. The id tiebreak is what guarantees this — without
	// it, sort.SliceStable falls back to original slice order, which
	// shuffles between Go map iteration runs.
	build := func() []*VMTemplate {
		return []*VMTemplate{
			{ID: "tpl-3", Name: "shared"},
			{ID: "tpl-1", Name: "shared"},
			{ID: "tpl-4", Name: "shared"},
			{ID: "tpl-2", Name: "shared"},
		}
	}
	a := build()
	b := build()
	SortTemplates(a, TemplateSortName, SortOrderAsc)
	SortTemplates(b, TemplateSortName, SortOrderAsc)
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("idx %d: a=%q b=%q — equal-name tie not deterministic", i, a[i].ID, b[i].ID)
		}
	}
}
