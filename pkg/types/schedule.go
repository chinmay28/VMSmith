package types

import (
	"sort"
	"strings"
	"time"
)

// ScheduleAction identifies the VM action fired by a schedule.
type ScheduleAction string

const (
	ScheduleActionSnapshot  ScheduleAction = "snapshot"
	ScheduleActionStart     ScheduleAction = "start"
	ScheduleActionStop      ScheduleAction = "stop"
	ScheduleActionRestart   ScheduleAction = "restart"
	ScheduleActionForceStop ScheduleAction = "force-stop"
	ScheduleActionReboot    ScheduleAction = "reboot"
	ScheduleActionSuspend   ScheduleAction = "suspend"
	ScheduleActionResume    ScheduleAction = "resume"
)

// IsValidScheduleAction reports whether a is one of the recognised actions.
func IsValidScheduleAction(a ScheduleAction) bool {
	switch a {
	case ScheduleActionSnapshot, ScheduleActionStart, ScheduleActionStop, ScheduleActionRestart,
		ScheduleActionForceStop, ScheduleActionReboot, ScheduleActionSuspend, ScheduleActionResume:
		return true
	default:
		return false
	}
}

// ScheduleCatchUpPolicy controls how the scheduler handles missed fires after downtime.
type ScheduleCatchUpPolicy string

const (
	ScheduleCatchUpSkip    ScheduleCatchUpPolicy = "skip"
	ScheduleCatchUpRunOnce ScheduleCatchUpPolicy = "run_once"
	ScheduleCatchUpRunAll  ScheduleCatchUpPolicy = "run_all"
)

// IsValidCatchUpPolicy reports whether p is one of the recognised policies.
// An empty policy is treated as valid (the scheduler defaults it to "skip").
func IsValidCatchUpPolicy(p ScheduleCatchUpPolicy) bool {
	switch p {
	case "", ScheduleCatchUpSkip, ScheduleCatchUpRunOnce, ScheduleCatchUpRunAll:
		return true
	default:
		return false
	}
}

// Schedule list sort fields.
const (
	ScheduleSortID            = "id"
	ScheduleSortName          = "name"
	ScheduleSortCreatedAt     = "created_at"
	ScheduleSortNextFire      = "next_fire_at"
	ScheduleSortLastFiredAt   = "last_fired_at"
	ScheduleSortVMID          = "vm_id"
	ScheduleSortAction        = "action"
	ScheduleSortTimezone      = "timezone"
	ScheduleSortEnabled       = "enabled"
	ScheduleSortCatchUpPolicy = "catch_up_policy"
)

// IsValidScheduleSort reports whether field is an accepted sort key.
func IsValidScheduleSort(field string) bool {
	switch field {
	case ScheduleSortID, ScheduleSortName, ScheduleSortCreatedAt, ScheduleSortNextFire, ScheduleSortLastFiredAt, ScheduleSortVMID, ScheduleSortAction, ScheduleSortTimezone, ScheduleSortEnabled, ScheduleSortCatchUpPolicy:
		return true
	default:
		return false
	}
}

// CreateScheduleRequest is the POST /api/v1/schedules request body.
type CreateScheduleRequest struct {
	Name           string                `json:"name"`
	VMID           string                `json:"vm_id,omitempty"`
	TagSelector    []string              `json:"tag_selector,omitempty"`
	Action         ScheduleAction        `json:"action"`
	CronSpec       string                `json:"cron_spec"`
	Timezone       string                `json:"timezone,omitempty"`
	Enabled        *bool                 `json:"enabled,omitempty"`
	CatchUpPolicy  ScheduleCatchUpPolicy `json:"catch_up_policy,omitempty"`
	MaxConcurrent  int                   `json:"max_concurrent,omitempty"`
	RetentionCount int                   `json:"retention_count,omitempty"`
	Params         map[string]any        `json:"params,omitempty"`
}

// ScheduleUpdateSpec is the PATCH /api/v1/schedules/{id} body. Pointer / nil
// fields express "no change"; a provided slice replaces the current value.
type ScheduleUpdateSpec struct {
	Name           *string                `json:"name,omitempty"`
	VMID           *string                `json:"vm_id,omitempty"`
	TagSelector    *[]string              `json:"tag_selector,omitempty"`
	Action         *ScheduleAction        `json:"action,omitempty"`
	CronSpec       *string                `json:"cron_spec,omitempty"`
	Timezone       *string                `json:"timezone,omitempty"`
	Enabled        *bool                  `json:"enabled,omitempty"`
	CatchUpPolicy  *ScheduleCatchUpPolicy `json:"catch_up_policy,omitempty"`
	MaxConcurrent  *int                   `json:"max_concurrent,omitempty"`
	RetentionCount *int                   `json:"retention_count,omitempty"`
	Params         *map[string]any        `json:"params,omitempty"`
}

// NormalizeScheduleTags trims, lowercases, deduplicates, and alphabetises a
// tag-selector list so the stored selector matches the lowercase VM tag
// vocabulary used everywhere else in the system.
func NormalizeScheduleTags(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// SortSchedules orders schedules in place by the given whitelisted field and
// order. All comparators tiebreak on ID so paginated responses are
// deterministic. Unknown fields fall back to ID; unknown order falls back to
// ascending. The vm_id axis is case-sensitive (VM IDs are opaque
// vm-<unix-nano> strings) and sinks schedules with an empty vm_id
// (tag_selector-targeted or all-VMs schedules) to the tail of asc / head of
// desc, mirroring the nil-trailing semantics on the events vm_id sort axis
// (5.4.93), the logs vm_id sort axis (5.4.94), and the schedule-runs vm_id
// sort axis (5.4.95). The action axis (5.4.99) is the symmetric sort
// counterpart to the existing case-insensitive ?action= filter on the same
// column — case-insensitive alphabetical compare on the action enum.
// Action is closed-and-total (every schedule resolves to exactly one action
// at create time), so the action branch diverges from the nil-trailing
// convention the same way the webhook delivery_status sort axis (5.4.98)
// does — there is no empty bucket to sink, just plain alphabetical compare
// with the id tiebreak.
func SortSchedules(items []*Schedule, field, order string) {
	desc := order == SortOrderDesc
	less := func(i, j int) bool {
		a, b := items[i], items[j]
		var cmp int
		switch field {
		case ScheduleSortName:
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case ScheduleSortCreatedAt:
			cmp = compareTime(a.CreatedAt, b.CreatedAt)
		case ScheduleSortNextFire:
			cmp = compareNullableTime(a.NextFireAt, b.NextFireAt)
		case ScheduleSortLastFiredAt:
			cmp = compareNullableTime(a.LastFiredAt, b.LastFiredAt)
		case ScheduleSortVMID:
			switch {
			case a.VMID == "" && b.VMID == "":
				cmp = 0
			case a.VMID == "":
				cmp = 1
			case b.VMID == "":
				cmp = -1
			default:
				cmp = strings.Compare(a.VMID, b.VMID)
			}
		case ScheduleSortAction:
			cmp = strings.Compare(strings.ToLower(string(a.Action)), strings.ToLower(string(b.Action)))
		case ScheduleSortTimezone:
			// Case-sensitive compare on `schedule.timezone` (5.4.112).
			// Symmetric sort counterpart to the case-sensitive `?timezone=`
			// exact-match filter on the same column. IANA timezone names
			// are case-sensitive (`America/New_York`, not
			// `america/new_york`) so the comparator preserves the operator's
			// stored casing, mirroring the filter contract. Schedules with
			// an empty `timezone` (the daemon's effective default is the
			// host-dependent `time.Local`, so an empty stored value means
			// "operator did not pin a zone") sink to the tail of asc / head
			// of desc, mirroring the nil-trailing semantics on the
			// `vm_id` axis (5.4.97) and every other nullable string axis
			// (ip, image, gpu, actor, last_fired_at, next_fire_at) rather
			// than collapsing to a default like the documented-default
			// boolean axes (auto_start / locked) — there is no documented
			// default for timezone because the runtime default is
			// host-dependent.
			switch {
			case a.Timezone == "" && b.Timezone == "":
				cmp = 0
			case a.Timezone == "":
				cmp = 1
			case b.Timezone == "":
				cmp = -1
			default:
				cmp = strings.Compare(a.Timezone, b.Timezone)
			}
		case ScheduleSortEnabled:
			// Boolean compare on `Schedule.Enabled` (5.4.113). The symmetric
			// sort counterpart to the tristate `?enabled=true|false`
			// exact-match filter on the same column so the same enabled
			// cohort can be both filtered and sorted on the same column.
			// Asc collation: false < true — disabled schedules at the
			// head, enabled cohort at the tail; desc surfaces the enabled
			// schedules (the cohort that actually fires) first, the natural
			// ordering for the operator query *"surface every live schedule
			// before I audit the cron landscape"*. Closed-and-total:
			// `Schedule.Enabled` is `json:"enabled"` without `omitempty` so
			// an absent payload key resolves to the zero value (false) and
			// every schedule belongs to exactly one of the two buckets.
			// Same rationale as the VM `auto_start` axis (5.4.108) and the
			// VM `locked` axis (5.4.109) — no nil-trailing bucket, unlike
			// the nullable string axes (`vm_id`, `timezone`) that sink
			// empty stored values to the tail.
			switch {
			case a.Enabled == b.Enabled:
				cmp = 0
			case !a.Enabled && b.Enabled:
				cmp = -1
			default:
				cmp = 1
			}
		case ScheduleSortCatchUpPolicy:
			// Case-insensitive compare on `schedule.catch_up_policy`
			// (5.4.116). Symmetric sort counterpart to the existing
			// case-insensitive `?catch_up_policy=` exact-match filter on
			// the same column so the same catch-up cohort can be both
			// filtered and sorted on the same column. Alphabetical
			// compare on the three-member enum: `run_all` < `run_once` <
			// `skip`. **Diverges from the nil-trailing convention**
			// because this column has a documented default — an empty
			// stored `catch_up_policy` resolves to `skip` (mirrors the
			// `?catch_up_policy=skip` empty-means-skip filter contract
			// and the engine's default in `internal/scheduler/engine.go`)
			// so empty schedules collate with explicit-skip schedules in
			// alphabetical order rather than sinking to the tail.
			// Same documented-default rationale as the VM `os_type` axis
			// (5.4.100) collapsing empty → `linux`, the VM `firmware`
			// axis (5.4.101) collapsing empty → `bios`, and the VM
			// `default_user` axis (5.4.91) collapsing empty → `root`,
			// unlike the nullable string axes (`vm_id`, `timezone`)
			// that sink empty stored values to the tail.
			ap := strings.ToLower(strings.TrimSpace(string(a.CatchUpPolicy)))
			if ap == "" {
				ap = string(ScheduleCatchUpSkip)
			}
			bp := strings.ToLower(strings.TrimSpace(string(b.CatchUpPolicy)))
			if bp == "" {
				bp = string(ScheduleCatchUpSkip)
			}
			cmp = strings.Compare(ap, bp)
		default:
			cmp = strings.Compare(a.ID, b.ID)
		}
		if cmp == 0 {
			cmp = strings.Compare(a.ID, b.ID)
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	}
	sort.SliceStable(items, less)
}

func compareTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	default:
		return 0
	}
}

// compareNullableTime orders schedules by an optional timestamp pointer. A nil
// pointer sorts after any concrete time in ascending order so the absent value
// sinks to the tail — used by both the next_fire_at and last_fired_at sort
// axes (a nil NextFireAt means "disabled / unscheduled"; a nil LastFiredAt
// means "never fired"). Either way nil sorts last in asc / first in desc.
func compareNullableTime(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	default:
		return compareTime(*a, *b)
	}
}

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
