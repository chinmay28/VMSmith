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
