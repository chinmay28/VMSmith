package api

import (
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func specBoolPtr(v bool) *bool { return &v }

func TestValidateVMSpec_InstallISOAndImageMutuallyExclusive(t *testing.T) {
	err := validateVMSpec(types.VMSpec{
		Name: "win", OSType: types.OSTypeWindows,
		Image: "win2022.qcow2", InstallISO: "/isos/win2022.iso",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr := err.(*types.APIError)
	if apiErr.Code != "invalid_install_iso" || !strings.Contains(apiErr.Message, "mutually exclusive") {
		t.Fatalf("err = %+v", apiErr)
	}
}

func TestValidateVMSpec_LinuxInstallISOAccepted(t *testing.T) {
	err := validateVMSpec(types.VMSpec{
		Name: "umbrel", InstallISO: "umbrelos-amd64-usb-installer.iso",
		Firmware: "uefi", DiskGB: 128,
	})
	if err != nil {
		t.Fatalf("linux install_iso should be accepted: %v", err)
	}
}

func TestValidateVMSpec_LinuxInstallISORejectsWindowsOnlyFields(t *testing.T) {
	for name, spec := range map[string]types.VMSpec{
		"image_index": {Name: "linux", InstallISO: "/isos/debian.iso", InstallImageIndex: 2},
		"locale":      {Name: "linux", InstallISO: "/isos/debian.iso", Locale: "de-DE"},
	} {
		err := validateVMSpec(spec)
		if err == nil {
			t.Fatalf("%s: expected error", name)
		}
		if apiErr := err.(*types.APIError); apiErr.Code != "invalid_install_iso" || !strings.Contains(apiErr.Message, "Windows") {
			t.Fatalf("%s: err = %+v", name, apiErr)
		}
	}
}

func TestValidateVMSpec_InstallISORejectsNUL(t *testing.T) {
	err := validateVMSpec(types.VMSpec{Name: "linux", InstallISO: "bad\x00.iso"})
	if err == nil || err.(*types.APIError).Code != "invalid_install_iso" {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateVMUpdateSpec_InstallISOEjectOnly(t *testing.T) {
	empty, iso := "", "/isos/other.iso"
	if err := validateVMUpdateSpec(types.VMUpdateSpec{InstallISO: &empty}); err != nil {
		t.Fatalf("eject should be accepted: %v", err)
	}
	err := validateVMUpdateSpec(types.VMUpdateSpec{InstallISO: &iso})
	if err == nil || err.(*types.APIError).Code != "invalid_install_iso" {
		t.Fatalf("attaching an ISO on PATCH should be rejected, err = %v", err)
	}
}

func TestStatusForAPIError_InvalidInstallISOIs400(t *testing.T) {
	// The manager's host probe returns invalid_install_iso when the ISO is
	// missing on the daemon host; it must surface as a 400, not a 500.
	if got := statusForAPIError(types.NewAPIError("invalid_install_iso", "missing"), 500); got != 400 {
		t.Fatalf("status = %d, want 400", got)
	}
}

func TestValidateVMSpec_InstallISOWithoutImageAccepted(t *testing.T) {
	err := validateVMSpec(types.VMSpec{
		Name: "win-install", OSType: types.OSTypeWindows,
		InstallISO: "/isos/win2022.iso", RAMMB: 4096, DiskGB: 64,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateVMSpec_NegativeInstallImageIndexRejected(t *testing.T) {
	err := validateVMSpec(types.VMSpec{
		Name: "win", OSType: types.OSTypeWindows,
		InstallISO: "/isos/w.iso", InstallImageIndex: -1,
	})
	if err == nil || err.(*types.APIError).Code != "invalid_install_iso" {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateVMSpec_SecureBootWithBIOSRejected(t *testing.T) {
	err := validateVMSpec(types.VMSpec{
		Name: "sb", Image: "img.qcow2",
		Firmware: "bios", SecureBoot: specBoolPtr(true),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.(*types.APIError).Code != "invalid_firmware" {
		t.Fatalf("err = %+v", err)
	}
}

func TestValidateVMSpec_Windows11WithExplicitBIOSRejected(t *testing.T) {
	// windows-11 defaults Secure Boot on, so an explicit bios firmware is
	// contradictory unless secure boot is explicitly disabled.
	err := validateVMSpec(types.VMSpec{
		Name: "win11", Image: "win11.qcow2",
		OSType: types.OSTypeWindows, OSVariant: "windows-11",
		Firmware: "bios", RAMMB: 4096, DiskGB: 64,
	})
	if err == nil || err.(*types.APIError).Code != "invalid_firmware" {
		t.Fatalf("err = %v", err)
	}

	// ...and explicitly disabling secure boot makes bios legal again.
	err = validateVMSpec(types.VMSpec{
		Name: "win11-legacy", Image: "win11.qcow2",
		OSType: types.OSTypeWindows, OSVariant: "windows-11",
		Firmware: "bios", SecureBoot: specBoolPtr(false), RAMMB: 4096, DiskGB: 64,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateVMSpec_SecureBootWithUEFIAccepted(t *testing.T) {
	err := validateVMSpec(types.VMSpec{
		Name: "sb-ok", Image: "img.qcow2",
		Firmware: "uefi", SecureBoot: specBoolPtr(true), TPM: specBoolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
