package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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

// streamSampleInterval is the cadence at which StreamVMStats polls Snapshot
// for new samples.  It runs faster than the default 10s sampling interval so
// new samples are delivered to subscribers within ~1s of being produced.
const streamSampleInterval = 1 * time.Second

// StreamVMStats handles GET /api/v1/vms/{vmID}/stats/stream (SSE).
//
// Each new MetricSample is delivered as a single SSE frame whose data field
// is the JSON-encoded sample.  The event id is a unix-nanosecond timestamp
// so clients can resume after a reconnect with Last-Event-ID, though replay
// is not supported by the in-memory metrics ring (the REST `/stats` endpoint
// provides initial backfill).  A 30s heartbeat comment defeats proxy idle
// timeouts.
//
// Returns:
//   - 503 metrics_disabled when the metrics manager is not wired
//   - 404 resource_not_found when the VM is unknown
//   - 200 with `text/event-stream` otherwise
//
// The stream ends when the client disconnects, the request context is
// cancelled (e.g., daemon shutdown), or the VM is deleted from the store.
func (s *Server) StreamVMStats(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	if s.metricsManager == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "metrics_disabled",
			"metrics collection is disabled; enable daemon.metrics.enabled in config")
		return
	}

	if _, err := s.vmManager.Get(r.Context(), vmID); err != nil {
		writeAPIError(w, http.StatusNotFound, types.NewAPIError("resource_not_found",
			fmt.Sprintf("vm %q not found", vmID)))
		return
	}

	sw := newSSEWriter(w)
	if sw == nil {
		return
	}

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	poll := time.NewTicker(streamSampleInterval)
	defer poll.Stop()

	var lastSent time.Time

	// Send an initial frame from the most recent sample (if any) so clients
	// receive something immediately on connect rather than waiting for the
	// next poll tick.
	if snap, err := s.metricsManager.Snapshot(vmID); err == nil && snap != nil && snap.Current != nil {
		if writeStatsFrame(sw, snap.Current) != nil {
			return
		}
		lastSent = snap.Current.Timestamp
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if err := sw.Heartbeat(); err != nil {
				return
			}
		case <-poll.C:
			// Verify the VM still exists; closing the stream on delete keeps
			// stale subscribers from holding state for vanished VMs.
			if _, err := s.vmManager.Get(r.Context(), vmID); err != nil {
				return
			}

			snap, err := s.metricsManager.Snapshot(vmID)
			if err != nil || snap == nil || snap.Current == nil {
				continue
			}
			if !snap.Current.Timestamp.After(lastSent) {
				continue
			}
			if writeStatsFrame(sw, snap.Current) != nil {
				return
			}
			lastSent = snap.Current.Timestamp
		}
	}
}

// writeStatsFrame marshals a MetricSample and writes one SSE frame using the
// sample timestamp's unix-nanos as the event id.
func writeStatsFrame(sw *sseWriter, sample *types.MetricSample) error {
	data, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	id := strconv.FormatInt(sample.Timestamp.UnixNano(), 10)
	return sw.WriteEvent(id, "vm.stats", string(data))
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

// TopVMItem is one entry in a top-N response from GetTopVMs.
type TopVMItem struct {
	VMID   string              `json:"vm_id"`
	Name   string              `json:"name"`
	State  string              `json:"state"`
	Value  float64             `json:"value"`
	Sample *types.MetricSample `json:"sample,omitempty"`
}

// TopVMResponse is the response body for GET /api/v1/vms/stats/top.
type TopVMResponse struct {
	Metric string      `json:"metric"`
	Limit  int         `json:"limit"`
	State  string      `json:"state"`
	Items  []TopVMItem `json:"items"`
}

// metricExtractor pulls a numeric value out of a MetricSample.
// Returns ok=false when the sample lacks the requested field.
type metricExtractor func(s *types.MetricSample) (float64, bool)

var topMetricExtractors = map[string]metricExtractor{
	"cpu": func(s *types.MetricSample) (float64, bool) {
		if s.CPUPercent == nil {
			return 0, false
		}
		return *s.CPUPercent, true
	},
	"mem": func(s *types.MetricSample) (float64, bool) {
		if s.MemUsedMB == nil {
			return 0, false
		}
		return float64(*s.MemUsedMB), true
	},
	"disk_read": func(s *types.MetricSample) (float64, bool) {
		if s.DiskReadBps == nil {
			return 0, false
		}
		return float64(*s.DiskReadBps), true
	},
	"disk_write": func(s *types.MetricSample) (float64, bool) {
		if s.DiskWriteBps == nil {
			return 0, false
		}
		return float64(*s.DiskWriteBps), true
	},
	"net_rx": func(s *types.MetricSample) (float64, bool) {
		if s.NetRxBps == nil {
			return 0, false
		}
		return float64(*s.NetRxBps), true
	},
	"net_tx": func(s *types.MetricSample) (float64, bool) {
		if s.NetTxBps == nil {
			return 0, false
		}
		return float64(*s.NetTxBps), true
	},
}

// supportedTopMetrics lists the accepted ?metric= values, ordered for stable
// error messages.
var supportedTopMetrics = []string{"cpu", "mem", "disk_read", "disk_write", "net_rx", "net_tx"}

const (
	defaultTopLimit = 5
	maxTopLimit     = 100
)

// GetTopVMs handles GET /api/v1/vms/stats/top.
//
// Query params:
//   - ?metric=<name>  — required: cpu | mem | disk_read | disk_write | net_rx | net_tx
//   - ?limit=<n>      — optional: 1..100 (default 5)
//   - ?state=<s>      — optional: running (default) | all
//
// Returns 503 when metrics are disabled, 400 for invalid params, 200 with the
// top-N items sorted by metric value descending. Items missing the requested
// metric (or whose VM has no current sample) are skipped.
func (s *Server) GetTopVMs(w http.ResponseWriter, r *http.Request) {
	if s.metricsManager == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "metrics_disabled",
			"metrics collection is disabled; enable daemon.metrics.enabled in config")
		return
	}

	q := r.URL.Query()

	metric := strings.TrimSpace(strings.ToLower(q.Get("metric")))
	if metric == "" {
		metric = "cpu"
	}
	extractor, ok := topMetricExtractors[metric]
	if !ok {
		writeErrorCode(w, http.StatusBadRequest, "invalid_metric",
			"metric must be one of: "+strings.Join(supportedTopMetrics, ", "))
		return
	}

	limit := defaultTopLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxTopLimit {
			writeErrorCode(w, http.StatusBadRequest, "invalid_limit",
				fmt.Sprintf("limit must be an integer between 1 and %d", maxTopLimit))
			return
		}
		limit = n
	}

	state := strings.TrimSpace(strings.ToLower(q.Get("state")))
	if state == "" {
		state = "running"
	}
	if state != "running" && state != "all" {
		writeErrorCode(w, http.StatusBadRequest, "invalid_state",
			"state must be 'running' or 'all'")
		return
	}

	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError,
			types.NewAPIError("internal_error", "failed to list VMs"))
		return
	}

	items := make([]TopVMItem, 0, len(vms))
	for _, v := range vms {
		if state == "running" && v.State != types.VMStateRunning {
			continue
		}
		snap, err := s.metricsManager.Snapshot(v.ID)
		if err != nil || snap == nil || snap.Current == nil {
			continue
		}
		val, ok := extractor(snap.Current)
		if !ok {
			continue
		}
		// Copy the sample so the caller cannot mutate the live ring entry.
		sample := *snap.Current
		items = append(items, TopVMItem{
			VMID:   v.ID,
			Name:   v.Name,
			State:  string(v.State),
			Value:  val,
			Sample: &sample,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Value != items[j].Value {
			return items[i].Value > items[j].Value
		}
		// Deterministic tiebreaker on VM ID so the response is stable.
		return items[i].VMID < items[j].VMID
	})

	if len(items) > limit {
		items = items[:limit]
	}

	writeJSON(w, http.StatusOK, TopVMResponse{
		Metric: metric,
		Limit:  limit,
		State:  state,
		Items:  items,
	})
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
