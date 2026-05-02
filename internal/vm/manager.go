package vm

import (
	"context"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// Manager defines the interface for VM lifecycle management.
// The MVP implements this with libvirt; KubeVirt can be added later
// by implementing the same interface.
type Manager interface {
	// VM lifecycle
	Create(ctx context.Context, spec types.VMSpec) (*types.VM, error)
	Clone(ctx context.Context, sourceID string, newName string) (*types.VM, error)
	Update(ctx context.Context, id string, patch types.VMUpdateSpec) (*types.VM, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*types.VM, error)
	List(ctx context.Context) ([]*types.VM, error)

	// Snapshots
	CreateSnapshot(ctx context.Context, vmID string, name string) (*types.Snapshot, error)
	RestoreSnapshot(ctx context.Context, vmID string, snapshotName string) error
	ListSnapshots(ctx context.Context, vmID string) ([]*types.Snapshot, error)
	DeleteSnapshot(ctx context.Context, vmID string, snapshotName string) error

	// Connection management
	Close() error
}
