package vm

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestCalculateQuotaUsage(t *testing.T) {
	usage := CalculateQuotaUsage([]*types.VM{
		{Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}},
		{Spec: types.VMSpec{CPUs: 4, RAMMB: 4096, DiskGB: 40}},
	}, config.QuotasConfig{MaxVMs: 5, MaxTotalCPUs: 16, MaxTotalRAMMB: 32768, MaxTotalDiskGB: 500})
	if usage.VMs.Used != 2 || usage.VMs.Limit != 5 {
		t.Fatalf("unexpected VM usage: %+v", usage.VMs)
	}
	if usage.CPUs.Used != 6 || usage.CPUs.Limit != 16 {
		t.Fatalf("unexpected CPU usage: %+v", usage.CPUs)
	}
	if usage.RAMMB.Used != 6144 || usage.RAMMB.Limit != 32768 {
		t.Fatalf("unexpected RAM usage: %+v", usage.RAMMB)
	}
	if usage.DiskGB.Used != 60 || usage.DiskGB.Limit != 500 {
		t.Fatalf("unexpected disk usage: %+v", usage.DiskGB)
	}
}

func TestQuotaManagerCreateRejectsExceededQuota(t *testing.T) {
	base := NewMockManager()
	base.SeedVM(&types.VM{ID: "vm-1", Name: "one", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mgr := WithQuotas(base, config.QuotasConfig{MaxVMs: 1})
	_, err := mgr.Create(context.Background(), types.VMSpec{Name: "two", Image: "ubuntu", CPUs: 2, RAMMB: 2048, DiskGB: 20})
	if err == nil {
		t.Fatal("expected quota error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok || apiErr.Code != "quota_exceeded" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestQuotaManagerUpdateRejectsExceededQuota(t *testing.T) {
	base := NewMockManager()
	base.SeedVM(&types.VM{ID: "vm-1", Name: "one", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	base.SeedVM(&types.VM{ID: "vm-2", Name: "two", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mgr := WithQuotas(base, config.QuotasConfig{MaxTotalCPUs: 4})
	_, err := mgr.Update(context.Background(), "vm-1", types.VMUpdateSpec{CPUs: 3})
	if err == nil {
		t.Fatal("expected quota error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok || apiErr.Code != "quota_exceeded" {
		t.Fatalf("unexpected error: %#v", err)
	}
	if !strings.Contains(apiErr.Message, "max_total_cpus") {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
}

func TestQuotaManagerSnapshotRetention(t *testing.T) {
	base := NewMockManager()
	base.SeedVM(&types.VM{ID: "vm-1", Name: "one", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	
	// Set snapshot limit to 2
	mgr := WithQuotas(base, config.QuotasConfig{MaxSnapshotsPerVM: 2})
	ctx := context.Background()
	
	// Create first two snapshots (within limit)
	_, err := mgr.CreateSnapshot(ctx, "vm-1", "snap1")
	if err != nil {
		t.Fatalf("CreateSnapshot 1 failed: %v", err)
	}
	_, err = mgr.CreateSnapshot(ctx, "vm-1", "snap2")
	if err != nil {
		t.Fatalf("CreateSnapshot 2 failed: %v", err)
	}
	
	snaps, _ := mgr.ListSnapshots(ctx, "vm-1")
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
	
	// Creating 3rd snapshot should succeed but prune snap1
	_, err = mgr.CreateSnapshot(ctx, "vm-1", "snap3")
	if err != nil {
		t.Fatalf("CreateSnapshot 3 failed: %v", err)
	}
	
	snaps, _ = mgr.ListSnapshots(ctx, "vm-1")
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots after retention pruning, got %d", len(snaps))
	}
	if snaps[0].Name != "snap2" && snaps[1].Name != "snap2" {
		t.Errorf("snap2 should be retained")
	}
	if snaps[0].Name != "snap3" && snaps[1].Name != "snap3" {
		t.Errorf("snap3 should be retained")
	}
}

func TestQuotaManagerSnapshotRetention_DeleteErrorIsHandled(t *testing.T) {
	base := NewMockManager()
	base.SeedVM(&types.VM{ID: "vm-1", Name: "one", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	
	mgr := WithQuotas(base, config.QuotasConfig{MaxSnapshotsPerVM: 1})
	ctx := context.Background()
	
	_, _ = mgr.CreateSnapshot(ctx, "vm-1", "snap1")
	
	// Force the mock to return an error on deletion
	base.DeleteSnapshotErr = fmt.Errorf("mock delete error")
	
	// Create second snapshot (over limit)
	_, err := mgr.CreateSnapshot(ctx, "vm-1", "snap2")
	if err != nil {
		t.Fatalf("CreateSnapshot 2 failed: %v", err)
	}
	
	// Deletion failed, so we should actually have 2 snapshots now
	snaps, _ := mgr.ListSnapshots(ctx, "vm-1")
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots because pruning failed, got %d", len(snaps))
	}
}
