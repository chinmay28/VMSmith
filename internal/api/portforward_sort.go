package api

import (
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parsePortForwardSort reads `?sort=` and `?order=` from the request,
// validates them against the whitelisted set, and returns the canonical
// (lowercase) values or a typed *APIError for the handler to surface as 400.
//
// Defaults: sort=id, order=asc. An empty value is treated as the default;
// explicitly passing an unsupported value is a 400 so callers cannot silently
// fall through to a different ordering.
func parsePortForwardSort(r *http.Request) (sortField, order string, err error) {
	sortField = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("sort")))
	if sortField == "" {
		sortField = types.PortForwardSortID
	}
	switch sortField {
	case types.PortForwardSortID,
		types.PortForwardSortHostPort,
		types.PortForwardSortGuestPort,
		types.PortForwardSortProtocol,
		types.PortForwardSortDescription:
	default:
		return "", "", types.NewAPIError(
			"invalid_sort",
			"sort must be one of: id, host_port, guest_port, protocol, description",
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
