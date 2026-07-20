package types

// HostStatus is one row of the multi-host overview (roadmap 5.5.4):
// GET /api/v1/hosts returns one entry per configured libvirt host —
// the implicit "local" host first — with the aggregate resources
// currently allocated to VMs placed on it.
type HostStatus struct {
	// Name is the operator-facing host identifier ("local" for the
	// implicit local host).
	Name string `json:"name"`
	// URI is the host's libvirt connection URI.
	URI string `json:"uri"`
	// Description is the free-form operator context from the config.
	Description string `json:"description,omitempty"`
	// Default marks the host new VMs land on when spec.host is empty.
	Default bool `json:"default"`
	// Reachable reports whether the daemon currently holds a live libvirt
	// connection to the host. Omitted (nil) when connectivity is unknown
	// (e.g. a manager that does not track per-host connections).
	Reachable *bool `json:"reachable,omitempty"`
	// VMCount / CPUs / RAMMB / DiskGB / GPUs aggregate the resources of
	// the VMs placed on this host (allocation, not live utilisation —
	// live per-VM metrics stay on /vms/{id}/stats).
	VMCount int `json:"vm_count"`
	CPUs    int `json:"cpus"`
	RAMMB   int `json:"ram_mb"`
	DiskGB  int `json:"disk_gb"`
	GPUs    int `json:"gpus"`
}
