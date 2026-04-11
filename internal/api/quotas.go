package api

import (
	"context"
	"fmt"

	"github.com/vmsmith/vmsmith/pkg/types"
)

type quotaUsage struct {
	VMs    int
	CPUs   int
	RAMMB  int
	DiskGB int
}

func collectQuotaUsage(vms []*types.VM) quotaUsage {
	usage := quotaUsage{}
	for _, vm := range vms {
		if vm == nil {
			continue
		}
		usage.VMs++
		usage.CPUs += vm.Spec.CPUs
		usage.RAMMB += vm.Spec.RAMMB
		usage.DiskGB += vm.Spec.DiskGB
	}
	return usage
}

func (s *Server) enforceCreateQuotas(ctx context.Context, spec types.VMSpec) error {
	if s.quotas.MaxVMs <= 0 && s.quotas.MaxTotalCPUs <= 0 && s.quotas.MaxTotalRAMMB <= 0 && s.quotas.MaxTotalDiskGB <= 0 {
		return nil
	}

	vms, err := s.vmManager.List(ctx)
	if err != nil {
		return err
	}
	usage := collectQuotaUsage(vms)
	usage.VMs++
	usage.CPUs += spec.CPUs
	usage.RAMMB += spec.RAMMB
	usage.DiskGB += spec.DiskGB

	return s.validateQuotaUsage(usage)
}

func (s *Server) enforceUpdateQuotas(ctx context.Context, current *types.VM, patch types.VMUpdateSpec) error {
	if current == nil {
		return nil
	}
	if s.quotas.MaxVMs <= 0 && s.quotas.MaxTotalCPUs <= 0 && s.quotas.MaxTotalRAMMB <= 0 && s.quotas.MaxTotalDiskGB <= 0 {
		return nil
	}

	vms, err := s.vmManager.List(ctx)
	if err != nil {
		return err
	}
	usage := collectQuotaUsage(vms)
	usage.CPUs -= current.Spec.CPUs
	usage.RAMMB -= current.Spec.RAMMB
	usage.DiskGB -= current.Spec.DiskGB

	nextCPUs := current.Spec.CPUs
	if patch.CPUs > 0 {
		nextCPUs = patch.CPUs
	}
	nextRAMMB := current.Spec.RAMMB
	if patch.RAMMB > 0 {
		nextRAMMB = patch.RAMMB
	}
	nextDiskGB := current.Spec.DiskGB
	if patch.DiskGB > 0 {
		nextDiskGB = patch.DiskGB
	}

	usage.CPUs += nextCPUs
	usage.RAMMB += nextRAMMB
	usage.DiskGB += nextDiskGB

	return s.validateQuotaUsage(usage)
}

func (s *Server) validateQuotaUsage(usage quotaUsage) error {
	if limit := s.quotas.MaxVMs; limit > 0 && usage.VMs > limit {
		return types.NewAPIError("quota_exceeded", fmt.Sprintf("VM quota exceeded: %d configured, limit is %d", usage.VMs, limit))
	}
	if limit := s.quotas.MaxTotalCPUs; limit > 0 && usage.CPUs > limit {
		return types.NewAPIError("quota_exceeded", fmt.Sprintf("CPU quota exceeded: %d configured, limit is %d", usage.CPUs, limit))
	}
	if limit := s.quotas.MaxTotalRAMMB; limit > 0 && usage.RAMMB > limit {
		return types.NewAPIError("quota_exceeded", fmt.Sprintf("RAM quota exceeded: %d MB configured, limit is %d MB", usage.RAMMB, limit))
	}
	if limit := s.quotas.MaxTotalDiskGB; limit > 0 && usage.DiskGB > limit {
		return types.NewAPIError("quota_exceeded", fmt.Sprintf("disk quota exceeded: %d GB configured, limit is %d GB", usage.DiskGB, limit))
	}
	return nil
}
