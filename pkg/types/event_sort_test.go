package types

import (
	"testing"
	"time"
)

func evtIDs(evts []*Event) []string {
	out := make([]string, len(evts))
	for i, e := range evts {
		out[i] = e.ID
	}
	return out
}

func TestSortEvents_ByID_Asc(t *testing.T) {
	evts := []*Event{
		{ID: "3"},
		{ID: "1"},
		{ID: "2"},
	}
	SortEvents(evts, EventSortID, SortOrderAsc)
	want := []string{"1", "2", "3"}
	got := evtIDs(evts)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortEvents_ByOccurredAt_DescIsNewestFirst(t *testing.T) {
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	evts := []*Event{
		{ID: "1", OccurredAt: base.Add(1 * time.Hour)},
		{ID: "3", OccurredAt: base.Add(3 * time.Hour)},
		{ID: "2", OccurredAt: base.Add(2 * time.Hour)},
	}
	SortEvents(evts, EventSortOccurredAt, SortOrderDesc)
	want := []string{"3", "2", "1"}
	if got := evtIDs(evts); !equalStrings(got, want) {
		t.Errorf("desc: got %v, want %v", got, want)
	}
}

func TestSortEvents_ByOccurredAt_FallsBackToCreatedAtWhenZero(t *testing.T) {
	// Legacy events written before OccurredAt was introduced only carry
	// CreatedAt; the comparator must transparently fall back so they don't
	// all collapse onto a single "zero" timestamp.
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	evts := []*Event{
		{ID: "1", CreatedAt: base.Add(1 * time.Hour)},
		{ID: "3", OccurredAt: base.Add(3 * time.Hour)},
		{ID: "2", CreatedAt: base.Add(2 * time.Hour)},
	}
	SortEvents(evts, EventSortOccurredAt, SortOrderAsc)
	want := []string{"1", "2", "3"}
	if got := evtIDs(evts); !equalStrings(got, want) {
		t.Errorf("asc: got %v, want %v", got, want)
	}
}

func TestSortEvents_ByType_CaseInsensitive(t *testing.T) {
	evts := []*Event{
		{ID: "3", Type: "vm.started"},
		{ID: "1", Type: "Image.created"},
		{ID: "2", Type: "snapshot.taken"},
	}
	SortEvents(evts, EventSortType, SortOrderAsc)
	// case-insensitive: "image.created" < "snapshot.taken" < "vm.started"
	want := []string{"1", "2", "3"}
	if got := evtIDs(evts); !equalStrings(got, want) {
		t.Errorf("asc: got %v, want %v", got, want)
	}
}

func TestSortEvents_ByType_TiebreaksOnID(t *testing.T) {
	evts := []*Event{
		{ID: "3", Type: "vm.started"},
		{ID: "1", Type: "vm.started"},
		{ID: "2", Type: "vm.started"},
	}
	SortEvents(evts, EventSortType, SortOrderAsc)
	want := []string{"1", "2", "3"}
	if got := evtIDs(evts); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortEvents_BySource(t *testing.T) {
	evts := []*Event{
		{ID: "3", Source: "system"},
		{ID: "1", Source: "app"},
		{ID: "2", Source: "libvirt"},
	}
	SortEvents(evts, EventSortSource, SortOrderAsc)
	// alpha: app < libvirt < system
	want := []string{"1", "2", "3"}
	if got := evtIDs(evts); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortEvents_BySeverity_CaseInsensitive(t *testing.T) {
	evts := []*Event{
		{ID: "3", Severity: "warn"},
		{ID: "1", Severity: "Error"},
		{ID: "2", Severity: "info"},
	}
	SortEvents(evts, EventSortSeverity, SortOrderAsc)
	// alpha case-insensitive: error < info < warn
	want := []string{"1", "2", "3"}
	if got := evtIDs(evts); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortEvents_UnknownFieldFallsBackToID(t *testing.T) {
	evts := []*Event{
		{ID: "3"},
		{ID: "1"},
		{ID: "2"},
	}
	SortEvents(evts, "attributes", SortOrderAsc)
	want := []string{"1", "2", "3"}
	if got := evtIDs(evts); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortEvents_StableEqualKeys(t *testing.T) {
	// Two independent sorts on equal-key data must produce the same order so
	// repeated paginated requests return deterministic results.
	build := func() []*Event {
		return []*Event{
			{ID: "3", Type: "vm.started"},
			{ID: "1", Type: "vm.started"},
			{ID: "4", Type: "vm.started"},
			{ID: "2", Type: "vm.started"},
		}
	}
	a, b := build(), build()
	SortEvents(a, EventSortType, SortOrderAsc)
	SortEvents(b, EventSortType, SortOrderAsc)
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("idx %d: a=%q b=%q — equal-type tie not deterministic", i, a[i].ID, b[i].ID)
		}
	}
}
