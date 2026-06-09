package api

import (
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseEventSort reads `?sort=` and `?order=` from the request, validates
// them against the whitelisted set, and returns the canonical (lowercase)
// values or a typed *APIError for the handler to surface as 400.
//
// Defaults: sort=id, order=desc — sequence-ID descending, which preserves
// the long-standing "newest first" contract `GET /api/v1/events` has
// shipped since 4.2 (events are appended in publish order, so sequence ID
// is a monotonic stand-in for occurred_at in practice). An empty value is
// treated as the default; explicitly passing an unsupported value is a
// 400 so callers cannot silently fall through to a different ordering.
func parseEventSort(r *http.Request) (sortField, order string, err error) {
	sortField = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("sort")))
	if sortField == "" {
		sortField = types.EventSortID
	}
	if !types.IsValidEventSort(sortField) {
		return "", "", types.NewAPIError(
			"invalid_sort",
			"sort must be one of: id, occurred_at, type, source, severity, actor",
		)
	}

	order = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("order")))
	if order == "" {
		// Default desc so the long-standing "newest first" contract is
		// preserved for clients that don't pass `?order=`.
		order = types.SortOrderDesc
	}
	switch order {
	case types.SortOrderAsc, types.SortOrderDesc:
	default:
		return "", "", types.NewAPIError(
			"invalid_order",
			"order must be 'asc' or 'desc'",
		)
	}

	return sortField, order, nil
}
