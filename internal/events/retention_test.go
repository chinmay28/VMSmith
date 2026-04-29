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
	mu       sync.Mutex
	calls    int
	deleted  int
	err      error
	maxSeen  int
	signalCh chan struct{}
}

func (f *fakePruneStore) PruneEvents(max int) (int, error) {
	f.mu.Lock()
	f.calls++
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

func (f *fakePruneStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestRetention_DisabledWhenZero(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{}
	r := NewRetention(store, 0, 100*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Retention.Run did not exit when disabled (maxRecords=0)")
	}
	if store.callCount() != 0 {
		t.Errorf("expected no PruneEvents calls when disabled, got %d", store.callCount())
	}
}

func TestRetention_DisabledWhenIntervalZero(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{}
	r := NewRetention(store, 100, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Retention.Run did not exit when interval=0")
	}
	if store.callCount() != 0 {
		t.Errorf("expected no PruneEvents calls when interval=0, got %d", store.callCount())
	}
}

func TestRetention_RunsOnceAtStartup(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 100, time.Hour, nil)
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
}

func TestRetention_PeriodicSweep(t *testing.T) {
	t.Parallel()
	store := &fakePruneStore{signalCh: make(chan struct{}, 16)}
	r := NewRetention(store, 100, 25*time.Millisecond, nil)
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

	store := &fakePruneStore{deleted: 3, signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 100, time.Hour, bus)
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
	if evt.Attributes["deleted"] != "3" {
		t.Errorf("attributes.deleted=%q, want 3", evt.Attributes["deleted"])
	}
	if evt.Attributes["max_records"] != "100" {
		t.Errorf("attributes.max_records=%q, want 100", evt.Attributes["max_records"])
	}
}

func TestRetention_NoEventWhenNothingDeleted(t *testing.T) {
	t.Parallel()
	captureStore := &capturingStore{}
	bus := New(captureStore)
	bus.Start()
	defer bus.Stop()

	store := &fakePruneStore{deleted: 0, signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 100, time.Hour, bus)
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
	store := &fakePruneStore{err: errors.New("disk full"), signalCh: make(chan struct{}, 4)}
	r := NewRetention(store, 100, time.Hour, nil)
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
