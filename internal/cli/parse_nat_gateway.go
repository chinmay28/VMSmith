package cli

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseCLINATGateway mirrors the API contract for `?nat_gateway=<addr>`
// (see internal/api/parse_nat_gateway.go). Empty disables; whitespace
// trimmed; case-insensitive — IPv6 literals pasted with mixed case match.
func parseCLINATGateway(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	return v, true
}

// cliVMMatchesNATGatewayFilter mirrors `vmMatchesNATGatewayFilter` on the
// API side. Match succeeds when the filter equals the stored gateway IP
// (case-insensitive). VMs with empty NatGateway drop out whenever the
// filter is set.
func cliVMMatchesNATGatewayFilter(vm *types.VM, filter string) bool {
	stored := strings.ToLower(strings.TrimSpace(vm.Spec.NatGateway))
	if stored == "" {
		return false
	}
	return stored == filter
}
