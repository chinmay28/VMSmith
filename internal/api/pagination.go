package api

import (
	"net/http"
	"strconv"
	"strings"
)

const maxPerPage = 2000

type paginationParams struct {
	Page    int
	PerPage int
}

func parsePagination(r *http.Request) paginationParams {
	q := r.URL.Query()

	page := 1
	if raw := strings.TrimSpace(q.Get("page")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}

	perPage := 0
	if raw := strings.TrimSpace(q.Get("per_page")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			perPage = parsed
		}
	} else if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			perPage = parsed
		}
	} else if strings.HasSuffix(r.URL.Path, "/logs") {
		perPage = 200
	}

	if perPage > maxPerPage {
		perPage = maxPerPage
	}

	return paginationParams{Page: page, PerPage: perPage}
}

func paginateSlice[T any](items []T, page, perPage int) []T {
	if len(items) == 0 || perPage <= 0 {
		return items
	}
	if page <= 0 {
		page = 1
	}

	start := (page - 1) * perPage
	if start >= len(items) {
		return items[:0]
	}

	end := start + perPage
	if end > len(items) {
		end = len(items)
	}

	return items[start:end]
}

func setTotalCountHeader(w http.ResponseWriter, total int) {
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
}
