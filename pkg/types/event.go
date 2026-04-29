package types

import "time"

// EventSchemaVersion is included in outbound payloads so consumers can detect
// breaking changes without sniffing fields.
const EventSchemaVersion = 1

// Event represents a system or VM lifecycle event.
//
// ID is a stringified monotonically-increasing uint64 assigned by the EventBus.
// Older events (before the EventBus) used "evt-<unix-nano>" IDs; those are
// preserved as-is and sort before numeric IDs lexicographically.
type Event struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`             // e.g., "vm.started", "vm.created"
	Source     string            `json:"source"`           // "libvirt" | "app" | "system"
	VMID       string            `json:"vm_id,omitempty"`
	ResourceID string            `json:"resource_id,omitempty"`
	Severity   string            `json:"severity"`         // "info" | "warn" | "error"
	Message    string            `json:"message"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Actor      string            `json:"actor,omitempty"`
	OccurredAt time.Time         `json:"occurred_at"`
	// CreatedAt is kept for backward compat; new code should use OccurredAt.
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// Event source constants.
const (
	EventSourceLibvirt = "libvirt"
	EventSourceApp     = "app"
	EventSourceSystem  = "system"
)

// Event severity constants.
const (
	EventSeverityInfo  = "info"
	EventSeverityWarn  = "warn"
	EventSeverityError = "error"
)
