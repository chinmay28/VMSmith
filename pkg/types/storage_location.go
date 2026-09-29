package types

// StorageLocation kinds.
const (
	// StorageLocationDefault is the implicit location backed by
	// storage.base_dir.
	StorageLocationDefault = "default"
	// StorageLocationConfigured is a named storage.disk_locations entry.
	StorageLocationConfigured = "configured"
	// StorageLocationDiscovered is an ad-hoc directory under
	// storage.disk_location_roots (an allowed root, a mount point, or a
	// directory VMs already live in), named by its absolute path.
	StorageLocationDiscovered = "discovered"
)

// StorageLocation is one entry of GET /api/v1/host/storage-locations: a
// directory VM disks can be placed in or moved to, with live capacity
// figures for its filesystem. Name is what VMSpec.DiskLocation and
// MoveDiskRequest.Location take: the configured name, or the absolute
// path for a discovered location.
type StorageLocation struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	// Kind is one of default, configured, discovered.
	Kind string `json:"kind"`
	// Default marks the implicit location backed by storage.base_dir.
	Default bool `json:"default"`
	// Available is false when the directory is missing or cannot be
	// statted (e.g. the drive is not mounted); Error carries the reason
	// and the capacity fields are zero.
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	// Warning flags a usable-but-risky location, e.g. a parent directory
	// QEMU's unprivileged user cannot traverse.
	Warning    string `json:"warning,omitempty"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	// VMCount is the number of VMs whose disk currently lives here.
	VMCount int `json:"vm_count"`
}

// MoveDiskRequest is the body of POST /api/v1/vms/{id}/disk/move.
type MoveDiskRequest struct {
	// Location is the target storage location: a configured name, or an
	// absolute directory path under storage.disk_location_roots.
	Location string `json:"location"`
}
