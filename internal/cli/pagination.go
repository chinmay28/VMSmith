package cli

import "fmt"

func normalizeLimitOffset(limit, offset int) (int, int, error) {
	if limit < 0 {
		return 0, 0, fmt.Errorf("--limit must be >= 0")
	}
	if offset < 0 {
		return 0, 0, fmt.Errorf("--offset must be >= 0")
	}
	return limit, offset, nil
}

func paginateSlice[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return items[:0]
	}
	if offset > 0 {
		items = items[offset:]
	}
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}
