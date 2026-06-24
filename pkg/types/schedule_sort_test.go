package types

import (
	"reflect"
	"testing"
	"time"
)

func tp(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func tt(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func collectScheduleIDs(items []*Schedule) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, s.ID)
	}
	return out
}

func TestSortSchedules_DefaultIsIDAsc(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-c", Name: "c"},
		{ID: "sched-a", Name: "a"},
		{ID: "sched-b", Name: "b"},
	}
	SortSchedules(items, ScheduleSortID, SortOrderAsc)
	want := []string{"sched-a", "sched-b", "sched-c"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("default id-asc: got %v, want %v", got, want)
	}
}

func TestSortSchedules_ByName_CaseInsensitive(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-1", Name: "Beta"},
		{ID: "sched-2", Name: "alpha"},
		{ID: "sched-3", Name: "Gamma"},
	}
	SortSchedules(items, ScheduleSortName, SortOrderAsc)
	want := []string{"sched-2", "sched-1", "sched-3"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("name-asc: got %v, want %v", got, want)
	}
}

func TestSortSchedules_ByCreatedAt_Desc(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-old", CreatedAt: tt("2026-05-01T00:00:00Z")},
		{ID: "sched-new", CreatedAt: tt("2026-06-01T00:00:00Z")},
		{ID: "sched-mid", CreatedAt: tt("2026-05-15T00:00:00Z")},
	}
	SortSchedules(items, ScheduleSortCreatedAt, SortOrderDesc)
	want := []string{"sched-new", "sched-mid", "sched-old"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("created_at-desc: got %v, want %v", got, want)
	}
}

func TestSortSchedules_ByNextFire_NilLastInAsc(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-a", NextFireAt: nil},
		{ID: "sched-b", NextFireAt: tp("2026-06-01T00:00:00Z")},
		{ID: "sched-c", NextFireAt: tp("2026-05-01T00:00:00Z")},
	}
	SortSchedules(items, ScheduleSortNextFire, SortOrderAsc)
	want := []string{"sched-c", "sched-b", "sched-a"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("next_fire_at asc nil-last: got %v, want %v", got, want)
	}
}

// TestSortSchedules_ByLastFired_NilLastInAsc is the headline case for 5.4.84:
// schedules with a nil LastFiredAt (never-fired) sink to the tail in ascending
// order, mirroring the existing next_fire_at handling. This is the SRE triage
// view: "show me the schedules I've fired least recently" puts the oldest
// last-fire at the top and never-fired schedules at the very bottom.
func TestSortSchedules_ByLastFired_NilLastInAsc(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-a", LastFiredAt: nil},
		{ID: "sched-b", LastFiredAt: tp("2026-06-01T00:00:00Z")},
		{ID: "sched-c", LastFiredAt: tp("2026-05-01T00:00:00Z")},
	}
	SortSchedules(items, ScheduleSortLastFiredAt, SortOrderAsc)
	want := []string{"sched-c", "sched-b", "sched-a"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("last_fired_at asc nil-last: got %v, want %v", got, want)
	}
}

// TestSortSchedules_ByLastFired_NilFirstInDesc is the descending counterpart —
// never-fired schedules sort to the head so operators get a head-of-list view
// of "never fired" before any concrete last-fire timestamp. Mirrors the
// next_fire_at descending nil-handling.
func TestSortSchedules_ByLastFired_NilFirstInDesc(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-mid", LastFiredAt: tp("2026-06-01T00:00:00Z")},
		{ID: "sched-nil", LastFiredAt: nil},
		{ID: "sched-old", LastFiredAt: tp("2026-05-01T00:00:00Z")},
	}
	SortSchedules(items, ScheduleSortLastFiredAt, SortOrderDesc)
	want := []string{"sched-nil", "sched-mid", "sched-old"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("last_fired_at desc nil-first: got %v, want %v", got, want)
	}
}

// TestSortSchedules_ByLastFired_TiebreaksOnID asserts the deterministic id
// tiebreak holds for equal LastFiredAt values — the same paginated-determinism
// contract every other sort axis on the list endpoint upholds.
func TestSortSchedules_ByLastFired_TiebreaksOnID(t *testing.T) {
	ts := tp("2026-06-01T00:00:00Z")
	items := []*Schedule{
		{ID: "sched-z", LastFiredAt: ts},
		{ID: "sched-a", LastFiredAt: ts},
		{ID: "sched-m", LastFiredAt: ts},
	}
	SortSchedules(items, ScheduleSortLastFiredAt, SortOrderAsc)
	want := []string{"sched-a", "sched-m", "sched-z"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("last_fired_at id-tiebreak: got %v, want %v", got, want)
	}
}

func TestSortSchedules_ByLastFired_AllNil_TiebreaksOnID(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-c", LastFiredAt: nil},
		{ID: "sched-a", LastFiredAt: nil},
		{ID: "sched-b", LastFiredAt: nil},
	}
	SortSchedules(items, ScheduleSortLastFiredAt, SortOrderAsc)
	want := []string{"sched-a", "sched-b", "sched-c"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("last_fired_at all-nil tiebreak: got %v, want %v", got, want)
	}
}

func TestIsValidScheduleSort_AcceptsLastFiredAt(t *testing.T) {
	if !IsValidScheduleSort(ScheduleSortLastFiredAt) {
		t.Fatal("last_fired_at must be an accepted sort key")
	}
	if !IsValidScheduleSort("last_fired_at") {
		t.Fatal("literal 'last_fired_at' must be accepted")
	}
}

// TestIsValidScheduleSort_AcceptsVMID covers the 5.4.97 vm_id sort axis on
// the schedules list — the symmetric sort counterpart to the existing
// `?vm_id=` exact-match filter on the same column.
func TestIsValidScheduleSort_AcceptsVMID(t *testing.T) {
	if !IsValidScheduleSort(ScheduleSortVMID) {
		t.Fatal("vm_id must be an accepted sort key")
	}
	if !IsValidScheduleSort("vm_id") {
		t.Fatal("literal 'vm_id' must be accepted")
	}
	for _, bad := range []string{"VM_ID", "vmid", "vm-id"} {
		if IsValidScheduleSort(bad) {
			t.Fatalf("expected %q invalid (case-sensitive whitelist)", bad)
		}
	}
}

// TestSortSchedules_ByVMID_AscCaseSensitive covers the case-sensitive
// ASCII comparator on the vm_id axis. Mirrors the events vm_id sort axis
// (5.4.93), the logs vm_id sort axis (5.4.94), and the schedule-runs
// vm_id sort axis (5.4.95) — VM IDs are opaque vm-<unix-nano> strings
// operators reference verbatim.
func TestSortSchedules_ByVMID_AscCaseSensitive(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-3", VMID: "vm-c"},
		{ID: "sched-1", VMID: "vm-A"},
		{ID: "sched-2", VMID: "vm-b"},
	}
	SortSchedules(items, ScheduleSortVMID, SortOrderAsc)
	want := []string{"sched-1", "sched-2", "sched-3"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("vm_id asc case-sensitive: got %v, want %v", got, want)
	}
}

// TestSortSchedules_ByVMID_EmptyTrailingAsc asserts schedules with an
// empty vm_id (tag_selector-targeted or all-VMs schedules) sink to the
// tail in asc, mirroring the nil-trailing semantics on every other
// nullable sort axis.
func TestSortSchedules_ByVMID_EmptyTrailingAsc(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-empty", VMID: ""},
		{ID: "sched-late", VMID: "vm-z"},
		{ID: "sched-early", VMID: "vm-a"},
	}
	SortSchedules(items, ScheduleSortVMID, SortOrderAsc)
	want := []string{"sched-early", "sched-late", "sched-empty"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("vm_id asc empty-trailing: got %v, want %v", got, want)
	}
}

// TestSortSchedules_ByVMID_EmptyLeadingDesc asserts schedules with an
// empty vm_id head the list in desc — the descending flip of the
// empty-trailing asc contract.
func TestSortSchedules_ByVMID_EmptyLeadingDesc(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-late", VMID: "vm-z"},
		{ID: "sched-empty", VMID: ""},
		{ID: "sched-early", VMID: "vm-a"},
	}
	SortSchedules(items, ScheduleSortVMID, SortOrderDesc)
	want := []string{"sched-empty", "sched-late", "sched-early"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("vm_id desc empty-leading: got %v, want %v", got, want)
	}
}

// TestSortSchedules_ByVMID_TiebreaksOnID covers the deterministic id
// tiebreak when multiple schedules share the same vm_id (e.g. one VM
// targeted by multiple nightly schedules).
func TestSortSchedules_ByVMID_TiebreaksOnID(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-z", VMID: "vm-shared"},
		{ID: "sched-a", VMID: "vm-shared"},
		{ID: "sched-m", VMID: "vm-shared"},
	}
	SortSchedules(items, ScheduleSortVMID, SortOrderAsc)
	want := []string{"sched-a", "sched-m", "sched-z"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("vm_id id-tiebreak: got %v, want %v", got, want)
	}
}

// TestSortSchedules_ByVMID_AllEmptyTiebreaksOnID covers the all-empty
// case: every schedule is tag_selector / all-VMs targeted; the
// comparator must still produce a stable id ordering rather than
// returning -1/1 arbitrarily.
func TestSortSchedules_ByVMID_AllEmptyTiebreaksOnID(t *testing.T) {
	items := []*Schedule{
		{ID: "sched-c", VMID: ""},
		{ID: "sched-a", VMID: ""},
		{ID: "sched-b", VMID: ""},
	}
	SortSchedules(items, ScheduleSortVMID, SortOrderAsc)
	want := []string{"sched-a", "sched-b", "sched-c"}
	if got := collectScheduleIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("vm_id all-empty tiebreak: got %v, want %v", got, want)
	}
}
