package network

import (
	"path/filepath"
	"testing"

	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func newTestPortForwarder(t *testing.T) (*PortForwarder, *store.Store) {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewPortForwarder(s), s
}

func TestPortForwarder_List_Empty(t *testing.T) {
	pf, _ := newTestPortForwarder(t)

	ports, err := pf.List("vm-x")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("expected 0 ports, got %d", len(ports))
	}
}

func TestPortForwarder_List_WithData(t *testing.T) {
	pf, s := newTestPortForwarder(t)

	fwd := &types.PortForward{
		ID: "pf-1", VMID: "vm-a", HostPort: 8080, GuestPort: 80, Protocol: types.ProtocolTCP,
	}
	if err := s.PutPortForward(fwd); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Correct VM returns the rule.
	ports, err := pf.List("vm-a")
	if err != nil {
		t.Fatalf("List vm-a: %v", err)
	}
	if len(ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(ports))
	}
	if ports[0].HostPort != 8080 {
		t.Errorf("HostPort = %d, want 8080", ports[0].HostPort)
	}

	// Different VM returns empty.
	ports, err = pf.List("vm-b")
	if err != nil {
		t.Fatalf("List vm-b: %v", err)
	}
	if len(ports) != 0 {
		t.Errorf("expected 0 ports for vm-b, got %d", len(ports))
	}
}

func TestPortForwarder_RestoreAll_Empty(t *testing.T) {
	pf, _ := newTestPortForwarder(t)

	// With no stored rules, RestoreAll should succeed without error.
	if err := pf.RestoreAll(); err != nil {
		t.Errorf("RestoreAll (empty store): %v", err)
	}
}

func TestPortForwarder_Remove_NotFound(t *testing.T) {
	pf, _ := newTestPortForwarder(t)

	err := pf.Remove("pf-nonexistent")
	if err == nil {
		t.Error("expected error removing nonexistent port forward")
	}
}
