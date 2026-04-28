package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func (s *Server) ListEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListEvents()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}

	// Filter by vm_id if provided
	vmID := strings.TrimSpace(r.URL.Query().Get("vm_id"))

	// Filter by since if provided
	sinceStr := strings.TrimSpace(r.URL.Query().Get("since"))
	var sinceTime time.Time
	if sinceStr != "" {
		parsed, err := time.Parse(time.RFC3339Nano, sinceStr)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_since", "since must be a valid RFC3339 timestamp"))
			return
		}
		sinceTime = parsed
	}

	filtered := make([]*types.Event, 0)
	for _, e := range events {
		if vmID != "" && e.VMID != vmID {
			continue
		}
		if !sinceTime.IsZero() && e.CreatedAt.Before(sinceTime) {
			continue
		}
		filtered = append(filtered, e)
	}
	events = filtered

	// Sort events descending by creation time
	sort.Slice(events, func(i, j int) bool {
		return events[i].CreatedAt.After(events[j].CreatedAt)
	})

	// Pagination
	total := len(events)
	pagination := parsePagination(r)
	events = paginateSlice(events, pagination.Page, pagination.PerPage)
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, events)
}
