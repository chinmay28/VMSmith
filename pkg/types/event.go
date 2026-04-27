package types

import "time"

// Event represents a system or VM lifecycle event.
type Event struct {
	ID        string    `json:"id"`
	VMID      string    `json:"vm_id,omitempty"`
	Type      string    `json:"type"` // e.g., "vm_started", "vm_stopped", "vm_crashed", "vm_created", "vm_deleted"
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}
