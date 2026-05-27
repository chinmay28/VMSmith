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
// return 400 invalid_catch_up_policy), enabled
// (tristate true/false), since/until (inclusive RFC3339 bounds on created_at;
// invalid values return 400 invalid_since/invalid_until; a schedule with a
// zero created_at is filtered OUT when any bound is set), search
// (case-insensitive substring across name, action, vm_id, and tag_selector).
// Sorting: sort=id|name|created_at|next_fire_at (default id), order=asc|desc
// (default asc). All comparators tiebreak on id.
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
		writeErrorCode(w, http.StatusBadRequest, "invalid_sort", "sort must be one of: id, name, created_at, next_fire_at")
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
	vmIDFilter := strings.TrimSpace(q.Get("vm_id"))
	tagSelectorFilter := strings.ToLower(strings.TrimSpace(q.Get("tag_selector")))
	actionFilter := strings.ToLower(strings.TrimSpace(q.Get("action")))
	catchUpFilter := strings.ToLower(strings.TrimSpace(q.Get("catch_up_policy")))
	if catchUpFilter != "" && !types.IsValidCatchUpPolicy(types.ScheduleCatchUpPolicy(catchUpFilter)) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_catch_up_policy", "catch_up_policy must be one of: skip, run_once, run_all")
		return
	}
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
		if enabledSet && sched.Enabled != enabledFilter {
			continue
		}
		if !snapshotInTimeRange(sched.CreatedAt, sinceTime, sinceSet, untilTime, untilSet) {
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

// ListScheduleRuns handles GET /api/v1/schedules/{id}/runs (reverse-chrono).
//
// Filters (applied before pagination so X-Total-Count reflects the post-filter
// population): status (exact, case-insensitive: running|success|error|skipped;
// invalid values return 400 invalid_status), since/until (inclusive RFC3339
// bounds on started_at; invalid values return 400 invalid_since/invalid_until;
// a run with a zero started_at is filtered OUT when any bound is set).
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
		if !snapshotInTimeRange(run.StartedAt, sinceTime, sinceSet, untilTime, untilSet) {
			continue
		}
		runs = append(runs, run)
	}

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
