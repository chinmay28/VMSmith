package types

import "testing"

func TestIsValidScheduleAction(t *testing.T) {
	valid := []ScheduleAction{
		ScheduleActionSnapshot,
		ScheduleActionStart,
		ScheduleActionStop,
		ScheduleActionRestart,
		ScheduleActionForceStop,
		ScheduleActionReboot,
		ScheduleActionSuspend,
		ScheduleActionResume,
	}
	for _, action := range valid {
		if !IsValidScheduleAction(action) {
			t.Fatalf("IsValidScheduleAction(%q) = false, want true", action)
		}
	}
	for _, action := range []ScheduleAction{"", "explode", "pause"} {
		if IsValidScheduleAction(action) {
			t.Fatalf("IsValidScheduleAction(%q) = true, want false", action)
		}
	}
}
