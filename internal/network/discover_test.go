package network

import (
	"strings"
	"testing"
)

func TestDiscoverInterfaces_NoLoopback(t *testing.T) {
	ifaces, err := DiscoverInterfaces()
	if err != nil {
		t.Fatalf("DiscoverInterfaces: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Name == "lo" {
			t.Error("loopback should be excluded from DiscoverInterfaces")
		}
	}
}

func TestDiscoverInterfaces_HasFields(t *testing.T) {
	ifaces, err := DiscoverInterfaces()
	if err != nil {
		t.Fatalf("DiscoverInterfaces: %v", err)
	}
	if len(ifaces) == 0 {
		t.Skip("no non-loopback interfaces")
	}
	for _, iface := range ifaces {
		if iface.Name == "" {
			t.Error("interface Name should not be empty")
		}
		// IsPhys is a boolean so we just verify it's populated without panic
	}
}

func TestIsPhysicalInterface_VirtualDevice(t *testing.T) {
	// "lo" has no /sys/class/net/lo/device so it's virtual
	if isPhysicalInterface("lo") {
		t.Error("loopback should not be physical")
	}
}

func TestIsPhysicalInterface_NonexistentDevice(t *testing.T) {
	if isPhysicalInterface("nonexistent-iface-xyz") {
		t.Error("nonexistent interface should not be physical")
	}
}

func TestFindInterfaceByName_NotFound(t *testing.T) {
	_, err := FindInterfaceByName("this-iface-does-not-exist-xyz")
	if err == nil {
		t.Error("expected error for nonexistent interface")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

func TestFindInterfaceByName_Found(t *testing.T) {
	ifaces, err := DiscoverInterfaces()
	if err != nil {
		t.Fatalf("DiscoverInterfaces: %v", err)
	}
	if len(ifaces) == 0 {
		t.Skip("no non-loopback interfaces available in test environment")
	}

	// Find the first UP interface to use as target.
	var target string
	for _, iface := range ifaces {
		if iface.IsUp {
			target = iface.Name
			break
		}
	}
	if target == "" {
		t.Skip("no UP interfaces available in test environment")
	}

	got, err := FindInterfaceByName(target)
	if err != nil {
		t.Fatalf("FindInterfaceByName(%q): %v", target, err)
	}
	if got.Name != target {
		t.Errorf("Name = %q, want %q", got.Name, target)
	}
	if !got.IsUp {
		t.Errorf("interface %q should be UP", target)
	}
}
