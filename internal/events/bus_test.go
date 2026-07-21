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

// TestBusSubscribeDuplicateNamesDoNotCollide guards against the bug where two
// callers passing the same name (e.g. two browser tabs subscribing as
// "sse-1.2.3.4") silently overwrote each other in the subscribers map and
// leaked the first channel.  Both subscribers must receive the event.
func TestBusSubscribeDuplicateNamesDoNotCollide(t *testing.T) {
	store := &mockStore{}
	bus := New(store)
	bus.Start()
	defer bus.Stop()

	// Same supplied name twice — must not collide internally.
	ch1, cancel1 := bus.Subscribe("sse-127.0.0.1")
	defer cancel1()
	ch2, cancel2 := bus.Subscribe("sse-127.0.0.1")
	defer cancel2()

	bus.Publish(&types.Event{Type: "vm.created"})

	recv := func(ch <-chan *types.Event, label string) {
		select {
		case evt := <-ch:
			if evt.Type != "vm.created" {
				t.Errorf("%s: expected vm.created, got %s", label, evt.Type)
			}
		case <-time.After(time.Second):
			t.Errorf("%s: timeout — second subscriber did not receive event (collision regression)", label)
		}
	}
	recv(ch1, "first")
	recv(ch2, "second")
}

func TestNewSystemEventWithAttrs(t *testing.T) {
	evt := NewSystemEventWithAttrs("port_forward.restore_failed", types.EventSeverityWarn,
		"failed to restore", map[string]string{"error": "iptables not found"})

	if evt.Type != "port_forward.restore_failed" {
		t.Errorf("Type = %q, want port_forward.restore_failed", evt.Type)
	}
	if evt.Source != types.EventSourceSystem {
		t.Errorf("Source = %q, want %q", evt.Source, types.EventSourceSystem)
	}
	if evt.Severity != types.EventSeverityWarn {
		t.Errorf("Severity = %q, want warn", evt.Severity)
	}
	if evt.Attributes["error"] != "iptables not found" {
		t.Errorf("attributes.error = %q, want iptables not found", evt.Attributes["error"])
	}
	if evt.OccurredAt.IsZero() {
		t.Error("OccurredAt should be set")
	}
}

func TestNewSystemEvent_NoAttrs(t *testing.T) {
	evt := NewSystemEvent("daemon.shutdown", types.EventSeverityInfo, "shutting down")
	if evt.Source != types.EventSourceSystem {
		t.Errorf("Source = %q, want %q", evt.Source, types.EventSourceSystem)
	}
	if evt.Attributes != nil {
		t.Errorf("expected nil Attributes, got %+v", evt.Attributes)
	}
}

// TestBusSlowSubscriberIsDropped guards the invariant that a subscriber whose
// channel is full does not block the bus.  We fill the buffered subscriber
// channel without ever reading from it, then publish more events than the
// subscriber buffer can hold and confirm the bus continued processing — the
// store reflects every persisted event even though the subscriber received
// at most subscriberBufSize.
func TestBusSlowSubscriberIsDropped(t *testing.T) {
	store := &mockStore{}
	bus := New(store)
	bus.Start()
	defer bus.Stop()

	// Slow subscriber — never reads from ch.
	_, cancel := bus.Subscribe("slow")
	defer cancel()

	// Fast subscriber — reads everything to confirm the bus still fans out
	// to healthy subscribers while a slow one is being dropped.
	fastCh, cancelFast := bus.Subscribe("fast")
	defer cancelFast()

	const total = subscriberBufSize + 32
	for i := range total {
		bus.Publish(&types.Event{Type: "tick", Message: fmt.Sprintf("%d", i)})
		select {
		case <-fastCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("fast subscriber missed event %d/%d before timeout", i+1, total)
		}
	}

	stored := store.all()
	if len(stored) != total {
		t.Fatalf("expected %d persisted events, got %d", total, len(stored))
	}
}

// TestBusPersistenceErrorStillFansOut covers the branch in process() where
// store.AppendEvent fails: the bus must still deliver to subscribers (with a
// transient ID prefix) instead of silently dropping the event.
func TestBusPersistenceErrorStillFansOut(t *testing.T) {
	store := &mockStore{Err: fmt.Errorf("disk full")}
	bus := New(store)
	bus.Start()
	defer bus.Stop()

	ch, cancel := bus.Subscribe("test")
	defer cancel()

	bus.Publish(&types.Event{Type: "vm.started"})

	select {
	case evt := <-ch:
		if evt.Type != "vm.started" {
			t.Fatalf("type = %q, want vm.started", evt.Type)
		}
		if evt.ID == "" {
			t.Error("ID should be assigned even when persistence fails")
		}
		// IDs from a failed persist are tagged so consumers can tell them apart.
		if len(evt.ID) < len("transient-") || evt.ID[:len("transient-")] != "transient-" {
			t.Errorf("ID = %q, want transient-* prefix on persistence failure", evt.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout — subscriber should still receive event when persistence fails")
	}
}

// TestBusPublishOverflowDropsWithoutBlocking verifies that a flood larger than
// the publish channel's buffer cannot deadlock Publish (which is invoked from
// the libvirt event loop and must never block).  We use a store that blocks
// AppendEvent until released, fill the publish buffer, then issue many more
// Publish calls and assert each returns within a short timeout.
func TestBusPublishOverflowDropsWithoutBlocking(t *testing.T) {
	gate := make(chan struct{})
	released := make(chan struct{})
	store := &blockingStore{gate: gate, released: released}
	bus := New(store)
	bus.Start()
	defer func() {
		close(gate) // unblock the run goroutine before Stop drains.
		bus.Stop()
	}()

	// First publish hits the run loop and blocks on AppendEvent (waiting on
	// gate).  Subsequent publishes accumulate in publishCh until it's full.
	for range publishBufSize + 16 {
		done := make(chan struct{})
		go func() {
			bus.Publish(&types.Event{Type: "flood"})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Publish blocked when buffer was full — must drop instead")
		}
	}
}

// blockingStore.AppendEvent waits on gate so the bus's run goroutine stalls
// long enough for publishCh to fill.
type blockingStore struct {
	mu       sync.Mutex
	gate     chan struct{}
	released chan struct{}
	seq      uint64
	events   []*types.Event
}

func (b *blockingStore) AppendEvent(evt *types.Event) (uint64, error) {
	<-b.gate
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	cp := *evt
	b.events = append(b.events, &cp)
	return b.seq, nil
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
