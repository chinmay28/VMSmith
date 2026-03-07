package network

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// DiscoverInterfaces enumerates the host's network interfaces with their
// IPs and physical/virtual status. This helps users pick which host NICs
// to attach VMs to.
func DiscoverInterfaces() ([]types.HostInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}

	var result []types.HostInterface
	for _, iface := range ifaces {
		// Skip loopback
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		hi := types.HostInterface{
			Name:   iface.Name,
			MAC:    iface.HardwareAddr.String(),
			IsUp:   iface.Flags&net.FlagUp != 0,
			IsPhys: isPhysicalInterface(iface.Name),
		}

		addrs, err := iface.Addrs()
		if err == nil {
			for _, addr := range addrs {
				hi.IPs = append(hi.IPs, addr.String())
			}
		}

		result = append(result, hi)
	}

	return result, nil
}

// isPhysicalInterface checks if an interface is backed by real hardware
// by looking at /sys/class/net/<name>/device. Virtual interfaces
// (bridges, veth, macvtap, etc.) won't have this symlink.
func isPhysicalInterface(name string) bool {
	devicePath := filepath.Join("/sys/class/net", name, "device")
	_, err := os.Stat(devicePath)
	return err == nil
}

// FindInterfaceByName returns the host interface matching the given name,
// or an error if not found or not suitable for VM attachment.
func FindInterfaceByName(name string) (*types.HostInterface, error) {
	ifaces, err := DiscoverInterfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range ifaces {
		if iface.Name == name {
			if !iface.IsUp {
				return &iface, fmt.Errorf("interface %s exists but is DOWN", name)
			}
			return &iface, nil
		}
	}

	// Build a helpful list of available interfaces
	var available []string
	for _, iface := range ifaces {
		if iface.IsUp {
			ips := strings.Join(iface.IPs, ", ")
			available = append(available, fmt.Sprintf("  %s (%s)", iface.Name, ips))
		}
	}

	return nil, fmt.Errorf("interface %q not found; available interfaces:\n%s",
		name, strings.Join(available, "\n"))
}
