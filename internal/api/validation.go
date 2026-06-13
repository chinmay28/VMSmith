package api

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/vmsmith/vmsmith/internal/logger"
	validatepkg "github.com/vmsmith/vmsmith/internal/validate"
	"github.com/vmsmith/vmsmith/pkg/types"
)

var vmNameRe = validatepkg.VMNameRe

// maxDescriptionLength bounds free-text description fields (VM, image, snapshot)
// at the API boundary so a misbehaving client cannot push arbitrarily large blobs
// into bbolt records.
const maxDescriptionLength = 1024

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
	if err := validateOSType(spec); err != nil {
		return err
	}
	if err := validateClockOffset(spec.ClockOffset); err != nil {
		return err
	}
	if err := validateDeviceOverrides(spec); err != nil {
		return err
	}
	if err := validateGPUs(spec.GPUs); err != nil {
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
	if patch.OSType != nil {
		return types.NewAPIError("os_type_immutable", "os_type cannot be changed after VM creation: the device profile (disk bus, NIC model, clock, Hyper-V, video, provisioning datasource) is baked at create time")
	}
	if patch.OSVariant != nil {
		return types.NewAPIError("os_type_immutable", "os_variant cannot be changed after VM creation: capture it at create time")
	}
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
	if patch.ClockOffset != nil {
		if err := validateClockOffset(*patch.ClockOffset); err != nil {
			return err
		}
	}
	if patch.DiskBus != nil {
		if err := validateDiskBus(*patch.DiskBus); err != nil {
			return err
		}
	}
	if patch.NICModel != nil {
		if err := validateNICModel(*patch.NICModel); err != nil {
			return err
		}
	}
	if _, err := normalizeTags(patch.Tags); err != nil {
		return err
	}
	return nil
}

// validateDiskBus is the shared vocabulary check used by VMSpec create
// validation and VMUpdateSpec.DiskBus PATCH validation. An empty string is
// allowed — on create it resolves to the OS-family default; on PATCH it
// clears the override.
func validateDiskBus(v string) error {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", types.DiskBusVirtio, types.DiskBusSATA:
		return nil
	default:
		return types.NewAPIError("invalid_disk_bus",
			fmt.Sprintf("disk_bus must be %q or %q", types.DiskBusVirtio, types.DiskBusSATA))
	}
}

// validateNICModel mirrors validateDiskBus for the NIC model override.
func validateNICModel(v string) error {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", types.NICModelVirtio, types.NICModelE1000e:
		return nil
	default:
		return types.NewAPIError("invalid_nic_model",
			fmt.Sprintf("nic_model must be %q or %q", types.NICModelVirtio, types.NICModelE1000e))
	}
}

// validateClockOffset checks an explicit ClockOffset value. An empty string
// resolves to the OS-family default at render time, so it is always allowed.
// Any non-empty value is matched case-insensitively against the libvirt
// vocabulary ("utc" / "localtime"); other values are rejected so a
// `clock_offset=foo` typo surfaces as a 400 rather than booting with the
// silent OS default.
func validateClockOffset(v string) error {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", types.ClockOffsetUTC, types.ClockOffsetLocaltime:
		return nil
	default:
		return types.NewAPIError("invalid_clock_offset",
			fmt.Sprintf("clock_offset must be %q or %q", types.ClockOffsetUTC, types.ClockOffsetLocaltime))
	}
}

// validateDeviceOverrides validates the optional per-VM device tuning
// fields added by roadmap 5.6.15 (disk_bus, nic_model, machine, firmware,
// virtio_win_iso). Empty values are always allowed — they resolve to the
// OS-family defaults in DomainParamsFromSpec / virtioWinISOPathForSpec.
// Non-empty values are matched case-insensitively against the libvirt
// vocabulary; anything else returns a 400 with a stable error code so the
// CLI / GUI can surface the typo to the operator instead of silently
// dropping back to the default.
func validateDeviceOverrides(spec types.VMSpec) error {
	if err := validateDiskBus(spec.DiskBus); err != nil {
		return err
	}
	if err := validateNICModel(spec.NICModel); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(spec.Firmware)) {
	case "", types.FirmwareBIOS, types.FirmwareUEFI, types.FirmwareOVMF:
		// ok
	default:
		return types.NewAPIError("invalid_firmware",
			fmt.Sprintf("firmware must be one of %q, %q, %q",
				types.FirmwareBIOS, types.FirmwareUEFI, types.FirmwareOVMF))
	}
	if !types.IsValidMachineType(spec.Machine) {
		return types.NewAPIError("invalid_machine",
			"machine must contain only letters, numbers, dots, hyphens, and underscores (e.g. pc-q35-6.2)")
	}
	// virtio_win_iso is a host path. We don't stat it here because the API
	// server may run on a different node from the libvirt daemon; the
	// LibvirtManager logs a warning + skips attachment if the path is
	// missing at create time. Reject only obviously-invalid inputs (NUL
	// bytes break filepath handling on every Unix).
	if strings.ContainsRune(spec.VirtioWinISO, '\x00') {
		return types.NewAPIError("invalid_spec", "virtio_win_iso must not contain NUL bytes")
	}
	return nil
}

// validateGPUs validates the optional VFIO passthrough GPU list. Each entry
// must be a syntactically valid PCI address in either the long
// ("0000:01:00.0") or short ("01:00.0") form; anything else returns 400
// `invalid_gpu`. Empty/whitespace entries are ignored (ResolvedGPUs drops
// them). The addresses are not checked against the host's actual GPU inventory
// here — the API server may run on a different node from the libvirt daemon,
// and an address that does not resolve to a device simply fails the domain
// start with a libvirt error the operator can act on.
func validateGPUs(gpus []string) error {
	for _, g := range gpus {
		if strings.TrimSpace(g) == "" {
			continue
		}
		if !types.IsValidPCIAddress(g) {
			return types.NewAPIError("invalid_gpu",
				fmt.Sprintf("gpu %q must be a PCI address like 0000:01:00.0 or 01:00.0", g))
		}
	}
	return nil
}

// windowsMinRAMMB and windowsMinDiskGB are the floor resource sizes vmsmith
// enforces for Windows guests ("2020 version and up": Server 2019/2022/2025 and
// Windows 10/11). They are guardrails against obviously-unbootable allocations,
// not Microsoft's exact minimums.
const (
	windowsMinRAMMB  = 2048
	windowsMinDiskGB = 32
)

// validateOSType validates the guest OS-family fields. An empty os_type means
// Linux. For Windows it additionally validates the optional os_variant against
// the known list and enforces minimum RAM/disk so the guest can actually boot.
// Resource minimums are only checked when the value is explicitly set (>0);
// zero means "use the server default", which is validated at create time.
//
// Both os_type and os_variant are matched case-insensitively here so a raw
// JSON POST with `"os_type": "Windows"` or `"os_variant": "Windows-Server-2022"`
// behaves the same as the CLI, which lowercases before sending.
func validateOSType(spec types.VMSpec) error {
	switch types.OSType(strings.ToLower(strings.TrimSpace(string(spec.OSType)))) {
	case "", types.OSTypeLinux:
		return nil
	case types.OSTypeWindows:
		// ok — fall through to Windows-specific checks
	default:
		return types.NewAPIError("invalid_os_type", fmt.Sprintf("os_type must be %q or %q", types.OSTypeLinux, types.OSTypeWindows))
	}

	if v := strings.ToLower(strings.TrimSpace(spec.OSVariant)); v != "" && !types.IsKnownWindowsVariant(v) {
		return types.NewAPIError("invalid_os_variant",
			fmt.Sprintf("os_variant must be one of %s", strings.Join(types.KnownWindowsVariants, ", ")))
	}
	if spec.RAMMB != 0 && spec.RAMMB < windowsMinRAMMB {
		return types.NewAPIError("invalid_spec", fmt.Sprintf("windows guests require at least %d MB ram_mb", windowsMinRAMMB))
	}
	if spec.DiskGB != 0 && spec.DiskGB < windowsMinDiskGB {
		return types.NewAPIError("invalid_spec", fmt.Sprintf("windows guests require at least %d GB disk_gb", windowsMinDiskGB))
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

// maxPortForwardDescriptionLength bounds the free-text label on a port-forward
// rule. It is shorter than the VM/image/snapshot description cap because
// port-forward descriptions are intended to be one-line labels (e.g. "web",
// "ssh-jumpbox", "metrics scrape target").
const maxPortForwardDescriptionLength = 256

func validatePortForwardDescription(description string) error {
	if len(description) > maxPortForwardDescriptionLength {
		return types.NewAPIError("invalid_port_forward", fmt.Sprintf("description must be at most %d characters", maxPortForwardDescriptionLength))
	}
	return nil
}

// normalizePortForwardTags re-wraps validate.NormalizeTags's error so the
// port-forward error surface stays consistent (invalid_port_forward) — same
// pattern as validatePortForwardDescription. Mirrors validateWebhookTags
// (2.2.15).
func normalizePortForwardTags(tags []string) ([]string, error) {
	normalized, err := validatepkg.NormalizeTags(tags)
	if err != nil {
		if apiErr, ok := err.(*types.APIError); ok {
			return nil, types.NewAPIError("invalid_port_forward", apiErr.Message)
		}
		return nil, err
	}
	return normalized, nil
}

func validateCreateSnapshotRequest(name, description string) error {
	if strings.TrimSpace(name) == "" {
		return types.NewAPIError("invalid_name", "snapshot name is required")
	}
	if len(description) > maxDescriptionLength {
		return types.NewAPIError("invalid_description", fmt.Sprintf("description must be at most %d characters", maxDescriptionLength))
	}
	return nil
}

func validateUpdateSnapshotRequest(description *string) error {
	if description != nil && len(*description) > maxDescriptionLength {
		return types.NewAPIError("invalid_description", fmt.Sprintf("description must be at most %d characters", maxDescriptionLength))
	}
	return nil
}

// normalizeSnapshotTags re-wraps validate.NormalizeTags's error code as
// `invalid_snapshot` so the snapshot error surface stays consistent with
// validateCreateSnapshotRequest / validateUpdateSnapshotRequest.  Mirrors
// the wrapper pattern used by validatePortForwardTags (2.2.16) and
// validateWebhookTags (2.2.15) — single source of truth for the tag
// alphabet, but each resource's error code is its own.
func normalizeSnapshotTags(tags []string) ([]string, error) {
	out, err := validatepkg.NormalizeTags(tags)
	if err == nil {
		return out, nil
	}
	if apiErr, ok := err.(*types.APIError); ok {
		return nil, types.NewAPIError("invalid_snapshot", apiErr.Message)
	}
	return nil, types.NewAPIError("invalid_snapshot", err.Error())
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
	if err := validateTemplateDescription(req.Description); err != nil {
		return err
	}
	// Mirror the VM create-path's `invalid_os_type` / `invalid_os_variant`
	// contract on the template create-path. Without this, a typo like
	// `{"os_type": "plan9"}` silently lowercases + persists, then
	// ResolvedOSType collapses it back to "linux" at read time, masking the
	// operator error.
	if err := validateOSType(types.VMSpec{
		OSType:    req.OSType,
		OSVariant: req.OSVariant,
		RAMMB:     req.RAMMB,
		DiskGB:    req.DiskGB,
	}); err != nil {
		return err
	}
	return validatepkg.ValidateTemplateRequest(req.Name, req.Image, req.CPUs, req.RAMMB, req.DiskGB)
}

const maxTemplateDescriptionLength = 1024

func validateTemplateDescription(desc string) error {
	if len(desc) > maxTemplateDescriptionLength {
		return types.NewAPIError("invalid_spec", fmt.Sprintf("description must be %d characters or fewer", maxTemplateDescriptionLength))
	}
	return nil
}

func validateUniqueTemplateName(name string, templates []*types.VMTemplate) error {
	return validatepkg.ValidateUniqueTemplateName(name, templates)
}

// validateImageDescription enforces a 1024-char cap on free-form image
// descriptions. Empty strings are allowed (treated as "not provided").
func validateImageDescription(description string) error {
	if len(description) > 1024 {
		return types.NewAPIError("invalid_spec", "description must be 1024 characters or fewer")
	}
	return nil
}

// validateWebhookDescription enforces a 1024-char cap on free-form webhook
// descriptions ("Slack notifier for production crashes", "PagerDuty
// escalations"). Empty strings are allowed; the PATCH path treats an
// explicit empty string as "clear", while the POST path treats it as "no
// description provided" since both shapes round-trip to the same stored
// value.
func validateWebhookDescription(description string) error {
	if len(description) > 1024 {
		return types.NewAPIError("invalid_webhook", "description must be 1024 characters or fewer")
	}
	return nil
}

// validateWebhookTags normalises (lowercase, trim, dedupe, alphabetise) the
// caller-supplied tag list using the shared validator. Returns the
// normalised slice on success, or an APIError with code `invalid_webhook` on
// any rule violation (empty value, length > 32 chars, illegal characters).
//
// Nil / empty input round-trips as nil so the persisted record omits the
// `tags` field entirely; this matters because every other persisted webhook
// pre-dating 2.2.15 has `Tags == nil` and the search/filter predicates treat
// an absent tag list as "no tags" rather than "the empty string".
func validateWebhookTags(tags []string) ([]string, error) {
	normalised, err := normalizeTags(tags)
	if err != nil {
		// Re-wrap with the webhook-scoped error code so the API surface stays
		// consistent with validateWebhookDescription. The validator returns a
		// generic `invalid_spec` for cross-resource use; webhook callers see
		// `invalid_webhook`.
		if apiErr, ok := err.(*types.APIError); ok {
			return nil, types.NewAPIError("invalid_webhook", apiErr.Message)
		}
		return nil, err
	}
	return normalised, nil
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
	case "invalid_name", "invalid_image", "invalid_spec", "invalid_description", "invalid_port_forward", "invalid_snapshot", "invalid_sort", "invalid_order", "invalid_webhook", "invalid_os_type", "invalid_os_variant", "invalid_clock_offset", "invalid_disk_bus", "invalid_nic_model", "invalid_machine", "invalid_firmware", "invalid_gpu", "os_type_immutable", "disk_shrink_not_allowed":
		return 400
	case "service_unavailable", "network_unavailable":
		return 503
	case "quota_exceeded":
		return 429
	case "vm_locked", "vm_already_stopped", "vm_not_running", "vm_not_paused", "vm_already_paused":
		return 409
	default:
		return fallback
	}
}

// logAndSanitizeManagerError records the raw (unsanitized) manager error to the
// structured log before stripping it down for the HTTP response. The
// client-facing messages produced by sanitizeManagerError are intentionally
// generic (e.g. "vm definition failed"), which leaves operators with no way to
// diagnose the underlying libvirt/qemu failure. Logging the full error here —
// keyed by the operation that produced it — preserves that detail in the ring
// buffer and log file (surfaced via `vmsmith logs list` and the GUI Log Viewer)
// while still returning the sanitized error to the caller. Already-typed API
// errors carry their full message to the client, so they are not re-logged.
func logAndSanitizeManagerError(op string, err error) error {
	if err == nil {
		return nil
	}
	if !types.IsAPIError(err) {
		logger.Error("api", "manager operation failed", "op", op, "error", err.Error())
	}
	return sanitizeManagerError(err)
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
