package vm

import (
	"context"
	"fmt"
	"sort"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// WithQuotas wraps a manager with quota enforcement. Zero-valued limits are treated as unlimited.
func WithQuotas(base Manager, quotas config.QuotasConfig) Manager {
	if base == nil {
		return nil
	}
	return &quotaManager{base: base, quotas: quotas}
}

type quotaManager struct {
	base   Manager
	quotas config.QuotasConfig
}

func (m *quotaManager) Create(ctx context.Context, spec types.VMSpec) (*types.VM, error) {
	if err := m.ensureCreateWithinQuota(ctx, spec); err != nil {
		return nil, err
	}
	return m.base.Create(ctx, spec)
}

func (m *quotaManager) Update(ctx context.Context, id string, patch types.VMUpdateSpec) (*types.VM, error) {
	if err := m.ensureUpdateWithinQuota(ctx, id, patch); err != nil {
		return nil, err
	}
	return m.base.Update(ctx, id, patch)
}

func (m *quotaManager) Start(ctx context.Context, id string) error { return m.base.Start(ctx, id) }
func (m *quotaManager) Stop(ctx context.Context, id string) error { return m.base.Stop(ctx, id) }
func (m *quotaManager) Delete(ctx context.Context, id string) error { return m.base.Delete(ctx, id) }
func (m *quotaManager) Get(ctx context.Context, id string) (*types.VM, error) { return m.base.Get(ctx, id) }
func (m *quotaManager) List(ctx context.Context) ([]*types.VM, error) { return m.base.List(ctx) }
func (m *quotaManager) CreateSnapshot(ctx context.Context, vmID string, name string) (*types.Snapshot, error) {
	snap, err := m.base.CreateSnapshot(ctx, vmID, name)
	if err != nil {
		return nil, err
	}

	if m.quotas.MaxSnapshotsPerVM > 0 {
		snaps, err := m.base.ListSnapshots(ctx, vmID)
		if err == nil && len(snaps) > m.quotas.MaxSnapshotsPerVM {
			sort.Slice(snaps, func(i, j int) bool {
				return snaps[i].CreatedAt.Before(snaps[j].CreatedAt)
			})
			toDelete := len(snaps) - m.quotas.MaxSnapshotsPerVM
			for i := 0; i < toDelete; i++ {
				if err := m.base.DeleteSnapshot(ctx, vmID, snaps[i].Name); err != nil {
					logger.Warn("quotaManager", "failed to delete old snapshot for retention", "vmID", vmID, "snapshot", snaps[i].Name, "error", err.Error())
				}
			}
		}
	}

	return snap, nil
}
func (m *quotaManager) RestoreSnapshot(ctx context.Context, vmID string, snapshotName string) error {
	return m.base.RestoreSnapshot(ctx, vmID, snapshotName)
}
func (m *quotaManager) ListSnapshots(ctx context.Context, vmID string) ([]*types.Snapshot, error) {
	return m.base.ListSnapshots(ctx, vmID)
}
func (m *quotaManager) DeleteSnapshot(ctx context.Context, vmID string, snapshotName string) error {
	return m.base.DeleteSnapshot(ctx, vmID, snapshotName)
}
func (m *quotaManager) Close() error { return m.base.Close() }

func CalculateQuotaUsage(vms []*types.VM, quotas config.QuotasConfig) types.QuotaUsage {
	usage := types.QuotaUsage{
		VMs:    types.QuotaUsageSummary{Limit: quotas.MaxVMs},
		CPUs:   types.QuotaUsageSummary{Limit: quotas.MaxTotalCPUs},
		RAMMB:  types.QuotaUsageSummary{Limit: quotas.MaxTotalRAMMB},
		DiskGB: types.QuotaUsageSummary{Limit: quotas.MaxTotalDiskGB},
	}
	for _, existing := range vms {
		if existing == nil {
			continue
		}
		usage.VMs.Used++
		usage.CPUs.Used += existing.Spec.CPUs
		usage.RAMMB.Used += existing.Spec.RAMMB
		usage.DiskGB.Used += existing.Spec.DiskGB
	}
	return usage
}

func (m *quotaManager) ensureCreateWithinQuota(ctx context.Context, spec types.VMSpec) error {
	vms, err := m.base.List(ctx)
	if err != nil {
		return err
	}
	usage := CalculateQuotaUsage(vms, m.quotas)
	return validateQuotaDelta(usage, 1, spec.CPUs, spec.RAMMB, spec.DiskGB)
}

func (m *quotaManager) ensureUpdateWithinQuota(ctx context.Context, id string, patch types.VMUpdateSpec) error {
	current, err := m.base.Get(ctx, id)
	if err != nil {
		return err
	}
	vms, err := m.base.List(ctx)
	if err != nil {
		return err
	}
	usage := CalculateQuotaUsage(vms, m.quotas)
	newCPUs := current.Spec.CPUs
	if patch.CPUs > 0 {
		newCPUs = patch.CPUs
	}
	newRAM := current.Spec.RAMMB
	if patch.RAMMB > 0 {
		newRAM = patch.RAMMB
	}
	newDisk := current.Spec.DiskGB
	if patch.DiskGB > 0 {
		newDisk = patch.DiskGB
	}
	return validateQuotaDelta(usage, 0, newCPUs-current.Spec.CPUs, newRAM-current.Spec.RAMMB, newDisk-current.Spec.DiskGB)
}

func validateQuotaDelta(usage types.QuotaUsage, vmDelta, cpuDelta, ramDelta, diskDelta int) error {
	if usage.VMs.Limit > 0 && usage.VMs.Used+vmDelta > usage.VMs.Limit {
		return quotaExceededError("max_vms", usage.VMs.Used+vmDelta, usage.VMs.Limit, "VMs")
	}
	if usage.CPUs.Limit > 0 && usage.CPUs.Used+cpuDelta > usage.CPUs.Limit {
		return quotaExceededError("max_total_cpus", usage.CPUs.Used+cpuDelta, usage.CPUs.Limit, "vCPUs")
	}
	if usage.RAMMB.Limit > 0 && usage.RAMMB.Used+ramDelta > usage.RAMMB.Limit {
		return quotaExceededError("max_total_ram_mb", usage.RAMMB.Used+ramDelta, usage.RAMMB.Limit, "RAM (MB)")
	}
	if usage.DiskGB.Limit > 0 && usage.DiskGB.Used+diskDelta > usage.DiskGB.Limit {
		return quotaExceededError("max_total_disk_gb", usage.DiskGB.Used+diskDelta, usage.DiskGB.Limit, "disk (GB)")
	}
	return nil
}

func quotaExceededError(field string, attempted int, limit int, label string) error {
	return types.NewAPIError("quota_exceeded", fmt.Sprintf("quota exceeded: %s would be %d/%d for %s", field, attempted, limit, label))
}
