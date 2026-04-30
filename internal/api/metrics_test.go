package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// --- Test-local minimal MetricsManager mock ---
// We define our own mock here so that metrics_test.go does NOT import
// internal/metrics (which transitively pulls in libvirt CGO headers).

type testMetricsMock struct {
	mu      sync.RWMutex
	samples map[string][]types.MetricSample
	states  map[string]string
	// ErrOnSnapshot, if non-nil, is returned by every Snapshot call.
	ErrOnSnapshot error
}

func newTestMetricsMock() *testMetricsMock {
	return &testMetricsMock{
		samples: make(map[string][]types.MetricSample),
		states:  make(map[string]string),
	}
}

func (m *testMetricsMock) seed(vmID string, s types.MetricSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples[vmID] = append(m.samples[vmID], s)
}

func (m *testMetricsMock) setState(vmID, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[vmID] = state
}

// Snapshot implements MetricsManager.
func (m *testMetricsMock) Snapshot(vmID string) (*types.VMStatsSnapshot, error) {
	if m.ErrOnSnapshot != nil {
		return nil, m.ErrOnSnapshot
	}
	m.mu.RLock()
	samples := m.samples[vmID]
	state := m.states[vmID]
	m.mu.RUnlock()

	if samples == nil {
		return nil, nil
	}

	var latest *types.MetricSample
	if len(samples) > 0 {
		l := samples[len(samples)-1]
		latest = &l
	}

	var lastAt *time.Time
	if latest != nil {
		t := latest.Timestamp
		lastAt = &t
	}

	return &types.VMStatsSnapshot{
		VMID:            vmID,
		State:           state,
		LastSampledAt:   lastAt,
		Current:         latest,
		History:         append([]types.MetricSample(nil), samples...),
		IntervalSeconds: 10,
		HistorySize:     360,
	}, nil
}

// testServerWithMetrics creates a test API server with a test MetricsManager.
func testServerWithMetrics(t *testing.T, metricsMgr MetricsManager) (*httptest.Server, *vm.MockManager, func()) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath
	cfg.Metrics.Enabled = true
	cfg.Metrics.SampleInterval = 10
	cfg.Metrics.HistorySize = 360

	mockMgr := vm.NewMockManager()
	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)

	apiServer := NewServerWithMetrics(mockMgr, storageMgr, portFwd, s, cfg, nil, metricsMgr)
	ts := httptest.NewServer(apiServer)

	cleanup := func() {
		ts.Close()
		s.Close()
	}

	return ts, mockMgr, cleanup
}

// seedTestVM creates a VM in the mock manager and returns it.
func seedTestVM(t *testing.T, mock *vm.MockManager, id, name string, state types.VMState) *types.VM {
	t.Helper()
	v := &types.VM{
		ID:        id,
		Name:      name,
		State:     state,
		IP:        "192.168.100.10",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Spec: types.VMSpec{
			CPUs:   2,
			RAMMB:  2048,
			DiskGB: 20,
		},
	}
	mock.SeedVM(v)
	return v
}

// --- GET /api/v1/vms/{id}/stats tests ---

func TestGetVMStats_MetricsDisabled(t *testing.T) {
	// nil metricsManager → 503
	ts, mockMgr, cleanup := testServerWithMetrics(t, nil)
	defer cleanup()

	v := seedTestVM(t, mockMgr, "vm-test-1", "test-vm", types.VMStateRunning)

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + v.ID + "/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Code != "metrics_disabled" {
		t.Errorf("code = %q, want %q", errResp.Code, "metrics_disabled")
	}
}

func TestGetVMStats_VMNotFound(t *testing.T) {
	m := newTestMetricsMock()
	ts, _, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-does-not-exist/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Code != "resource_not_found" {
		t.Errorf("code = %q, want %q", errResp.Code, "resource_not_found")
	}
}

func TestGetVMStats_NoSamplesYet(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	v := seedTestVM(t, mockMgr, "vm-nosamples", "no-samples", types.VMStateRunning)

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + v.ID + "/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var snap types.VMStatsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.VMID != v.ID {
		t.Errorf("VMID = %q, want %q", snap.VMID, v.ID)
	}
	if snap.Current != nil {
		t.Error("expected Current to be nil for VM with no samples")
	}
	if snap.History == nil {
		t.Error("expected History to be a non-nil slice (may be empty)")
	}
}

func TestGetVMStats_WithSamples(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	v := seedTestVM(t, mockMgr, "vm-withsamples", "with-samples", types.VMStateRunning)

	cpu := 42.5
	mem := uint64(1024)
	m.seed(v.ID, types.MetricSample{
		Timestamp:  time.Now().Add(-20 * time.Second),
		CPUPercent: &cpu,
		MemUsedMB:  &mem,
	})
	cpu2 := 55.0
	m.seed(v.ID, types.MetricSample{
		Timestamp:  time.Now(),
		CPUPercent: &cpu2,
	})
	m.setState(v.ID, "running")

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + v.ID + "/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var snap types.VMStatsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.VMID != v.ID {
		t.Errorf("VMID = %q, want %q", snap.VMID, v.ID)
	}
	if snap.Current == nil {
		t.Fatal("expected Current to be non-nil")
	}
	if snap.Current.CPUPercent == nil || *snap.Current.CPUPercent != cpu2 {
		t.Errorf("Current.CPUPercent = %v, want %v", snap.Current.CPUPercent, cpu2)
	}
	if len(snap.History) != 2 {
		t.Errorf("len(History) = %d, want 2", len(snap.History))
	}
	if snap.LastSampledAt == nil {
		t.Error("expected LastSampledAt to be set")
	}
}

func TestGetVMStats_SinceFilter(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	v := seedTestVM(t, mockMgr, "vm-sincefilter", "since-filter", types.VMStateRunning)

	base := time.Now().Add(-5 * time.Minute).Truncate(time.Second)
	cpu := 10.0
	m.seed(v.ID, types.MetricSample{Timestamp: base, CPUPercent: &cpu})
	cpu2 := 20.0
	m.seed(v.ID, types.MetricSample{Timestamp: base.Add(2 * time.Minute), CPUPercent: &cpu2})
	cpu3 := 30.0
	m.seed(v.ID, types.MetricSample{Timestamp: base.Add(4 * time.Minute), CPUPercent: &cpu3})

	// Filter: only samples after 1 minute into the test range (should return the last 2).
	cutoff := base.Add(1 * time.Minute)
	since := cutoff.UTC().Format(time.RFC3339)

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + v.ID + "/stats?since=" + since)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var snap types.VMStatsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(snap.History) != 2 {
		t.Errorf("len(History) = %d, want 2 (since filter should exclude first sample)", len(snap.History))
	}
}

func TestGetVMStats_InvalidSinceParam(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	v := seedTestVM(t, mockMgr, "vm-invalidsince", "invalid-since", types.VMStateRunning)

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + v.ID + "/stats?since=not-a-date")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Code != "invalid_since_param" {
		t.Errorf("code = %q, want %q", errResp.Code, "invalid_since_param")
	}
}

func TestGetVMStats_FieldsProjection(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	v := seedTestVM(t, mockMgr, "vm-fields", "fields-vm", types.VMStateRunning)

	cpu := 50.0
	mem := uint64(512)
	diskRd := uint64(1024)
	m.seed(v.ID, types.MetricSample{
		Timestamp:   time.Now(),
		CPUPercent:  &cpu,
		MemUsedMB:   &mem,
		DiskReadBps: &diskRd,
	})

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + v.ID + "/stats?fields=cpu")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var snap types.VMStatsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.Current == nil {
		t.Fatal("expected Current to be non-nil")
	}
	// CPU should be present.
	if snap.Current.CPUPercent == nil {
		t.Error("expected CPUPercent to be set")
	}
	// Memory should be absent (not in ?fields=cpu).
	if snap.Current.MemUsedMB != nil {
		t.Error("expected MemUsedMB to be nil after projection")
	}
	// DiskReadBps should be absent.
	if snap.Current.DiskReadBps != nil {
		t.Error("expected DiskReadBps to be nil after projection")
	}
}

func TestGetVMStats_MetricsError(t *testing.T) {
	m := newTestMetricsMock()
	m.ErrOnSnapshot = types.NewAPIError("metrics_error", "forced error")
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	v := seedTestVM(t, mockMgr, "vm-metrr", "metrics-err-vm", types.VMStateRunning)

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + v.ID + "/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestGetVMStats_StoppedVMReturnsFrozenHistory(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	// VM is stopped but exists in the manager.
	v := seedTestVM(t, mockMgr, "vm-stopped", "stopped-vm", types.VMStateStopped)

	// Seed some historical samples.
	cpu := 30.0
	m.seed(v.ID, types.MetricSample{Timestamp: time.Now().Add(-2 * time.Minute), CPUPercent: &cpu})
	m.setState(v.ID, "stopped")

	resp, err := http.Get(ts.URL + "/api/v1/vms/" + v.ID + "/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (frozen history for stopped VM)", resp.StatusCode)
	}

	var snap types.VMStatsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.State != "stopped" {
		t.Errorf("State = %q, want %q", snap.State, "stopped")
	}
	if len(snap.History) == 0 {
		t.Error("expected frozen history samples for stopped VM")
	}
}

// --- GET /metrics Prometheus endpoint tests ---

func TestPrometheusMetrics_Disabled(t *testing.T) {
	ts, _, cleanup := testServerWithMetrics(t, nil)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	// With nil metrics manager, the endpoint returns 503.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestPrometheusMetrics_Enabled(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	v := seedTestVM(t, mockMgr, "vm-prom", "prom-vm", types.VMStateRunning)

	cpu := 75.0
	mem := uint64(2048)
	rdBps := uint64(500_000)
	wrBps := uint64(100_000)
	rxBps := uint64(1_000_000)
	txBps := uint64(200_000)
	m.seed(v.ID, types.MetricSample{
		Timestamp:    time.Now(),
		CPUPercent:   &cpu,
		MemUsedMB:    &mem,
		DiskReadBps:  &rdBps,
		DiskWriteBps: &wrBps,
		NetRxBps:     &rxBps,
		NetTxBps:     &txBps,
	})

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "text/plain; version=0.0.4" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain; version=0.0.4")
	}

	var b []byte
	tmp := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(tmp)
		b = append(b, tmp[:n]...)
		if readErr != nil {
			break
		}
	}
	body := string(b)

	for _, keyword := range []string{
		"vmsmith_vm_cpu_percent",
		"vmsmith_vm_mem_used_mb",
		"vmsmith_vm_disk_read_bps",
		"vmsmith_vm_disk_write_bps",
		"vmsmith_vm_net_rx_bps",
		"vmsmith_vm_net_tx_bps",
	} {
		if !containsStr(body, keyword) {
			t.Errorf("expected Prometheus output to contain %q, body:\n%s", keyword, body)
		}
	}
	// Check the VM ID label appears somewhere.
	if !containsStr(body, v.ID) {
		t.Errorf("expected VM ID %q in Prometheus output, body:\n%s", v.ID, body)
	}
}

func TestEscapePromLabel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"vm-1234", "vm-1234"},
		{`with "quote"`, `with \"quote\"`},
		{`back\slash`, `back\\slash`},
		{"new\nline", `new\nline`},
		{`mix"\` + "\n" + `end`, `mix\"\\\nend`},
	}
	for _, c := range cases {
		got := escapePromLabel(c.in)
		if got != c.want {
			t.Errorf("escapePromLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- GET /api/v1/vms/stats/top tests ---

func TestGetTopVMs_MetricsDisabled(t *testing.T) {
	ts, _, cleanup := testServerWithMetrics(t, nil)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=cpu")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Code != "metrics_disabled" {
		t.Errorf("code = %q, want %q", errResp.Code, "metrics_disabled")
	}
}

func TestGetTopVMs_OrdersByCPUDesc(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	now := time.Now()
	for i, c := range []struct {
		id   string
		name string
		cpu  float64
	}{
		{"vm-a", "alpha", 10.0},
		{"vm-b", "bravo", 80.0},
		{"vm-c", "charlie", 50.0},
	} {
		v := seedTestVM(t, mockMgr, c.id, c.name, types.VMStateRunning)
		cpu := c.cpu
		m.seed(v.ID, types.MetricSample{
			Timestamp:  now.Add(time.Duration(i) * time.Second),
			CPUPercent: &cpu,
		})
		m.setState(v.ID, "running")
	}

	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=cpu&limit=10")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body TopVMResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Metric != "cpu" {
		t.Errorf("metric = %q, want cpu", body.Metric)
	}
	if body.Limit != 10 {
		t.Errorf("limit = %d, want 10", body.Limit)
	}
	if len(body.Items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(body.Items))
	}
	want := []string{"vm-b", "vm-c", "vm-a"}
	for i, w := range want {
		if body.Items[i].VMID != w {
			t.Errorf("items[%d].VMID = %q, want %q (full order: %+v)", i, body.Items[i].VMID, w, body.Items)
		}
	}
	if body.Items[0].Value != 80.0 {
		t.Errorf("top value = %v, want 80.0", body.Items[0].Value)
	}
	if body.Items[0].Sample == nil || body.Items[0].Sample.CPUPercent == nil {
		t.Error("expected sample with CPUPercent on top item")
	}
}

func TestGetTopVMs_LimitTruncates(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	for i := 0; i < 6; i++ {
		v := seedTestVM(t, mockMgr, fmt.Sprintf("vm-%d", i), fmt.Sprintf("vm-%d", i), types.VMStateRunning)
		cpu := float64(i)
		m.seed(v.ID, types.MetricSample{Timestamp: time.Now(), CPUPercent: &cpu})
	}

	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=cpu&limit=3")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body TopVMResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 3 {
		t.Fatalf("len(items) = %d, want 3 (limit)", len(body.Items))
	}
	if body.Items[0].Value < body.Items[2].Value {
		t.Errorf("items not in descending order: %+v", body.Items)
	}
}

func TestGetTopVMs_DefaultsCpuAndLimit(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	for i := 0; i < 8; i++ {
		v := seedTestVM(t, mockMgr, fmt.Sprintf("vm-d%d", i), fmt.Sprintf("d%d", i), types.VMStateRunning)
		cpu := float64(i)
		m.seed(v.ID, types.MetricSample{Timestamp: time.Now(), CPUPercent: &cpu})
	}

	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body TopVMResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Metric != "cpu" {
		t.Errorf("metric = %q, want cpu (default)", body.Metric)
	}
	if body.Limit != 5 {
		t.Errorf("limit = %d, want 5 (default)", body.Limit)
	}
	if len(body.Items) != 5 {
		t.Errorf("len(items) = %d, want 5", len(body.Items))
	}
}

func TestGetTopVMs_SkipsRunningOnlyByDefault(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	vRun := seedTestVM(t, mockMgr, "vm-run", "running-vm", types.VMStateRunning)
	vStop := seedTestVM(t, mockMgr, "vm-stop", "stopped-vm", types.VMStateStopped)

	cpu := 20.0
	m.seed(vRun.ID, types.MetricSample{Timestamp: time.Now(), CPUPercent: &cpu})
	cpu2 := 99.0
	m.seed(vStop.ID, types.MetricSample{Timestamp: time.Now(), CPUPercent: &cpu2})

	// Default state filter excludes the stopped VM even though it has a higher
	// (frozen) CPU sample.
	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=cpu")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var body TopVMResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].VMID != vRun.ID {
		t.Errorf("expected only running VM, got %+v", body.Items)
	}

	// state=all surfaces both.
	resp2, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=cpu&state=all")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp2.Body.Close()
	var bodyAll TopVMResponse
	if err := json.NewDecoder(resp2.Body).Decode(&bodyAll); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(bodyAll.Items) != 2 {
		t.Errorf("state=all len(items) = %d, want 2", len(bodyAll.Items))
	}
	if bodyAll.Items[0].VMID != vStop.ID {
		t.Errorf("state=all top = %q, want %q (highest CPU)", bodyAll.Items[0].VMID, vStop.ID)
	}
}

func TestGetTopVMs_SkipsVMsWithoutMetric(t *testing.T) {
	m := newTestMetricsMock()
	ts, mockMgr, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	// VM with mem but no net.
	v1 := seedTestVM(t, mockMgr, "vm-mem", "mem-only", types.VMStateRunning)
	mem := uint64(1024)
	m.seed(v1.ID, types.MetricSample{Timestamp: time.Now(), MemUsedMB: &mem})

	// VM with net.
	v2 := seedTestVM(t, mockMgr, "vm-net", "net-vm", types.VMStateRunning)
	rx := uint64(500_000)
	m.seed(v2.ID, types.MetricSample{Timestamp: time.Now(), NetRxBps: &rx})

	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=net_rx")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var body TopVMResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].VMID != v2.ID {
		t.Errorf("expected only net-vm in net_rx response, got %+v", body.Items)
	}
}

func TestGetTopVMs_InvalidMetric(t *testing.T) {
	m := newTestMetricsMock()
	ts, _, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=bogus")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Code != "invalid_metric" {
		t.Errorf("code = %q, want invalid_metric", errResp.Code)
	}
}

func TestGetTopVMs_InvalidLimit(t *testing.T) {
	m := newTestMetricsMock()
	ts, _, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	for _, raw := range []string{"0", "-1", "abc", "9999"} {
		resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=cpu&limit=" + raw)
		if err != nil {
			t.Fatalf("GET (%s): %v", raw, err)
		}
		body := errorResponse{}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%q status = %d, want 400", raw, resp.StatusCode)
		}
		if body.Code != "invalid_limit" {
			t.Errorf("limit=%q code = %q, want invalid_limit", raw, body.Code)
		}
	}
}

func TestGetTopVMs_InvalidState(t *testing.T) {
	m := newTestMetricsMock()
	ts, _, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=cpu&state=paused")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var errResp errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", errResp.Code)
	}
}

func TestGetTopVMs_EmptyWhenNoVMs(t *testing.T) {
	m := newTestMetricsMock()
	ts, _, cleanup := testServerWithMetrics(t, m)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms/stats/top?metric=cpu")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body TopVMResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 0 {
		t.Errorf("len(items) = %d, want 0", len(body.Items))
	}
}

func containsStr(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
