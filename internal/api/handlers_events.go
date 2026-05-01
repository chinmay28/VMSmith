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
//   - vm_id     — filter by VM ID (exact match)
//   - type      — filter by event type (exact match)
//   - source    — "libvirt" | "app" | "system"
//   - severity  — "info" | "warn" | "error"
//   - since     — RFC3339 timestamp; only events with occurred_at after this
//   - until     — seq ID (uint64); exclude events with ID ≥ this value
//   - page, per_page — pagination
func (s *Server) ListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	vmID := strings.TrimSpace(q.Get("vm_id"))
	evtType := strings.TrimSpace(q.Get("type"))
	source := strings.TrimSpace(q.Get("source"))
	severity := strings.TrimSpace(q.Get("severity"))
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

	pagination := parsePagination(r)

	filter := store.EventFilter{
		VMID:     vmID,
		Type:     evtType,
		Source:   source,
		Severity: severity,
		UntilSeq: untilSeq,
		Page:     pagination.Page,
		PerPage:  pagination.PerPage,
	}
	if !sinceTime.IsZero() {
		filter.Since = sinceTime
	}

	filtered, total, err := s.store.ListEventsFiltered(filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	if filtered == nil {
		filtered = []*types.Event{}
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
func (s *Server) StreamEvents(w http.ResponseWriter, r *http.Request) {
	const sseReplayLimit = 1000

	// Track this handler in the active SSE connection count for as long as
	// it is running (covers replay overflow path and post-write disconnects).
	s.eventStreamConns.Add(1)
	defer s.eventStreamConns.Add(-1)

	sw := newSSEWriter(w)
	if sw == nil {
		return // newSSEWriter already wrote 500
	}

	// Determine replay starting point from Last-Event-ID or ?since= (uint64).
	var afterSeq uint64
	if lastID := strings.TrimSpace(r.Header.Get("Last-Event-ID")); lastID != "" {
		if seq, err := strconv.ParseUint(lastID, 10, 64); err == nil {
			afterSeq = seq
		}
	} else if sinceStr := strings.TrimSpace(r.URL.Query().Get("since")); sinceStr != "" {
		if seq, err := strconv.ParseUint(sinceStr, 10, 64); err == nil {
			afterSeq = seq
		}
	}

	// Replay missed events.
	if afterSeq > 0 {
		replayed, err := s.store.ListEventsAfterSeq(afterSeq, sseReplayLimit+1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "replay failed")
			return
		}
		if len(replayed) > sseReplayLimit {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			json.NewEncoder(w).Encode(types.NewAPIError("event_stream_replay_window_exceeded",
				"client is too far behind; use GET /api/v1/events with pagination to catch up"))
			return
		}
		for _, evt := range replayed {
			data, _ := json.Marshal(evt)
			if err := sw.WriteEvent(evt.ID, evt.Type, string(data)); err != nil {
				return
			}
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
		case evt, ok := <-ch:
			if !ok {
				return
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
