package types

import "time"

// Snapshot represents a point-in-time capture of a VM's state.
type Snapshot struct {
	ID          string    `json:"id"`
	VMID        string    `json:"vm_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// SnapshotSpec carries the operator-supplied parameters when creating a snapshot.
type SnapshotSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
