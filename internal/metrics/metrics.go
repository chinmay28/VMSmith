// Package metrics provides VM resource metrics collection and storage.
// A background sampler polls libvirt's bulk stats API at a configurable
// interval and stores samples in per-VM in-memory ring buffers.
package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// Manager is the interface for starting/stopping the sampler and querying
// historical metrics for a specific VM.
type Manager interface {
	// Start launches the sampling goroutine.  It should be called once after
	// the daemon connects to libvirt.
	Start(ctx context.Context) error

	// Stop shuts down the sampler gracefully.
	Stop() error

	// Snapshot returns current metrics plus history for the given VM ID.
	// It returns (nil, nil) when no data has been collected yet for that VM.
	Snapshot(vmID string) (*types.VMStatsSnapshot, error)
}

// MockMetricsManager is an in-memory implementation of Manager for tests.
// Samples must be injected via SeedSample before Snapshot is called.
type MockMetricsManager struct {
	mu       sync.RWMutex
	rings    map[string]*ring
	states   map[string]string
	interval int
	histSize int

	// SnapshotErr, if non-nil, is returned by every Snapshot call.
	SnapshotErr error
}

// NewMockMetricsManager creates a new mock metrics manager.
func NewMockMetricsManager() *MockMetricsManager {
	return &MockMetricsManager{
		rings:    make(map[string]*ring),
		states:   make(map[string]string),
		interval: 10,
		histSize: 360,
	}
}

// Start is a no-op for the mock.
func (m *MockMetricsManager) Start(_ context.Context) error { return nil }

// Stop is a no-op for the mock.
func (m *MockMetricsManager) Stop() error { return nil }

// SeedSample injects a MetricSample for the given VM ID.
func (m *MockMetricsManager) SeedSample(vmID string, sample types.MetricSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rings[vmID]; !ok {
		m.rings[vmID] = newRing(m.histSize)
	}
	m.rings[vmID].push(sample)
}

// SetState records the state string for a VM (used in Snapshot responses).
func (m *MockMetricsManager) SetState(vmID, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[vmID] = state
}

// Snapshot returns the accumulated samples for vmID.  Returns nil if no
// samples have been seeded.
func (m *MockMetricsManager) Snapshot(vmID string) (*types.VMStatsSnapshot, error) {
	if m.SnapshotErr != nil {
		return nil, m.SnapshotErr
	}

	m.mu.RLock()
	r, ok := m.rings[vmID]
	state := m.states[vmID]
	m.mu.RUnlock()

	if !ok {
		return nil, nil
	}

	history := r.all()
	latest := r.latest()

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
		History:         history,
		IntervalSeconds: m.interval,
		HistorySize:     m.histSize,
	}, nil
}
