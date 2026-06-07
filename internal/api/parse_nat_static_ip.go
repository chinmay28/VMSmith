package api

import (
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// parseNATStaticIPFilter parses the optional `?nat_static_ip=<addr>` query
// parameter used by `GET /vms`. Contract mirrors `parseGuestIPFilter` on the
// port-forward list (5.4.73):
//
//   - empty value disables the filter
//   - whitespace is trimmed
//   - case-insensitive (matters for IPv6 literals operators paste verbatim
//     in either case)
//
// No validation rejection: nat_static_ip is a free-form CIDR / IP literal
// that operators paste verbatim. Garbage simply matches no VMs.
func parseNATStaticIPFilter(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	return v, true
}

// vmMatchesNATStaticIPFilter reports whether the VM's NatStaticIP matches the
// requested filter value. The stored value is a CIDR (e.g. `192.168.100.50/24`)
// but operators commonly remember only the IP. Match succeeds when the filter
// is exactly the stored value (case-insensitive) OR when the filter equals the
// IP portion of the stored CIDR. VMs with an empty NatStaticIP (DHCP) are
// filtered OUT whenever the filter is set, mirroring the empty-disables-match
// contract on the `?guest_ip=` port-forward filter (5.4.73).
func vmMatchesNATStaticIPFilter(vm *types.VM, filter string) bool {
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
