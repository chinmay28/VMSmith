package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// GetVMStats handles GET /api/v1/vms/{vmID}/stats
//
// Query params:
//   - ?since=<rfc3339>  — truncate history to samples after this time
//   - ?fields=cpu,mem,disk,net  — project which metric fields to include
//
// Returns 503 when metrics are disabled, 404 when the VM is unknown to both
// the VM manager and the metrics ring, 200 with frozen history when the VM
// exists but is stopped.
func (s *Server) GetVMStats(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	// Metrics disabled.
	if s.metricsManager == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "metrics_disabled",
			"metrics collection is disabled; enable daemon.metrics.enabled in config")
		return
	}

	// Check VM exists in the manager.
	vmObj, vmErr := s.vmManager.Get(r.Context(), vmID)
	if vmErr != nil {
		// VM not found in the store at all.
		writeAPIError(w, http.StatusNotFound, types.NewAPIError("resource_not_found",
			fmt.Sprintf("vm %q not found", vmID)))
		return
	}

	snap, err := s.metricsManager.Snapshot(vmID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError,
			types.NewAPIError("metrics_error", "failed to retrieve metrics: "+err.Error()))
		return
	}

	// Build a minimal snapshot from vmObj state when no metrics data exists yet.
	if snap == nil {
		snap = &types.VMStatsSnapshot{
			VMID:            vmID,
			State:           string(vmObj.State),
			History:         []types.MetricSample{},
			IntervalSeconds: s.metricsConfig.SampleInterval,
			HistorySize:     s.metricsConfig.HistorySize,
		}
	} else {
		// Overlay the authoritative VM state from the manager.
		snap.State = string(vmObj.State)
	}

	// Ensure History is never null in JSON.
	if snap.History == nil {
		snap.History = []types.MetricSample{}
	}

	// Apply ?since= filter.
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		since, err := time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			writeErrorCode(w, http.StatusBadRequest, "invalid_since_param",
				"since must be an RFC 3339 timestamp (e.g. 2006-01-02T15:04:05Z)")
			return
		}
		filtered := snap.History[:0]
		for _, s := range snap.History {
			if s.Timestamp.After(since) {
				filtered = append(filtered, s)
			}
		}
		snap.History = filtered
		if snap.Current != nil && !snap.Current.Timestamp.After(since) {
			snap.Current = nil
		}
	}

	// Apply ?fields= projection.
	if fieldsStr := r.URL.Query().Get("fields"); fieldsStr != "" {
		fields := parseFieldsParam(fieldsStr)
		snap.History = projectSamples(snap.History, fields)
		if snap.Current != nil {
			projected := projectSample(*snap.Current, fields)
			snap.Current = &projected
		}
	}

	writeJSON(w, http.StatusOK, snap)
}

// parseFieldsParam parses a comma-separated fields parameter into a set.
// Recognised tokens: cpu, mem, disk, net.
func parseFieldsParam(raw string) map[string]bool {
	fields := make(map[string]bool)
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(strings.ToLower(f))
		if f != "" {
			fields[f] = true
		}
	}
	return fields
}

// projectSample zeroes out metric fields not in the requested set.
func projectSample(s types.MetricSample, fields map[string]bool) types.MetricSample {
	out := types.MetricSample{Timestamp: s.Timestamp}
	if fields["cpu"] {
		out.CPUPercent = s.CPUPercent
	}
	if fields["mem"] {
		out.MemUsedMB = s.MemUsedMB
		out.MemAvailMB = s.MemAvailMB
	}
	if fields["disk"] {
		out.DiskReadBps = s.DiskReadBps
		out.DiskWriteBps = s.DiskWriteBps
	}
	if fields["net"] {
		out.NetRxBps = s.NetRxBps
		out.NetTxBps = s.NetTxBps
	}
	return out
}

func projectSamples(in []types.MetricSample, fields map[string]bool) []types.MetricSample {
	out := make([]types.MetricSample, len(in))
	for i, s := range in {
		out[i] = projectSample(s, fields)
	}
	return out
}

// escapePromLabel escapes a string for use as a Prometheus text-format label
// value.  The format requires only three escapes: backslash, double quote,
// and newline.  Other bytes (including non-ASCII UTF-8) are passed through
// unchanged — Prometheus accepts arbitrary UTF-8 in label values.
func escapePromLabel(s string) string {
	if !strings.ContainsAny(s, "\\\"\n") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PrometheusMetrics handles GET /metrics and returns Prometheus text-format metrics.
// This endpoint is exempt from auth and rate limiting; it is served directly by
// the chi router outside the /api/v1 group.
func (s *Server) PrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metricsManager == nil {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "# metrics collection disabled")
		return
	}

	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		http.Error(w, "failed to list VMs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)

	// Emit HELP + TYPE lines followed by per-VM gauge values.
	metricDefs := []struct {
		name string
		help string
	}{
		{"vmsmith_vm_cpu_percent", "Current CPU utilisation percent for the VM"},
		{"vmsmith_vm_mem_used_mb", "Memory used by the VM in MiB"},
		{"vmsmith_vm_disk_read_bps", "Disk read throughput in bytes per second"},
		{"vmsmith_vm_disk_write_bps", "Disk write throughput in bytes per second"},
		{"vmsmith_vm_net_rx_bps", "Network receive throughput in bytes per second"},
		{"vmsmith_vm_net_tx_bps", "Network transmit throughput in bytes per second"},
	}
	for _, d := range metricDefs {
		fmt.Fprintf(w, "# HELP %s %s\n", d.name, d.help)
		fmt.Fprintf(w, "# TYPE %s gauge\n", d.name)
	}

	for _, v := range vms {
		snap, err := s.metricsManager.Snapshot(v.ID)
		if err != nil || snap == nil || snap.Current == nil {
			continue
		}
		cur := snap.Current
		labels := fmt.Sprintf(`vm_id="%s",vm_name="%s"`, escapePromLabel(v.ID), escapePromLabel(v.Name))

		if cur.CPUPercent != nil {
			fmt.Fprintf(w, "vmsmith_vm_cpu_percent{%s} %g\n", labels, *cur.CPUPercent)
		}
		if cur.MemUsedMB != nil {
			fmt.Fprintf(w, "vmsmith_vm_mem_used_mb{%s} %d\n", labels, *cur.MemUsedMB)
		}
		if cur.DiskReadBps != nil {
			fmt.Fprintf(w, "vmsmith_vm_disk_read_bps{%s} %d\n", labels, *cur.DiskReadBps)
		}
		if cur.DiskWriteBps != nil {
			fmt.Fprintf(w, "vmsmith_vm_disk_write_bps{%s} %d\n", labels, *cur.DiskWriteBps)
		}
		if cur.NetRxBps != nil {
			fmt.Fprintf(w, "vmsmith_vm_net_rx_bps{%s} %d\n", labels, *cur.NetRxBps)
		}
		if cur.NetTxBps != nil {
			fmt.Fprintf(w, "vmsmith_vm_net_tx_bps{%s} %d\n", labels, *cur.NetTxBps)
		}
	}
}
