package api

import (
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseWebhookSort reads `?sort=` and `?order=` from the request, validates
// them against the whitelisted set, and returns the canonical (lowercase)
// values or a typed *APIError for the handler to surface as 400.
//
// Defaults: sort=id, order=asc. An empty value is treated as the default;
// explicitly passing an unsupported value is a 400 so callers cannot silently
// fall through to a different ordering.
func parseWebhookSort(r *http.Request) (sortField, order string, err error) {
	sortField = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("sort")))
	if sortField == "" {
		sortField = types.WebhookSortID
	}
	if !types.IsValidWebhookSort(sortField) {
		return "", "", types.NewAPIError(
			"invalid_sort",
			"sort must be one of: id, url, created_at, last_delivery_at, delivery_status, active, description",
		)
	}

	order = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("order")))
	if order == "" {
		order = types.SortOrderAsc
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
