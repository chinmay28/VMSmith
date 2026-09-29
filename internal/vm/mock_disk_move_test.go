package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func apiErrCode(err error) string {
	var apiErr *types.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func TestMockManager_CreateInDiskLocation(t *testing.T) {
	m := NewMockManager()
	m.DiskLocations = map[string]string{"bulk": "/mnt/bulk"}
	ctx := context.Background()

	vm, err := m.Create(ctx, types.VMSpec{Name: "a", Image: "img", DiskLocation: " Bulk "})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if vm.Spec.DiskLocation != "bulk" || vm.DiskPath != "/mnt/bulk/"+vm.ID+"/disk.qcow2" {
		t.Fatalf("placement = %q %q", vm.Spec.DiskLocation, vm.DiskPath)
	}

	def, err := m.Create(ctx, types.VMSpec{Name: "b", Image: "img", DiskLocation: "default"})
	if err != nil || def.Spec.DiskLocation != "" || def.DiskPath != "/var/lib/vmsmith/vms/"+def.ID+"/disk.qcow2" {
		t.Fatalf("default placement = %+v, %v", def, err)
	}

	if _, err := m.Create(ctx, types.VMSpec{Name: "c", Image: "img", DiskLocation: "nope"}); apiErrCode(err) != "invalid_disk_location" {
		t.Fatalf("unknown location err = %v", err)
	}

	clone, err := m.Clone(ctx, vm.ID, "a-clone")
	if err != nil || clone.DiskPath != "/mnt/bulk/"+clone.ID+"/disk.qcow2" || clone.Spec.DiskLocation != "bulk" {
		t.Fatalf("clone placement = %+v, %v", clone, err)
	}
}

func TestMockManager_MoveDisk(t *testing.T) {
	m := NewMockManager()
	m.DiskLocations = map[string]string{"bulk": "/mnt/bulk"}
	ctx := context.Background()
	m.SeedVM(&types.VM{ID: "vm-1", Name: "one", State: types.VMStateRunning, DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2"})

	if _, err := m.MoveDisk(ctx, "vm-1", "bulk"); apiErrCode(err) != "vm_running" {
		t.Fatalf("running err = %v", err)
	}
	if err := m.Stop(ctx, "vm-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MoveDisk(ctx, "vm-1", "default"); apiErrCode(err) != "disk_location_unchanged" {
		t.Fatalf("unchanged err = %v", err)
	}
	if _, err := m.MoveDisk(ctx, "vm-1", "nope"); apiErrCode(err) != "invalid_disk_location" {
		t.Fatalf("unknown err = %v", err)
	}

	moved, err := m.MoveDisk(ctx, "vm-1", "bulk")
	if err != nil {
		t.Fatalf("MoveDisk: %v", err)
	}
	if moved.DiskPath != "/mnt/bulk/vm-1/disk.qcow2" || moved.Spec.DiskLocation != "bulk" {
		t.Fatalf("moved = %+v", moved)
	}
	got, _ := m.Get(ctx, "vm-1")
	if got.DiskPath != moved.DiskPath {
		t.Fatalf("stored path = %q", got.DiskPath)
	}

	back, err := m.MoveDisk(ctx, "vm-1", "")
	if err != nil || back.Spec.DiskLocation != "" || back.DiskPath != "/var/lib/vmsmith/vms/vm-1/disk.qcow2" {
		t.Fatalf("move back = %+v, %v", back, err)
	}

	m.MoveDiskErr = errors.New("boom")
	if _, err := m.MoveDisk(ctx, "vm-1", "bulk"); err == nil || err.Error() != "boom" {
		t.Fatalf("injected err = %v", err)
	}
	if _, err := NewMockManager().MoveDisk(ctx, "missing", "default"); err == nil {
		t.Fatal("missing vm moved")
	}
}
