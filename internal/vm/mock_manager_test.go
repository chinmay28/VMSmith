package vm

import (
	"context"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestMockManager_CreateAndGet(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, err := m.Create(ctx, types.VMSpec{Name: "test", Image: "ubuntu", CPUs: 4, RAMMB: 8192})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if vm.Name != "test" {
		t.Errorf("Name = %q", vm.Name)
	}
	if vm.State != types.VMStateRunning {
		t.Errorf("State = %q", vm.State)
	}
	if vm.Spec.CPUs != 4 {
		t.Errorf("CPUs = %d", vm.Spec.CPUs)
	}

	got, err := m.Get(ctx, vm.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "test" {
		t.Errorf("Get Name = %q", got.Name)
	}
}

func TestMockManager_Defaults(t *testing.T) {
	m := NewMockManager()
	vm, _ := m.Create(context.Background(), types.VMSpec{Name: "defaults"})

	if vm.Spec.CPUs != 2 {
		t.Errorf("default CPUs = %d, want 2", vm.Spec.CPUs)
	}
	if vm.Spec.RAMMB != 2048 {
		t.Errorf("default RAM = %d, want 2048", vm.Spec.RAMMB)
	}
}

func TestMockManager_Lifecycle(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "lifecycle"})
	id := vm.ID

	// Stop
	if err := m.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, _ := m.Get(ctx, id)
	if got.State != types.VMStateStopped {
		t.Errorf("after stop: State = %q", got.State)
	}

	// Start
	if err := m.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, _ = m.Get(ctx, id)
	if got.State != types.VMStateRunning {
		t.Errorf("after start: State = %q", got.State)
	}

	// Delete
	if err := m.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := m.Get(ctx, id)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestMockManager_Snapshots(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "snaphost"})
	id := vm.ID

	// Create
	snap, err := m.CreateSnapshot(ctx, id, "snap1")
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.Name != "snap1" {
		t.Errorf("Name = %q", snap.Name)
	}

	// List
	snaps, _ := m.ListSnapshots(ctx, id)
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}

	// Restore
	if err := m.RestoreSnapshot(ctx, id, "snap1"); err != nil {
		t.Errorf("Restore: %v", err)
	}

	// Restore nonexistent
	if err := m.RestoreSnapshot(ctx, id, "nonexistent"); err == nil {
		t.Error("expected error for nonexistent snapshot")
	}

	// Delete
	if err := m.DeleteSnapshot(ctx, id, "snap1"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	snaps, _ = m.ListSnapshots(ctx, id)
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots after delete, got %d", len(snaps))
	}
}

func TestMockManager_ErrorInjection(t *testing.T) {
	m := NewMockManager()
	m.CreateErr = types.ErrTest

	_, err := m.Create(context.Background(), types.VMSpec{Name: "fail"})
	if err != types.ErrTest {
		t.Errorf("err = %v, want ErrTest", err)
	}
}

func TestMockManager_NotFound(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	if _, err := m.Get(ctx, "nope"); err == nil {
		t.Error("expected not found error")
	}
	if err := m.Start(ctx, "nope"); err == nil {
		t.Error("expected not found error")
	}
	if err := m.Stop(ctx, "nope"); err == nil {
		t.Error("expected not found error")
	}
	if err := m.Delete(ctx, "nope"); err == nil {
		t.Error("expected not found error")
	}
}

func TestMockManager_List(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vms, _ := m.List(ctx)
	if len(vms) != 0 {
		t.Errorf("empty list: got %d", len(vms))
	}

	m.Create(ctx, types.VMSpec{Name: "a"})
	m.Create(ctx, types.VMSpec{Name: "b"})

	vms, _ = m.List(ctx)
	if len(vms) != 2 {
		t.Errorf("expected 2, got %d", len(vms))
	}
}

func TestMockManager_Update(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "updatable", CPUs: 2, RAMMB: 2048, DiskGB: 20})
	id := vm.ID

	updated, err := m.Update(ctx, id, types.VMUpdateSpec{CPUs: 4, RAMMB: 8192})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Spec.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", updated.Spec.CPUs)
	}
	if updated.Spec.RAMMB != 8192 {
		t.Errorf("RAMMB = %d, want 8192", updated.Spec.RAMMB)
	}
	if updated.Spec.DiskGB != 20 {
		t.Errorf("DiskGB changed unexpectedly: %d", updated.Spec.DiskGB)
	}
}

func TestMockManager_Update_DiskGrow(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "growable", DiskGB: 20})
	updated, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{DiskGB: 40})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Spec.DiskGB != 40 {
		t.Errorf("DiskGB = %d, want 40", updated.Spec.DiskGB)
	}
}

func TestMockManager_Update_DiskShrinkRejected(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "fixed", DiskGB: 40})
	_, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{DiskGB: 20})
	if err == nil {
		t.Error("expected error when attempting to shrink disk")
	}
}

func TestMockManager_Update_NotFound(t *testing.T) {
	m := NewMockManager()
	_, err := m.Update(context.Background(), "nope", types.VMUpdateSpec{CPUs: 4})
	if err == nil {
		t.Error("expected not found error")
	}
}

func TestMockManager_Update_ErrorInjection(t *testing.T) {
	m := NewMockManager()
	m.UpdateErr = types.ErrTest

	vm, _ := m.Create(context.Background(), types.VMSpec{Name: "blocked", CPUs: 2, RAMMB: 2048, DiskGB: 20})
	_, err := m.Update(context.Background(), vm.ID, types.VMUpdateSpec{CPUs: 4})
	if err != types.ErrTest {
		t.Errorf("err = %v, want ErrTest", err)
	}
}

func TestMockManager_Update_NetworkIP(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	spec := types.VMSpec{
		Name: "multi-net", CPUs: 2, RAMMB: 2048, DiskGB: 20,
		Networks: []types.NetworkAttachment{
			{Name: "data", Mode: types.NetworkModeMacvtap, HostInterface: "eth1", StaticIP: "10.0.0.5/24"},
		},
	}
	vm, _ := m.Create(ctx, spec)
	updated, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{
		NetworkIPs: []types.NetworkIPUpdate{{Index: 0, StaticIP: "10.0.0.99/24", Gateway: "10.0.0.1"}},
	})
	if err != nil {
		t.Fatalf("Update network IP: %v", err)
	}
	if updated.Spec.Networks[0].StaticIP != "10.0.0.99/24" {
		t.Errorf("StaticIP = %q, want 10.0.0.99/24", updated.Spec.Networks[0].StaticIP)
	}
	if updated.Spec.Networks[0].Gateway != "10.0.0.1" {
		t.Errorf("Gateway = %q, want 10.0.0.1", updated.Spec.Networks[0].Gateway)
	}
}

func TestMockManager_Update_NetworkIP_ClearIP(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	spec := types.VMSpec{
		Name: "clear-ip", CPUs: 2, RAMMB: 2048, DiskGB: 20,
		Networks: []types.NetworkAttachment{
			{Name: "data", Mode: types.NetworkModeMacvtap, HostInterface: "eth1", StaticIP: "10.0.0.5/24"},
		},
	}
	vm, _ := m.Create(ctx, spec)
	// Empty static_ip switches the interface to DHCP
	updated, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{
		NetworkIPs: []types.NetworkIPUpdate{{Index: 0, StaticIP: ""}},
	})
	if err != nil {
		t.Fatalf("Update network IP: %v", err)
	}
	if updated.Spec.Networks[0].StaticIP != "" {
		t.Errorf("StaticIP = %q, want empty (DHCP)", updated.Spec.Networks[0].StaticIP)
	}
}

func TestMockManager_Update_NetworkIP_OutOfRange(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "no-extra-nets", CPUs: 2, RAMMB: 2048, DiskGB: 20})
	_, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{
		NetworkIPs: []types.NetworkIPUpdate{{Index: 0, StaticIP: "10.0.0.5/24"}},
	})
	if err == nil {
		t.Error("expected error for out-of-range network index")
	}
}

func TestMockManager_Update_NoChange(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "unchanged", CPUs: 2, RAMMB: 2048, DiskGB: 20})
	// Empty patch — zero values are ignored
	updated, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{})
	if err != nil {
		t.Fatalf("Update with empty patch: %v", err)
	}
	if updated.Spec.CPUs != 2 {
		t.Errorf("CPUs changed unexpectedly: %d", updated.Spec.CPUs)
	}
}

func TestMockManager_SeedVM(t *testing.T) {
	m := NewMockManager()

	m.SeedVM(&types.VM{ID: "seeded-1", Name: "pre-existing"})

	got, err := m.Get(context.Background(), "seeded-1")
	if err != nil {
		t.Fatalf("Get seeded: %v", err)
	}
	if got.Name != "pre-existing" {
		t.Errorf("Name = %q", got.Name)
	}
}
