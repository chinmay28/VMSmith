package vm

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// MockManager implements Manager for testing without libvirt.
type MockManager struct {
	mu        sync.RWMutex
	vms       map[string]*types.VM
	snapshots map[string][]*types.Snapshot // vmID -> snapshots
	nextID    int

	// Hooks for injecting errors in tests
	CreateErr          error
	CloneErr           error
	UpdateErr          error
	StartErr           error
	StopErr            error
	RestartErr         error
	DeleteErr          error
	GetErr             error
	ListErr            error
	CreateSnapshotErr  error
	RestoreSnapshotErr error
	DeleteSnapshotErr  error
	CreateDelay        time.Duration
}

// NewMockManager creates a new mock VM manager.
func NewMockManager() *MockManager {
	return &MockManager{
		vms:       make(map[string]*types.VM),
		snapshots: make(map[string][]*types.Snapshot),
	}
}

func (m *MockManager) Create(ctx context.Context, spec types.VMSpec) (*types.VM, error) {
	if m.CreateErr != nil {
		return nil, m.CreateErr
	}
	if m.CreateDelay > 0 {
		time.Sleep(m.CreateDelay)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextID++
	id := fmt.Sprintf("vm-mock-%d", m.nextID)

	if spec.CPUs == 0 {
		spec.CPUs = 2
	}
	if spec.RAMMB == 0 {
		spec.RAMMB = 2048
	}
	if spec.DiskGB == 0 {
		spec.DiskGB = 20
	}

	vm := &types.VM{
		ID:          id,
		Name:        spec.Name,
		Description: spec.Description,
		Tags:        append([]string(nil), spec.Tags...),
		Spec:        spec,
		State:       types.VMStateRunning,
		IP:          "192.168.100.10",
		DiskPath:    fmt.Sprintf("/var/lib/vmsmith/vms/%s/disk.qcow2", id),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	m.vms[id] = vm
	return vm, nil
}

func (m *MockManager) Clone(ctx context.Context, sourceID string, newName string) (*types.VM, error) {
	if m.CloneErr != nil {
		return nil, m.CloneErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	source, ok := m.vms[sourceID]
	if !ok {
		return nil, fmt.Errorf("vms/%s: not found", sourceID)
	}

	m.nextID++
	id := fmt.Sprintf("vm-mock-%d", m.nextID)

	spec := source.Spec
	spec.Name = newName
	spec.Tags = append([]string(nil), source.Spec.Tags...)
	spec.Networks = append([]types.NetworkAttachment(nil), source.Spec.Networks...)

	clone := &types.VM{
		ID:          id,
		Name:        newName,
		Description: source.Description,
		Tags:        append([]string(nil), source.Tags...),
		Spec:        spec,
		State:       types.VMStateStopped,
		IP:          "",
		DiskPath:    fmt.Sprintf("/var/lib/vmsmith/vms/%s/disk.qcow2", id),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	m.vms[id] = clone
	return clone, nil
}

func (m *MockManager) Update(ctx context.Context, id string, patch types.VMUpdateSpec) (*types.VM, error) {
	if m.UpdateErr != nil {
		return nil, m.UpdateErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	vm, ok := m.vms[id]
	if !ok {
		return nil, fmt.Errorf("vms/%s: not found", id)
	}

	if patch.CPUs > 0 {
		vm.Spec.CPUs = patch.CPUs
	}
	if patch.RAMMB > 0 {
		vm.Spec.RAMMB = patch.RAMMB
	}
	if patch.DiskGB > 0 {
		if patch.DiskGB < vm.Spec.DiskGB {
			return nil, fmt.Errorf("disk can only grow: requested %d GB is less than current %d GB", patch.DiskGB, vm.Spec.DiskGB)
		}
		vm.Spec.DiskGB = patch.DiskGB
	}
	if patch.Description != "" {
		vm.Description = patch.Description
		vm.Spec.Description = patch.Description
	}
	if patch.Tags != nil {
		vm.Tags = append([]string(nil), patch.Tags...)
		vm.Spec.Tags = append([]string(nil), patch.Tags...)
	}
	if patch.NatStaticIP != "" {
		parsedIP, _, err := net.ParseCIDR(patch.NatStaticIP)
		if err != nil {
			return nil, fmt.Errorf("invalid nat_static_ip %q: must be CIDR notation e.g. 192.168.100.50/24", patch.NatStaticIP)
		}
		vm.Spec.NatStaticIP = parsedIP.String() + "/24"
		vm.IP = parsedIP.String()
		if patch.NatGateway != "" {
			vm.Spec.NatGateway = patch.NatGateway
		}
	}

	vm.UpdatedAt = time.Now()
	vmCopy := *vm
	return &vmCopy, nil
}

func (m *MockManager) Start(ctx context.Context, id string) error {
	if m.StartErr != nil {
		return m.StartErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	vm, ok := m.vms[id]
	if !ok {
		return fmt.Errorf("vms/%s: not found", id)
	}

	vm.State = types.VMStateRunning
	vm.UpdatedAt = time.Now()
	return nil
}

func (m *MockManager) Stop(ctx context.Context, id string) error {
	if m.StopErr != nil {
		return m.StopErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	vm, ok := m.vms[id]
	if !ok {
		return fmt.Errorf("vms/%s: not found", id)
	}

	vm.State = types.VMStateStopped
	vm.UpdatedAt = time.Now()
	return nil
}

func (m *MockManager) Restart(ctx context.Context, id string) error {
	if m.RestartErr != nil {
		return m.RestartErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	vm, ok := m.vms[id]
	if !ok {
		return fmt.Errorf("vms/%s: not found", id)
	}

	vm.State = types.VMStateRunning
	vm.UpdatedAt = time.Now()
	return nil
}

func (m *MockManager) Delete(ctx context.Context, id string) error {
	if m.DeleteErr != nil {
		return m.DeleteErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.vms[id]; !ok {
		return fmt.Errorf("vms/%s: not found", id)
	}
	delete(m.vms, id)
	delete(m.snapshots, id)
	return nil
}

func (m *MockManager) Get(ctx context.Context, id string) (*types.VM, error) {
	if m.GetErr != nil {
		return nil, m.GetErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	vm, ok := m.vms[id]
	if !ok {
		return nil, fmt.Errorf("vms/%s: not found", id)
	}

	// Return a copy to prevent mutation
	vmCopy := *vm
	return &vmCopy, nil
}

func (m *MockManager) List(ctx context.Context) ([]*types.VM, error) {
	if m.ListErr != nil {
		return nil, m.ListErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*types.VM
	for _, v := range m.vms {
		vmCopy := *v
		result = append(result, &vmCopy)
	}
	return result, nil
}

func (m *MockManager) CreateSnapshot(ctx context.Context, vmID string, name string) (*types.Snapshot, error) {
	if m.CreateSnapshotErr != nil {
		return nil, m.CreateSnapshotErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.vms[vmID]; !ok {
		return nil, fmt.Errorf("vms/%s: not found", vmID)
	}

	snap := &types.Snapshot{
		ID:        fmt.Sprintf("%s/%s", vmID, name),
		VMID:      vmID,
		Name:      name,
		CreatedAt: time.Now(),
	}

	m.snapshots[vmID] = append(m.snapshots[vmID], snap)
	return snap, nil
}

func (m *MockManager) RestoreSnapshot(ctx context.Context, vmID string, snapshotName string) error {
	if m.RestoreSnapshotErr != nil {
		return m.RestoreSnapshotErr
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	snaps, ok := m.snapshots[vmID]
	if !ok {
		return fmt.Errorf("vms/%s: not found", vmID)
	}

	for _, s := range snaps {
		if s.Name == snapshotName {
			return nil
		}
	}
	return fmt.Errorf("snapshot %s not found", snapshotName)
}

func (m *MockManager) ListSnapshots(ctx context.Context, vmID string) ([]*types.Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.vms[vmID]; !ok {
		return nil, fmt.Errorf("vms/%s: not found", vmID)
	}

	return m.snapshots[vmID], nil
}

func (m *MockManager) DeleteSnapshot(ctx context.Context, vmID string, snapshotName string) error {
	if m.DeleteSnapshotErr != nil {
		return m.DeleteSnapshotErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snaps, ok := m.snapshots[vmID]
	if !ok {
		return fmt.Errorf("vms/%s: not found", vmID)
	}

	for i, s := range snaps {
		if s.Name == snapshotName {
			m.snapshots[vmID] = append(snaps[:i], snaps[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("snapshot %s not found", snapshotName)
}

func (m *MockManager) Close() error {
	return nil
}

// --- test helpers ---

// SeedVM injects a VM directly for test setup.
func (m *MockManager) SeedVM(vm *types.VM) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vms[vm.ID] = vm
}

// VMCount returns the number of tracked VMs.
func (m *MockManager) VMCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.vms)
}
