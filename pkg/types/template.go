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

// TemplateUpdateSpec defines the fields that may be edited on an existing
// template via PATCH /api/v1/templates/{id}. PATCH semantics mirror the
// VM convention: an empty Description means "no change", and a nil Tags
// slice means "no change". Passing a non-nil but empty Tags slice
// (`"tags": []`) clears the tag set.
type TemplateUpdateSpec struct {
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}
