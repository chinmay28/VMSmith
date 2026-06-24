package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// ScheduleStore is the persistence surface used by the schedule REST handlers.
type ScheduleStore interface {
	PutSchedule(*types.Schedule) error
	GetSchedule(id string) (*types.Schedule, error)
	ListSchedules() ([]*types.Schedule, error)
	DeleteSchedule(id string) error
	ListRuns(scheduleID string, limit int) ([]*types.ScheduleRun, error)
}

// ScheduleController lets the API server inform the running scheduler about
// CRUD changes and validate / fire schedules without importing the concrete
// engine (mirrors the WebhookRegistrar pattern).
type ScheduleController interface {
	Register(*types.Schedule)
	Unregister(id string)
	RunNow(ctx context.Context, id, actor string) error
	ValidateSpec(cronSpec, timezone string) error
	NextFireTime(cronSpec, timezone string) (time.Time, error)
}

// SetScheduleSubsystem wires schedule persistence and the runtime engine into
// the server. With no store the endpoints return 503.
func (s *Server) SetScheduleSubsystem(store ScheduleStore, ctrl ScheduleController) {
	s.scheduleStore = store
	s.scheduleController = ctrl
}

func (s *Server) requireScheduleSubsystem(w http.ResponseWriter) bool {
	if s.scheduleStore == nil || s.scheduleController == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "schedules_disabled", "schedule subsystem not configured")
		return false
	}
	return true
}

const maxScheduleNameLen = 128

// CreateSchedule handles POST /api/v1/schedules.
func (s *Server) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.requireScheduleSubsystem(w) {
		return
	}
	var req types.CreateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeErrorCode(w, http.StatusBadRequest, "invalid_name", "name is required")
		return
	}
	if len(name) > maxScheduleNameLen {
		writeErrorCode(w, http.StatusBadRequest, "invalid_name", "name must be 128 characters or fewer")
		return
	}
	if !types.IsValidScheduleAction(req.Action) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_action", "action must be one of: snapshot, start, stop, restart")
		return
	}
	if strings.TrimSpace(req.CronSpec) == "" {
		writeErrorCode(w, http.StatusBadRequest, "invalid_cron_spec", "cron_spec is required")
		return
	}
	if !types.IsValidCatchUpPolicy(req.CatchUpPolicy) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_catch_up_policy", "catch_up_policy must be one of: skip, run_once, run_all")
		return
	}
	vmID := strings.TrimSpace(req.VMID)
	tagSelector := types.NormalizeScheduleTags(req.TagSelector)
	if vmID != "" && len(tagSelector) > 0 {
		writeErrorCode(w, http.StatusBadRequest, "invalid_target", "vm_id and tag_selector are mutually exclusive")
		return
	}
	if req.MaxConcurrent < 0 || req.RetentionCount < 0 {
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "max_concurrent and retention_count must be non-negative")
		return
	}
	if err := s.scheduleController.ValidateSpec(req.CronSpec, req.Timezone); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	policy := req.CatchUpPolicy
	if policy == "" {
		policy = types.ScheduleCatchUpSkip
	}

	now := time.Now().UTC()
	sched := &types.Schedule{
		ID:             fmt.Sprintf("sched-%d", now.UnixNano()),
		Name:           name,
		VMID:           vmID,
		TagSelector:    tagSelector,
		Action:         req.Action,
		CronSpec:       strings.TrimSpace(req.CronSpec),
		Timezone:       strings.TrimSpace(req.Timezone),
		Enabled:        enabled,
		CatchUpPolicy:  policy,
		MaxConcurrent:  req.MaxConcurrent,
		RetentionCount: req.RetentionCount,
		Params:         req.Params,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if enabled {
		if next, err := s.scheduleController.NextFireTime(sched.CronSpec, sched.Timezone); err == nil {
			n := next.UTC()
			sched.NextFireAt = &n
		}
	}

	if err := s.scheduleStore.PutSchedule(sched); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to persist schedule")
		return
	}
	s.scheduleController.Register(sched)
	s.publishAppEvent("schedule.created", sched.VMID, fmt.Sprintf("schedule %q created", sched.Name),
		map[string]string{"schedule_id": sched.ID, "action": string(sched.Action)})

	writeJSON(w, http.StatusCreated, sched)
}

// ListSchedules handles GET /api/v1/schedules.
//
// Filters (applied before sort + pagination so X-Total-Count reflects the
// post-filter population): vm_id (exact), tag_selector (case-insensitive
// exact-membership against the schedule's tag_selector list — a schedule
// matches when any entry equals the value; schedules with an empty
// tag_selector (vm_id-targeted or all-VMs) are NOT matched, mirroring the
// webhook ?event_type= membership semantics), action (exact), catch_up_policy
// (case-insensitive exact-match: skip|run_once|run_all; an empty stored policy
// is treated as "skip" so ?catch_up_policy=skip matches it; invalid values
// return 400 invalid_catch_up_policy), timezone (case-sensitive exact-match
// against the stored Timezone field — IANA names are case-sensitive
// `America/New_York` not `america/new_york`; whitespace-trimmed; empty
// disables the filter; mirrors the ?vm_id= / ?actor= exact-match contracts;
// no default-fallback for empty stored values since the engine's effective
// default is host-dependent (`time.Local`)), enabled
// (tristate true/false), since/until (inclusive RFC3339 bounds on created_at;
// invalid values return 400 invalid_since/invalid_until; a schedule with a
// zero created_at is filtered OUT when any bound is set),
// next_fire_since/next_fire_until (inclusive RFC3339 bounds on next_fire_at —
// the cron-computed wall-clock time of the schedule's next planned fire;
// closes the "what fires in the next 4 hours" operator query that the
// next_fire_at sort axis can order but not narrow; whitespace-trimmed; empty
// disables; invalid values return 400 invalid_next_fire_since /
// invalid_next_fire_until; schedules with a nil NextFireAt — disabled or
// otherwise stalled — are filtered OUT when any bound is set, mirroring the
// zero-created_at handling and the snapshotInTimeRange contract),
// last_fired_since/last_fired_until (inclusive RFC3339 bounds on
// last_fired_at — the wall-clock time of the schedule's most recent fire;
// closes the SRE triage operator queries "which schedules fired during
// yesterday's maintenance window" / "which schedules haven't fired since the
// last daemon restart" that the categorical ?enabled= filter and the
// ?next_fire_since=/?next_fire_until= filter on the *next* fire cannot
// answer; whitespace-trimmed; empty disables; invalid values return 400
// invalid_last_fired_since / invalid_last_fired_until; schedules with a nil
// LastFiredAt — never-fired schedules — are filtered OUT when any bound is
// set, mirroring the webhook ?last_delivery_since=/?last_delivery_until=
// nil-handling and the next_fire range nil-exclusion), prefix (5.4.82 —
// case-sensitive `HasPrefix(sched.Name, prefix)` filter; whitespace-trimmed;
// empty disables; the fifth and final member of the name-prefix filter
// family alongside snapshots (5.4.75), VMs (5.4.76), images (5.4.77), and
// templates (5.4.78) so the same cohort-discrimination query
// (?prefix=nightly-, ?prefix=auto-, ?prefix=backup-) round-trips 1:1 across
// every name-prefix axis; case-sensitive because schedule names share the
// same case-sensitive free-form alphabet as VM / template names; applied
// right after the time-range filters and before ?search= so it composes
// additively with every other schedule filter), search
// (case-insensitive substring across name, action, vm_id, and tag_selector).
// Sorting: sort=id|name|created_at|next_fire_at|last_fired_at (default id),
// order=asc|desc (default asc). Both nullable axes (next_fire_at /
// last_fired_at) sort schedules with a nil timestamp at the tail of the
// ascending list and the head of the descending list. All comparators
// tiebreak on id.
func (s *Server) ListSchedules(w http.ResponseWriter, r *http.Request) {
	if !s.requireScheduleSubsystem(w) {
		return
	}
	q := r.URL.Query()

	sortField := strings.ToLower(strings.TrimSpace(q.Get("sort")))
	if sortField == "" {
		sortField = types.ScheduleSortID
	}
	if !types.IsValidScheduleSort(sortField) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_sort", "sort must be one of: id, name, created_at, next_fire_at, last_fired_at")
		return
	}
	order := strings.ToLower(strings.TrimSpace(q.Get("order")))
	if order == "" {
		order = types.SortOrderAsc
	}
	if order != types.SortOrderAsc && order != types.SortOrderDesc {
		writeErrorCode(w, http.StatusBadRequest, "invalid_order", "order must be 'asc' or 'desc'")
		return
	}

	enabledFilter, enabledSet, apiErr := parseTristateBoolParam(q.Get("enabled"), "enabled")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	sinceTime, sinceSet, apiErr := parseTimeRangeParam(q.Get("since"), "since")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	untilTime, untilSet, apiErr := parseTimeRangeParam(q.Get("until"), "until")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	nextFireSinceTime, nextFireSinceSet, apiErr := parseTimeRangeParam(q.Get("next_fire_since"), "next_fire_since")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	nextFireUntilTime, nextFireUntilSet, apiErr := parseTimeRangeParam(q.Get("next_fire_until"), "next_fire_until")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	lastFiredSinceTime, lastFiredSinceSet, apiErr := parseTimeRangeParam(q.Get("last_fired_since"), "last_fired_since")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	lastFiredUntilTime, lastFiredUntilSet, apiErr := parseTimeRangeParam(q.Get("last_fired_until"), "last_fired_until")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	vmIDFilter := strings.TrimSpace(q.Get("vm_id"))
	tagSelectorFilter := strings.ToLower(strings.TrimSpace(q.Get("tag_selector")))
	actionFilter := strings.ToLower(strings.TrimSpace(q.Get("action")))
	catchUpFilter := strings.ToLower(strings.TrimSpace(q.Get("catch_up_policy")))
	if catchUpFilter != "" && !types.IsValidCatchUpPolicy(types.ScheduleCatchUpPolicy(catchUpFilter)) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_catch_up_policy", "catch_up_policy must be one of: skip, run_once, run_all")
		return
	}
	timezoneFilter := strings.TrimSpace(q.Get("timezone"))
	prefixFilter := strings.TrimSpace(q.Get("prefix"))
	searchFilter := strings.ToLower(strings.TrimSpace(q.Get("search")))

	all, err := s.scheduleStore.ListSchedules()
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to list schedules")
		return
	}

	out := make([]*types.Schedule, 0, len(all))
	for _, sched := range all {
		if sched == nil {
			continue
		}
		if vmIDFilter != "" && sched.VMID != vmIDFilter {
			continue
		}
		if tagSelectorFilter != "" && !scheduleHasTagSelector(sched, tagSelectorFilter) {
			continue
		}
		if actionFilter != "" && string(sched.Action) != actionFilter {
			continue
		}
		if catchUpFilter != "" && scheduleCatchUpPolicy(sched) != catchUpFilter {
			continue
		}
		if timezoneFilter != "" && sched.Timezone != timezoneFilter {
			continue
		}
		if enabledSet && sched.Enabled != enabledFilter {
			continue
		}
		if !snapshotInTimeRange(sched.CreatedAt, sinceTime, sinceSet, untilTime, untilSet) {
			continue
		}
		if nextFireSinceSet || nextFireUntilSet {
			if sched.NextFireAt == nil {
				continue
			}
			if !snapshotInTimeRange(*sched.NextFireAt, nextFireSinceTime, nextFireSinceSet, nextFireUntilTime, nextFireUntilSet) {
				continue
			}
		}
		if lastFiredSinceSet || lastFiredUntilSet {
			if sched.LastFiredAt == nil {
				continue
			}
			if !snapshotInTimeRange(*sched.LastFiredAt, lastFiredSinceTime, lastFiredSinceSet, lastFiredUntilTime, lastFiredUntilSet) {
				continue
			}
		}
		if prefixFilter != "" && !strings.HasPrefix(sched.Name, prefixFilter) {
			continue
		}
		if searchFilter != "" && !scheduleMatchesSearch(sched, searchFilter) {
			continue
		}
		out = append(out, sched)
	}
	types.SortSchedules(out, sortField, order)

	total := len(out)
	pagination := parsePagination(r)
	out = paginateSlice(out, pagination.Page, pagination.PerPage)
	if out == nil {
		out = []*types.Schedule{}
	}
	setTotalCountHeader(w, total)
	writeJSON(w, http.StatusOK, out)
}

// scheduleHasTagSelector reports whether the schedule's tag_selector list
// contains the (already lowercased + trimmed) needle. Schedules with an empty
// tag_selector (vm_id-targeted or all-VMs) return false — mirroring the
// webhook ?event_type= selector semantics: the filter scopes to explicit
// tag-selector membership, not catch-all schedules.
func scheduleHasTagSelector(sched *types.Schedule, needle string) bool {
	for _, t := range sched.TagSelector {
		if strings.ToLower(t) == needle {
			return true
		}
	}
	return false
}

// scheduleCatchUpPolicy returns the schedule's effective catch-up policy
// (lowercased), treating an empty stored value as "skip" — the default the
// engine applies at fire time. This lets ?catch_up_policy=skip match schedules
// persisted without an explicit policy, mirroring the VM ?default_user=root
// empty-means-root filter semantics.
func scheduleCatchUpPolicy(sched *types.Schedule) string {
	if sched.CatchUpPolicy == "" {
		return string(types.ScheduleCatchUpSkip)
	}
	return strings.ToLower(string(sched.CatchUpPolicy))
}

func scheduleMatchesSearch(sched *types.Schedule, needle string) bool {
	if strings.Contains(strings.ToLower(sched.Name), needle) {
		return true
	}
	if strings.Contains(strings.ToLower(string(sched.Action)), needle) {
		return true
	}
	if strings.Contains(strings.ToLower(sched.VMID), needle) {
		return true
	}
	for _, t := range sched.TagSelector {
		if strings.Contains(strings.ToLower(t), needle) {
			return true
		}
	}
	return false
}

// GetSchedule handles GET /api/v1/schedules/{id}.
func (s *Server) GetSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.requireScheduleSubsystem(w) {
		return
	}
	sched, err := s.scheduleStore.GetSchedule(chi.URLParam(r, "scheduleID"))
	if err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

// UpdateSchedule handles PATCH /api/v1/schedules/{id} with pointer semantics.
func (s *Server) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.requireScheduleSubsystem(w) {
		return
	}
	id := chi.URLParam(r, "scheduleID")
	current, err := s.scheduleStore.GetSchedule(id)
	if err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "schedule not found")
		return
	}

	var req types.ScheduleUpdateSpec
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if req.Name == nil && req.VMID == nil && req.TagSelector == nil && req.Action == nil &&
		req.CronSpec == nil && req.Timezone == nil && req.Enabled == nil && req.CatchUpPolicy == nil &&
		req.MaxConcurrent == nil && req.RetentionCount == nil && req.Params == nil {
		writeErrorCode(w, http.StatusBadRequest, "noop_update", "no fields to update")
		return
	}

	updated := *current

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > maxScheduleNameLen {
			writeErrorCode(w, http.StatusBadRequest, "invalid_name", "name must be 1-128 characters")
			return
		}
		updated.Name = name
	}
	if req.Action != nil {
		if !types.IsValidScheduleAction(*req.Action) {
			writeErrorCode(w, http.StatusBadRequest, "invalid_action", "action must be one of: snapshot, start, stop, restart")
			return
		}
		updated.Action = *req.Action
	}
	if req.CatchUpPolicy != nil {
		if !types.IsValidCatchUpPolicy(*req.CatchUpPolicy) {
			writeErrorCode(w, http.StatusBadRequest, "invalid_catch_up_policy", "catch_up_policy must be one of: skip, run_once, run_all")
			return
		}
		policy := *req.CatchUpPolicy
		if policy == "" {
			policy = types.ScheduleCatchUpSkip
		}
		updated.CatchUpPolicy = policy
	}
	if req.VMID != nil {
		updated.VMID = strings.TrimSpace(*req.VMID)
	}
	if req.TagSelector != nil {
		updated.TagSelector = types.NormalizeScheduleTags(*req.TagSelector)
	}
	if updated.VMID != "" && len(updated.TagSelector) > 0 {
		writeErrorCode(w, http.StatusBadRequest, "invalid_target", "vm_id and tag_selector are mutually exclusive")
		return
	}
	if req.CronSpec != nil {
		updated.CronSpec = strings.TrimSpace(*req.CronSpec)
	}
	if req.Timezone != nil {
		updated.Timezone = strings.TrimSpace(*req.Timezone)
	}
	if req.CronSpec != nil || req.Timezone != nil {
		if err := s.scheduleController.ValidateSpec(updated.CronSpec, updated.Timezone); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
	}
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
	}
	if req.MaxConcurrent != nil {
		if *req.MaxConcurrent < 0 {
			writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "max_concurrent must be non-negative")
			return
		}
		updated.MaxConcurrent = *req.MaxConcurrent
	}
	if req.RetentionCount != nil {
		if *req.RetentionCount < 0 {
			writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "retention_count must be non-negative")
			return
		}
		updated.RetentionCount = *req.RetentionCount
	}
	if req.Params != nil {
		updated.Params = *req.Params
	}

	updated.UpdatedAt = time.Now().UTC()
	updated.NextFireAt = nil
	if updated.Enabled {
		if next, err := s.scheduleController.NextFireTime(updated.CronSpec, updated.Timezone); err == nil {
			n := next.UTC()
			updated.NextFireAt = &n
		}
	}

	if err := s.scheduleStore.PutSchedule(&updated); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to persist schedule")
		return
	}
	s.scheduleController.Register(&updated)
	s.publishAppEvent("schedule.updated", updated.VMID, fmt.Sprintf("schedule %q updated", updated.Name),
		map[string]string{"schedule_id": updated.ID})

	writeJSON(w, http.StatusOK, &updated)
}

// DeleteSchedule handles DELETE /api/v1/schedules/{id}.
func (s *Server) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.requireScheduleSubsystem(w) {
		return
	}
	id := chi.URLParam(r, "scheduleID")
	sched, err := s.scheduleStore.GetSchedule(id)
	if err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "schedule not found")
		return
	}
	if err := s.scheduleStore.DeleteSchedule(id); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to delete schedule")
		return
	}
	s.scheduleController.Unregister(id)
	s.publishAppEvent("schedule.deleted", sched.VMID, fmt.Sprintf("schedule %q deleted", sched.Name),
		map[string]string{"schedule_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// ListScheduleRuns handles GET /api/v1/schedules/{id}/runs.
//
// Filters (applied before sort + pagination so X-Total-Count reflects the
// post-filter population): status (exact, case-insensitive:
// running|success|error|skipped; invalid values return 400 invalid_status),
// skip_reason (exact, case-insensitive, 5.4.65 — one of vm_not_found,
// vm_already_stopped, vm_already_running, concurrent_run, catch_up_skipped,
// queue_full; whitespace-trimmed; empty disables; invalid values return 400
// invalid_skip_reason. Runs with an empty skip_reason field — every
// non-skipped run and any skipped run persisted without a reason — are
// filtered OUT whenever the filter is set, mirroring the nil-finished_at
// exclusion in the finished_since / finished_until family so the same
// "only rows where the field is populated" semantics applies. The
// symmetric categorical counterpart to ?status= for skipped runs: ?status=
// can narrow to skip but not by reason, and operators want to slice by
// reason — "show me every run skipped because the queue was full" /
// "every catch-up skip from yesterday's startup"),
// vm_id (exact, case-sensitive — VM IDs are opaque vm-<unix-nano> strings;
// whitespace-trimmed; empty disables the filter), since/until (inclusive
// RFC3339 bounds on started_at; invalid values return 400
// invalid_since/invalid_until; a run with a zero started_at is filtered OUT
// when any bound is set), finished_since/finished_until (inclusive RFC3339
// bounds on the run's nullable finished_at; whitespace-trimmed; empty disables;
// invalid values return 400 invalid_finished_since/invalid_finished_until;
// runs with a nil finished_at — typically still-running runs — are filtered
// OUT when any bound is set, mirroring the next_fire_at handling),
// min_duration_ms/max_duration_ms (inclusive non-negative integer bounds on
// the run's `finished_at - started_at` duration in milliseconds; 5.4.64;
// whitespace-trimmed; empty disables; non-numeric or negative values return
// 400 invalid_min_duration_ms / invalid_max_duration_ms; runs with a nil
// finished_at — still-running runs have no known duration — are filtered
// OUT when either bound is set, mirroring the finished_at range filter's
// nil-handling; closes the "show me every run that took ≥ 5 minutes" / "every
// run that completed in under a second" triage query the categorical
// ?status= can't answer; the symmetric range counterpart to the duration
// sort axis added in 5.4.63), search (case-insensitive substring match
// across the run's error and skip_reason fields — whitespace-trimmed;
// id/schedule_id/vm_id/status are intentionally excluded from the haystack to
// avoid noisy matches on short numeric queries; empty disables).
//
// Sorting: sort=id|started_at|finished_at|status|duration (default started_at
// to preserve the legacy newest-first contract), order=asc|desc (default desc
// when sort defaults to started_at so still-running runs land at the head;
// otherwise asc). Unknown values return 400 invalid_sort/invalid_order. All
// comparators tiebreak on id so paginated requests are deterministic. A nil
// finished_at sorts after any concrete time in ascending order so still-
// running runs sink to the tail; the duration axis applies the same nil-
// trailing semantics — runs with no known duration sink to the tail.
func (s *Server) ListScheduleRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requireScheduleSubsystem(w) {
		return
	}
	id := chi.URLParam(r, "scheduleID")
	if _, err := s.scheduleStore.GetSchedule(id); err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "schedule not found")
		return
	}

	q := r.URL.Query()
	statusFilter := strings.ToLower(strings.TrimSpace(q.Get("status")))
	if statusFilter != "" && !types.IsValidScheduleRunStatus(types.ScheduleRunStatus(statusFilter)) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_status", "status must be one of: running, success, error, skipped")
		return
	}
	skipReasonFilter := strings.ToLower(strings.TrimSpace(q.Get("skip_reason")))
	if skipReasonFilter != "" && !types.IsValidScheduleRunSkipReason(types.ScheduleRunSkipReason(skipReasonFilter)) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_skip_reason", "skip_reason must be one of: vm_not_found, vm_already_stopped, vm_already_running, concurrent_run, catch_up_skipped, queue_full")
		return
	}
	vmIDFilter := strings.TrimSpace(q.Get("vm_id"))
	searchFilter := strings.ToLower(strings.TrimSpace(q.Get("search")))
	sinceTime, sinceSet, apiErr := parseTimeRangeParam(q.Get("since"), "since")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	untilTime, untilSet, apiErr := parseTimeRangeParam(q.Get("until"), "until")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	finishedSinceTime, finishedSinceSet, apiErr := parseTimeRangeParam(q.Get("finished_since"), "finished_since")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	finishedUntilTime, finishedUntilSet, apiErr := parseTimeRangeParam(q.Get("finished_until"), "finished_until")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	minDurationMs, minDurationSet, apiErr := parseCountRangeParam(q.Get("min_duration_ms"), "min_duration_ms")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	maxDurationMs, maxDurationSet, apiErr := parseCountRangeParam(q.Get("max_duration_ms"), "max_duration_ms")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}

	sortFieldRaw := strings.TrimSpace(q.Get("sort"))
	sortField := strings.ToLower(sortFieldRaw)
	if sortField == "" {
		sortField = types.ScheduleRunSortStartedAt
	}
	if !types.IsValidScheduleRunSort(sortField) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_sort", "sort must be one of: id, started_at, finished_at, status, duration, vm_id, skip_reason")
		return
	}
	orderRaw := strings.TrimSpace(q.Get("order"))
	order := strings.ToLower(orderRaw)
	if order == "" {
		// Default desc when the sort axis defaults to started_at so the
		// newest-first contract is preserved on a bare GET.
		if sortFieldRaw == "" {
			order = types.SortOrderDesc
		} else {
			order = types.SortOrderAsc
		}
	}
	if order != types.SortOrderAsc && order != types.SortOrderDesc {
		writeErrorCode(w, http.StatusBadRequest, "invalid_order", "order must be 'asc' or 'desc'")
		return
	}

	all, err := s.scheduleStore.ListRuns(id, 0)
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to list runs")
		return
	}

	runs := make([]*types.ScheduleRun, 0, len(all))
	for _, run := range all {
		if run == nil {
			continue
		}
		if statusFilter != "" && string(run.Status) != statusFilter {
			continue
		}
		if skipReasonFilter != "" && string(run.SkipReason) != skipReasonFilter {
			continue
		}
		if vmIDFilter != "" && run.VMID != vmIDFilter {
			continue
		}
		if !snapshotInTimeRange(run.StartedAt, sinceTime, sinceSet, untilTime, untilSet) {
			continue
		}
		if finishedSinceSet || finishedUntilSet {
			if run.FinishedAt == nil {
				continue
			}
			if !snapshotInTimeRange(*run.FinishedAt, finishedSinceTime, finishedSinceSet, finishedUntilTime, finishedUntilSet) {
				continue
			}
		}
		if minDurationSet || maxDurationSet {
			if run.FinishedAt == nil {
				continue
			}
			durationMs := int(run.FinishedAt.Sub(run.StartedAt) / time.Millisecond)
			if durationMs < 0 {
				durationMs = 0
			}
			if !countInRange(durationMs, minDurationMs, minDurationSet, maxDurationMs, maxDurationSet) {
				continue
			}
		}
		if searchFilter != "" && !scheduleRunMatchesSearch(run, searchFilter) {
			continue
		}
		runs = append(runs, run)
	}

	types.SortScheduleRuns(runs, sortField, order)

	total := len(runs)
	pagination := parsePagination(r)
	runs = paginateSlice(runs, pagination.Page, pagination.PerPage)
	if runs == nil {
		runs = []*types.ScheduleRun{}
	}
	setTotalCountHeader(w, total)
	writeJSON(w, http.StatusOK, runs)
}

// RunNowSchedule handles POST /api/v1/schedules/{id}/run-now.
func (s *Server) RunNowSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.requireScheduleSubsystem(w) {
		return
	}
	id := chi.URLParam(r, "scheduleID")
	if err := s.scheduleController.RunNow(r.Context(), id, "api"); err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "schedule not found")
		return
	}
	sched, err := s.scheduleStore.GetSchedule(id)
	if err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "schedule not found")
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

// scheduleRunMatchesSearch reports whether the run's error or skip_reason
// fields contain the given (already lowercased and trimmed) needle. The
// haystack intentionally excludes id / schedule_id / vm_id / status so short
// numeric needles don't generate noisy matches against opaque IDs.
func scheduleRunMatchesSearch(run *types.ScheduleRun, needle string) bool {
	if needle == "" {
		return true
	}
	if strings.Contains(strings.ToLower(run.Error), needle) {
		return true
	}
	if strings.Contains(strings.ToLower(string(run.SkipReason)), needle) {
		return true
	}
	return false
}
