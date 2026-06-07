package cli

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseCLINATStaticIP mirrors the API contract for `?nat_static_ip=<addr>`
// (see internal/api/parse_nat_static_ip.go). Empty disables; whitespace
// trimmed; case-insensitive — IPv6 literals pasted with mixed case match.
func parseCLINATStaticIP(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	return v, true
}

// cliVMMatchesNATStaticIPFilter mirrors `vmMatchesNATStaticIPFilter` on the
// API side. Match succeeds when the filter equals the stored CIDR
// (case-insensitive) OR the stored IP portion. VMs with empty NatStaticIP
// drop out whenever the filter is set.
func cliVMMatchesNATStaticIPFilter(vm *types.VM, filter string) bool {
	stored := strings.ToLower(strings.TrimSpace(vm.Spec.NatStaticIP))
	if stored == "" {
		return false
	}
	if stored == filter {
		return true
	}
	if i := strings.Index(stored, "/"); i >= 0 && stored[:i] == filter {
		return true
	}
	return false
}
