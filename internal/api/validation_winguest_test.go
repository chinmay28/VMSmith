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

func TestValidateVMSpec_InstallISORequiresWindows(t *testing.T) {
	err := validateVMSpec(types.VMSpec{
		Name: "linux", InstallISO: "/isos/win2022.iso",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.(*types.APIError).Code != "invalid_install_iso" {
		t.Fatalf("err = %+v", err)
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
