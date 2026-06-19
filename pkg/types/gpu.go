package types

import (
	"regexp"
	"strings"
)

// GPUDevice describes a host PCI GPU (display controller) that can be passed
// through to a VM via VFIO. Returned by GET /api/v1/host/gpus and rendered by
// `vmsmith host gpus`.
type GPUDevice struct {
	// Address is the PCI address in canonical domain:bus:slot.function form,
	// e.g. "0000:01:00.0".
	Address string `json:"address"`
	// VendorID / DeviceID are the raw 0x-prefixed PCI ids, e.g. "0x10de" /
	// "0x2704" for an RTX 4080.
	VendorID string `json:"vendor_id"`
	DeviceID string `json:"device_id"`
	// Vendor is a human-readable vendor name derived from VendorID
	// ("NVIDIA", "AMD", "Intel") or the raw id when unknown.
	Vendor string `json:"vendor"`
	// Class is the raw PCI class code, e.g. "0x030000" (VGA controller) or
	// "0x030200" (3D controller).
	Class string `json:"class"`
	// Driver is the kernel driver currently bound to the device: "nvidia" or
	// "nouveau" when the host owns it, "vfio-pci" when it is ready for
	// passthrough, or "" when unbound.
	Driver string `json:"driver,omitempty"`
	// IOMMUGroup is the kernel IOMMU group number. VFIO passthrough requires
	// every device in a group to be assigned to the same guest, so vmsmith
	// attaches the whole group when this GPU is selected.
	IOMMUGroup int `json:"iommu_group"`
	// GroupDevices lists every assignable PCI function that shares this
	// device's IOMMU group (the GPU plus, typically, its HDMI audio
	// function). PCI bridges are excluded. These are the addresses vmsmith
	// actually attaches when this GPU is requested for passthrough.
	GroupDevices []string `json:"group_devices,omitempty"`
}

// pciAddrRe matches a PCI address in either the long "0000:01:00.0" form or the
// short "01:00.0" form (the domain defaults to 0000). The slot byte is limited
// to 00-1f (5 bits) and the function digit to 0-7 (3 bits), matching the PCI
// address space libvirt expects.
var pciAddrRe = regexp.MustCompile(`^(?:[0-9a-fA-F]{4}:)?[0-9a-fA-F]{2}:[0-1][0-9a-fA-F]\.[0-7]$`)

// IsValidPCIAddress reports whether s is a syntactically valid PCI address in
// either the long ("0000:01:00.0") or short ("01:00.0") form. Whitespace is
// trimmed first. An empty string is not valid.
func IsValidPCIAddress(s string) bool {
	return pciAddrRe.MatchString(strings.TrimSpace(s))
}

// NormalizePCIAddress lowercases the address and prepends the default "0000"
// PCI domain when the caller passed the short bus:slot.function form. Returns
// "" when the input is not a valid PCI address.
func NormalizePCIAddress(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !pciAddrRe.MatchString(s) {
		return ""
	}
	if strings.Count(s, ":") == 1 {
		s = "0000:" + s
	}
	return s
}

// PCIAddressParts splits a PCI address into the 0x-prefixed hex components the
// libvirt <address domain=… bus=… slot=… function=…/> element expects. The
// input may be in either the long or short form. ok is false for an invalid
// address.
func PCIAddressParts(addr string) (domain, bus, slot, function string, ok bool) {
	addr = NormalizePCIAddress(addr)
	if addr == "" {
		return "", "", "", "", false
	}
	// addr is "0000:01:00.0"
	colonParts := strings.Split(addr, ":")
	slotFunc := strings.Split(colonParts[2], ".")
	return "0x" + colonParts[0], "0x" + colonParts[1], "0x" + slotFunc[0], "0x" + slotFunc[1], true
}

// ResolvedGPUs returns the VM's requested passthrough GPU addresses normalized
// to the canonical long form, with invalid entries dropped and duplicates
// removed while preserving the caller's order. The result is the set of host
// PCI functions the operator explicitly asked for; vmsmith expands these to
// their full IOMMU groups at domain-render time (see internal/host).
func (s VMSpec) ResolvedGPUs() []string {
	seen := make(map[string]bool, len(s.GPUs))
	var out []string
	for _, g := range s.GPUs {
		n := NormalizePCIAddress(g)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
