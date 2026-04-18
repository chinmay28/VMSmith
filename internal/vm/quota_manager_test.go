package vm

import (
	"context"
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
