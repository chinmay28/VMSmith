package vm

import (
	"strings"
	"testing"

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
