package cli

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseCLIGuestIP mirrors the API's parseGuestIPFilter contract for the
// `--guest-ip` flag on `vmsmith port list`. Empty disables; otherwise the
// value is whitespace-trimmed and lowercased before comparison so operators
// can paste either case form of an IPv6 literal and still hit the same
// rules. There is no validation rejection — guest_ip is free-form and any
// garbage simply matches no rules.
func parseCLIGuestIP(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	return v, true
}

// cliPortMatchesGuestIPFilter mirrors the API's portMatchesGuestIPFilter so
// the CLI list path filters identically to the daemon, without going through
// the HTTP layer.
func cliPortMatchesGuestIPFilter(pf *types.PortForward, filter string) bool {
	return strings.ToLower(strings.TrimSpace(pf.GuestIP)) == filter
}
