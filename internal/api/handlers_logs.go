package api

import (
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/internal/logger"
)

type logsResponse struct {
	Entries []logger.Entry `json:"entries"`
	Total   int            `json:"total"`
}

// GetLogs handles GET /api/v1/logs
//
// Query parameters:
//
//	level     – minimum level to return: debug | info | warn | error (default: debug)
//	page      – 1-indexed page number (default: 1)
//	per_page  – page size (default: 200, max: 2000)
//	limit     – alias for per_page
//	since     – RFC3339 timestamp; only return entries strictly AFTER
//	            this time.  Whitespace is trimmed; empty disables the
//	            bound; invalid values return 400 `invalid_since`.
//	until     – RFC3339 timestamp; only return entries at-or-BEFORE
//	            this time (inclusive upper bound — mirrors the
//	            snapshot/image/VM/template/webhook time-range filters
//	            family).  Whitespace is trimmed; empty disables the
//	            bound; invalid values return 400 `invalid_until`.
//	source    – filter by source: cli | api | daemon (empty = all)
//	vm_id     – exact-match filter against the entry's structured
//	            `vm_id` field. Whitespace-trimmed; empty = no filter.
//	            Composes additively with the other filters so
//	            `X-Total-Count` reflects the post-filter population.
//	search    – case-insensitive substring match over message, source,
//	            level, and every value in the structured fields map.
//	            Whitespace-trimmed; field *keys* are intentionally
//	            excluded from the haystack.
//	sort      – timestamp | level | source (default: timestamp)
//	order     – asc | desc (default: asc — preserves the legacy
//	            oldest-first contract).  level orders by severity rank
//	            (debug < info < warn < error), not alphabetically.
func (s *Server) GetLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sortField, order, sortErr := parseLogSort(r)
	if sortErr != nil {
		writeAPIError(w, http.StatusBadRequest, sortErr)
		return
	}

	since, _, sinceErr := parseTimeRangeParam(q.Get("since"), "since")
	if sinceErr != nil {
		writeAPIError(w, http.StatusBadRequest, sinceErr)
		return
	}
	until, untilSet, untilErr := parseTimeRangeParam(q.Get("until"), "until")
	if untilErr != nil {
		writeAPIError(w, http.StatusBadRequest, untilErr)
		return
	}

	level := q.Get("level")
	if level == "" {
		level = "debug"
	}

	entries := logger.Get().Entries(level, since, 0)

	if untilSet {
		filtered := entries[:0]
		for _, e := range entries {
			if !e.Timestamp.After(until) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if src := q.Get("source"); src != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Source == src {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if vmID := strings.TrimSpace(q.Get("vm_id")); vmID != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if logger.EntryMatchesVMID(e, vmID) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if search := strings.ToLower(strings.TrimSpace(q.Get("search"))); search != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if logger.EntryMatchesSearch(e, search) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	logger.SortEntries(entries, sortField, order)

	total := len(entries)
	pagination := parsePagination(r)
	entries = paginateSlice(entries, pagination.Page, pagination.PerPage)
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, logsResponse{
		Entries: entries,
		Total:   len(entries),
	})
}
