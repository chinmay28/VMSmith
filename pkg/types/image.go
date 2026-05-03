package types

import "time"

// Image represents a portable VM disk image.
type Image struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	SizeBytes   int64     `json:"size_bytes"`
	Format      string    `json:"format"` // qcow2
	SourceVM    string    `json:"source_vm,omitempty"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// ImageUpdateSpec defines fields that can be changed on an existing image.
// Description: empty string is treated as "no change". Tags: nil = no change;
// non-nil (including an empty slice) replaces the current tag set.
type ImageUpdateSpec struct {
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}
