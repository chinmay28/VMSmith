package network

import (
	"testing"
)

func TestFindInterfaceByName_NotFound(t *testing.T) {
	_, err := FindInterfaceByName("this-iface-does-not-exist-xyz")
	if err == nil {
		t.Error("expected error for nonexistent interface")
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
