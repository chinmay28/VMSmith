package vm

import (
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// TestDomainParamsFromSpec_DeviceOverrides covers the five per-VM device
// fields added in roadmap 5.6.15. Each case asserts both the DomainParams
// shape *and* the rendered XML so a regression in either layer surfaces.
func TestDomainParamsFromSpec_DeviceOverrides(t *testing.T) {
	cases := []struct {
		name             string
		spec             types.VMSpec
		wantDiskBus      string
		wantDiskTarget   string
		wantCIDromTarget string
		wantNICModel     string
		wantMachine      string
		wantFirmware     string
		xmlMustContain   []string
		xmlMustNotHave   []string
	}{
		{
			name:             "linux defaults unchanged",
			spec:             types.VMSpec{Name: "lx", CPUs: 2, RAMMB: 2048},
			wantDiskBus:      "virtio",
			wantDiskTarget:   "vda",
			wantCIDromTarget: "",
			wantNICModel:     "virtio",
			wantMachine:      "pc-q35-6.2",
			wantFirmware:     "",
			xmlMustContain: []string{
				"<target dev='vda' bus='virtio'/>",
				"<model type='virtio'/>",
			},
			xmlMustNotHave: []string{"firmware='efi'"},
		},
		{
			name:             "linux pinned to sata",
			spec:             types.VMSpec{Name: "lx-sata", CPUs: 2, RAMMB: 2048, DiskBus: "sata"},
			wantDiskBus:      "sata",
			wantDiskTarget:   "sda",
			wantCIDromTarget: "sdb",
			wantNICModel:     "virtio",
			wantMachine:      "pc-q35-6.2",
			xmlMustContain: []string{
				"<target dev='sda' bus='sata'/>",
			},
		},
		{
			name:             "windows pinned to virtio (post-driver-install)",
			spec:             types.VMSpec{Name: "win-virtio", CPUs: 4, RAMMB: 4096, OSType: types.OSTypeWindows, DiskBus: "virtio", NICModel: "virtio"},
			wantDiskBus:      "virtio",
			wantDiskTarget:   "vda",
			wantCIDromTarget: "sda",
			wantNICModel:     "virtio",
			wantMachine:      "pc-q35-6.2",
			xmlMustContain: []string{
				"<target dev='vda' bus='virtio'/>",
				"<model type='virtio'/>",
			},
			xmlMustNotHave: []string{"<model type='e1000e'/>"},
		},
		{
			name:           "windows uefi (firmware='efi') emits attribute",
			spec:           types.VMSpec{Name: "win-uefi", CPUs: 4, RAMMB: 4096, OSType: types.OSTypeWindows, Firmware: "uefi"},
			wantDiskBus:    "sata",
			wantDiskTarget: "sda",
			wantNICModel:   "e1000e",
			wantMachine:    "pc-q35-6.2",
			wantFirmware:   "efi",
			xmlMustContain: []string{`<os firmware='efi'>`},
		},
		{
			name:           "ovmf is an alias for uefi",
			spec:           types.VMSpec{Name: "win-ovmf", CPUs: 4, RAMMB: 4096, OSType: types.OSTypeWindows, Firmware: "ovmf"},
			wantFirmware:   "efi",
			xmlMustContain: []string{`<os firmware='efi'>`},
		},
		{
			name:           "explicit bios stays attribute-less",
			spec:           types.VMSpec{Name: "lx-bios", CPUs: 2, RAMMB: 2048, Firmware: "bios"},
			wantFirmware:   "",
			xmlMustNotHave: []string{"firmware='"},
		},
		{
			name:           "custom machine type",
			spec:           types.VMSpec{Name: "lx-mach", CPUs: 2, RAMMB: 2048, Machine: "pc-q35-rhel9.6.0"},
			wantMachine:    "pc-q35-rhel9.6.0",
			xmlMustContain: []string{"machine='pc-q35-rhel9.6.0'"},
		},
		{
			name:           "nic model override pins e1000e on linux",
			spec:           types.VMSpec{Name: "lx-nic", CPUs: 2, RAMMB: 2048, NICModel: "e1000e"},
			wantNICModel:   "e1000e",
			xmlMustContain: []string{"<model type='e1000e'/>"},
			xmlMustNotHave: []string{"<model type='virtio'/>"},
		},
		{
			name:           "mixed-case overrides lowercased",
			spec:           types.VMSpec{Name: "lx-mixed", CPUs: 2, RAMMB: 2048, DiskBus: "SATA", NICModel: "E1000E"},
			wantDiskBus:    "sata",
			wantDiskTarget: "sda",
			wantNICModel:   "e1000e",
			xmlMustContain: []string{"bus='sata'", "<model type='e1000e'/>"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			params := DomainParamsFromSpec(c.spec, "/tmp/disk.qcow2", "/tmp/cidata.iso", "vmsmith-net", "52:54:00:aa:bb:cc")

			if c.wantDiskBus != "" && params.DiskBus != c.wantDiskBus {
				t.Errorf("DiskBus = %q, want %q", params.DiskBus, c.wantDiskBus)
			}
			if c.wantDiskTarget != "" && params.DiskTarget != c.wantDiskTarget {
				t.Errorf("DiskTarget = %q, want %q", params.DiskTarget, c.wantDiskTarget)
			}
			if c.wantCIDromTarget != "" && params.CloudInitTarget != c.wantCIDromTarget {
				t.Errorf("CloudInitTarget = %q, want %q", params.CloudInitTarget, c.wantCIDromTarget)
			}
			if c.wantNICModel != "" && params.NICModel != c.wantNICModel {
				t.Errorf("NICModel = %q, want %q", params.NICModel, c.wantNICModel)
			}
			if c.wantMachine != "" && params.Machine != c.wantMachine {
				t.Errorf("Machine = %q, want %q", params.Machine, c.wantMachine)
			}
			if params.FirmwareAttr != c.wantFirmware {
				t.Errorf("FirmwareAttr = %q, want %q", params.FirmwareAttr, c.wantFirmware)
			}

			xml, err := GenerateDomainXML(params)
			if err != nil {
				t.Fatalf("GenerateDomainXML: %v", err)
			}
			for _, needle := range c.xmlMustContain {
				if !strings.Contains(xml, needle) {
					t.Errorf("xml missing %q:\n%s", needle, xml)
				}
			}
			for _, banned := range c.xmlMustNotHave {
				if strings.Contains(xml, banned) {
					t.Errorf("xml should not contain %q:\n%s", banned, xml)
				}
			}
		})
	}
}

// TestDomainParamsFromSpec_NICModelOverrideAppliedToEveryInterface ensures the
// NIC override flows into every interface entry (primary NAT + extras), not
// just the first one. Regression for forgetting to update one of the
// fmt.Sprintf interface XML strings.
func TestDomainParamsFromSpec_NICModelOverrideAppliedToEveryInterface(t *testing.T) {
	spec := types.VMSpec{
		Name:     "multi-nic-override",
		CPUs:     2,
		RAMMB:    2048,
		NICModel: "e1000e",
		Networks: []types.NetworkAttachment{
			{HostInterface: "eth1", Mode: types.NetworkModeMacvtap, MacAddress: "52:54:00:aa:bb:01"},
			{Mode: types.NetworkModeBridge, Bridge: "br-data", MacAddress: "52:54:00:aa:bb:02"},
		},
	}
	params := DomainParamsFromSpec(spec, "/tmp/d.qcow2", "", "vmsmith-net", "52:54:00:11:22:33")
	if len(params.Interfaces) != 3 {
		t.Fatalf("expected 3 interfaces, got %d", len(params.Interfaces))
	}
	for i, iface := range params.Interfaces {
		if !strings.Contains(iface.XML, "type='e1000e'") {
			t.Errorf("interface[%d] should use e1000e:\n%s", i, iface.XML)
		}
		if strings.Contains(iface.XML, "type='virtio'") {
			t.Errorf("interface[%d] still references virtio:\n%s", i, iface.XML)
		}
	}
}
