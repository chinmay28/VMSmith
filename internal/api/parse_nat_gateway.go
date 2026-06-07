package api

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseNATGatewayFilter parses the optional `?nat_gateway=<addr>` query
// parameter used by `GET /vms`. Contract mirrors `parseNATStaticIPFilter`
// (5.4.79):
//
//   - empty value disables the filter
//   - whitespace is trimmed
//   - case-insensitive (matters for IPv6 literals operators paste verbatim
//     in either case)
//
// No validation rejection: nat_gateway is a free-form IP literal that
// operators paste verbatim. Garbage simply matches no VMs.
func parseNATGatewayFilter(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	return v, true
}

// vmMatchesNATGatewayFilter reports whether the VM's NatGateway matches the
// requested filter value. Unlike `nat_static_ip` (which is a CIDR with a
// distinct IP-portion), `nat_gateway` is always a plain gateway IP, so a
// single case-insensitive exact-match is sufficient. VMs with an empty
// NatGateway (DHCP-assigned, default gateway) are filtered OUT whenever the
// filter is set, mirroring the empty-stored-excludes contract on the
// `?nat_static_ip=` filter (5.4.79).
func vmMatchesNATGatewayFilter(vm *types.VM, filter string) bool {
	stored := strings.ToLower(strings.TrimSpace(vm.Spec.NatGateway))
	if stored == "" {
		return false
	}
	return stored == filter
}
