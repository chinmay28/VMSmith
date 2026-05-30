package api

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseOSTypeFilter parses the optional `?os_type=<linux|windows>` query
// parameter shared by `GET /vms` and `GET /templates`.
//
// Contract:
//   - Empty / whitespace-only input returns (OSType(""), false, nil) so the
//     caller can short-circuit without distinguishing "no filter" from "match
//     all".
//   - A case-insensitive `linux` or `windows` (with surrounding whitespace
//     trimmed) returns the lowercased canonical OSType and set=true.
//   - Anything else returns a typed `*types.APIError` with code
//     `invalid_os_type` so the handler can pass it through `writeAPIError`.
//
// Garbage values 400 rather than silently matching nothing — mirrors the
// create-path validation in validateOSType, where a typo like `?os_type=widnows`
// returning "every linux VM" would be more misleading than failing the
// request.
func parseOSTypeFilter(raw string) (types.OSType, bool, *types.APIError) {
	normalised := normaliseOSType(raw)
	if normalised == "" {
		return "", false, nil
	}
	switch normalised {
	case types.OSTypeLinux, types.OSTypeWindows:
		return normalised, true, nil
	default:
		return "", false, types.NewAPIError(
			"invalid_os_type",
			fmt.Sprintf("os_type must be %q or %q", types.OSTypeLinux, types.OSTypeWindows),
		)
	}
}

// normaliseOSType is the shared trim+lowercase pipeline used by both the
// filter parser above and the few other handler-side reads of os_type. It is
// exported across the package so the same normalisation rule is applied
// everywhere (no drift between `validateOSType`, the filter, and the
// `ResolvedOSType` helper on the types).
func normaliseOSType(raw string) types.OSType {
	return types.OSType(strings.ToLower(strings.TrimSpace(raw)))
}
