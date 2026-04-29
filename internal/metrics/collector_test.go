package metrics

import (
	"testing"
	"time"
)

// fakeProvider feeds a pre-recorded sequence of domain stats rounds to the
// sampler.  Each call to GetAllDomainStats returns the next round and advances
// the index; if the sequence is exhausted, it returns an empty slice.
type fakeProvider struct {
	rounds [][]domainSample
	idx    int
}

func (f *fakeProvider) GetAllDomainStats() ([]domainSample, error) {
	if f.idx >= len(f.rounds) {
		return nil, nil
	}
	r := f.rounds[f.idx]
	f.idx++
	return r, nil
}

// staticResolver maps name → vmID with a fixed table.
func staticResolver(table map[string]string) NameToIDFunc {
	return func(name string) (string, bool) {
		id, ok := table[name]
		return id, ok
	}
}

// newTestManager wires up a manager around a fakeProvider with controlled time.
func newTestManager(rounds [][]domainSample, table map[string]string) *LibvirtMetricsManager {
	provider := &fakeProvider{rounds: rounds}
	m := newManagerForProvider(provider, staticResolver(table), 10*time.Second, 360)
	return m
}

func TestProcessSamples_KeysByVMID(t *testing.T) {
	// libvirt domain name "my-vm" maps to vmsmith VM ID "vm-12345".
	// The API/CLI query by vmID, so the ring must be keyed by vmID.
	rounds := [][]domainSample{
		{
			{Name: "my-vm", State: "running",
				HasCPU: true, CPUTimeNs: 1_000_000_000, NumVcpus: 2},
		},
	}
	m := newTestManager(rounds, map[string]string{"my-vm": "vm-12345"})

	now := time.Unix(1000, 0)
	m.processSamples(rounds[0], now)

	// Lookup by VM ID must succeed.
	snap, err := m.Snapshot("vm-12345")
	if err != nil {
		t.Fatalf("Snapshot by ID: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snapshot indexed by vmID")
	}
	if snap.State != "running" {
		t.Errorf("State = %q, want running", snap.State)
	}

	// Lookup by domain name must NOT succeed (would indicate the regression).
	if snap, _ := m.Snapshot("my-vm"); snap != nil {
		t.Errorf("expected Snapshot by domain name to return nil, got %+v", snap)
	}

	// vmNames cache populated.
	if id, ok := m.vmNames["my-vm"]; !ok || id != "vm-12345" {
		t.Errorf("vmNames[my-vm] = (%q, %v), want (vm-12345, true)", id, ok)
	}
}

func TestProcessSamples_SkipsUnresolvable(t *testing.T) {
	// A domain whose name has no VM ID mapping must be skipped — otherwise
	// libvirt-level domains (libvirt internal VMs, e.g. "default") would
	// pollute the ring map.
	rounds := [][]domainSample{
		{
			{Name: "unknown-vm", State: "running",
				HasCPU: true, CPUTimeNs: 1_000_000_000, NumVcpus: 1},
		},
	}
	m := newTestManager(rounds, map[string]string{}) // empty resolver

	m.processSamples(rounds[0], time.Unix(1000, 0))

	if len(m.rings) != 0 {
		t.Errorf("expected no rings for unresolvable VM, got %d", len(m.rings))
	}
}

func TestProcessSamples_CPURate(t *testing.T) {
	// Two rounds 10s apart, CPU time advancing by 5s on a 2-vCPU domain →
	// expected CPU% = (5_000_000_000 / 10_000_000_000 / 2) * 100 = 25%.
	rounds := [][]domainSample{
		{{Name: "v1", HasCPU: true, CPUTimeNs: 0, NumVcpus: 2, State: "running"}},
		{{Name: "v1", HasCPU: true, CPUTimeNs: 5_000_000_000, NumVcpus: 2, State: "running"}},
	}
	m := newTestManager(rounds, map[string]string{"v1": "vm-A"})

	t0 := time.Unix(1000, 0)
	t1 := t0.Add(10 * time.Second)
	m.processSamples(rounds[0], t0)
	m.processSamples(rounds[1], t1)

	snap, _ := m.Snapshot("vm-A")
	if snap == nil || snap.Current == nil || snap.Current.CPUPercent == nil {
		t.Fatalf("expected CPUPercent set, snap=%+v", snap)
	}
	got := *snap.Current.CPUPercent
	if got < 24.99 || got > 25.01 {
		t.Errorf("CPU%% = %v, want ~25", got)
	}
}

func TestProcessSamples_CounterReset(t *testing.T) {
	// Counter-reset scenario: second round's cumulative bytes < first round's
	// (libvirt domain restarted, counters back to 0).  A negative delta must
	// produce nil rate, not a wraparound.
	rounds := [][]domainSample{
		{{Name: "v1", HasDisk: true, DiskRdBytes: 1_000_000, DiskWrBytes: 500_000, State: "running"}},
		{{Name: "v1", HasDisk: true, DiskRdBytes: 100, DiskWrBytes: 50, State: "running"}}, // reset
	}
	m := newTestManager(rounds, map[string]string{"v1": "vm-B"})

	t0 := time.Unix(1000, 0)
	m.processSamples(rounds[0], t0)
	m.processSamples(rounds[1], t0.Add(10*time.Second))

	snap, _ := m.Snapshot("vm-B")
	if snap == nil || snap.Current == nil {
		t.Fatal("expected snapshot with current sample")
	}
	if snap.Current.DiskReadBps != nil {
		t.Errorf("DiskReadBps = %v, want nil after reset", *snap.Current.DiskReadBps)
	}
	if snap.Current.DiskWriteBps != nil {
		t.Errorf("DiskWriteBps = %v, want nil after reset", *snap.Current.DiskWriteBps)
	}

	// After the reset, a third round with normal advance should produce a rate.
	round3 := []domainSample{{Name: "v1", HasDisk: true, DiskRdBytes: 10_100, DiskWrBytes: 5_050, State: "running"}}
	m.processSamples(round3, t0.Add(20*time.Second))
	snap, _ = m.Snapshot("vm-B")
	if snap.Current.DiskReadBps == nil || *snap.Current.DiskReadBps != 1000 {
		t.Errorf("post-reset DiskReadBps = %v, want 1000", snap.Current.DiskReadBps)
	}
}

func TestProcessSamples_NetworkRate(t *testing.T) {
	rounds := [][]domainSample{
		{{Name: "v1", HasNet: true, NetRxBytes: 0, NetTxBytes: 0, State: "running"}},
		{{Name: "v1", HasNet: true, NetRxBytes: 1_050_000, NetTxBytes: 2_100_000, State: "running"}},
	}
	m := newTestManager(rounds, map[string]string{"v1": "vm-C"})
	t0 := time.Unix(1000, 0)
	m.processSamples(rounds[0], t0)
	m.processSamples(rounds[1], t0.Add(10*time.Second))

	snap, _ := m.Snapshot("vm-C")
	if snap.Current.NetRxBps == nil || *snap.Current.NetRxBps != 105_000 {
		t.Errorf("NetRxBps = %v, want 105000", snap.Current.NetRxBps)
	}
	if snap.Current.NetTxBps == nil || *snap.Current.NetTxBps != 210_000 {
		t.Errorf("NetTxBps = %v, want 210000", snap.Current.NetTxBps)
	}
}

func TestProcessSamples_StalePrune(t *testing.T) {
	// VM seen in round 1, missing from round 2.  Until 5 minutes pass the
	// ring is retained; once the latest sample is older than the prune
	// threshold it must be evicted.
	rounds := [][]domainSample{
		{{Name: "v1", HasCPU: true, CPUTimeNs: 0, NumVcpus: 1, State: "running"}},
		{}, // empty round — VM disappeared
	}
	m := newTestManager(rounds, map[string]string{"v1": "vm-D"})

	t0 := time.Unix(1000, 0)
	m.processSamples(rounds[0], t0)
	if _, ok := m.rings["vm-D"]; !ok {
		t.Fatal("ring should exist after first round")
	}

	// 1 minute later: still well within prune window.
	m.processSamples(rounds[1], t0.Add(time.Minute))
	if _, ok := m.rings["vm-D"]; !ok {
		t.Errorf("ring evicted too early; should survive at 1min")
	}

	// 6 minutes later: past the 5-minute prune threshold.
	m.processSamples(rounds[1], t0.Add(6*time.Minute))
	if _, ok := m.rings["vm-D"]; ok {
		t.Errorf("ring should be evicted after 5+ minutes idle")
	}
	if _, ok := m.vmNames["v1"]; ok {
		t.Errorf("vmNames cache entry should be cleaned up too")
	}
}

func TestProcessSamples_PartialDataNoRate(t *testing.T) {
	// A single sample (no prev) → no rates emitted, just timestamps and state.
	rounds := [][]domainSample{
		{{Name: "v1", HasCPU: true, CPUTimeNs: 12345, NumVcpus: 1,
			HasDisk: true, DiskRdBytes: 1, DiskWrBytes: 1,
			HasNet: true, NetRxBytes: 1, NetTxBytes: 1,
			State: "running"}},
	}
	m := newTestManager(rounds, map[string]string{"v1": "vm-E"})
	m.processSamples(rounds[0], time.Unix(1000, 0))

	snap, _ := m.Snapshot("vm-E")
	if snap == nil || snap.Current == nil {
		t.Fatal("expected snapshot")
	}
	if snap.Current.CPUPercent != nil || snap.Current.DiskReadBps != nil || snap.Current.NetRxBps != nil {
		t.Errorf("first sample should have nil rates (no prev), got %+v", snap.Current)
	}
}

func TestProcessSamples_BalloonMemory(t *testing.T) {
	// 256 MiB RSS → 256 MB reported.  Available - Unused: 1024 - 256 = 768 MiB.
	rounds := [][]domainSample{
		{{Name: "v1", State: "running",
			HasBalloon:    true,
			BalloonRssSet: true, MemRSSKiB: 256 * 1024,
			BalloonAvailSet: true, MemAvailableKiB: 1024 * 1024,
			BalloonUnusedSet: true, MemUnusedKiB: 256 * 1024,
		}},
	}
	m := newTestManager(rounds, map[string]string{"v1": "vm-F"})
	m.processSamples(rounds[0], time.Unix(1000, 0))

	snap, _ := m.Snapshot("vm-F")
	if snap.Current.MemUsedMB == nil || *snap.Current.MemUsedMB != 256 {
		t.Errorf("MemUsedMB = %v, want 256", snap.Current.MemUsedMB)
	}
	if snap.Current.MemAvailMB == nil || *snap.Current.MemAvailMB != 768 {
		t.Errorf("MemAvailMB = %v, want 768", snap.Current.MemAvailMB)
	}
}

func TestProcessSamples_CPUClampedAt100(t *testing.T) {
	// Burst delta exceeds dt*vcpus (shouldn't happen normally but guard against it).
	rounds := [][]domainSample{
		{{Name: "v1", HasCPU: true, CPUTimeNs: 0, NumVcpus: 1, State: "running"}},
		{{Name: "v1", HasCPU: true, CPUTimeNs: 25_000_000_000, NumVcpus: 1, State: "running"}},
	}
	m := newTestManager(rounds, map[string]string{"v1": "vm-G"})

	t0 := time.Unix(1000, 0)
	m.processSamples(rounds[0], t0)
	m.processSamples(rounds[1], t0.Add(10*time.Second))

	snap, _ := m.Snapshot("vm-G")
	if snap.Current.CPUPercent == nil {
		t.Fatal("expected CPUPercent set")
	}
	if *snap.Current.CPUPercent != 100.0 {
		t.Errorf("CPU%% = %v, want clamped 100", *snap.Current.CPUPercent)
	}
}

func TestProcessSamples_NoResolverFallsBackToName(t *testing.T) {
	// When no resolver is wired (legacy / direct test path), keying falls back
	// to the domain name.  Verifies back-compat.
	rounds := [][]domainSample{
		{{Name: "v1", State: "running", HasCPU: true, CPUTimeNs: 1, NumVcpus: 1}},
	}
	provider := &fakeProvider{rounds: rounds}
	m := newManagerForProvider(provider, nil /* no resolver */, 10*time.Second, 360)

	m.processSamples(rounds[0], time.Unix(1000, 0))
	if snap, _ := m.Snapshot("v1"); snap == nil {
		t.Errorf("expected fallback indexing by domain name without resolver")
	}
}
