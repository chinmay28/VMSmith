package types

import "time"

// Snapshot represents a point-in-time capture of a VM's state.
type Snapshot struct {
	ID          string    `json:"id"`
	VMID        string    `json:"vm_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// SnapshotSpec carries the operator-supplied parameters when creating a snapshot.
type SnapshotSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// SnapshotUpdateSpec carries the editable metadata for an existing snapshot.
// Only fields that are non-nil are applied; the snapshot disk/memory state is
// never rewritten by an update.  Description round-trips through libvirt's
// <description> element; Tags persist out-of-band in the snapshot-metadata
// bbolt bucket (libvirt's domainsnapshot schema does not permit <metadata>,
// so tags cannot live alongside description in the XML).
//
// Pointer semantics:
//   - Description == nil leaves the description untouched.  An empty
//     string clears it.
//   - Tags == nil leaves the tag list untouched.  An explicit empty slice
//     clears every tag.
type SnapshotUpdateSpec struct {
	Description *string   `json:"description,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
}
