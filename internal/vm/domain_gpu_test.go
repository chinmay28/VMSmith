package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/host"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestGenerateDomainXML_GPUPassthrough(t *testing.T) {
	params := DomainParams{
		Name:     "gpu-vm",
		CPUs:     8,
		RAMMB:    16384,
		DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2",
		Emulator: "/usr/bin/qemu-system-x86_64",
		GPUAddresses: []string{
			"0000:01:00.0", // GPU
			"0000:01:00.1", // HDMI audio companion
		},
	}

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}

	checks := []string{
		"<hostdev mode='subsystem' type='pci' managed='yes'>",
		"<address domain='0x0000' bus='0x01' slot='0x00' function='0x0'/>",
		"<address domain='0x0000' bus='0x01' slot='0x00' function='0x1'/>",
	}
	for _, needle := range checks {
		if !strings.Contains(xml, needle) {
			t.Errorf("domain XML missing %q\n---\n%s", needle, xml)
		}
	}

	if got := strings.Count(xml, "<hostdev"); got != 2 {
		t.Errorf("expected 2 <hostdev> entries, got %d", got)
	}
}

func TestGenerateDomainXML_NoGPUByDefault(t *testing.T) {
	params := DomainParams{
		Name:     "plain-vm",
		CPUs:     2,
		RAMMB:    2048,
		DiskPath: "/var/lib/vmsmith/vms/vm-2/disk.qcow2",
		Emulator: "/usr/bin/qemu-system-x86_64",
	}
	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	if strings.Contains(xml, "<hostdev") {
		t.Errorf("domain XML unexpectedly contains a <hostdev> entry:\n%s", xml)
	}
}

func TestGenerateDomainXML_InvalidGPUAddress(t *testing.T) {
	params := DomainParams{
		Name:         "bad-vm",
		CPUs:         2,
		RAMMB:        2048,
		DiskPath:     "/var/lib/vmsmith/vms/vm-3/disk.qcow2",
		Emulator:     "/usr/bin/qemu-system-x86_64",
		GPUAddresses: []string{"not-a-pci-address"},
	}
	if _, err := GenerateDomainXML(params); err == nil {
		t.Fatal("expected error for invalid GPU PCI address, got nil")
	}
}

func TestDomainParamsFromSpec_GPUs(t *testing.T) {
	spec := types.VMSpec{
		Name:  "gpu-spec",
		CPUs:  4,
		RAMMB: 8192,
		GPUs:  []string{"01:00.0", "01:00.0", "01:00.1"}, // dup + short form
	}
	params := DomainParamsFromSpec(spec, "/disk.qcow2", "", "vmsmith-net", "52:54:00:aa:bb:cc")
	want := []string{"0000:01:00.0", "0000:01:00.1"}
	if len(params.GPUAddresses) != 2 || params.GPUAddresses[0] != want[0] || params.GPUAddresses[1] != want[1] {
		t.Errorf("params.GPUAddresses = %v, want %v", params.GPUAddresses, want)
	}
}

func TestApplyGPUsThenGenerateDomainXML_ExpandsIOMMUGroup(t *testing.T) {
	root := t.TempDir()
	devices := filepath.Join(root, "devices")
	groups := filepath.Join(root, "iommu_groups")
	for _, d := range []struct {
		addr, class, vendor, device, driver, group string
	}{
		{"0000:01:00.0", "0x030000", "0x10de", "0x2704", "nvidia", "15"},
		{"0000:01:00.1", "0x040300", "0x10de", "0x22bb", "snd_hda_intel", "15"},
		{"0000:00:01.0", "0x060400", "0x8086", "0x1901", "pcieport", "15"},
	} {
		devDir := filepath.Join(devices, d.addr)
		if err := os.MkdirAll(devDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{"class": d.class + "\n", "vendor": d.vendor + "\n", "device": d.device + "\n"} {
			if err := os.WriteFile(filepath.Join(devDir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		driverTarget := filepath.Join(root, "drivers", d.driver)
		if err := os.MkdirAll(driverTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(driverTarget, filepath.Join(devDir, "driver")); err != nil {
			t.Fatal(err)
		}
		groupDir := filepath.Join(groups, d.group)
		groupDevices := filepath.Join(groupDir, "devices")
		if err := os.MkdirAll(groupDevices, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(groupDir, filepath.Join(devDir, "iommu_group")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(devDir, filepath.Join(groupDevices, d.addr)); err != nil {
			t.Fatal(err)
		}
	}
	oldPCIRoot, oldIOMMURoot := host.TestingSetSysfsRoots(devices, groups)
	defer host.TestingSetSysfsRoots(oldPCIRoot, oldIOMMURoot)

	spec := types.VMSpec{
		Name:  "gpu-e2e",
		CPUs:  8,
		RAMMB: 16384,
		GPUs:  []string{"0000:01:00.0"},
	}
	params := DomainParamsFromSpec(spec, "/var/lib/vmsmith/vms/vm-1/disk.qcow2", "", "vmsmith-net", "52:54:00:aa:bb:cc")

	mgr := &LibvirtManager{}
	mgr.applyGPUs(&params, spec)

	if got, want := params.GPUAddresses, []string{"0000:01:00.0", "0000:01:00.1"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("applyGPUs params.GPUAddresses = %v, want %v", got, want)
	}

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	for _, needle := range []string{
		"<address domain='0x0000' bus='0x01' slot='0x00' function='0x0'/>",
		"<address domain='0x0000' bus='0x01' slot='0x00' function='0x1'/>",
	} {
		if !strings.Contains(xml, needle) {
			t.Fatalf("domain XML missing %q\n---\n%s", needle, xml)
		}
	}
	if strings.Contains(xml, "function='0x4'") {
		t.Fatalf("domain XML unexpectedly attached bridge device:\n%s", xml)
	}
}
