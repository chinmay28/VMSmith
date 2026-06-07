package cli

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseCLIIP mirrors the API contract for `?ip=<addr>` (see
// internal/api/parse_ip.go). Empty disables; whitespace trimmed;
// case-insensitive (IPv6 literals pasted with mixed case match).
func parseCLIIP(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	return v, true
}

// cliVMMatchesIPFilter mirrors `vmMatchesIPFilter` on the API side.
// Match succeeds when the filter equals the VM's runtime-discovered IP
// (case-insensitive). VMs with an empty IP (stopped, no lease yet) drop
// out whenever the filter is set.
func cliVMMatchesIPFilter(vm *types.VM, filter string) bool {
	stored := strings.ToLower(strings.TrimSpace(vm.IP))
	if stored == "" {
		return false
	}
	return stored == filter
}
