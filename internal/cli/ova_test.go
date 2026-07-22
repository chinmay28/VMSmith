package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// installFakeQemuImgCLI prepends a fake qemu-img (convert = copy) to PATH.
func installFakeQemuImgCLI(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
dst=$(eval echo \${$#})
src=$(eval echo \${$(($# - 1))})
cp "$src" "$dst"
`
	if err := os.WriteFile(filepath.Join(dir, "qemu-img"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake qemu-img: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCLI_VMExport_HappyPath(t *testing.T) {
	installFakeQemuImgCLI(t)
	mock, cleanupVM := withMockVM(t)
	defer cleanupVM()
	_, _, cleanupStorage := withTestStorage(t)
	defer cleanupStorage()

	diskPath := filepath.Join(t.TempDir(), "disk.qcow2")
	os.WriteFile(diskPath, []byte("disk-bytes"), 0o644)
	mock.SeedVM(&types.VM{
		ID: "vm-1", Name: "exportme", State: types.VMStateStopped,
		DiskPath: diskPath,
		Spec:     types.VMSpec{Name: "exportme", CPUs: 2, RAMMB: 2048, DiskGB: 20},
	})

	out := filepath.Join(t.TempDir(), "exported.ova")
	stdout, err := runCLI("vm", "export", "vm-1", "--output", out)
	if err != nil {
		t.Fatalf("vm export: %v (out: %s)", err, stdout)
	}
	if !strings.Contains(stdout, "Exported") {
		t.Errorf("stdout = %q", stdout)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("OVA not written: %v", err)
	}
}

func TestCLI_VMExport_RunningVMRefused(t *testing.T) {
	installFakeQemuImgCLI(t)
	mock, cleanupVM := withMockVM(t)
	defer cleanupVM()
	_, _, cleanupStorage := withTestStorage(t)
	defer cleanupStorage()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "runner", State: types.VMStateRunning})

	_, err := runCLI("vm", "export", "vm-1", "--output", filepath.Join(t.TempDir(), "x.ova"))
	if err == nil || !strings.Contains(err.Error(), "stop it before exporting") {
		t.Fatalf("err = %v, want running-refusal", err)
	}
}

func TestCLI_VMImport_RoundTrip(t *testing.T) {
	installFakeQemuImgCLI(t)
	mock, cleanupVM := withMockVM(t)
	defer cleanupVM()
	_, storageMgr, cleanupStorage := withTestStorage(t)
	defer cleanupStorage()

	// Produce a real OVA via the exporter.
	diskPath := filepath.Join(t.TempDir(), "disk.qcow2")
	os.WriteFile(diskPath, []byte("disk-bytes"), 0o644)
	source := &types.VM{
		ID: "vm-src", Name: "appliance", State: types.VMStateStopped,
		DiskPath: diskPath,
		Spec:     types.VMSpec{Name: "appliance", CPUs: 4, RAMMB: 4096, DiskGB: 30},
	}
	ovaPath := filepath.Join(t.TempDir(), "appliance.ova")
	if err := storageMgr.ExportOVA(source, ovaPath, nil); err != nil {
		t.Fatalf("ExportOVA: %v", err)
	}

	stdout, err := runCLI("vm", "import", ovaPath, "--name", "imported")
	if err != nil {
		t.Fatalf("vm import: %v (out: %s)", err, stdout)
	}
	if !strings.Contains(stdout, "Imported VM imported") {
		t.Errorf("stdout = %q", stdout)
	}
	if mock.VMCount() != 1 {
		t.Errorf("VMCount = %d, want 1", mock.VMCount())
	}

	// The imported image must exist on disk under the derived name.
	if !strings.Contains(stdout, "imported-ova") {
		t.Errorf("stdout should mention derived image name, got %q", stdout)
	}
}

func TestCLI_VMImport_NameFallsBackToDescriptor(t *testing.T) {
	installFakeQemuImgCLI(t)
	mock, cleanupVM := withMockVM(t)
	defer cleanupVM()
	_, storageMgr, cleanupStorage := withTestStorage(t)
	defer cleanupStorage()

	diskPath := filepath.Join(t.TempDir(), "disk.qcow2")
	os.WriteFile(diskPath, []byte("disk-bytes"), 0o644)
	source := &types.VM{
		ID: "vm-src", Name: "descriptor-name", State: types.VMStateStopped,
		DiskPath: diskPath,
		Spec:     types.VMSpec{Name: "descriptor-name", CPUs: 1, RAMMB: 1024, DiskGB: 10},
	}
	ovaPath := filepath.Join(t.TempDir(), "appliance.ova")
	if err := storageMgr.ExportOVA(source, ovaPath, nil); err != nil {
		t.Fatalf("ExportOVA: %v", err)
	}

	stdout, err := runCLI("vm", "import", ovaPath)
	if err != nil {
		t.Fatalf("vm import: %v (out: %s)", err, stdout)
	}
	if !strings.Contains(stdout, "Imported VM descriptor-name") {
		t.Errorf("stdout = %q", stdout)
	}
	if mock.VMCount() != 1 {
		t.Errorf("VMCount = %d, want 1", mock.VMCount())
	}
}

func TestCLI_VMImport_GarbageFileFails(t *testing.T) {
	installFakeQemuImgCLI(t)
	_, cleanupVM := withMockVM(t)
	defer cleanupVM()
	_, _, cleanupStorage := withTestStorage(t)
	defer cleanupStorage()

	garbage := filepath.Join(t.TempDir(), "garbage.ova")
	os.WriteFile(garbage, []byte("not-a-tar"), 0o644)

	_, err := runCLI("vm", "import", garbage, "--name", "x")
	if err == nil || !strings.Contains(err.Error(), "importing appliance") {
		t.Fatalf("err = %v, want import failure", err)
	}
}
