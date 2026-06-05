package api

import (
	"fmt"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseClockOffsetFilter parses the optional `?clock_offset=<utc|localtime>`
// query parameter used by `GET /vms`. Mirrors the parseNICModelFilter
// contract: empty disables; a recognised value (case-insensitive,
// whitespace-trimmed) returns the canonical lowercased form; anything else
// returns a 400 with the stable `invalid_clock_offset` code so the CLI / GUI
// can surface the typo.
//
// Semantics intentionally match the create-path validation in
// validateClockOffset — the same two-value vocabulary, case-insensitive +
// trim-then-compare.
//
// Match semantics live with the handler, not here: an empty stored
// clock_offset resolves to the OS-family default (utc for Linux, localtime
// for Windows) via VMSpec.ResolvedClockOffset, so `?clock_offset=utc` matches
// both stored "utc" AND Linux VMs with no override; `?clock_offset=localtime`
// matches stored "localtime" AND Windows VMs with no override. This mirrors
// how `?disk_bus=virtio` and `?nic_model=virtio` match empty-stored Linux VMs
// via the documented OS-family defaults (5.4.69 / 5.4.70).
func parseClockOffsetFilter(raw string) (string, bool, *types.APIError) {
	normalised := strings.ToLower(strings.TrimSpace(raw))
	if normalised == "" {
		return "", false, nil
	}
	switch normalised {
	case types.ClockOffsetUTC, types.ClockOffsetLocaltime:
		return normalised, true, nil
	default:
		return "", false, types.NewAPIError(
			"invalid_clock_offset",
			fmt.Sprintf("clock_offset must be one of: %s, %s",
				types.ClockOffsetUTC, types.ClockOffsetLocaltime),
		)
	}
}

// vmMatchesClockOffsetFilter reports whether the VM matches the requested
// clock_offset bucket. Resolution defers to VMSpec.ResolvedClockOffset so a
// VM whose stored clock_offset is empty matches its OS-family default —
// Linux VMs match `?clock_offset=utc`, Windows VMs match
// `?clock_offset=localtime`. The helper centralises the empty-means-default
// semantics so the handler loop stays a single-line predicate call.
func vmMatchesClockOffsetFilter(spec types.VMSpec, filter string) bool {
	return spec.ResolvedClockOffset() == filter
}
