package api

import (
	"net/http"
	"strings"
	"time"

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
//	since     – RFC3339 timestamp; only return entries after this time
//	source    – filter by source: cli | api | daemon (empty = all)
//	search    – case-insensitive substring match over message, source,
//	            level, and every value in the structured fields map.
//	            Whitespace-trimmed; field *keys* are intentionally
//	            excluded from the haystack.
func (s *Server) GetLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	level := q.Get("level")
	if level == "" {
		level = "debug"
	}

	var since time.Time
	if raw := q.Get("since"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			since = parsed
		}
	}

	entries := logger.Get().Entries(level, since, 0)

	if src := q.Get("source"); src != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Source == src {
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

	total := len(entries)
	pagination := parsePagination(r)
	entries = paginateSlice(entries, pagination.Page, pagination.PerPage)
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, logsResponse{
		Entries: entries,
		Total:   len(entries),
	})
}
