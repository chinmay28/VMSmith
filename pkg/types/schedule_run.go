package types

import "time"

// ScheduleRunStatus reports the current or final state of an individual schedule fire.
type ScheduleRunStatus string

const (
	ScheduleRunStatusRunning ScheduleRunStatus = "running"
	ScheduleRunStatusSuccess ScheduleRunStatus = "success"
	ScheduleRunStatusError   ScheduleRunStatus = "error"
	ScheduleRunStatusSkipped ScheduleRunStatus = "skipped"
)

// IsValidScheduleRunStatus reports whether s is one of the recognized run
// statuses (running, success, error, skipped).
func IsValidScheduleRunStatus(s ScheduleRunStatus) bool {
	switch s {
	case ScheduleRunStatusRunning, ScheduleRunStatusSuccess, ScheduleRunStatusError, ScheduleRunStatusSkipped:
		return true
	default:
		return false
	}
}

// ScheduleRunSkipReason explains why a schedule fire did not execute.
type ScheduleRunSkipReason string

const (
	ScheduleRunSkipReasonVMNotFound       ScheduleRunSkipReason = "vm_not_found"
	ScheduleRunSkipReasonVMAlreadyStopped ScheduleRunSkipReason = "vm_already_stopped"
	ScheduleRunSkipReasonVMAlreadyRunning ScheduleRunSkipReason = "vm_already_running"
	ScheduleRunSkipReasonConcurrentRun    ScheduleRunSkipReason = "concurrent_run"
	ScheduleRunSkipReasonCatchUpSkipped   ScheduleRunSkipReason = "catch_up_skipped"
	ScheduleRunSkipReasonQueueFull        ScheduleRunSkipReason = "queue_full"
)

// IsValidScheduleRunSkipReason reports whether r is one of the recognized
// skip reasons (vm_not_found, vm_already_stopped, vm_already_running,
// concurrent_run, catch_up_skipped, queue_full). Used by the GET
// /schedules/{id}/runs `?skip_reason=` filter (5.4.65) to reject typos at
// the API boundary with 400 `invalid_skip_reason`.
func IsValidScheduleRunSkipReason(r ScheduleRunSkipReason) bool {
	switch r {
	case ScheduleRunSkipReasonVMNotFound,
		ScheduleRunSkipReasonVMAlreadyStopped,
		ScheduleRunSkipReasonVMAlreadyRunning,
		ScheduleRunSkipReasonConcurrentRun,
		ScheduleRunSkipReasonCatchUpSkipped,
		ScheduleRunSkipReasonQueueFull:
		return true
	default:
		return false
	}
}

// ScheduleRun captures one attempted schedule execution for one resolved VM.
type ScheduleRun struct {
	ID         string                `json:"id"`
	ScheduleID string                `json:"schedule_id"`
	VMID       string                `json:"vm_id"`
	StartedAt  time.Time             `json:"started_at"`
	FinishedAt *time.Time            `json:"finished_at,omitempty"`
	Status     ScheduleRunStatus     `json:"status"`
	Error      string                `json:"error,omitempty"`
	SkipReason ScheduleRunSkipReason `json:"skip_reason,omitempty"`
}
