package types

// StorageLocation is one entry of GET /api/v1/host/storage-locations: a
// named directory VM disks can be placed in or moved to, with live
// capacity figures for its filesystem.
type StorageLocation struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	// Default marks the implicit location backed by storage.base_dir.
	Default bool `json:"default"`
	// Available is false when the directory is missing or cannot be
	// statted (e.g. the drive is not mounted); Error carries the reason
	// and the capacity fields are zero.
	Available  bool   `json:"available"`
	Error      string `json:"error,omitempty"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
	// VMCount is the number of VMs whose disk currently lives here.
	VMCount int `json:"vm_count"`
}

// MoveDiskRequest is the body of POST /api/v1/vms/{id}/disk/move.
type MoveDiskRequest struct {
	// Location is the target storage location name.
	Location string `json:"location"`
}
