package types

import "time"

// MetricSample holds a single point-in-time snapshot of VM resource utilisation.
// All metric fields are pointers so that nil signals "unavailable" (e.g., first
// sample after boot has no prior counter to compute a rate from, or the guest
// agent is absent for RAM-pressure metrics).
type MetricSample struct {
	Timestamp    time.Time `json:"timestamp"`
	CPUPercent   *float64  `json:"cpu_percent,omitempty"`
	MemUsedMB    *uint64   `json:"mem_used_mb,omitempty"`
	MemAvailMB   *uint64   `json:"mem_avail_mb,omitempty"`
	DiskReadBps  *uint64   `json:"disk_read_bps,omitempty"`
	DiskWriteBps *uint64   `json:"disk_write_bps,omitempty"`
	NetRxBps     *uint64   `json:"net_rx_bps,omitempty"`
	NetTxBps     *uint64   `json:"net_tx_bps,omitempty"`
}

// VMStatsSnapshot is the response body for GET /api/v1/vms/{id}/stats.
// History is ordered oldest-first; Current mirrors the most recent History entry
// (or nil if no sample has been collected yet).
type VMStatsSnapshot struct {
	VMID            string         `json:"vm_id"`
	State           string         `json:"state"`
	LastSampledAt   *time.Time     `json:"last_sampled_at,omitempty"`
	Current         *MetricSample  `json:"current,omitempty"`
	History         []MetricSample `json:"history"`
	IntervalSeconds int            `json:"interval_seconds"`
	HistorySize     int            `json:"history_size"`
}
