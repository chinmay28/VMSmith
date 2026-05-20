package api

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseTristateBoolParam parses a query param that is either absent (empty
// after whitespace trim) or one of "true" / "false" (case-insensitive, "1" /
// "0" accepted as aliases). Returns (value, set, err) — when `set` is false
// the param is absent and the caller should disable the filter; when `set`
// is true the bool result drives an exact-match predicate. A non-empty value
// that does not parse returns an APIError with code `invalid_<param>`.
func parseTristateBoolParam(raw, paramName string) (bool, bool, *types.APIError) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return false, false, nil
	}
	switch trimmed {
	case "true", "1":
		return true, true, nil
	case "false", "0":
		return false, true, nil
	}
	return false, false, types.NewAPIError(
		"invalid_"+paramName,
		fmt.Sprintf("%s must be 'true' or 'false'", paramName),
	)
}
