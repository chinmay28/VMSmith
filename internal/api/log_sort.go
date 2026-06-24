package api

import (
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseLogSort reads `?sort=` and `?order=` from the request, validates
// them against the whitelisted set, and returns the canonical (lowercase)
// values or a typed *APIError for the handler to surface as 400.
//
// Defaults: sort=timestamp, order=asc — preserves the long-standing
// oldest-first contract `GET /api/v1/logs` has shipped since the ring
// buffer was introduced.  The GUI's LogViewer relies on this for the
// chat-style "newest at bottom + auto-scroll" rendering.  An empty value
// is treated as the default; explicitly passing an unsupported value is a
// 400 so callers cannot silently fall through to a different ordering.
func parseLogSort(r *http.Request) (sortField, order string, err error) {
	sortField = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("sort")))
	if sortField == "" {
		sortField = logger.EntrySortTimestamp
	}
	switch sortField {
	case logger.EntrySortTimestamp,
		logger.EntrySortLevel,
		logger.EntrySortSource,
		logger.EntrySortVMID:
	default:
		return "", "", types.NewAPIError(
			"invalid_sort",
			"sort must be one of: timestamp, level, source, vm_id",
		)
	}

	order = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("order")))
	if order == "" {
		order = logger.EntrySortOrderAsc
	}
	switch order {
	case logger.EntrySortOrderAsc, logger.EntrySortOrderDesc:
	default:
		return "", "", types.NewAPIError(
			"invalid_order",
			"order must be 'asc' or 'desc'",
		)
	}

	return sortField, order, nil
}
