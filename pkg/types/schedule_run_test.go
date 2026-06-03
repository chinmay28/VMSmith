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

// TestIsValidScheduleRunSkipReason covers the 5.4.65 skip-reason whitelist
// helper consumed by the GET /schedules/{id}/runs `?skip_reason=` filter.
// Recognizes the six engine-produced reasons (vm_not_found,
// vm_already_stopped, vm_already_running, concurrent_run, catch_up_skipped,
// queue_full) and rejects empty / mixed-case / typos / leading whitespace so
// the API rejection contract at the boundary matches.
func TestIsValidScheduleRunSkipReason(t *testing.T) {
	valid := []ScheduleRunSkipReason{
		ScheduleRunSkipReasonVMNotFound,
		ScheduleRunSkipReasonVMAlreadyStopped,
		ScheduleRunSkipReasonVMAlreadyRunning,
		ScheduleRunSkipReasonConcurrentRun,
		ScheduleRunSkipReasonCatchUpSkipped,
		ScheduleRunSkipReasonQueueFull,
	}
	for _, r := range valid {
		if !IsValidScheduleRunSkipReason(r) {
			t.Errorf("expected %q to be valid", r)
		}
	}

	invalid := []ScheduleRunSkipReason{
		"", "VM_NOT_FOUND", "vm_missing", "queue-full", "queuefull", " queue_full", "queue_full ",
	}
	for _, r := range invalid {
		if IsValidScheduleRunSkipReason(r) {
			t.Errorf("expected %q to be invalid", r)
		}
	}
}
