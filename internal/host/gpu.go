// Package host enumerates host hardware that vmsmith can expose to VMs.
//
// The GPU discovery here reads sysfs (no external commands) so it works in the
// daemon's minimal environment. It powers GET /api/v1/host/gpus and the IOMMU
// group expansion that the VM create path uses to assemble VFIO <hostdev>
// entries.
package host

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// sysfs roots. Package-level vars (rather than constants) so tests can point
// discovery at a fixture tree.
var (
	sysfsPCIDevices  = "/sys/bus/pci/devices"
	sysfsIOMMUGroups = "/sys/kernel/iommu_groups"
)

// TestingSetSysfsRoots swaps the sysfs roots used by GPU discovery/expansion
// and returns the previous values. Intended for tests in sibling packages that
// need to exercise applyGPUs -> GenerateDomainXML end-to-end against a fixture.
func TestingSetSysfsRoots(devicesRoot, groupsRoot string) (oldDevicesRoot, oldGroupsRoot string) {
	oldDevicesRoot, oldGroupsRoot = sysfsPCIDevices, sysfsIOMMUGroups
	sysfsPCIDevices, sysfsIOMMUGroups = devicesRoot, groupsRoot
	return oldDevicesRoot, oldGroupsRoot
}

// pciClassDisplayPrefix matches PCI display-controller class codes: VGA
// controllers ("0x0300xx"), 3D controllers ("0x0302xx"), and other display
// controllers all start with "0x03".
const pciClassDisplayPrefix = "0x03"

// pciClassBridgePrefix matches PCI bridge class codes ("0x06xxxx"). Bridges are
// never assignable and are excluded from IOMMU group membership so passing a
// GPU through does not drag a host bridge along.
const pciClassBridgePrefix = "0x06"

// DiscoverGPUs enumerates host PCI display controllers (GPUs) along with their
// vendor/device ids, current kernel driver, and IOMMU group membership. It
// returns an empty slice (not an error) on a host with no GPUs or no sysfs PCI
// tree, so callers can render an empty list cleanly.
func DiscoverGPUs() ([]types.GPUDevice, error) {
	entries, err := os.ReadDir(sysfsPCIDevices)
	if err != nil {
		if os.IsNotExist(err) {
			return []types.GPUDevice{}, nil
		}
		return nil, err
	}

	var gpus []types.GPUDevice
	for _, entry := range entries {
		addr := entry.Name()
		devDir := filepath.Join(sysfsPCIDevices, addr)

		// class is read for every PCI device on the host (not just GPUs) so
		// use the silent variant — a non-display device's class is read and
		// discarded, but a missing/unreadable class file shouldn't spam
		// warnings on every GET /host/gpus.
		class := readOptionalSysfsString(filepath.Join(devDir, "class"))
		if !strings.HasPrefix(class, pciClassDisplayPrefix) {
			continue
		}

		vendorID := readSysfsString(filepath.Join(devDir, "vendor"))
		group, _ := readIOMMUGroup(devDir)

		gpu := types.GPUDevice{
			Address:    addr,
			VendorID:   vendorID,
			DeviceID:   readSysfsString(filepath.Join(devDir, "device")),
			Vendor:     vendorName(vendorID),
			Class:      class,
			IOMMUGroup: group,
			// driver may legitimately be missing for unbound devices (e.g. a
			// GPU pre-rebound to vfio-pci via vfio-pci.ids= before the module
			// loads), and boot_vga is only present on the firmware-selected
			// primary display. Read both as optional so a healthy passthrough
			// host doesn't log a warning per GET /host/gpus.
			Driver:       readOptionalLinkBase(filepath.Join(devDir, "driver")),
			GroupDevices: groupDevices(group),
			BootVGA:      readOptionalSysfsString(filepath.Join(devDir, "boot_vga")) == "1",
		}
		gpus = append(gpus, gpu)
	}

	sort.Slice(gpus, func(i, j int) bool { return gpus[i].Address < gpus[j].Address })
	if gpus == nil {
		gpus = []types.GPUDevice{}
	}
	return gpus, nil
}

// ExpandIOMMUGroups expands a set of requested GPU PCI addresses to the full
// set of assignable functions that must be passed through together. For each
// requested address it adds every non-bridge device sharing its IOMMU group
// (the GPU plus its HDMI audio function, for example). Addresses whose IOMMU
// group cannot be resolved (no IOMMU, or a fixture without the group tree) fall
// back to passing the requested address alone. The result is deduplicated and
// sorted for a stable domain XML.
func ExpandIOMMUGroups(addrs []string) []string {
	seen := make(map[string]bool)
	for _, raw := range addrs {
		addr := types.NormalizePCIAddress(raw)
		if addr == "" {
			continue
		}
		group, ok := readIOMMUGroup(filepath.Join(sysfsPCIDevices, addr))
		members := []string{addr}
		if ok {
			if gm := groupDevices(group); len(gm) > 0 {
				members = gm
			} else {
				logger.Warn("daemon", "falling back to bare GPU because IOMMU group members could not be resolved", "gpu", addr, "iommu_group", strconv.Itoa(group))
			}
		} else {
			logger.Warn("daemon", "falling back to bare GPU because IOMMU group could not be resolved", "gpu", addr)
		}
		for _, m := range members {
			seen[m] = true
		}
	}

	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// readIOMMUGroup resolves the IOMMU group number for a PCI device directory by
// reading its iommu_group symlink (…/iommu_group -> …/iommu_group/<N>). The
// link is missing on hosts where IOMMU is disabled, so the read is optional;
// ExpandIOMMUGroups already logs a higher-level fallback warning when the
// caller actually needs the group.
func readIOMMUGroup(devDir string) (int, bool) {
	base := readOptionalLinkBase(filepath.Join(devDir, "iommu_group"))
	if base == "" {
		return 0, false
	}
	n, err := strconv.Atoi(base)
	if err != nil {
		return 0, false
	}
	return n, true
}

// groupDevices returns the assignable PCI functions in an IOMMU group, sorted,
// with bridges filtered out. Returns nil when the group tree is unavailable.
func groupDevices(group int) []string {
	dir := filepath.Join(sysfsIOMMUGroups, strconv.Itoa(group), "devices")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		addr := e.Name()
		// The higher-level "skipping IOMMU group member" warning below covers
		// the missing-class case, so use the silent reader to avoid double
		// logging the same operational state.
		class := readOptionalSysfsString(filepath.Join(sysfsPCIDevices, addr, "class"))
		if class == "" {
			logger.Warn("daemon", "skipping IOMMU group member with unreadable PCI class", "gpu", addr, "iommu_group", strconv.Itoa(group))
			continue
		}
		if strings.HasPrefix(class, pciClassBridgePrefix) {
			continue
		}
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

// readSysfsString reads a one-line sysfs attribute that the caller treats as
// mandatory. It trims surrounding whitespace and returns "" on any error,
// logging the failure so an operator sees it. Use readOptionalSysfsString for
// attributes that are absent during normal operation.
func readSysfsString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		logger.Warn("daemon", "failed to read sysfs attribute", "path", path, "error", err.Error())
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readOptionalSysfsString reads a sysfs attribute that may legitimately be
// missing on a healthy host (boot_vga is only present on the primary display,
// for example). Same return contract as readSysfsString but silent on missing.
func readOptionalSysfsString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readOptionalLinkBase returns the basename of a symlink target, or "" when
// the link cannot be read. Used for the driver and iommu_group symlinks, which
// are legitimately absent on unbound devices and on hosts without IOMMU; the
// callers that need to surface either condition do so at a higher level.
func readOptionalLinkBase(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// vendorName maps the common GPU PCI vendor ids to a human-readable name,
// falling back to the raw id for anything else.
func vendorName(vendorID string) string {
	switch strings.ToLower(strings.TrimSpace(vendorID)) {
	case "0x10de":
		return "NVIDIA"
	case "0x1002":
		return "AMD"
	case "0x8086":
		return "Intel"
	default:
		return vendorID
	}
}
