package events

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// mockStore implements Store in memory.
type mockStore struct {
	mu     sync.Mutex
	events []*types.Event
	seq    uint64
	Err    error
}

func (m *mockStore) AppendEvent(evt *types.Event) (uint64, error) {
	if m.Err != nil {
		return 0, m.Err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	evt.ID = fmt.Sprintf("%d", m.seq)
	cp := *evt
	m.events = append(m.events, &cp)
	return m.seq, nil
}

func (m *mockStore) all() []*types.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*types.Event, len(m.events))
	copy(out, m.events)
	return out
}

func TestBusPublishAndSubscribe(t *testing.T) {
	store := &mockStore{}
	bus := New(store)
	bus.Start()
	defer bus.Stop()

	ch, cancel := bus.Subscribe("test")
	defer cancel()

	bus.Publish(&types.Event{Type: "vm.started", Source: types.EventSourceLibvirt, VMID: "vm-1"})

	select {
	case evt := <-ch:
		if evt.Type != "vm.started" {
			t.Fatalf("expected vm.started, got %s", evt.Type)
		}
		if evt.VMID != "vm-1" {
			t.Fatalf("expected vm-1, got %s", evt.VMID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	stored := store.all()
	if len(stored) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(stored))
	}
}

func TestBusMultipleSubscribers(t *testing.T) {
	store := &mockStore{}
	bus := New(store)
	bus.Start()
	defer bus.Stop()

	ch1, cancel1 := bus.Subscribe("sub1")
	ch2, cancel2 := bus.Subscribe("sub2")
	defer cancel1()
	defer cancel2()

	bus.Publish(&types.Event{Type: "vm.created"})

	recv := func(ch <-chan *types.Event, name string) {
		select {
		case evt := <-ch:
			if evt.Type != "vm.created" {
				t.Errorf("%s: expected vm.created, got %s", name, evt.Type)
			}
		case <-time.After(time.Second):
			t.Errorf("%s: timeout", name)
		}
	}
	recv(ch1, "sub1")
	recv(ch2, "sub2")
}

func TestBusMonotonicIDs(t *testing.T) {
	store := &mockStore{}
	bus := New(store)
	bus.Start()
	defer bus.Stop()

	ch, cancel := bus.Subscribe("test")
	defer cancel()

	const n = 10
	for i := range n {
		bus.Publish(&types.Event{Type: "tick", Message: fmt.Sprintf("%d", i)})
	}

	for range n {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout")
		}
	}

	stored := store.all()
	if len(stored) != n {
		t.Fatalf("expected %d stored, got %d", n, len(stored))
	}
	for i, e := range stored {
		expected := fmt.Sprintf("%d", i+1)
		if e.ID != expected {
			t.Errorf("event %d: expected ID %s, got %s", i, expected, e.ID)
		}
	}
}

func TestBusCancelUnsubscribes(t *testing.T) {
	store := &mockStore{}
	bus := New(store)
	bus.Start()
	defer bus.Stop()

	ch, cancel := bus.Subscribe("test")
	cancel()

	bus.Publish(&types.Event{Type: "vm.started"})

	// Give the bus goroutine time to process.
	time.Sleep(50 * time.Millisecond)

	select {
	case <-ch:
		t.Fatal("should not receive after cancel")
	default:
	}
}

func TestBusStopDrains(t *testing.T) {
	store := &mockStore{}
	bus := New(store)
	bus.Start()

	const n = 5
	for range n {
		bus.Publish(&types.Event{Type: "drain-test"})
	}
	bus.Stop()

	stored := store.all()
	if len(stored) != n {
		t.Errorf("expected %d events drained, got %d", n, len(stored))
	}
}

func TestBusOccurredAtSet(t *testing.T) {
	store := &mockStore{}
	bus := New(store)
	bus.Start()
	defer bus.Stop()

	ch, cancel := bus.Subscribe("test")
	defer cancel()

	before := time.Now()
	bus.Publish(&types.Event{Type: "ts-check"})

	select {
	case evt := <-ch:
		if evt.OccurredAt.IsZero() {
			t.Error("OccurredAt should be set")
		}
		if evt.OccurredAt.Before(before) {
			t.Error("OccurredAt should not be before publish time")
		}
		if evt.CreatedAt.IsZero() {
			t.Error("CreatedAt should be set for backward compat")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
