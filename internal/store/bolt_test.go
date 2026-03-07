package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func tempDB(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	return s, func() { s.Close(); os.Remove(path) }
}

// --- VM tests ---

func TestPutGetVM(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	vm := &types.VM{
		ID:   "vm-1",
		Name: "test-vm",
		Spec: types.VMSpec{
			Name:   "test-vm",
			Image:  "ubuntu-22.04",
			CPUs:   4,
			RAMMB:  4096,
			DiskGB: 50,
		},
		State:     types.VMStateRunning,
		IP:        "192.168.100.10",
		DiskPath:  "/var/lib/vmsmith/vms/vm-1/disk.qcow2",
		CreatedAt: time.Now().Truncate(time.Millisecond),
	}

	if err := s.PutVM(vm); err != nil {
		t.Fatalf("PutVM: %v", err)
	}

	got, err := s.GetVM("vm-1")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}

	if got.Name != vm.Name {
		t.Errorf("Name = %q, want %q", got.Name, vm.Name)
	}
	if got.Spec.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", got.Spec.CPUs)
	}
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want %q", got.State, types.VMStateRunning)
	}
	if got.IP != "192.168.100.10" {
		t.Errorf("IP = %q, want 192.168.100.10", got.IP)
	}
}

func TestGetVM_NotFound(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	_, err := s.GetVM("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent VM")
	}
}

func TestListVMs(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	// Empty list
	vms, err := s.ListVMs()
	if err != nil {
		t.Fatalf("ListVMs empty: %v", err)
	}
	if len(vms) != 0 {
		t.Errorf("expected 0 VMs, got %d", len(vms))
	}

	// Add two VMs
	s.PutVM(&types.VM{ID: "vm-1", Name: "first"})
	s.PutVM(&types.VM{ID: "vm-2", Name: "second"})

	vms, err = s.ListVMs()
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	if len(vms) != 2 {
		t.Errorf("expected 2 VMs, got %d", len(vms))
	}
}

func TestDeleteVM(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	s.PutVM(&types.VM{ID: "vm-1", Name: "doomed"})

	if err := s.DeleteVM("vm-1"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}

	_, err := s.GetVM("vm-1")
	if err == nil {
		t.Fatal("expected VM to be deleted")
	}
}

func TestPutVM_Update(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	s.PutVM(&types.VM{ID: "vm-1", Name: "original", State: types.VMStateRunning})
	s.PutVM(&types.VM{ID: "vm-1", Name: "updated", State: types.VMStateStopped})

	got, _ := s.GetVM("vm-1")
	if got.Name != "updated" {
		t.Errorf("Name = %q, want updated", got.Name)
	}
	if got.State != types.VMStateStopped {
		t.Errorf("State = %q, want stopped", got.State)
	}
}

// --- Image tests ---

func TestImageCRUD(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	img := &types.Image{
		ID:        "img-1",
		Name:      "ubuntu-base",
		Path:      "/var/lib/vmsmith/images/ubuntu-base.qcow2",
		SizeBytes: 1073741824,
		Format:    "qcow2",
		SourceVM:  "vm-1",
		CreatedAt: time.Now(),
	}

	// Create
	if err := s.PutImage(img); err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	// Read
	got, err := s.GetImage("img-1")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if got.Name != "ubuntu-base" {
		t.Errorf("Name = %q, want ubuntu-base", got.Name)
	}
	if got.SizeBytes != 1073741824 {
		t.Errorf("SizeBytes = %d, want 1073741824", got.SizeBytes)
	}

	// List
	imgs, _ := s.ListImages()
	if len(imgs) != 1 {
		t.Errorf("expected 1 image, got %d", len(imgs))
	}

	// Delete
	s.DeleteImage("img-1")
	_, err = s.GetImage("img-1")
	if err == nil {
		t.Fatal("expected image to be deleted")
	}
}

// --- Port forward tests ---

func TestPortForwardCRUD(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	pf := &types.PortForward{
		ID:        "pf-1",
		VMID:      "vm-1",
		HostPort:  2222,
		GuestPort: 22,
		GuestIP:   "192.168.100.10",
		Protocol:  types.ProtocolTCP,
	}

	// Create
	if err := s.PutPortForward(pf); err != nil {
		t.Fatalf("PutPortForward: %v", err)
	}

	// List all
	pfs, _ := s.ListPortForwards("")
	if len(pfs) != 1 {
		t.Fatalf("expected 1 port forward, got %d", len(pfs))
	}
	if pfs[0].HostPort != 2222 {
		t.Errorf("HostPort = %d, want 2222", pfs[0].HostPort)
	}

	// List filtered by VM
	s.PutPortForward(&types.PortForward{ID: "pf-2", VMID: "vm-2", HostPort: 3333})
	filtered, _ := s.ListPortForwards("vm-1")
	if len(filtered) != 1 {
		t.Errorf("expected 1 filtered port forward, got %d", len(filtered))
	}

	// Delete
	s.DeletePortForward("pf-1")
	all, _ := s.ListPortForwards("")
	if len(all) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(all))
	}
}

func TestStoreReopenPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persist.db")

	// Write data
	s1, _ := New(path)
	s1.PutVM(&types.VM{ID: "vm-persist", Name: "survivor"})
	s1.Close()

	// Reopen and verify
	s2, _ := New(path)
	defer s2.Close()

	got, err := s2.GetVM("vm-persist")
	if err != nil {
		t.Fatalf("data not persisted: %v", err)
	}
	if got.Name != "survivor" {
		t.Errorf("Name = %q, want survivor", got.Name)
	}
}

// --- VM with Networks (round-trip JSON) ---

func TestVMWithNetworksRoundTrip(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	vm := &types.VM{
		ID:   "vm-net",
		Name: "multi-net",
		Spec: types.VMSpec{
			Name:  "multi-net",
			Image: "ubuntu",
			Networks: []types.NetworkAttachment{
				{Name: "data", Mode: types.NetworkModeMacvtap, HostInterface: "eth1", StaticIP: "192.168.1.100/24"},
				{Name: "storage", Mode: types.NetworkModeBridge, Bridge: "br-eth2"},
			},
		},
		State: types.VMStateRunning,
	}

	s.PutVM(vm)
	got, _ := s.GetVM("vm-net")

	if len(got.Spec.Networks) != 2 {
		t.Fatalf("expected 2 networks, got %d", len(got.Spec.Networks))
	}
	if got.Spec.Networks[0].StaticIP != "192.168.1.100/24" {
		t.Errorf("network[0] StaticIP = %q", got.Spec.Networks[0].StaticIP)
	}
	if got.Spec.Networks[1].Mode != types.NetworkModeBridge {
		t.Errorf("network[1] Mode = %q, want bridge", got.Spec.Networks[1].Mode)
	}
}
