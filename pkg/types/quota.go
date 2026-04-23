package types

// QuotaUsageSummary tracks the current allocation and optional limit for one resource.
type QuotaUsageSummary struct {
	Used  int `json:"used"`
	Limit int `json:"limit,omitempty"`
}

// QuotaUsage describes current VM allocation totals against configured quota caps.
type QuotaUsage struct {
	VMs    QuotaUsageSummary `json:"vms"`
	CPUs   QuotaUsageSummary `json:"cpus"`
	RAMMB  QuotaUsageSummary `json:"ram_mb"`
	DiskGB QuotaUsageSummary `json:"disk_gb"`
}

// HostResourceUsageSummary describes current host utilization for one resource.
type HostResourceUsageSummary struct {
	Used       uint64 `json:"used"`
	Total      uint64 `json:"total"`
	Available  uint64 `json:"available,omitempty"`
	Percentage int    `json:"percentage,omitempty"`
}

// HostStats describes top-level host capacity and utilization for the dashboard.
type HostStats struct {
	VMCount int                      `json:"vm_count"`
	CPU     HostResourceUsageSummary `json:"cpu"`
	RAM     HostResourceUsageSummary `json:"ram"`
	Disk    HostResourceUsageSummary `json:"disk"`
}
