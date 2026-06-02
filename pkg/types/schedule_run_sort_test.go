package types

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestSortScheduleRuns_DefaultIDAsc(t *testing.T) {
	runs := []*ScheduleRun{
		{ID: "run-3"},
		{ID: "run-1"},
		{ID: "run-2"},
	}
	SortScheduleRuns(runs, ScheduleRunSortID, SortOrderAsc)
	got := []string{runs[0].ID, runs[1].ID, runs[2].ID}
	want := []string{"run-1", "run-2", "run-3"}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("id asc: got %v, want %v", got, want)
		}
	}
}

func TestSortScheduleRuns_ByStartedAt_AscTiebreaksOnID(t *testing.T) {
	t0 := mustParse(t, "2026-05-20T02:00:00Z")
	t1 := mustParse(t, "2026-05-21T02:00:00Z")
	runs := []*ScheduleRun{
		{ID: "run-b", StartedAt: t1},
		{ID: "run-c", StartedAt: t0},
		{ID: "run-a", StartedAt: t0},
	}
	SortScheduleRuns(runs, ScheduleRunSortStartedAt, SortOrderAsc)
	// run-a and run-c share t0; tie-break on ID alphabetical.
	if runs[0].ID != "run-a" || runs[1].ID != "run-c" || runs[2].ID != "run-b" {
		t.Fatalf("started_at asc tiebreak: %s, %s, %s", runs[0].ID, runs[1].ID, runs[2].ID)
	}
}

func TestSortScheduleRuns_ByStartedAt_Desc(t *testing.T) {
	t0 := mustParse(t, "2026-05-20T02:00:00Z")
	t1 := mustParse(t, "2026-05-21T02:00:00Z")
	t2 := mustParse(t, "2026-05-22T02:00:00Z")
	runs := []*ScheduleRun{
		{ID: "run-a", StartedAt: t0},
		{ID: "run-b", StartedAt: t1},
		{ID: "run-c", StartedAt: t2},
	}
	SortScheduleRuns(runs, ScheduleRunSortStartedAt, SortOrderDesc)
	if runs[0].ID != "run-c" || runs[1].ID != "run-b" || runs[2].ID != "run-a" {
		t.Fatalf("started_at desc: %s, %s, %s", runs[0].ID, runs[1].ID, runs[2].ID)
	}
}

func TestSortScheduleRuns_ByFinishedAt_NilTrailingAsc(t *testing.T) {
	t0 := mustParse(t, "2026-05-20T02:01:00Z")
	t1 := mustParse(t, "2026-05-21T02:01:00Z")
	runs := []*ScheduleRun{
		{ID: "run-running", FinishedAt: nil}, // still running
		{ID: "run-late", FinishedAt: ptrTime(t1)},
		{ID: "run-early", FinishedAt: ptrTime(t0)},
	}
	SortScheduleRuns(runs, ScheduleRunSortFinishedAt, SortOrderAsc)
	// Concrete finishes come first (ascending); nil sinks to the tail.
	if runs[0].ID != "run-early" || runs[1].ID != "run-late" || runs[2].ID != "run-running" {
		t.Fatalf("finished_at asc nil-trailing: %s, %s, %s", runs[0].ID, runs[1].ID, runs[2].ID)
	}
}

func TestSortScheduleRuns_ByFinishedAt_NilLeadingDesc(t *testing.T) {
	t0 := mustParse(t, "2026-05-20T02:01:00Z")
	t1 := mustParse(t, "2026-05-21T02:01:00Z")
	runs := []*ScheduleRun{
		{ID: "run-late", FinishedAt: ptrTime(t1)},
		{ID: "run-running", FinishedAt: nil},
		{ID: "run-early", FinishedAt: ptrTime(t0)},
	}
	SortScheduleRuns(runs, ScheduleRunSortFinishedAt, SortOrderDesc)
	// Descending of ascending-nil-trailing flips to nil-leading.
	if runs[0].ID != "run-running" || runs[1].ID != "run-late" || runs[2].ID != "run-early" {
		t.Fatalf("finished_at desc nil-leading: %s, %s, %s", runs[0].ID, runs[1].ID, runs[2].ID)
	}
}

func TestSortScheduleRuns_ByStatus_TiebreaksOnID(t *testing.T) {
	runs := []*ScheduleRun{
		{ID: "run-z", Status: ScheduleRunStatusSuccess},
		{ID: "run-x", Status: ScheduleRunStatusError},
		{ID: "run-y", Status: ScheduleRunStatusError},
		{ID: "run-w", Status: ScheduleRunStatusRunning},
	}
	SortScheduleRuns(runs, ScheduleRunSortStatus, SortOrderAsc)
	// Statuses alphabetical: error < running < success.
	want := []string{"run-x", "run-y", "run-w", "run-z"}
	for i, w := range want {
		if runs[i].ID != w {
			t.Fatalf("status asc tiebreak: idx=%d got=%s want=%s", i, runs[i].ID, w)
		}
	}
}

func TestSortScheduleRuns_ByDuration_AscNilTrailing(t *testing.T) {
	start := mustParse(t, "2026-05-20T02:00:00Z")
	// Three concrete durations: 30s, 2m, 1h; plus one still-running.
	runs := []*ScheduleRun{
		{ID: "run-long", StartedAt: start, FinishedAt: ptrTime(start.Add(time.Hour))},
		{ID: "run-running", StartedAt: start, FinishedAt: nil},
		{ID: "run-short", StartedAt: start, FinishedAt: ptrTime(start.Add(30 * time.Second))},
		{ID: "run-medium", StartedAt: start, FinishedAt: ptrTime(start.Add(2 * time.Minute))},
	}
	SortScheduleRuns(runs, ScheduleRunSortDuration, SortOrderAsc)
	// Concrete durations ascending; nil-duration sinks to the tail.
	want := []string{"run-short", "run-medium", "run-long", "run-running"}
	for i, w := range want {
		if runs[i].ID != w {
			t.Fatalf("duration asc: idx=%d got=%s want=%s", i, runs[i].ID, w)
		}
	}
}

func TestSortScheduleRuns_ByDuration_DescNilLeading(t *testing.T) {
	start := mustParse(t, "2026-05-20T02:00:00Z")
	runs := []*ScheduleRun{
		{ID: "run-short", StartedAt: start, FinishedAt: ptrTime(start.Add(30 * time.Second))},
		{ID: "run-long", StartedAt: start, FinishedAt: ptrTime(start.Add(time.Hour))},
		{ID: "run-running", StartedAt: start, FinishedAt: nil},
	}
	SortScheduleRuns(runs, ScheduleRunSortDuration, SortOrderDesc)
	// Descending of nil-trailing flips to nil-leading.
	want := []string{"run-running", "run-long", "run-short"}
	for i, w := range want {
		if runs[i].ID != w {
			t.Fatalf("duration desc: idx=%d got=%s want=%s", i, runs[i].ID, w)
		}
	}
}

func TestSortScheduleRuns_ByDuration_TiebreaksOnID(t *testing.T) {
	// Three concrete runs sharing the same 1-minute duration but distinct
	// IDs.  The comparator must tiebreak on ID so paginated requests are
	// deterministic.
	start := mustParse(t, "2026-05-20T02:00:00Z")
	end := start.Add(time.Minute)
	runs := []*ScheduleRun{
		{ID: "run-c", StartedAt: start, FinishedAt: ptrTime(end)},
		{ID: "run-a", StartedAt: start, FinishedAt: ptrTime(end)},
		{ID: "run-b", StartedAt: start, FinishedAt: ptrTime(end)},
	}
	SortScheduleRuns(runs, ScheduleRunSortDuration, SortOrderAsc)
	if runs[0].ID != "run-a" || runs[1].ID != "run-b" || runs[2].ID != "run-c" {
		t.Fatalf("duration tiebreak: %s, %s, %s", runs[0].ID, runs[1].ID, runs[2].ID)
	}
}

func TestSortScheduleRuns_UnknownFieldFallsBackToID(t *testing.T) {
	runs := []*ScheduleRun{
		{ID: "run-c"},
		{ID: "run-a"},
		{ID: "run-b"},
	}
	SortScheduleRuns(runs, "garbage", SortOrderAsc)
	if runs[0].ID != "run-a" || runs[1].ID != "run-b" || runs[2].ID != "run-c" {
		t.Fatalf("unknown field fallback: %s, %s, %s", runs[0].ID, runs[1].ID, runs[2].ID)
	}
}

func TestIsValidScheduleRunSort(t *testing.T) {
	for _, ok := range []string{"id", "started_at", "finished_at", "status", "duration"} {
		if !IsValidScheduleRunSort(ok) {
			t.Errorf("expected %q valid", ok)
		}
	}
	for _, bad := range []string{"", "name", "memory", "STARTED_AT", "garbage"} {
		if IsValidScheduleRunSort(bad) {
			t.Errorf("expected %q invalid", bad)
		}
	}
}
