package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// ListEvents handles GET /api/v1/events
//
// Query params:
//   - vm_id        — filter by VM ID (exact match)
//   - type         — filter by event type (exact match)
//   - source       — "libvirt" | "app" | "system"
//   - severity     — "info" | "warn" | "error" (exact match)
//   - min_severity — severity floor: "info" | "warn" | "error". Returns events
//     ranked at-or-above it (info < warn < error). Unknown values return 400
//     invalid_min_severity. Composes additively with the exact-match severity.
//   - actor        — case-sensitive exact-match against evt.Actor (e.g.
//     "system", "app", or an API-key alias). Lets operators slice the
//     timeline to a single human/bot without the noisy substring matches
//     ?search= gives. Mirrors the per-VM log filter (5.4.18).
//   - resource_id  — filter by ResourceID (exact match, case-sensitive)
//   - type_prefix  — case-insensitive prefix match on event type (e.g.,
//     `snapshot.` matches every `snapshot.*` subtype)
//   - search       — case-insensitive substring match across message, type,
//     source, severity, actor, vm_id, resource_id, and attribute values.
//     The numeric event ID is intentionally excluded.
//   - since        — RFC3339 timestamp; only events with occurred_at after this
//   - until        — seq ID (uint64); exclude events with ID ≥ this value
//   - sort         — id | occurred_at | type | source | severity (default occurred_at)
//   - order        — asc | desc (default desc — "newest first")
//   - page, per_page — pagination
func (s *Server) ListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sortField, order, sortErr := parseEventSort(r)
	if sortErr != nil {
		writeAPIError(w, http.StatusBadRequest, sortErr)
		return
	}

	vmID := strings.TrimSpace(q.Get("vm_id"))
	evtType := strings.TrimSpace(q.Get("type"))
	typePrefix := strings.ToLower(strings.TrimSpace(q.Get("type_prefix")))
	source := strings.TrimSpace(q.Get("source"))
	severity := strings.TrimSpace(q.Get("severity"))
	minSeverity := strings.ToLower(strings.TrimSpace(q.Get("min_severity")))
	if minSeverity != "" {
		if _, ok := types.EventSeverityRank(minSeverity); !ok {
			writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_min_severity", "min_severity must be one of: info, warn, error"))
			return
		}
	}
	actor := strings.TrimSpace(q.Get("actor"))
	resourceID := strings.TrimSpace(q.Get("resource_id"))
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))
	untilStr := strings.TrimSpace(q.Get("until"))
	sinceStr := strings.TrimSpace(q.Get("since"))

	var untilSeq uint64
	if untilStr != "" {
		v, err := strconv.ParseUint(untilStr, 10, 64)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_until", "until must be a uint64 sequence ID"))
			return
		}
		untilSeq = v
	}

	var sinceTime time.Time
	if sinceStr != "" {
		parsed, err := time.Parse(time.RFC3339Nano, sinceStr)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_since", "since must be a valid RFC3339 timestamp"))
			return
		}
		sinceTime = parsed
	}

	// Sort default matches the legacy sequence-ID-desc contract — skip
	// the pre-pagination re-sort for the cheap path. Any explicit
	// `?sort=` (or non-default `?order=asc`) triggers a re-sort over the
	// full filtered set, so we ask the store for the unpaginated result
	// and paginate after sorting.
	defaultSort := sortField == types.EventSortID && order == types.SortOrderDesc

	pagination := parsePagination(r)

	filter := store.EventFilter{
		VMID:        vmID,
		Type:        evtType,
		Source:      source,
		Severity:    severity,
		MinSeverity: minSeverity,
		Actor:       actor,
		ResourceID:  resourceID,
		TypePrefix:  typePrefix,
		Search:      search,
		UntilSeq:    untilSeq,
	}
	if !sinceTime.IsZero() {
		filter.Since = sinceTime
	}
	if defaultSort {
		filter.Page = pagination.Page
		filter.PerPage = pagination.PerPage
	}

	filtered, total, err := s.store.ListEventsFiltered(filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	if filtered == nil {
		filtered = []*types.Event{}
	}

	if !defaultSort {
		types.SortEvents(filtered, sortField, order)
		// Apply pagination over the sorted slice so X-Total-Count
		// (already set from the unpaginated fetch) still reflects the
		// post-filter / pre-pagination count.
		if pagination.PerPage > 0 {
			page := pagination.Page
			if page < 1 {
				page = 1
			}
			start := (page - 1) * pagination.PerPage
			if start >= len(filtered) {
				filtered = []*types.Event{}
			} else {
				end := start + pagination.PerPage
				if end > len(filtered) {
					end = len(filtered)
				}
				filtered = filtered[start:end]
			}
		}
	}

	setTotalCountHeader(w, total)
	writeJSON(w, http.StatusOK, filtered)
}

// StreamEvents handles GET /api/v1/events/stream (SSE).
//
// On connect, replays up to sseReplayLimit events after the ID in
// Last-Event-ID header (or ?since= query param as uint64 fallback).
// After replay, streams new events published to the EventBus in real time.
// A 30-second heartbeat comment prevents proxy idle timeouts.
//
// Returns 410 Gone with code event_stream_replay_window_exceeded when the
// client is more than sseReplayLimit events behind — the client should fall
// back to GET /api/v1/events with pagination to catch up.
//
// Server-side filters mirror the cross-cutting predicate set on
// GET /api/v1/events: ?vm_id=, ?type=, ?type_prefix=, ?source=, ?severity=,
// ?min_severity=, ?actor=, ?resource_id=, ?search=. Filters apply to both replayed and live
// events so the client sees the same membership it would from /events. The
// replay-overflow check counts pre-filter so a faraway client still gets the
// 410 fail-fast even if their filter would have dropped the noise.
func (s *Server) StreamEvents(w http.ResponseWriter, r *http.Request) {
	const sseReplayLimit = 1000

	// Track this handler in the active SSE connection count for as long as
	// it is running (covers replay overflow path and post-write disconnects).
	s.eventStreamConns.Add(1)
	defer s.eventStreamConns.Add(-1)

	q := r.URL.Query()
	minSeverity := strings.ToLower(strings.TrimSpace(q.Get("min_severity")))
	if minSeverity != "" {
		if _, ok := types.EventSeverityRank(minSeverity); !ok {
			writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_min_severity", "min_severity must be one of: info, warn, error"))
			return
		}
	}
	filter := types.EventStreamFilter{
		VMID:        strings.TrimSpace(q.Get("vm_id")),
		Type:        strings.TrimSpace(q.Get("type")),
		Source:      strings.TrimSpace(q.Get("source")),
		Severity:    strings.TrimSpace(q.Get("severity")),
		MinSeverity: minSeverity,
		Actor:       strings.TrimSpace(q.Get("actor")),
		ResourceID:  strings.TrimSpace(q.Get("resource_id")),
		TypePrefix:  strings.ToLower(strings.TrimSpace(q.Get("type_prefix"))),
		Search:      strings.ToLower(strings.TrimSpace(q.Get("search"))),
	}

	// Determine replay starting point from Last-Event-ID or ?since= (uint64).
	// Computed before the SSE response status is committed so a replay-overflow
	// short-circuit can still return 410.
	var afterSeq uint64
	if lastID := strings.TrimSpace(r.Header.Get("Last-Event-ID")); lastID != "" {
		if seq, err := strconv.ParseUint(lastID, 10, 64); err == nil {
			afterSeq = seq
		}
	} else if sinceStr := strings.TrimSpace(q.Get("since")); sinceStr != "" {
		if seq, err := strconv.ParseUint(sinceStr, 10, 64); err == nil {
			afterSeq = seq
		}
	}

	// Fetch missed events (if any) and short-circuit with 410 on overflow
	// before committing the SSE 200 response status.
	var replayed []*types.Event
	if afterSeq > 0 {
		got, err := s.store.ListEventsAfterSeq(afterSeq, sseReplayLimit+1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "replay failed")
			return
		}
		if len(got) > sseReplayLimit {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			json.NewEncoder(w).Encode(types.NewAPIError("event_stream_replay_window_exceeded",
				"client is too far behind; use GET /api/v1/events with pagination to catch up"))
			return
		}
		replayed = got
	}

	sw := newSSEWriter(w)
	if sw == nil {
		return // newSSEWriter already wrote 500
	}

	for _, evt := range replayed {
		if filter.HasAny() && !types.EventMatchesStreamFilter(evt, filter) {
			continue
		}
		data, _ := json.Marshal(evt)
		if err := sw.WriteEvent(evt.ID, evt.Type, string(data)); err != nil {
			return
		}
	}

	// Subscribe to live events from the bus (if wired).
	if s.eventBus == nil {
		// No bus: stream heartbeats-only until the client disconnects.
		ticker := time.NewTicker(sseHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-s.shutdownNotify:
				_ = sw.WriteEvent("", "shutdown", `{"message":"server is shutting down"}`)
				return
			case <-ticker.C:
				if err := sw.Heartbeat(); err != nil {
					return
				}
			}
		}
	}

	ch, cancel := s.eventBus.Subscribe("sse-" + r.RemoteAddr)
	defer cancel()

	ticker := time.NewTicker(sseHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdownNotify:
			_ = sw.WriteEvent("", "shutdown", `{"message":"server is shutting down"}`)
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if filter.HasAny() && !types.EventMatchesStreamFilter(evt, filter) {
				continue
			}
			data, _ := json.Marshal(evt)
			if err := sw.WriteEvent(evt.ID, evt.Type, string(data)); err != nil {
				return
			}
		case <-ticker.C:
			if err := sw.Heartbeat(); err != nil {
				return
			}
		}
	}
}

// publishAppEvent is a helper for API handlers to emit app-source events via
// the EventBus.  It is a no-op when no bus is wired.
func (s *Server) publishAppEvent(evtType, vmID, message string, attrs map[string]string) {
	if s.eventBus == nil {
		return
	}
	s.eventBus.Publish(events.NewAppEvent(evtType, vmID, message, attrs))
}

// publishSystemEvent is a helper for API handlers to emit system-source events.
// It is a no-op when no bus is wired.
func (s *Server) publishSystemEvent(evtType, severity, message string, attrs map[string]string) {
	if s.eventBus == nil {
		return
	}
	s.eventBus.Publish(events.NewSystemEventWithAttrs(evtType, severity, message, attrs))
}
