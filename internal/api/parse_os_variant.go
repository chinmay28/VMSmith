package api

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseOSVariantFilter parses the optional `?os_variant=<...>` query parameter
// shared by `GET /vms` (and, in a future step, `GET /templates`). It mirrors
// the contract of parseOSTypeFilter at the Windows-variant granularity.
//
// Contract:
//   - Empty / whitespace-only input returns ("", false, nil) so the caller
//     can short-circuit without distinguishing "no filter" from "match all".
//   - A case-insensitive value matching one of types.KnownWindowsVariants
//     (with surrounding whitespace trimmed) returns the canonical lowercased
//     variant string and set=true.
//   - Anything else returns a typed *types.APIError with code
//     `invalid_os_variant` so the handler can pass it through writeAPIError.
//
// Garbage values 400 rather than silently matching nothing — mirrors the
// validateOSVariant create-path contract, where a typo like
// `?os_variant=windows-12` returning "every other windows VM" would be more
// misleading than failing the request.
//
// Unlike parseOSTypeFilter, there is NO "empty stored value resolves to a
// default" semantics. OSVariant is only meaningful on Windows guests and the
// field is intentionally free-form-but-validated; an empty stored OSVariant
// means "operator did not specify an edition" and is filtered OUT whenever
// the filter is set, mirroring the webhook event_type membership semantics.
func parseOSVariantFilter(raw string) (string, bool, *types.APIError) {
	normalised := normaliseOSVariant(raw)
	if normalised == "" {
		return "", false, nil
	}
	if !types.IsKnownWindowsVariant(normalised) {
		return "", false, types.NewAPIError(
			"invalid_os_variant",
			fmt.Sprintf("os_variant must be one of: %s", strings.Join(types.KnownWindowsVariants, ", ")),
		)
	}
	return normalised, true, nil
}

// normaliseOSVariant is the shared trim+lowercase pipeline used by the filter
// parser above and any future handler-side reads of os_variant. Keeps the
// normalisation rule in one place so there's no drift between validation,
// filter, and storage.
func normaliseOSVariant(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
