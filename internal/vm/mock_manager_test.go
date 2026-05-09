package vm

import (
	"context"
	"fmt"
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

func TestMockManager_Clone(t *testing.T) {
	m := NewMockManager()
	m.SeedVM(&types.VM{
		ID:          "vm-source",
		Name:        "source",
		Description: "base vm",
		Tags:        []string{"prod", "web"},
		State:       types.VMStateRunning,
		IP:          "192.168.100.50",
		DiskPath:    "/var/lib/vmsmith/vms/vm-source/disk.qcow2",
		Spec: types.VMSpec{
			Name:        "source",
			CPUs:        4,
			RAMMB:       8192,
			DiskGB:      80,
			Image:       "ubuntu-24.04",
			Description: "base vm",
			Tags:        []string{"prod", "web"},
		},
	})

	clone, err := m.Clone(context.Background(), "vm-source", "clone-a")
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if clone.ID == "vm-source" {
		t.Fatalf("clone reused source ID")
	}
	if clone.Name != "clone-a" || clone.Spec.Name != "clone-a" {
		t.Fatalf("clone name = %q / %q, want clone-a", clone.Name, clone.Spec.Name)
	}
	if clone.State != types.VMStateStopped {
		t.Fatalf("clone state = %q, want stopped", clone.State)
	}
	if clone.IP != "" {
		t.Fatalf("clone IP = %q, want empty", clone.IP)
	}
	if clone.Spec.CPUs != 4 || clone.Spec.RAMMB != 8192 || clone.Spec.DiskGB != 80 || clone.Spec.Image != "ubuntu-24.04" {
		t.Fatalf("clone spec mismatch: %+v", clone.Spec)
	}
	if len(clone.Tags) != 2 || clone.Tags[0] != "prod" || clone.Tags[1] != "web" {
		t.Fatalf("clone tags = %#v, want copied tags", clone.Tags)
	}

	source, err := m.Get(context.Background(), "vm-source")
	if err != nil {
		t.Fatalf("Get source: %v", err)
	}
	if source.State != types.VMStateRunning || source.IP != "192.168.100.50" {
		t.Fatalf("source mutated after clone: %+v", source)
	}
}

func TestMockManager_Clone_NotFound(t *testing.T) {
	m := NewMockManager()
	if _, err := m.Clone(context.Background(), "missing", "clone-a"); err == nil {
		t.Fatal("expected clone error for missing source VM")
	}
}

func TestMockManager_Clone_ErrorInjection(t *testing.T) {
	m := NewMockManager()
	m.CloneErr = fmt.Errorf("clone boom")
	if _, err := m.Clone(context.Background(), "vm-source", "clone-a"); err == nil || err.Error() != "clone boom" {
		t.Fatalf("Clone error = %v, want clone boom", err)
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

func TestMockManager_Restart(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "rebooter"})
	id := vm.ID

	if err := m.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := m.Restart(ctx, id); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	got, _ := m.Get(ctx, id)
	if got.State != types.VMStateRunning {
		t.Errorf("after restart: State = %q, want running", got.State)
	}

	// Restart-from-running should also leave it running.
	if err := m.Restart(ctx, id); err != nil {
		t.Fatalf("Restart from running: %v", err)
	}
	got, _ = m.Get(ctx, id)
	if got.State != types.VMStateRunning {
		t.Errorf("after second restart: State = %q, want running", got.State)
	}
}

func TestMockManager_ForceStop(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	v, _ := m.Create(ctx, types.VMSpec{Name: "kill-me"})
	if err := m.ForceStop(ctx, v.ID); err != nil {
		t.Fatalf("ForceStop: %v", err)
	}
	got, _ := m.Get(ctx, v.ID)
	if got.State != types.VMStateStopped {
		t.Errorf("after ForceStop: State = %q, want stopped", got.State)
	}
}

func TestMockManager_ForceStop_AlreadyStopped(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	v, _ := m.Create(ctx, types.VMSpec{Name: "stopped"})
	if err := m.Stop(ctx, v.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	err := m.ForceStop(ctx, v.ID)
	if err == nil {
		t.Fatal("expected error on already-stopped force-stop")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok || apiErr.Code != "vm_already_stopped" {
		t.Fatalf("err = %v, want vm_already_stopped APIError", err)
	}
}

func TestMockManager_ForceStop_NotFound(t *testing.T) {
	m := NewMockManager()
	if err := m.ForceStop(context.Background(), "vm-missing"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestMockManager_ForceStop_ErrorInjection(t *testing.T) {
	m := NewMockManager()
	vm, _ := m.Create(context.Background(), types.VMSpec{Name: "boom"})
	m.ForceStopErr = fmt.Errorf("force-stop boom")
	if err := m.ForceStop(context.Background(), vm.ID); err == nil || err.Error() != "force-stop boom" {
		t.Fatalf("err = %v, want force-stop boom", err)
	}
}

func TestMockManager_Restart_NotFound(t *testing.T) {
	m := NewMockManager()
	if err := m.Restart(context.Background(), "vm-missing"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestMockManager_Restart_ErrorInjection(t *testing.T) {
	m := NewMockManager()
	vm, _ := m.Create(context.Background(), types.VMSpec{Name: "boom"})
	m.RestartErr = fmt.Errorf("restart boom")
	if err := m.Restart(context.Background(), vm.ID); err == nil || err.Error() != "restart boom" {
		t.Fatalf("err = %v, want restart boom", err)
	}
}

func TestMockManager_SuspendResume_HappyPath(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, _ := m.Create(ctx, types.VMSpec{Name: "pauser"})

	if err := m.Suspend(ctx, vm.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	got, _ := m.Get(ctx, vm.ID)
	if got.State != types.VMStatePaused {
		t.Errorf("after Suspend: State = %q, want paused", got.State)
	}

	if err := m.Resume(ctx, vm.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got, _ = m.Get(ctx, vm.ID)
	if got.State != types.VMStateRunning {
		t.Errorf("after Resume: State = %q, want running", got.State)
	}
}

func TestMockManager_Suspend_NotRunning(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, _ := m.Create(ctx, types.VMSpec{Name: "stopped"})
	_ = m.Stop(ctx, vm.ID)

	err := m.Suspend(ctx, vm.ID)
	apiErr, ok := err.(*types.APIError)
	if !ok || apiErr.Code != "vm_not_running" {
		t.Fatalf("err = %v, want vm_not_running APIError", err)
	}
}

func TestMockManager_Suspend_AlreadyPaused(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, _ := m.Create(ctx, types.VMSpec{Name: "twice"})
	if err := m.Suspend(ctx, vm.ID); err != nil {
		t.Fatalf("first Suspend: %v", err)
	}

	err := m.Suspend(ctx, vm.ID)
	apiErr, ok := err.(*types.APIError)
	if !ok || apiErr.Code != "vm_already_paused" {
		t.Fatalf("err = %v, want vm_already_paused APIError", err)
	}
}

func TestMockManager_Resume_NotPaused(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, _ := m.Create(ctx, types.VMSpec{Name: "running"})

	err := m.Resume(ctx, vm.ID)
	apiErr, ok := err.(*types.APIError)
	if !ok || apiErr.Code != "vm_not_paused" {
		t.Fatalf("err = %v, want vm_not_paused APIError", err)
	}
}

func TestMockManager_Suspend_NotFound(t *testing.T) {
	m := NewMockManager()
	if err := m.Suspend(context.Background(), "vm-missing"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestMockManager_Resume_NotFound(t *testing.T) {
	m := NewMockManager()
	if err := m.Resume(context.Background(), "vm-missing"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestMockManager_Suspend_ErrorInjection(t *testing.T) {
	m := NewMockManager()
	vm, _ := m.Create(context.Background(), types.VMSpec{Name: "boom"})
	m.SuspendErr = fmt.Errorf("suspend boom")
	if err := m.Suspend(context.Background(), vm.ID); err == nil || err.Error() != "suspend boom" {
		t.Fatalf("err = %v, want suspend boom", err)
	}
}

func TestMockManager_Resume_ErrorInjection(t *testing.T) {
	m := NewMockManager()
	vm, _ := m.Create(context.Background(), types.VMSpec{Name: "boom"})
	m.ResumeErr = fmt.Errorf("resume boom")
	if err := m.Resume(context.Background(), vm.ID); err == nil || err.Error() != "resume boom" {
		t.Fatalf("err = %v, want resume boom", err)
	}
}

func TestMockManager_Snapshots(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "snaphost"})
	id := vm.ID

	// Create
	snap, err := m.CreateSnapshot(ctx, id, types.SnapshotSpec{Name: "snap1", Description: "before patch"})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.Name != "snap1" {
		t.Errorf("Name = %q", snap.Name)
	}
	if snap.Description != "before patch" {
		t.Errorf("Description = %q, want 'before patch'", snap.Description)
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

func TestMockManager_UpdateSnapshot_SetsDescription(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "edithost"})
	id := vm.ID
	if _, err := m.CreateSnapshot(ctx, id, types.SnapshotSpec{Name: "s1", Description: "old"}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	desc := "fresh"
	snap, err := m.UpdateSnapshot(ctx, id, "s1", types.SnapshotUpdateSpec{Description: &desc})
	if err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}
	if snap.Description != "fresh" {
		t.Errorf("returned description = %q, want fresh", snap.Description)
	}
	listed, _ := m.ListSnapshots(ctx, id)
	if len(listed) != 1 || listed[0].Description != "fresh" {
		t.Errorf("manager state did not pick up description: %+v", listed)
	}
}

func TestMockManager_UpdateSnapshot_TrimsWhitespace(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "trimhost"})
	id := vm.ID
	m.CreateSnapshot(ctx, id, types.SnapshotSpec{Name: "s"})

	desc := "   padded   "
	snap, err := m.UpdateSnapshot(ctx, id, "s", types.SnapshotUpdateSpec{Description: &desc})
	if err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}
	if snap.Description != "padded" {
		t.Errorf("description = %q, want trimmed 'padded'", snap.Description)
	}
}

func TestMockManager_UpdateSnapshot_NilDescriptionIsNoOp(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "noop"})
	id := vm.ID
	m.CreateSnapshot(ctx, id, types.SnapshotSpec{Name: "s", Description: "kept"})

	if _, err := m.UpdateSnapshot(ctx, id, "s", types.SnapshotUpdateSpec{}); err != nil {
		t.Fatalf("UpdateSnapshot: %v", err)
	}
	listed, _ := m.ListSnapshots(ctx, id)
	if len(listed) != 1 || listed[0].Description != "kept" {
		t.Errorf("description should be unchanged, got %+v", listed)
	}
}

func TestMockManager_UpdateSnapshot_VMNotFound(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	desc := "anything"
	if _, err := m.UpdateSnapshot(ctx, "vm-nope", "snap", types.SnapshotUpdateSpec{Description: &desc}); err == nil {
		t.Fatal("expected error for unknown vm")
	}
}

func TestMockManager_UpdateSnapshot_SnapshotNotFound(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "h"})
	id := vm.ID

	desc := "anything"
	if _, err := m.UpdateSnapshot(ctx, id, "missing", types.SnapshotUpdateSpec{Description: &desc}); err == nil {
		t.Fatal("expected error for unknown snapshot")
	}
}

func TestMockManager_UpdateSnapshot_ErrorInjection(t *testing.T) {
	m := NewMockManager()
	m.UpdateSnapshotErr = types.ErrTest
	desc := "x"
	if _, err := m.UpdateSnapshot(context.Background(), "vm", "snap", types.SnapshotUpdateSpec{Description: &desc}); err != types.ErrTest {
		t.Errorf("err = %v, want ErrTest", err)
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

func TestMockManager_Update_IP(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "readdressable", CPUs: 2, RAMMB: 2048, DiskGB: 20})
	updated, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{NatStaticIP: "192.168.100.50/24"})
	if err != nil {
		t.Fatalf("Update IP: %v", err)
	}
	if updated.IP != "192.168.100.50" {
		t.Errorf("IP = %q, want 192.168.100.50", updated.IP)
	}
	if updated.Spec.NatStaticIP != "192.168.100.50/24" {
		t.Errorf("NatStaticIP = %q, want 192.168.100.50/24", updated.Spec.NatStaticIP)
	}
}

func TestMockManager_Update_IP_AcceptsPlainIP(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "plain-ip"})
	// Plain IP without /24 — should be treated as /24 by the mock
	updated, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{NatStaticIP: "192.168.100.20/24"})
	if err != nil {
		t.Fatalf("Update IP: %v", err)
	}
	if updated.IP != "192.168.100.20" {
		t.Errorf("IP = %q, want 192.168.100.20", updated.IP)
	}
}

func TestMockManager_Update_IP_InvalidCIDR(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "invalid-ip"})
	_, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{NatStaticIP: "not-an-ip"})
	if err == nil {
		t.Error("expected error for invalid CIDR")
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

func TestMockManager_Update_AutoStart(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "autostart", AutoStart: false})
	if vm.Spec.AutoStart {
		t.Fatalf("initial AutoStart = true, want false")
	}

	enable := true
	updated, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{AutoStart: &enable})
	if err != nil {
		t.Fatalf("Update enable AutoStart: %v", err)
	}
	if !updated.Spec.AutoStart {
		t.Fatalf("AutoStart = false after enable, want true")
	}

	disable := false
	updated, err = m.Update(ctx, vm.ID, types.VMUpdateSpec{AutoStart: &disable})
	if err != nil {
		t.Fatalf("Update disable AutoStart: %v", err)
	}
	if updated.Spec.AutoStart {
		t.Fatalf("AutoStart = true after disable, want false")
	}

	// nil pointer means "no change" — leave the flag alone.
	updated, err = m.Update(ctx, vm.ID, types.VMUpdateSpec{Description: "still off"})
	if err != nil {
		t.Fatalf("Update description: %v", err)
	}
	if updated.Spec.AutoStart {
		t.Fatalf("AutoStart got flipped on a nil-AutoStart patch")
	}
}

func TestMockManager_Update_Locked(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "locked-test", Locked: false})
	if vm.Spec.Locked {
		t.Fatalf("initial Locked = true, want false")
	}

	enable := true
	updated, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{Locked: &enable})
	if err != nil {
		t.Fatalf("Update enable Locked: %v", err)
	}
	if !updated.Spec.Locked {
		t.Fatalf("Locked = false after enable, want true")
	}

	disable := false
	updated, err = m.Update(ctx, vm.ID, types.VMUpdateSpec{Locked: &disable})
	if err != nil {
		t.Fatalf("Update disable Locked: %v", err)
	}
	if updated.Spec.Locked {
		t.Fatalf("Locked = true after disable, want false")
	}

	// nil pointer means "no change" — leave the flag alone.
	updated, err = m.Update(ctx, vm.ID, types.VMUpdateSpec{Description: "still off"})
	if err != nil {
		t.Fatalf("Update description: %v", err)
	}
	if updated.Spec.Locked {
		t.Fatalf("Locked got flipped on a nil-Locked patch")
	}
}

func TestMockManager_Delete_Locked(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()

	vm, _ := m.Create(ctx, types.VMSpec{Name: "delete-locked", Locked: true})

	err := m.Delete(ctx, vm.ID)
	if err == nil {
		t.Fatalf("Delete locked VM succeeded, want vm_locked error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("Delete error type = %T (%v), want *types.APIError", err, err)
	}
	if apiErr.Code != "vm_locked" {
		t.Fatalf("Delete error code = %q, want %q", apiErr.Code, "vm_locked")
	}

	// Confirm the VM still exists after the rejected delete.
	if _, err := m.Get(ctx, vm.ID); err != nil {
		t.Fatalf("VM gone after rejected delete: %v", err)
	}

	// Unlock and delete should now succeed.
	unlock := false
	if _, err := m.Update(ctx, vm.ID, types.VMUpdateSpec{Locked: &unlock}); err != nil {
		t.Fatalf("Update unlock: %v", err)
	}
	if err := m.Delete(ctx, vm.ID); err != nil {
		t.Fatalf("Delete after unlock: %v", err)
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
