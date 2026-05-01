package events

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

type fakePruneStore struct {
	mu          sync.Mutex
	recordCalls int
	ageCalls    int
	deleted     int
	deletedAge  int
	err         error
	ageErr      error
	maxSeen     int
	maxAgeSeen  time.Duration
	signalCh    chan struct{}
}

func (f *fakePruneStore) PruneEvents(max int) (int, error) {
	f.mu.Lock()
	f.recordCalls++
	f.maxSeen = max
	d := f.deleted
	err := f.err
	ch := f.signalCh
	f.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return d, err
}

func (f *fakePruneStore) PruneEventsByAge(age time.Duration) (int, error) {
	f.mu.Lock()
	f.ageCalls++
	f.maxAgeSeen = age
	d := f.deletedAge
	err := f.ageErr
	f.mu.Unlock()
	return d, err
}

func (f *fakePruneStore) recordCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recordCalls
}

func (f *fakePruneStore) ageCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ageCalls
}

func TestRetention_DisabledWhenAllLimitsZero(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{}
	r := NewRetention(store, 0, 0, 100*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Retention.Run did not exit when both limits disabled")
	}
	if store.recordCallCount() != 0 || store.ageCallCount() != 0 {
		t.Errorf("expected no Prune calls when fully disabled, got records=%d age=%d",
			store.recordCallCount(), store.ageCallCount())
	}
}

func TestRetention_DisabledWhenIntervalZero(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{}
	r := NewRetention(store, 100, time.Hour, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Retention.Run did not exit when interval=0")
	}
	if store.recordCallCount() != 0 || store.ageCallCount() != 0 {
		t.Errorf("expected no Prune calls when interval=0, got records=%d age=%d",
			store.recordCallCount(), store.ageCallCount())
	}
}

func TestRetention_AgeOnlyEnabled(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 0, 30*time.Minute, 50*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.ageCallCount() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if store.ageCallCount() < 1 {
		t.Fatalf("expected age sweep to run, got %d calls", store.ageCallCount())
	}
	if store.recordCallCount() != 0 {
		t.Errorf("expected no record-based sweeps when maxRecords=0, got %d", store.recordCallCount())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.maxAgeSeen != 30*time.Minute {
		t.Errorf("PruneEventsByAge called with max=%s, want 30m", store.maxAgeSeen)
	}
}

func TestRetention_RunsOnceAtStartup(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 100, time.Hour, time.Hour, nil)
	ctx, cancel := context.WithCancel(context.Background())

	go r.Run(ctx)

	select {
	case <-store.signalCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected initial sweep within 2s")
	}
	cancel()

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.maxSeen != 100 {
		t.Errorf("PruneEvents called with max=%d, want 100", store.maxSeen)
	}
	if store.maxAgeSeen != time.Hour {
		t.Errorf("PruneEventsByAge called with max=%s, want 1h", store.maxAgeSeen)
	}
}

func TestRetention_PeriodicSweep(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{signalCh: make(chan struct{}, 16)}
	r := NewRetention(store, 100, 0, 25*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	for i := 0; i < 2; i++ {
		select {
		case <-store.signalCh:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("expected sweep #%d within 500ms", i+1)
		}
	}
}

func TestRetention_PublishesSystemEventOnDelete(t *testing.T) {
	t.Parallel()
	captureStore := &capturingStore{}
	bus := New(captureStore)
	bus.Start()
	defer bus.Stop()

	store := &fakePruneStore{deleted: 3, deletedAge: 7, signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 100, 24*time.Hour, time.Hour, bus)
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	select {
	case <-store.signalCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected initial sweep within 2s")
	}
	cancel()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if captureStore.count.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := captureStore.count.Load(); got != 1 {
		t.Fatalf("expected 1 published system event, got %d", got)
	}
	captureStore.mu.Lock()
	defer captureStore.mu.Unlock()
	evt := captureStore.last
	if evt == nil {
		t.Fatal("no event captured")
	}
	if evt.Type != "events.retention_pruned" {
		t.Errorf("event type=%q, want events.retention_pruned", evt.Type)
	}
	if evt.Source != types.EventSourceSystem {
		t.Errorf("event source=%q, want %s", evt.Source, types.EventSourceSystem)
	}
	if evt.Attributes["deleted"] != "10" {
		t.Errorf("attributes.deleted=%q, want 10", evt.Attributes["deleted"])
	}
	if evt.Attributes["deleted_max_records"] != "3" {
		t.Errorf("attributes.deleted_max_records=%q, want 3", evt.Attributes["deleted_max_records"])
	}
	if evt.Attributes["deleted_max_age"] != "7" {
		t.Errorf("attributes.deleted_max_age=%q, want 7", evt.Attributes["deleted_max_age"])
	}
	if evt.Attributes["max_records"] != "100" {
		t.Errorf("attributes.max_records=%q, want 100", evt.Attributes["max_records"])
	}
	if evt.Attributes["max_age_seconds"] != "86400" {
		t.Errorf("attributes.max_age_seconds=%q, want 86400", evt.Attributes["max_age_seconds"])
	}
}

func TestRetention_NoEventWhenNothingDeleted(t *testing.T) {
	t.Parallel()
	captureStore := &capturingStore{}
	bus := New(captureStore)
	bus.Start()
	defer bus.Stop()

	store := &fakePruneStore{deleted: 0, deletedAge: 0, signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 100, time.Hour, time.Hour, bus)
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	select {
	case <-store.signalCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected initial sweep within 2s")
	}
	cancel()

	time.Sleep(50 * time.Millisecond)
	if got := captureStore.count.Load(); got != 0 {
		t.Errorf("expected no events when deleted=0, got %d", got)
	}
}

func TestRetention_PruneErrorDoesNotPanic(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{err: errors.New("disk full"), ageErr: errors.New("disk full"), signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 100, time.Hour, time.Hour, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	select {
	case <-store.signalCh:
	case <-time.After(2 * time.Second):
		t.Fatal("expected initial sweep within 2s even on error")
	}
	cancel()
}

func TestItoa(t *testing.T) {
	t.Parallel()
	cases := map[int]string{
		0:    "0",
		1:    "1",
		42:   "42",
		1000: "1000",
		-7:   "-7",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

// capturingStore satisfies the events.Store interface and records the most
// recent appended event.
type capturingStore struct {
	count atomic.Int64
	mu    sync.Mutex
	last  *types.Event
}

func (s *capturingStore) AppendEvent(evt *types.Event) (uint64, error) {
	s.mu.Lock()
	cp := *evt
	s.last = &cp
	s.mu.Unlock()
	n := s.count.Add(1)
	return uint64(n), nil
}
