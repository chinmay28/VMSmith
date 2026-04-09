package network

import (
	"fmt"
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

func TestPortForwarder_Add_InvalidInputRejectedBeforeStoreOrIptables(t *testing.T) {
	pf, s := newTestPortForwarder(t)
	applyCalled := false
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		applyCalled = true
		return nil
	}

	_, err := pf.Add("vm-a", 0, 22, "192.168.100.10", types.ProtocolTCP)
	if err == nil {
		t.Fatal("expected validation error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.Code != "invalid_port_forward" {
		t.Fatalf("error code = %q, want invalid_port_forward", apiErr.Code)
	}
	if applyCalled {
		t.Fatal("expected iptables rule application to be skipped for invalid input")
	}

	ports, err := s.ListPortForwards("")
	if err != nil {
		t.Fatalf("ListPortForwards: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected no stored port forwards after validation failure, got %d", len(ports))
	}
}

func TestPortForwarder_Add_DuplicateHostPortProtocolRejected(t *testing.T) {
	pf, s := newTestPortForwarder(t)
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	}

	existing := &types.PortForward{
		ID: "pf-existing", VMID: "vm-a", HostPort: 2222, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
	}
	if err := s.PutPortForward(existing); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := pf.Add("vm-b", 2222, 2222, "192.168.100.20", types.ProtocolTCP)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.Code != "port_forward_conflict" {
		t.Fatalf("error code = %q, want port_forward_conflict", apiErr.Code)
	}

	ports, err := pf.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ports) != 1 {
		t.Fatalf("expected 1 stored port forward after rejection, got %d", len(ports))
	}
}

func TestPortForwarder_RestoreAll_Empty(t *testing.T) {
	pf, _ := newTestPortForwarder(t)
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	}

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

func TestPortForwarder_Add_Success(t *testing.T) {
	pf, s := newTestPortForwarder(t)
	var appliedActions []string
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		appliedActions = append(appliedActions, action)
		return nil
	}

	fwd, err := pf.Add("vm-a", 2222, 22, "192.168.100.10", types.ProtocolTCP)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fwd.HostPort != 2222 {
		t.Errorf("HostPort = %d, want 2222", fwd.HostPort)
	}
	if fwd.GuestPort != 22 {
		t.Errorf("GuestPort = %d, want 22", fwd.GuestPort)
	}
	if fwd.GuestIP != "192.168.100.10" {
		t.Errorf("GuestIP = %q, want 192.168.100.10", fwd.GuestIP)
	}
	if fwd.Protocol != types.ProtocolTCP {
		t.Errorf("Protocol = %q, want tcp", fwd.Protocol)
	}
	if fwd.VMID != "vm-a" {
		t.Errorf("VMID = %q, want vm-a", fwd.VMID)
	}
	if fwd.ID == "" {
		t.Error("ID should not be empty")
	}
	if len(appliedActions) != 1 || appliedActions[0] != "add" {
		t.Errorf("expected single 'add' action, got %v", appliedActions)
	}

	// Verify persisted
	stored, err := s.ListPortForwards("vm-a")
	if err != nil {
		t.Fatalf("ListPortForwards: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected 1 stored, got %d", len(stored))
	}
}

func TestPortForwarder_Add_DefaultProtocol(t *testing.T) {
	pf, _ := newTestPortForwarder(t)
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	}

	fwd, err := pf.Add("vm-a", 8080, 80, "192.168.100.10", "")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fwd.Protocol != types.ProtocolTCP {
		t.Errorf("Protocol = %q, want tcp (default)", fwd.Protocol)
	}
}

func TestPortForwarder_Add_UDP(t *testing.T) {
	pf, _ := newTestPortForwarder(t)
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	}

	fwd, err := pf.Add("vm-a", 5353, 53, "192.168.100.10", types.ProtocolUDP)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fwd.Protocol != types.ProtocolUDP {
		t.Errorf("Protocol = %q, want udp", fwd.Protocol)
	}
}

func TestPortForwarder_Add_SamePortDifferentProtocol(t *testing.T) {
	pf, s := newTestPortForwarder(t)
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	}

	existing := &types.PortForward{
		ID: "pf-tcp", VMID: "vm-a", HostPort: 8080, GuestPort: 80,
		GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
	}
	if err := s.PutPortForward(existing); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Same port but UDP should succeed
	_, err := pf.Add("vm-a", 8080, 80, "192.168.100.10", types.ProtocolUDP)
	if err != nil {
		t.Fatalf("same port different protocol should succeed: %v", err)
	}
}

func TestPortForwarder_Remove_Success(t *testing.T) {
	pf, s := newTestPortForwarder(t)
	var removedPorts []int
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		if action == "remove" {
			removedPorts = append(removedPorts, hostPort)
		}
		return nil
	}

	fwd := &types.PortForward{
		ID: "pf-rm", VMID: "vm-a", HostPort: 3000, GuestPort: 3000,
		GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
	}
	if err := s.PutPortForward(fwd); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := pf.Remove("pf-rm"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(removedPorts) != 1 || removedPorts[0] != 3000 {
		t.Errorf("expected iptables remove on port 3000, got %v", removedPorts)
	}

	// Verify deleted from store
	stored, err := s.ListPortForwards("")
	if err != nil {
		t.Fatalf("ListPortForwards: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("expected 0 stored after remove, got %d", len(stored))
	}
}

func TestPortForwarder_RestoreAll_WithRules(t *testing.T) {
	pf, s := newTestPortForwarder(t)
	var restored []int
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		if action == "add" {
			restored = append(restored, hostPort)
		}
		return nil
	}

	for _, hp := range []int{2222, 8080, 9090} {
		fwd := &types.PortForward{
			ID: fmt.Sprintf("pf-%d", hp), VMID: "vm-a", HostPort: hp, GuestPort: hp,
			GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
		}
		if err := s.PutPortForward(fwd); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := pf.RestoreAll(); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	if len(restored) != 3 {
		t.Errorf("expected 3 rules restored, got %d", len(restored))
	}
}

func TestPortForwarder_ApplyRule_UnknownAction(t *testing.T) {
	pf, _ := newTestPortForwarder(t)
	err := pf.applyRule("bogus", 80, 80, "1.2.3.4", "tcp")
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestPortForwarder_List_AllVMs(t *testing.T) {
	pf, s := newTestPortForwarder(t)

	for _, vm := range []string{"vm-a", "vm-b"} {
		fwd := &types.PortForward{
			ID: "pf-" + vm, VMID: vm, HostPort: 80, GuestPort: 80,
			GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
		}
		if err := s.PutPortForward(fwd); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Empty string lists all VMs
	all, err := pf.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 total port forwards, got %d", len(all))
	}
}
