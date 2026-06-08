package api

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseIPFilter parses the optional `?ip=<addr>` query parameter used by
// `GET /vms` (5.4.81). Matches against the VM's runtime-discovered IP
// (`vm.IP`) — the value shown in the VM list/detail table, populated by
// the libvirt DHCP lease lookup with fallback to the IP portion of
// `spec.nat_static_ip` for static-IP VMs. Closes the operator query
// *"which VM is at 192.168.100.42 right now?"* that `?nat_static_ip=`
// (5.4.79) cannot answer for DHCP-assigned VMs because those VMs have
// an empty `spec.nat_static_ip`.
//
// Contract mirrors `parseNATGatewayFilter` (5.4.80):
//
//   - empty value disables the filter
//   - whitespace is trimmed
//   - case-insensitive (matters for IPv6 literals operators paste in
//     either case)
//
// No validation rejection: ip is a free-form IP literal operators paste
// verbatim. Garbage simply matches no VMs.
func parseIPFilter(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	return v, true
}

// vmMatchesIPFilter reports whether the VM's runtime IP matches the
// requested filter value. VMs with an empty IP (stopped, no lease yet,
// no static IP configured) are filtered OUT whenever the filter is set,
// mirroring the empty-stored-excludes contract on `?nat_static_ip=` and
// `?nat_gateway=`.
func vmMatchesIPFilter(vm *types.VM, filter string) bool {
	stored := strings.ToLower(strings.TrimSpace(vm.IP))
	if stored == "" {
		return false
	}
	return stored == filter
}
