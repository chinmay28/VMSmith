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
