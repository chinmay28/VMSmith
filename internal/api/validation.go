package api

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	validatepkg "github.com/vmsmith/vmsmith/internal/validate"
	"github.com/vmsmith/vmsmith/pkg/types"
)

var vmNameRe = validatepkg.VMNameRe

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
	if err := validateOptionalVMResourceValue(spec.CPUs, 1, 128, "cpus"); err != nil {
		return err
	}
	if err := validateOptionalVMResourceValue(spec.RAMMB, 128, 1024*1024, "ram_mb"); err != nil {
		return err
	}
	if err := validateOptionalVMResourceValue(spec.DiskGB, 1, 1024*10, "disk_gb"); err != nil {
		return err
	}
	if spec.NatStaticIP != "" {
		if err := validateCIDR(spec.NatStaticIP, "nat_static_ip"); err != nil {
			return err
		}
	}
	if spec.NatGateway != "" && net.ParseIP(spec.NatGateway) == nil {
		return types.NewAPIError("invalid_spec", "nat_gateway must be a valid IP address")
	}
	if _, err := normalizeTags(spec.Tags); err != nil {
		return err
	}
	return nil
}

func validateVMUpdateSpec(patch types.VMUpdateSpec) error {
	if err := validateOptionalVMResourceValue(patch.CPUs, 1, 128, "cpus"); err != nil {
		return err
	}
	if err := validateOptionalVMResourceValue(patch.RAMMB, 128, 1024*1024, "ram_mb"); err != nil {
		return err
	}
	if err := validateOptionalVMResourceValue(patch.DiskGB, 1, 1024*10, "disk_gb"); err != nil {
		return err
	}
	if patch.NatStaticIP != "" {
		if err := validateCIDR(patch.NatStaticIP, "nat_static_ip"); err != nil {
			return err
		}
	}
	if patch.NatGateway != "" && net.ParseIP(patch.NatGateway) == nil {
		return types.NewAPIError("invalid_spec", "nat_gateway must be a valid IP address")
	}
	if _, err := normalizeTags(patch.Tags); err != nil {
		return err
	}
	return nil
}

func validateVMResourceValue(value, min, max int, field string) error {
	if value < min || value > max {
		return types.NewAPIError("invalid_spec", fmt.Sprintf("%s must be between %d and %d", field, min, max))
	}
	return nil
}

func validateOptionalVMResourceValue(value, min, max int, field string) error {
	return validatepkg.ValidateOptionalVMResourceValue(value, min, max, field)
}

func validateUniqueVMName(name string, vms []*types.VM) error {
	for _, vm := range vms {
		if vm == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(vm.Name), name) {
			return types.NewAPIError("invalid_name", fmt.Sprintf("vm name %q already exists", name))
		}
	}
	return nil
}

func normalizeTags(tags []string) ([]string, error) {
	return validatepkg.NormalizeTags(tags)
}

func validateCIDR(value, field string) error {
	if _, _, err := net.ParseCIDR(value); err != nil {
		return types.NewAPIError("invalid_spec", fmt.Sprintf("%s must be valid CIDR notation, e.g. 192.168.100.50/24", field))
	}
	return nil
}

func validatePortForward(hostPort, guestPort int, proto types.Protocol) error {
	return types.ValidatePortForward(hostPort, guestPort, proto)
}

func validateCreateSnapshotRequest(name string) error {
	if strings.TrimSpace(name) == "" {
		return types.NewAPIError("invalid_name", "snapshot name is required")
	}
	return nil
}

func validateCloneVMRequest(req cloneVMRequest) error {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return types.NewAPIError("invalid_name", "vm name is required")
	}
	if !vmNameRe.MatchString(name) {
		return types.NewAPIError("invalid_name", "vm name must be 1-64 characters and contain only letters, numbers, and hyphens")
	}
	return nil
}

func validateCreateImageRequest(vmID, name string) error {
	if strings.TrimSpace(vmID) == "" {
		return types.NewAPIError("invalid_spec", "vm_id is required")
	}
	if strings.TrimSpace(name) == "" {
		return types.NewAPIError("invalid_image", "image name is required")
	}
	return nil
}

func validateTemplateRequest(req createTemplateRequest) error {
	return validatepkg.ValidateTemplateRequest(req.Name, req.Image, req.CPUs, req.RAMMB, req.DiskGB)
}

func validateUniqueTemplateName(name string, templates []*types.VMTemplate) error {
	return validatepkg.ValidateUniqueTemplateName(name, templates)
}

func validateUploadedImage(filename string, data []byte) error {
	trimmedName := strings.TrimSpace(filename)
	if trimmedName == "" {
		return types.NewAPIError("invalid_image", "uploaded filename is required")
	}
	if strings.ToLower(filepath.Ext(trimmedName)) != ".qcow2" {
		return types.NewAPIError("invalid_image", "uploaded file must have a .qcow2 extension")
	}
	if len(data) == 0 {
		return types.NewAPIError("invalid_image", "uploaded image file cannot be empty")
	}
	return nil
}

func isAPIErrorCode(err error, code string) bool {
	apiErr, ok := err.(*types.APIError)
	return ok && apiErr.Code == code
}

func statusForAPIError(err error, fallback int) int {
	apiErr, ok := err.(*types.APIError)
	if !ok {
		return fallback
	}

	switch apiErr.Code {
	case "resource_not_found":
		return 404
	case "invalid_name", "invalid_image", "invalid_spec", "disk_shrink_not_allowed":
		return 400
	case "service_unavailable", "network_unavailable":
		return 503
	case "quota_exceeded":
		return 429
	case "vm_locked":
		return 409
	default:
		return fallback
	}
}

func sanitizeManagerError(err error) error {
	if err == nil {
		return nil
	}
	if types.IsAPIError(err) {
		return err
	}

	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)

	switch {
	case strings.HasSuffix(lower, "not found"):
		return types.NewAPIError("resource_not_found", "resource not found")
	case strings.Contains(lower, "disk can only grow"):
		return types.NewAPIError("disk_shrink_not_allowed", "disk can only grow")
	case strings.Contains(lower, "invalid nat_static_ip"):
		return types.NewAPIError("invalid_spec", "nat_static_ip must be valid CIDR notation, e.g. 192.168.100.50/24")
	case strings.Contains(lower, "connecting to libvirt"):
		return types.NewAPIError("service_unavailable", "vm backend is unavailable")
	case strings.Contains(lower, "ensuring nat network") ||
		strings.Contains(lower, "ensuring network") ||
		strings.Contains(lower, "defining network") ||
		strings.Contains(lower, "setting autostart") ||
		strings.Contains(lower, "starting network") ||
		strings.Contains(lower, "looking up network") ||
		strings.Contains(lower, "updating dhcp reservation") ||
		strings.Contains(lower, "adding dhcp reservation"):
		return types.NewAPIError("network_unavailable", "vm network is unavailable")
	case strings.Contains(lower, "creating overlay disk") ||
		strings.Contains(lower, "resizing disk") ||
		strings.Contains(lower, "qemu-img"):
		return types.NewAPIError("storage_error", "vm disk operation failed")
	case strings.Contains(lower, "creating cloud-init iso") ||
		strings.Contains(lower, "regenerating cloud-init iso") ||
		strings.Contains(lower, "genisoimage") ||
		strings.Contains(lower, "mkisofs"):
		return types.NewAPIError("config_generation_failed", "vm configuration generation failed")
	case strings.Contains(lower, "defining domain") ||
		strings.Contains(lower, "redefining domain") ||
		strings.Contains(lower, "generating domain xml") ||
		strings.Contains(lower, "parsing domain template") ||
		strings.Contains(lower, "executing domain template"):
		return types.NewAPIError("vm_definition_failed", "vm definition failed")
	case strings.Contains(lower, "starting domain") ||
		strings.Contains(lower, "restarting domain") ||
		strings.Contains(lower, "force-stopping domain"):
		return types.NewAPIError("vm_state_change_failed", "vm state change failed")
	case strings.Contains(lower, "creating snapshot") ||
		strings.Contains(lower, "listing snapshots") ||
		strings.Contains(lower, "looking up snapshot"):
		return types.NewAPIError("snapshot_operation_failed", "snapshot operation failed")
	default:
		return types.NewAPIError("internal_error", "operation failed")
	}
}
