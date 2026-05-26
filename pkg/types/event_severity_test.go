package types

import "testing"

func TestEventSeverityRank(t *testing.T) {
	cases := []struct {
		in       string
		wantRank int
		wantOK   bool
	}{
		{"info", 0, true},
		{"warn", 1, true},
		{"error", 2, true},
		{"INFO", 0, true},    // case-insensitive
		{"  warn ", 1, true}, // trimmed
		{"Error", 2, true},
		{"", 0, false},
		{"debug", 0, false}, // not an event severity
		{"critical", 0, false},
	}
	for _, c := range cases {
		gotRank, gotOK := EventSeverityRank(c.in)
		if gotOK != c.wantOK || (gotOK && gotRank != c.wantRank) {
			t.Errorf("EventSeverityRank(%q) = (%d, %v), want (%d, %v)", c.in, gotRank, gotOK, c.wantRank, c.wantOK)
		}
	}
}

func TestEventMeetsMinSeverity(t *testing.T) {
	cases := []struct {
		name        string
		evtSeverity string
		minSeverity string
		want        bool
	}{
		{"warn meets warn floor", "warn", "warn", true},
		{"error meets warn floor", "error", "warn", true},
		{"info below warn floor", "info", "warn", false},
		{"info meets info floor", "info", "info", true},
		{"warn meets info floor", "warn", "info", true},
		{"error below nothing (error floor)", "error", "error", true},
		{"warn below error floor", "warn", "error", false},
		{"empty floor is no-op", "info", "", true},
		{"unknown floor is no-op", "info", "bogus", true},
		{"case-insensitive floor", "ERROR", "warn", true},
		{"unknown severity treated as info — passes info floor", "weird", "info", true},
		{"unknown severity treated as info — fails warn floor", "weird", "warn", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evt := &Event{Severity: c.evtSeverity}
			if got := EventMeetsMinSeverity(evt, c.minSeverity); got != c.want {
				t.Errorf("EventMeetsMinSeverity(severity=%q, min=%q) = %v, want %v", c.evtSeverity, c.minSeverity, got, c.want)
			}
		})
	}
}

func TestEventMeetsMinSeverity_NilEvent(t *testing.T) {
	if EventMeetsMinSeverity(nil, "info") {
		t.Error("nil event should never match")
	}
}
