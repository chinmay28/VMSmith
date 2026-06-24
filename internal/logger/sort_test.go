package logger

import (
	"testing"
	"time"
)

func TestSortEntries_DefaultIsTimestampAsc(t *testing.T) {
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0.Add(2 * time.Second), Level: "info", Source: "api", Message: "third"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "first"},
		{Timestamp: t0.Add(1 * time.Second), Level: "info", Source: "api", Message: "second"},
	}
	SortEntries(entries, "", EntrySortOrderAsc)
	want := []string{"first", "second", "third"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_TimestampDesc(t *testing.T) {
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0, Level: "info", Source: "api", Message: "first"},
		{Timestamp: t0.Add(2 * time.Second), Level: "info", Source: "api", Message: "third"},
		{Timestamp: t0.Add(1 * time.Second), Level: "info", Source: "api", Message: "second"},
	}
	SortEntries(entries, EntrySortTimestamp, EntrySortOrderDesc)
	want := []string{"third", "second", "first"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_LevelOrderedBySeverity(t *testing.T) {
	// All entries share a timestamp so we test the level-rank comparator
	// in isolation.  Severity-asc means debug → info → warn → error.
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0, Level: "error", Source: "api", Message: "e"},
		{Timestamp: t0, Level: "debug", Source: "api", Message: "d"},
		{Timestamp: t0, Level: "warn", Source: "api", Message: "w"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "i"},
	}
	SortEntries(entries, EntrySortLevel, EntrySortOrderAsc)
	want := []string{"d", "i", "w", "e"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_LevelDescPutsErrorsFirst(t *testing.T) {
	// Sort desc by severity — the operator triage view.
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0, Level: "info", Source: "api", Message: "i"},
		{Timestamp: t0, Level: "error", Source: "api", Message: "e"},
		{Timestamp: t0, Level: "debug", Source: "api", Message: "d"},
		{Timestamp: t0, Level: "warn", Source: "api", Message: "w"},
	}
	SortEntries(entries, EntrySortLevel, EntrySortOrderDesc)
	want := []string{"e", "w", "i", "d"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_LevelTiebreaksOnTimestamp(t *testing.T) {
	// Two errors with different timestamps — order should be by time.
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0.Add(1 * time.Second), Level: "error", Source: "api", Message: "second-error"},
		{Timestamp: t0, Level: "error", Source: "api", Message: "first-error"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "info"},
	}
	SortEntries(entries, EntrySortLevel, EntrySortOrderAsc)
	want := []string{"info", "first-error", "second-error"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_SourceCaseInsensitive(t *testing.T) {
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0, Level: "info", Source: "Daemon", Message: "d"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "a"},
		{Timestamp: t0, Level: "info", Source: "CLI", Message: "c"},
	}
	SortEntries(entries, EntrySortSource, EntrySortOrderAsc)
	want := []string{"a", "c", "d"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_SourceTiebreaksOnTimestamp(t *testing.T) {
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0.Add(1 * time.Second), Level: "info", Source: "api", Message: "later-api"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "earlier-api"},
	}
	SortEntries(entries, EntrySortSource, EntrySortOrderAsc)
	if entries[0].Message != "earlier-api" || entries[1].Message != "later-api" {
		t.Fatalf("source tiebreak on timestamp asc failed: got %v", entries)
	}
}

func TestSortEntries_UnknownFieldFallsBackToTimestamp(t *testing.T) {
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0.Add(1 * time.Second), Level: "info", Source: "api", Message: "second"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "first"},
	}
	SortEntries(entries, "bogus", EntrySortOrderAsc)
	if entries[0].Message != "first" || entries[1].Message != "second" {
		t.Fatalf("unknown sort field should fall back to timestamp asc; got %v", entries)
	}
}

func TestSortEntries_StableOnEqualKeys(t *testing.T) {
	// Two entries identical on the active sort key (level) and timestamp;
	// SliceStable should preserve insertion order for the inner tiebreak
	// chain.
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0, Level: "info", Source: "api", Message: "a"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "b"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "c"},
	}
	SortEntries(entries, EntrySortLevel, EntrySortOrderAsc)
	for i, want := range []string{"a", "b", "c"} {
		if entries[i].Message != want {
			t.Fatalf("position %d: want %q, got %q", i, want, entries[i].Message)
		}
	}
}

func TestSortEntries_VMIDAsc_CaseSensitive(t *testing.T) {
	// Case-sensitive ASCII order: capital 'V' (0x56) sorts before
	// lower-case 'v' (0x76).
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0, Level: "info", Source: "api", Message: "lower", Fields: map[string]string{"vm_id": "vm-abc"}},
		{Timestamp: t0, Level: "info", Source: "api", Message: "upper", Fields: map[string]string{"vm_id": "VM-abc"}},
		{Timestamp: t0, Level: "info", Source: "api", Message: "mid", Fields: map[string]string{"vm_id": "vm-bcd"}},
	}
	SortEntries(entries, EntrySortVMID, EntrySortOrderAsc)
	want := []string{"upper", "lower", "mid"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_VMIDAsc_EmptyTrailing(t *testing.T) {
	// Entries with no vm_id field (host-level lines like daemon startup)
	// sink to the tail of asc — mirrors the events vm_id sort axis.
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0, Level: "info", Source: "daemon", Message: "no-vmid"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "vm-2", Fields: map[string]string{"vm_id": "vm-2"}},
		{Timestamp: t0, Level: "info", Source: "daemon", Message: "empty-fields", Fields: map[string]string{}},
		{Timestamp: t0, Level: "info", Source: "api", Message: "vm-1", Fields: map[string]string{"vm_id": "vm-1"}},
	}
	SortEntries(entries, EntrySortVMID, EntrySortOrderAsc)
	want := []string{"vm-1", "vm-2", "no-vmid", "empty-fields"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_VMIDDesc_EmptyLeading(t *testing.T) {
	// Entries with no vm_id field head the list on desc.
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0, Level: "info", Source: "api", Message: "vm-1", Fields: map[string]string{"vm_id": "vm-1"}},
		{Timestamp: t0, Level: "info", Source: "daemon", Message: "no-vmid"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "vm-2", Fields: map[string]string{"vm_id": "vm-2"}},
	}
	SortEntries(entries, EntrySortVMID, EntrySortOrderDesc)
	want := []string{"no-vmid", "vm-2", "vm-1"}
	for i, e := range entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestSortEntries_VMID_TiebreakOnTimestamp(t *testing.T) {
	// Two entries share the same vm_id but differ on timestamp — tiebreak
	// on timestamp (asc), source (case-insensitive).
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0.Add(2 * time.Second), Level: "info", Source: "api", Message: "second", Fields: map[string]string{"vm_id": "vm-1"}},
		{Timestamp: t0, Level: "info", Source: "api", Message: "first", Fields: map[string]string{"vm_id": "vm-1"}},
	}
	SortEntries(entries, EntrySortVMID, EntrySortOrderAsc)
	if entries[0].Message != "first" || entries[1].Message != "second" {
		t.Fatalf("vm_id tiebreak on timestamp asc failed: got %v", entries)
	}
}

func TestSortEntries_VMID_AllEmpty_TiebreaksOnTimestamp(t *testing.T) {
	// When every entry has no vm_id we collapse into the inner tiebreak
	// chain (timestamp, then source), preserving deterministic order.
	t0 := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	entries := []Entry{
		{Timestamp: t0.Add(1 * time.Second), Level: "info", Source: "api", Message: "later"},
		{Timestamp: t0, Level: "info", Source: "api", Message: "earlier"},
	}
	SortEntries(entries, EntrySortVMID, EntrySortOrderAsc)
	if entries[0].Message != "earlier" || entries[1].Message != "later" {
		t.Fatalf("all-empty vm_id should tiebreak on timestamp asc: got %v", entries)
	}
}

func TestLevelRank_Mapping(t *testing.T) {
	cases := []struct {
		level string
		want  int
	}{
		{"debug", 0},
		{"DEBUG", 0},
		{"info", 1},
		{"warn", 2},
		{"warning", 2},
		{"error", 3},
		{"", -1},
		{"bogus", -1},
	}
	for _, c := range cases {
		if got := levelRank(c.level); got != c.want {
			t.Fatalf("levelRank(%q): want %d, got %d", c.level, c.want, got)
		}
	}
}
