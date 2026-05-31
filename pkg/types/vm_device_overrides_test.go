package types

import "testing"

func TestVMSpecResolvedDiskBus(t *testing.T) {
	cases := []struct {
		name string
		spec VMSpec
		want string
	}{
		{"linux default", VMSpec{}, DiskBusVirtio},
		{"windows default", VMSpec{OSType: OSTypeWindows}, DiskBusSATA},
		{"explicit virtio on windows", VMSpec{OSType: OSTypeWindows, DiskBus: DiskBusVirtio}, DiskBusVirtio},
		{"explicit sata on linux", VMSpec{DiskBus: DiskBusSATA}, DiskBusSATA},
		{"mixed-case input lowercased", VMSpec{DiskBus: "SATA"}, DiskBusSATA},
		{"whitespace stripped", VMSpec{DiskBus: "  virtio  "}, DiskBusVirtio},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.ResolvedDiskBus(); got != c.want {
				t.Errorf("ResolvedDiskBus() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestVMSpecResolvedNICModel(t *testing.T) {
	cases := []struct {
		name string
		spec VMSpec
		want string
	}{
		{"linux default", VMSpec{}, NICModelVirtio},
		{"windows default", VMSpec{OSType: OSTypeWindows}, NICModelE1000e},
		{"explicit virtio on windows pins perf", VMSpec{OSType: OSTypeWindows, NICModel: NICModelVirtio}, NICModelVirtio},
		{"explicit e1000e on linux", VMSpec{NICModel: NICModelE1000e}, NICModelE1000e},
		{"mixed-case input lowercased", VMSpec{NICModel: "VirtIO"}, NICModelVirtio},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.ResolvedNICModel(); got != c.want {
				t.Errorf("ResolvedNICModel() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestVMSpecResolvedFirmwareAttr(t *testing.T) {
	cases := []struct {
		name string
		spec VMSpec
		want string
	}{
		{"empty -> bios (no attr)", VMSpec{}, ""},
		{"bios explicit -> no attr", VMSpec{Firmware: FirmwareBIOS}, ""},
		{"uefi -> efi", VMSpec{Firmware: FirmwareUEFI}, "efi"},
		{"ovmf alias -> efi", VMSpec{Firmware: FirmwareOVMF}, "efi"},
		{"mixed case", VMSpec{Firmware: "UEFI"}, "efi"},
		{"whitespace", VMSpec{Firmware: "  ovmf  "}, "efi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.ResolvedFirmwareAttr(); got != c.want {
				t.Errorf("ResolvedFirmwareAttr() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestIsValidMachineType(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"pc-q35-6.2", true},
		{"pc-q35-rhel9.6.0", true},
		{"q35", true},
		{"virt-7.2", true},
		{"machine_with_underscore", true},
		{"  pc-q35-6.2  ", true},
		{"pc q35", false},    // whitespace inside
		{"pc;rm -rf", false}, // shell metacharacter
		{"pc'q35", false},    // quote
		{"pc/q35", false},    // path separator
		{`pc"q35`, false},    // double quote
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := IsValidMachineType(c.in); got != c.want {
				t.Errorf("IsValidMachineType(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
