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
	ForceStop(ctx context.Context, id string) error
	Restart(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*types.VM, error)
	List(ctx context.Context) ([]*types.VM, error)

	// Snapshots
	CreateSnapshot(ctx context.Context, vmID string, spec types.SnapshotSpec) (*types.Snapshot, error)
	UpdateSnapshot(ctx context.Context, vmID string, snapshotName string, patch types.SnapshotUpdateSpec) (*types.Snapshot, error)
	RestoreSnapshot(ctx context.Context, vmID string, snapshotName string) error
	ListSnapshots(ctx context.Context, vmID string) ([]*types.Snapshot, error)
	DeleteSnapshot(ctx context.Context, vmID string, snapshotName string) error

	// Console access — returns the host/port (vnc) or pty path (serial)
	// the daemon's console proxy should dial.  Returns a typed
	// `vm_not_running` API error when the VM is stopped (graphics + pty
	// only exist while the domain is alive) and `console_unavailable`
	// when the domain XML carries no matching device for the intent.
	GetConsoleEndpoint(ctx context.Context, id string, intent types.ConsoleIntent) (*types.ConsoleEndpoint, error)

	// Connection management
	Close() error
}
