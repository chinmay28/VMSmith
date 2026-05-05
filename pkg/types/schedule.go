package types

import "time"

// ScheduleAction identifies the VM action fired by a schedule.
type ScheduleAction string

const (
	ScheduleActionSnapshot ScheduleAction = "snapshot"
	ScheduleActionStart    ScheduleAction = "start"
	ScheduleActionStop     ScheduleAction = "stop"
	ScheduleActionRestart  ScheduleAction = "restart"
)

// ScheduleCatchUpPolicy controls how the scheduler handles missed fires after downtime.
type ScheduleCatchUpPolicy string

const (
	ScheduleCatchUpSkip    ScheduleCatchUpPolicy = "skip"
	ScheduleCatchUpRunOnce ScheduleCatchUpPolicy = "run_once"
	ScheduleCatchUpRunAll  ScheduleCatchUpPolicy = "run_all"
)

// Schedule represents a recurring VM action definition.
type Schedule struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	VMID           string                `json:"vm_id,omitempty"`
	TagSelector    []string              `json:"tag_selector,omitempty"`
	Action         ScheduleAction        `json:"action"`
	CronSpec       string                `json:"cron_spec"`
	Timezone       string                `json:"timezone,omitempty"`
	Enabled        bool                  `json:"enabled"`
	CatchUpPolicy  ScheduleCatchUpPolicy `json:"catch_up_policy,omitempty"`
	MaxConcurrent  int                   `json:"max_concurrent,omitempty"`
	RetentionCount int                   `json:"retention_count,omitempty"`
	Params         map[string]any        `json:"params,omitempty"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
	LastFiredAt    *time.Time            `json:"last_fired_at,omitempty"`
	LastResult     string                `json:"last_result,omitempty"`
	NextFireAt     *time.Time            `json:"next_fire_at,omitempty"`
}
