package host

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeSysfs builds a minimal sysfs PCI + IOMMU group tree under a temp dir and
// points the package-level roots at it for the duration of the test.
//
// Layout modelled on a desktop with an RTX 4080 (01:00.0 GPU + 01:00.1 HDMI
// audio sharing IOMMU group 15, behind a PCIe root port bridge also in the
// group) and an Intel iGPU (00:02.0 in group 2).
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	devices := filepath.Join(root, "devices")
	groups := filepath.Join(root, "iommu_groups")

	type dev struct {
		addr, class, vendor, device, driver string
		group, bootVGA                      string
	}
	devs := []dev{
		{"0000:00:02.0", "0x030000", "0x8086", "0x4680", "i915", "2", "1"},
		{"0000:01:00.0", "0x030000", "0x10de", "0x2704", "nvidia", "15", "0"},
		{"0000:01:00.1", "0x040300", "0x10de", "0x22bb", "snd_hda_intel", "15", "0"},
		{"0000:00:01.0", "0x060400", "0x8086", "0x1901", "pcieport", "15", "0"}, // bridge in group 15
		{"0000:03:00.0", "0x010601", "0x8086", "0xa282", "ahci", "9", "0"},      // SATA controller, not a GPU
	}

	for _, d := range devs {
		devDir := filepath.Join(devices, d.addr)
		if err := os.MkdirAll(devDir, 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(devDir, "class"), d.class+"\n")
		writeFile(t, filepath.Join(devDir, "vendor"), d.vendor+"\n")
		writeFile(t, filepath.Join(devDir, "device"), d.device+"\n")
		writeFile(t, filepath.Join(devDir, "boot_vga"), d.bootVGA+"\n")

		// driver symlink: devDir/driver -> ../../bus/pci/drivers/<driver>
		driverTarget := filepath.Join(root, "drivers", d.driver)
		if err := os.MkdirAll(driverTarget, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(driverTarget, filepath.Join(devDir, "driver")); err != nil {
			t.Fatal(err)
		}

		// iommu_group symlink: devDir/iommu_group -> iommu_groups/<group>
		groupDir := filepath.Join(groups, d.group)
		groupDevices := filepath.Join(groupDir, "devices")
		if err := os.MkdirAll(groupDevices, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(groupDir, filepath.Join(devDir, "iommu_group")); err != nil {
			t.Fatal(err)
		}
		// register membership: iommu_groups/<group>/devices/<addr> -> ../../../devices/<addr>
		if err := os.Symlink(devDir, filepath.Join(groupDevices, d.addr)); err != nil {
			t.Fatal(err)
		}
	}

	oldDev, oldGrp := sysfsPCIDevices, sysfsIOMMUGroups
	sysfsPCIDevices = devices
	sysfsIOMMUGroups = groups
	t.Cleanup(func() {
		sysfsPCIDevices = oldDev
		sysfsIOMMUGroups = oldGrp
	})
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverGPUs(t *testing.T) {
	fakeSysfs(t)

	gpus, err := DiscoverGPUs()
	if err != nil {
		t.Fatalf("DiscoverGPUs: %v", err)
	}

	// Only the two display controllers (00:02.0 iGPU, 01:00.0 NVIDIA) should
	// be returned — not the audio function, bridge, or SATA controller.
	if len(gpus) != 2 {
		t.Fatalf("got %d GPUs, want 2: %+v", len(gpus), gpus)
	}

	// Sorted by address: 00:02.0 first.
	igpu := gpus[0]
	if igpu.Address != "0000:00:02.0" || igpu.Vendor != "Intel" {
		t.Errorf("igpu = %+v, want addr 0000:00:02.0 vendor Intel", igpu)
	}
	if !igpu.BootVGA {
		t.Errorf("igpu.BootVGA = %v, want true", igpu.BootVGA)
	}

	nv := gpus[1]
	if nv.Address != "0000:01:00.0" {
		t.Fatalf("nv.Address = %q, want 0000:01:00.0", nv.Address)
	}
	if nv.Vendor != "NVIDIA" {
		t.Errorf("nv.Vendor = %q, want NVIDIA", nv.Vendor)
	}
	if nv.DeviceID != "0x2704" {
		t.Errorf("nv.DeviceID = %q, want 0x2704", nv.DeviceID)
	}
	if nv.Driver != "nvidia" {
		t.Errorf("nv.Driver = %q, want nvidia", nv.Driver)
	}
	if nv.BootVGA {
		t.Errorf("nv.BootVGA = %v, want false", nv.BootVGA)
	}
	if nv.IOMMUGroup != 15 {
		t.Errorf("nv.IOMMUGroup = %d, want 15", nv.IOMMUGroup)
	}
	// GroupDevices should include the GPU + its audio function, but NOT the bridge.
	want := []string{"0000:01:00.0", "0000:01:00.1"}
	if !reflect.DeepEqual(nv.GroupDevices, want) {
		t.Errorf("nv.GroupDevices = %v, want %v", nv.GroupDevices, want)
	}
}

func TestExpandIOMMUGroups(t *testing.T) {
	fakeSysfs(t)

	// Requesting just the GPU function pulls in its audio companion, excludes
	// the bridge, and accepts the short form.
	got := ExpandIOMMUGroups([]string{"01:00.0"})
	want := []string{"0000:01:00.0", "0000:01:00.1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpandIOMMUGroups([01:00.0]) = %v, want %v", got, want)
	}

	// Deduplication across overlapping requests.
	got = ExpandIOMMUGroups([]string{"0000:01:00.0", "0000:01:00.1"})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpandIOMMUGroups(both) = %v, want %v", got, want)
	}

	// Invalid entries are dropped.
	got = ExpandIOMMUGroups([]string{"bogus", "00:02.0"})
	if !reflect.DeepEqual(got, []string{"0000:00:02.0"}) {
		t.Errorf("ExpandIOMMUGroups(bogus+igpu) = %v, want [0000:00:02.0]", got)
	}
}

func TestDiscoverGPUsNoSysfs(t *testing.T) {
	oldDev := sysfsPCIDevices
	sysfsPCIDevices = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { sysfsPCIDevices = oldDev })

	gpus, err := DiscoverGPUs()
	if err != nil {
		t.Fatalf("DiscoverGPUs on missing sysfs: %v", err)
	}
	if len(gpus) != 0 {
		t.Errorf("got %d GPUs, want 0", len(gpus))
	}
}

// ExpandIOMMUGroups falls back to the bare address when the IOMMU group tree
// is unavailable (no IOMMU enabled on the host).
func TestExpandIOMMUGroupsNoIOMMU(t *testing.T) {
	oldDev, oldGrp := sysfsPCIDevices, sysfsIOMMUGroups
	sysfsPCIDevices = filepath.Join(t.TempDir(), "none")
	sysfsIOMMUGroups = filepath.Join(t.TempDir(), "none")
	t.Cleanup(func() { sysfsPCIDevices, sysfsIOMMUGroups = oldDev, oldGrp })

	got := ExpandIOMMUGroups([]string{"01:00.0"})
	if !reflect.DeepEqual(got, []string{"0000:01:00.0"}) {
		t.Errorf("ExpandIOMMUGroups with no IOMMU = %v, want [0000:01:00.0]", got)
	}
}

func TestProductionIOMMURootPathName(t *testing.T) {
	if sysfsIOMMUGroups != "/sys/kernel/iommu_groups" {
		t.Fatalf("sysfsIOMMUGroups = %q, want /sys/kernel/iommu_groups", sysfsIOMMUGroups)
	}
}
