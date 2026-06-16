package network

import (
	"fmt"
	"path/filepath"
	"strings"
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

	_, err := pf.Add("vm-a", 0, 22, "192.168.100.10", types.ProtocolTCP, AddOptions{})
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

	_, err := pf.Add("vm-b", 2222, 2222, "192.168.100.20", types.ProtocolTCP, AddOptions{})
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

	fwd, err := pf.Add("vm-a", 2222, 22, "192.168.100.10", types.ProtocolTCP, AddOptions{})
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

	fwd, err := pf.Add("vm-a", 8080, 80, "192.168.100.10", "", AddOptions{})
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

	fwd, err := pf.Add("vm-a", 5353, 53, "192.168.100.10", types.ProtocolUDP, AddOptions{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fwd.Protocol != types.ProtocolUDP {
		t.Errorf("Protocol = %q, want udp", fwd.Protocol)
	}
}

func TestPortForwarder_Add_PersistsDescription(t *testing.T) {
	pf, s := newTestPortForwarder(t)
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	}

	fwd, err := pf.Add("vm-a", 2222, 22, "192.168.100.10", types.ProtocolTCP, AddOptions{Description: "ssh-jumpbox"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fwd.Description != "ssh-jumpbox" {
		t.Errorf("Description = %q, want ssh-jumpbox", fwd.Description)
	}

	stored, err := s.ListPortForwards("vm-a")
	if err != nil {
		t.Fatalf("ListPortForwards: %v", err)
	}
	if len(stored) != 1 || stored[0].Description != "ssh-jumpbox" {
		t.Errorf("persisted description = %q, want ssh-jumpbox", stored[0].Description)
	}
}

func TestPortForwarder_Add_DescriptionOmittedWhenEmpty(t *testing.T) {
	pf, _ := newTestPortForwarder(t)
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	}

	fwd, err := pf.Add("vm-a", 9090, 90, "192.168.100.10", types.ProtocolTCP, AddOptions{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if fwd.Description != "" {
		t.Errorf("Description = %q, want empty", fwd.Description)
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
	_, err := pf.Add("vm-a", 8080, 80, "192.168.100.10", types.ProtocolUDP, AddOptions{})
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

func TestPortForwarder_Update_SetsDescription(t *testing.T) {
	pf, s := newTestPortForwarder(t)

	fwd := &types.PortForward{
		ID: "pf-1", VMID: "vm-a", HostPort: 8080, GuestPort: 80,
		GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
	}
	if err := s.PutPortForward(fwd); err != nil {
		t.Fatalf("seed: %v", err)
	}

	desc := "  ssh-jumpbox  "
	updated, err := pf.Update("pf-1", UpdateOptions{Description: &desc})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// TrimSpace applied
	if updated.Description != "ssh-jumpbox" {
		t.Errorf("Description = %q, want %q", updated.Description, "ssh-jumpbox")
	}
	// 5-tuple untouched
	if updated.HostPort != 8080 || updated.GuestPort != 80 || updated.Protocol != types.ProtocolTCP {
		t.Errorf("5-tuple changed unexpectedly: %+v", updated)
	}

	stored, err := s.ListPortForwards("vm-a")
	if err != nil {
		t.Fatalf("ListPortForwards: %v", err)
	}
	if len(stored) != 1 || stored[0].Description != "ssh-jumpbox" {
		t.Errorf("persisted description = %q, want ssh-jumpbox", stored[0].Description)
	}
}

func TestPortForwarder_Update_ClearsDescription(t *testing.T) {
	pf, s := newTestPortForwarder(t)

	fwd := &types.PortForward{
		ID: "pf-1", VMID: "vm-a", HostPort: 8080, GuestPort: 80,
		GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
		Description: "to be cleared",
	}
	if err := s.PutPortForward(fwd); err != nil {
		t.Fatalf("seed: %v", err)
	}

	empty := ""
	updated, err := pf.Update("pf-1", UpdateOptions{Description: &empty})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Description != "" {
		t.Errorf("Description = %q, want empty", updated.Description)
	}
}

func TestPortForwarder_Update_NilDescriptionIsNoOp(t *testing.T) {
	pf, _ := newTestPortForwarder(t)

	fwd := &types.PortForward{
		ID: "pf-1", VMID: "vm-a", HostPort: 8080, GuestPort: 80,
		GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
		Description: "leave-me-alone",
	}
	if err := pf.store.PutPortForward(fwd); err != nil {
		t.Fatalf("seed: %v", err)
	}

	updated, err := pf.Update("pf-1", UpdateOptions{Description: nil})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Description != "leave-me-alone" {
		t.Errorf("Description = %q, want unchanged", updated.Description)
	}
}

func TestPortForwarder_Add_PersistsTags(t *testing.T) {
	pf, _ := newTestPortForwarder(t)
	pf.applyRuleFn = func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	}

	rule, err := pf.Add("vm-tagged", 8080, 80, "192.168.100.10", types.ProtocolTCP, AddOptions{
		Tags: []string{"production", "web"},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got, want := strings.Join(rule.Tags, ","), "production,web"; got != want {
		t.Errorf("rule.Tags = %q, want %q", got, want)
	}

	stored, err := pf.store.ListPortForwards("vm-tagged")
	if err != nil {
		t.Fatalf("ListPortForwards: %v", err)
	}
	if len(stored) != 1 || strings.Join(stored[0].Tags, ",") != "production,web" {
		t.Errorf("persisted Tags = %v", stored[0].Tags)
	}
}

func TestPortForwarder_Update_SetsTags(t *testing.T) {
	pf, _ := newTestPortForwarder(t)

	fwd := &types.PortForward{
		ID: "pf-tag", VMID: "vm-a", HostPort: 8080, GuestPort: 80,
		GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
	}
	if err := pf.store.PutPortForward(fwd); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tags := []string{"audit", "production"}
	updated, err := pf.Update("pf-tag", UpdateOptions{Tags: &tags})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if strings.Join(updated.Tags, ",") != "audit,production" {
		t.Errorf("Tags = %v", updated.Tags)
	}

	stored, _ := pf.store.ListPortForwards("vm-a")
	if strings.Join(stored[0].Tags, ",") != "audit,production" {
		t.Errorf("persisted Tags = %v", stored[0].Tags)
	}
}

func TestPortForwarder_Update_ClearsTagsWithEmptySlice(t *testing.T) {
	pf, _ := newTestPortForwarder(t)

	fwd := &types.PortForward{
		ID: "pf-tag", VMID: "vm-a", HostPort: 8080, GuestPort: 80,
		GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
		Tags: []string{"old", "set"},
	}
	if err := pf.store.PutPortForward(fwd); err != nil {
		t.Fatalf("seed: %v", err)
	}

	empty := []string{}
	updated, err := pf.Update("pf-tag", UpdateOptions{Tags: &empty})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Tags) != 0 {
		t.Errorf("expected tags cleared; got %v", updated.Tags)
	}
}

func TestPortForwarder_Update_NilTagsIsNoOp(t *testing.T) {
	pf, _ := newTestPortForwarder(t)

	fwd := &types.PortForward{
		ID: "pf-tag", VMID: "vm-a", HostPort: 8080, GuestPort: 80,
		GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
		Tags: []string{"keep", "me"},
	}
	if err := pf.store.PutPortForward(fwd); err != nil {
		t.Fatalf("seed: %v", err)
	}

	updated, err := pf.Update("pf-tag", UpdateOptions{Tags: nil})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if strings.Join(updated.Tags, ",") != "keep,me" {
		t.Errorf("Tags = %v, want unchanged", updated.Tags)
	}
}

func TestPortForwarder_Update_NotFound(t *testing.T) {
	pf, _ := newTestPortForwarder(t)
	desc := "x"
	_, err := pf.Update("pf-missing", UpdateOptions{Description: &desc})
	if err == nil {
		t.Fatal("expected resource_not_found error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	if apiErr.Code != "resource_not_found" {
		t.Errorf("Code = %q, want resource_not_found", apiErr.Code)
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
