package types

import "time"

// Image represents a portable VM disk image.
type Image struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	Format    string    `json:"format"` // qcow2
	SourceVM  string    `json:"source_vm,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
