package types

import "time"

// VMTemplate stores reusable VM defaults that can be applied when creating new VMs.
type VMTemplate struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Image       string              `json:"image"`
	CPUs        int                 `json:"cpus,omitempty"`
	RAMMB       int                 `json:"ram_mb,omitempty"`
	DiskGB      int                 `json:"disk_gb,omitempty"`
	Description string              `json:"description,omitempty"`
	Tags        []string            `json:"tags,omitempty"`
	DefaultUser string              `json:"default_user,omitempty"`
	Networks    []NetworkAttachment `json:"networks,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}
