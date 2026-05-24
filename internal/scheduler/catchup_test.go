package scheduler

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, spec string) interface {
	Next(time.Time) time.Time
} {
	t.Helper()
	s, err := cronParser.Parse(spec)
	if err != nil {
		t.Fatalf("parse %q: %v", spec, err)
	}
	return s
}

func TestMissedFires_Daily(t *testing.T) {
	sched := mustParse(t, "0 0 2 * * *") // 02:00 daily
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 4, 12, 0, 0, 0, time.UTC)

	got := missedFires(sched, last, now, 0)
	if len(got) != 4 { // 02:00 on Jan 1,2,3,4 (all <= now)
		t.Fatalf("expected 4 missed fires, got %d: %v", len(got), got)
	}
	// chronological order
	for i := 1; i < len(got); i++ {
		if !got[i].After(got[i-1]) {
			t.Fatalf("fires not chronological: %v", got)
		}
	}
}

func TestMissedFires_CapApplied(t *testing.T) {
	sched := mustParse(t, "0 0 * * * *") // hourly
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC) // ~216 missed

	got := missedFires(sched, last, now, 100)
	if len(got) != 100 {
		t.Fatalf("expected cap of 100, got %d", len(got))
	}
}

func TestMissedFires_ZeroLastTick(t *testing.T) {
	sched := mustParse(t, "0 0 2 * * *")
	if got := missedFires(sched, time.Time{}, time.Now(), 0); got != nil {
		t.Fatalf("zero lastTick should yield no fires, got %v", got)
	}
}

func TestMissedFires_FutureLastTick(t *testing.T) {
	sched := mustParse(t, "0 0 2 * * *")
	last := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := missedFires(sched, last, now, 0); got != nil {
		t.Fatalf("future lastTick should yield no fires, got %v", got)
	}
}
