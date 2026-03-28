package api

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

var vmNameRe = regexp.MustCompile(`^(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]{0,62}[a-zA-Z0-9])$`)

func validateVMSpec(spec types.VMSpec) error {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return types.NewAPIError("invalid_name", "vm name is required")
	}
	if !vmNameRe.MatchString(name) {
		return types.NewAPIError("invalid_name", "vm name must be 1-64 characters and contain only letters, numbers, and hyphens")
	}
	if strings.TrimSpace(spec.Image) == "" {
		return types.NewAPIError("invalid_image", "image is required")
	}
	if spec.CPUs != 0 && (spec.CPUs < 1 || spec.CPUs > 128) {
		return types.NewAPIError("invalid_spec", "cpus must be between 1 and 128")
	}
	if spec.RAMMB != 0 && (spec.RAMMB < 128 || spec.RAMMB > 1024*1024) {
		return types.NewAPIError("invalid_spec", "ram_mb must be between 128 and 1048576")
	}
	if spec.DiskGB != 0 && (spec.DiskGB < 1 || spec.DiskGB > 1024*10) {
		return types.NewAPIError("invalid_spec", "disk_gb must be between 1 and 10240")
	}
	if spec.NatStaticIP != "" {
		if err := validateCIDR(spec.NatStaticIP, "nat_static_ip"); err != nil {
			return err
		}
	}
	if spec.NatGateway != "" && net.ParseIP(spec.NatGateway) == nil {
		return types.NewAPIError("invalid_spec", "nat_gateway must be a valid IP address")
	}
	return nil
}

func validateVMUpdateSpec(patch types.VMUpdateSpec) error {
	if patch.CPUs != 0 && (patch.CPUs < 1 || patch.CPUs > 128) {
		return types.NewAPIError("invalid_spec", "cpus must be between 1 and 128")
	}
	if patch.RAMMB != 0 && (patch.RAMMB < 128 || patch.RAMMB > 1024*1024) {
		return types.NewAPIError("invalid_spec", "ram_mb must be between 128 and 1048576")
	}
	if patch.DiskGB != 0 && (patch.DiskGB < 1 || patch.DiskGB > 1024*10) {
		return types.NewAPIError("invalid_spec", "disk_gb must be between 1 and 10240")
	}
	if patch.NatStaticIP != "" {
		if err := validateCIDR(patch.NatStaticIP, "nat_static_ip"); err != nil {
			return err
		}
	}
	if patch.NatGateway != "" && net.ParseIP(patch.NatGateway) == nil {
		return types.NewAPIError("invalid_spec", "nat_gateway must be a valid IP address")
	}
	return nil
}

func validateCIDR(value, field string) error {
	if _, _, err := net.ParseCIDR(value); err != nil {
		return types.NewAPIError("invalid_spec", fmt.Sprintf("%s must be valid CIDR notation, e.g. 192.168.100.50/24", field))
	}
	return nil
}

func validatePortForward(hostPort, guestPort int, proto types.Protocol) error {
	if hostPort < 1 || hostPort > 65535 {
		return types.NewAPIError("invalid_port_forward", "host_port must be between 1 and 65535")
	}
	if guestPort < 1 || guestPort > 65535 {
		return types.NewAPIError("invalid_port_forward", "guest_port must be between 1 and 65535")
	}
	if proto != types.ProtocolTCP && proto != types.ProtocolUDP {
		return types.NewAPIError("invalid_port_forward", "protocol must be tcp or udp")
	}
	return nil
}

func sanitizeManagerError(err error) error {
	if err == nil {
		return nil
	}
	if types.IsAPIError(err) {
		return err
	}
	msg := err.Error()
	switch {
	case strings.HasSuffix(msg, "not found"):
		return types.NewAPIError("resource_not_found", "resource not found")
	case strings.Contains(msg, "disk can only grow"):
		return types.NewAPIError("disk_shrink_not_allowed", "disk can only grow")
	case strings.Contains(msg, "invalid nat_static_ip"):
		return types.NewAPIError("invalid_spec", "nat_static_ip must be valid CIDR notation, e.g. 192.168.100.50/24")
	default:
		return types.NewAPIError("internal_error", "operation failed")
	}
}
