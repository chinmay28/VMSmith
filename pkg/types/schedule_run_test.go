package types

import "testing"

func TestIsValidScheduleRunStatus(t *testing.T) {
	valid := []ScheduleRunStatus{
		ScheduleRunStatusRunning,
		ScheduleRunStatusSuccess,
		ScheduleRunStatusError,
		ScheduleRunStatusSkipped,
	}
	for _, s := range valid {
		if !IsValidScheduleRunStatus(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}

	invalid := []ScheduleRunStatus{"", "SUCCESS", "done", "queued", "failed", " success"}
	for _, s := range invalid {
		if IsValidScheduleRunStatus(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}
