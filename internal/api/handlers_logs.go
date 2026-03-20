package api

import (
	"net/http"
	"strconv"
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
//	level  – minimum level to return: debug | info | warn | error  (default: info)
//	limit  – max entries to return (default: 200, max: 2000)
//	since  – RFC3339 timestamp; only return entries after this time
//	source – filter by source: cli | api | daemon (empty = all)
func (s *Server) GetLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	level := q.Get("level")
	if level == "" {
		level = "debug"
	}

	limit := 200
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			if n > 2000 {
				n = 2000
			}
			limit = n
		}
	}

	var since time.Time
	if s := q.Get("since"); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			since = t
		}
	}

	entries := logger.Get().Entries(level, since, limit)

	// Optional source filter (applied after ring-buffer retrieval).
	if src := q.Get("source"); src != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Source == src {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	writeJSON(w, http.StatusOK, logsResponse{
		Entries: entries,
		Total:   len(entries),
	})
}
