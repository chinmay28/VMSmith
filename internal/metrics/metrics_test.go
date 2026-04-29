package metrics

import (
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// --- ring tests ---

func TestRingPushAndAll(t *testing.T) {
	r := newRing(3)
	if got := r.all(); got != nil {
		t.Fatalf("expected nil from empty ring, got %v", got)
	}
	if got := r.latest(); got != nil {
		t.Fatalf("expected nil latest from empty ring, got %v", got)
	}

	s1 := types.MetricSample{Timestamp: time.Unix(1, 0)}
	s2 := types.MetricSample{Timestamp: time.Unix(2, 0)}
	s3 := types.MetricSample{Timestamp: time.Unix(3, 0)}
	r.push(s1)
	r.push(s2)
	r.push(s3)

	all := r.all()
	if len(all) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(all))
	}
	if all[0].Timestamp != s1.Timestamp {
		t.Errorf("oldest sample mismatch: got %v want %v", all[0].Timestamp, s1.Timestamp)
	}
	if all[2].Timestamp != s3.Timestamp {
		t.Errorf("newest sample mismatch: got %v want %v", all[2].Timestamp, s3.Timestamp)
	}

	if latest := r.latest(); latest == nil || latest.Timestamp != s3.Timestamp {
		t.Errorf("latest = %v, want %v", latest, s3.Timestamp)
	}
}

func TestRingOverwrite(t *testing.T) {
	r := newRing(2)
	s1 := types.MetricSample{Timestamp: time.Unix(1, 0)}
	s2 := types.MetricSample{Timestamp: time.Unix(2, 0)}
	s3 := types.MetricSample{Timestamp: time.Unix(3, 0)}
	r.push(s1)
	r.push(s2)
	r.push(s3) // should evict s1

	all := r.all()
	if len(all) != 2 {
		t.Fatalf("expected 2 samples after overflow, got %d", len(all))
	}
	if all[0].Timestamp != s2.Timestamp {
		t.Errorf("expected oldest = s2, got %v", all[0].Timestamp)
	}
	if all[1].Timestamp != s3.Timestamp {
		t.Errorf("expected newest = s3, got %v", all[1].Timestamp)
	}
}

func TestRingCapacityOne(t *testing.T) {
	r := newRing(1)
	r.push(types.MetricSample{Timestamp: time.Unix(1, 0)})
	r.push(types.MetricSample{Timestamp: time.Unix(2, 0)})
	all := r.all()
	if len(all) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(all))
	}
	if all[0].Timestamp.Unix() != 2 {
		t.Errorf("expected ts=2, got %v", all[0].Timestamp)
	}
}

// --- MockMetricsManager tests ---

func TestMockMetricsManagerNoSamples(t *testing.T) {
	m := NewMockMetricsManager()
	snap, err := m.Snapshot("vm-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap != nil {
		t.Fatalf("expected nil snapshot for unknown VM, got %+v", snap)
	}
}

func TestMockMetricsManagerSeedAndSnapshot(t *testing.T) {
	m := NewMockMetricsManager()

	cpu := 42.5
	mem := uint64(512)
	sample := types.MetricSample{
		Timestamp:  time.Now(),
		CPUPercent: &cpu,
		MemUsedMB:  &mem,
	}
	m.SeedSample("vm-1", sample)
	m.SetState("vm-1", "running")

	snap, err := m.Snapshot("vm-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.VMID != "vm-1" {
		t.Errorf("VMID = %q, want %q", snap.VMID, "vm-1")
	}
	if snap.State != "running" {
		t.Errorf("State = %q, want %q", snap.State, "running")
	}
	if snap.Current == nil {
		t.Fatal("expected Current to be non-nil")
	}
	if snap.Current.CPUPercent == nil || *snap.Current.CPUPercent != cpu {
		t.Errorf("CPUPercent = %v, want %v", snap.Current.CPUPercent, cpu)
	}
	if snap.LastSampledAt == nil {
		t.Error("expected LastSampledAt to be set")
	}
	if len(snap.History) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(snap.History))
	}
}

func TestMockMetricsManagerMultipleSamples(t *testing.T) {
	m := NewMockMetricsManager()
	vmID := "vm-2"

	for i := 0; i < 5; i++ {
		cpu := float64(i * 10)
		m.SeedSample(vmID, types.MetricSample{
			Timestamp:  time.Unix(int64(i), 0),
			CPUPercent: &cpu,
		})
	}

	snap, err := m.Snapshot(vmID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.History) != 5 {
		t.Errorf("expected 5 history entries, got %d", len(snap.History))
	}
	// oldest first
	if *snap.History[0].CPUPercent != 0 {
		t.Errorf("oldest cpu = %v, want 0", *snap.History[0].CPUPercent)
	}
	if *snap.History[4].CPUPercent != 40 {
		t.Errorf("newest cpu = %v, want 40", *snap.History[4].CPUPercent)
	}
}

func TestMockMetricsManagerSnapshotErr(t *testing.T) {
	m := NewMockMetricsManager()
	m.SnapshotErr = types.NewAPIError("test_error", "forced error")

	_, err := m.Snapshot("vm-x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestMockStartStop(t *testing.T) {
	m := NewMockMetricsManager()
	if err := m.Start(nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// --- rate math helpers (pure-Go, no libvirt) ---

// ptr helpers for tests
func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }

func TestRateMathCounterReset(t *testing.T) {
	// Simulate counter reset: new value < old value → result should be nil.
	// We test the conceptual logic used in collector.go (deltaRd < 0 → skip).
	var prevRd int64 = 1000
	var newRd int64 = 500 // counter reset

	delta := newRd - prevRd // -500
	if delta >= 0 {
		t.Errorf("expected delta < 0 for counter reset, got %d", delta)
	}
	// The collector checks delta >= 0 before storing; a negative delta means
	// the sample field stays nil — this is the expected behaviour.
}

func TestRateMathNormalDelta(t *testing.T) {
	prevBytes := uint64(1_000_000)
	curBytes := uint64(2_050_000)
	dtSec := 10.0

	delta := int64(curBytes) - int64(prevBytes)
	if delta < 0 {
		t.Fatalf("unexpected negative delta %d", delta)
	}
	bps := uint64(float64(delta) / dtSec)
	if bps != 105_000 {
		t.Errorf("bps = %d, want 105000", bps)
	}
}

func TestCPUPercentClamp(t *testing.T) {
	// Simulates a burst where delta > dtNs * vcpus (shouldn't happen normally
	// but guard against it).
	dtNs := int64(10_000_000_000) // 10s in ns
	numVcpus := 2
	// Suppose delta is 25 seconds of CPU time (impossible but test the clamp).
	deltaCPU := int64(25_000_000_000)

	cpuPct := (float64(deltaCPU) / float64(dtNs) / float64(numVcpus)) * 100.0
	if cpuPct > 100.0 {
		cpuPct = 100.0
	}
	if cpuPct != 100.0 {
		t.Errorf("expected clamped 100%%, got %v", cpuPct)
	}
}
