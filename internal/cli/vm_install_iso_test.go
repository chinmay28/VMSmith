package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestCLI_VMCreate_InstallISOWithoutImage(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	out, err := runCLI("vm", "create", "umbrel",
		"--install-iso", "umbrelos-amd64-usb-installer.iso",
		"--firmware", "uefi", "--secure-boot=false", "--disk", "128")
	if err != nil {
		t.Fatalf("vm create --install-iso: %v", err)
	}
	if !strings.Contains(out, "Install ISO: /var/lib/vmsmith/images/umbrelos-amd64-usb-installer.iso") {
		t.Errorf("expected resolved install ISO in output, got: %q", out)
	}
	if !strings.Contains(out, "--eject-iso") {
		t.Errorf("expected eject hint in output, got: %q", out)
	}

	vms, _ := mock.List(context.Background())
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(vms))
	}
	spec := vms[0].Spec
	if spec.Image != "" || spec.Firmware != "uefi" || spec.SecureBoot == nil || *spec.SecureBoot {
		t.Errorf("spec = %+v, want blank-disk uefi install with secure boot explicitly off", spec)
	}
}

func TestCLI_VMCreate_RequiresImageOrInstallISO(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "create", "nothing")
	if err == nil || !strings.Contains(err.Error(), "--image or --install-iso") {
		t.Fatalf("expected image-or-iso error, got %v", err)
	}

	_, err = runCLI("vm", "create", "both", "--image", "ubuntu", "--install-iso", "debian.iso")
	if err == nil {
		t.Fatal("expected --image and --install-iso to be mutually exclusive")
	}
}

func TestCLI_VMEdit_EjectISO(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{
		ID: "vm-iso", Name: "installer",
		Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 64, InstallISO: "/isos/debian.iso"},
	})

	if _, err := runCLI("vm", "edit", "vm-iso", "--eject-iso"); err != nil {
		t.Fatalf("vm edit --eject-iso: %v", err)
	}
	got, err := mock.Get(context.Background(), "vm-iso")
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.InstallISO != "" {
		t.Errorf("InstallISO = %q after eject, want empty", got.Spec.InstallISO)
	}
}
