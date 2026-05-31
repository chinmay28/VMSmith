package api

import (
	"testing"
	"time"
)

func TestParseTimeRangeParam(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantSet bool
		wantErr bool
	}{
		{"empty", "", false, false},
		{"whitespace only", "   ", false, false},
		{"rfc3339 second precision", "2026-05-01T00:00:00Z", true, false},
		{"rfc3339 with timezone", "2026-05-01T12:00:00-07:00", true, false},
		{"rfc3339 nano", "2026-05-01T00:00:00.123456789Z", true, false},
		{"trimmed", "  2026-05-01T00:00:00Z  ", true, false},
		{"invalid - random text", "last-tuesday", false, true},
		{"invalid - partial date", "2026-13-99", false, true},
		{"invalid - unix epoch number", "1735689600", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, set, err := parseTimeRangeParam(tc.raw, "since")
			if tc.wantErr && err == nil {
				t.Fatalf("want error, got nil (value=%v set=%v)", v, set)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if set != tc.wantSet {
				t.Fatalf("set = %v, want %v", set, tc.wantSet)
			}
			if tc.wantErr && err.Code != "invalid_since" {
				t.Fatalf("error code = %q, want invalid_since", err.Code)
			}
		})
	}
}

func TestSnapshotInTimeRange(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		ct       time.Time
		since    time.Time
		sinceSet bool
		until    time.Time
		untilSet bool
		want     bool
	}{
		{"no bounds", t1, time.Time{}, false, time.Time{}, false, true},
		{"in range", t1, t0, true, t2, true, true},
		{"before since", t0, t1, true, time.Time{}, false, false},
		{"after until", t2, time.Time{}, false, t1, true, false},
		{"equal to since", t0, t0, true, time.Time{}, false, true},
		{"equal to until", t2, time.Time{}, false, t2, true, true},
		{"zero with bounds excluded", time.Time{}, t0, true, time.Time{}, false, false},
		{"zero without bounds included", time.Time{}, time.Time{}, false, time.Time{}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := snapshotInTimeRange(tc.ct, tc.since, tc.sinceSet, tc.until, tc.untilSet)
			if got != tc.want {
				t.Fatalf("snapshotInTimeRange = %v, want %v", got, tc.want)
			}
		})
	}
}
