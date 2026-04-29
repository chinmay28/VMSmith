package metrics

import (
	"sync"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// ring is a fixed-size circular buffer of MetricSample values.
// It is safe for concurrent use.
type ring struct {
	mu   sync.RWMutex
	buf  []types.MetricSample
	head int // index of the next write position
	size int // number of valid entries currently stored
	cap  int // maximum capacity
}

// newRing allocates a new ring buffer with the given capacity.
func newRing(capacity int) *ring {
	if capacity <= 0 {
		capacity = 1
	}
	return &ring{
		buf: make([]types.MetricSample, capacity),
		cap: capacity,
	}
}

// push appends a sample, overwriting the oldest entry when the buffer is full.
func (r *ring) push(s types.MetricSample) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf[r.head] = s
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
}

// all returns a copy of the stored samples in chronological order (oldest first).
func (r *ring) all() []types.MetricSample {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return nil
	}

	out := make([]types.MetricSample, r.size)
	// oldest entry is at (head - size + cap) % cap
	start := (r.head - r.size + r.cap) % r.cap
	for i := range r.size {
		out[i] = r.buf[(start+i)%r.cap]
	}
	return out
}

// latest returns the most recently pushed sample, or nil if empty.
func (r *ring) latest() *types.MetricSample {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return nil
	}
	// head points to the *next* write slot, so the last written is head-1.
	idx := (r.head - 1 + r.cap) % r.cap
	s := r.buf[idx]
	return &s
}
