package vm

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// memEventStoreCounter is a thread-safe events.Store stub that just hands out
// monotonic IDs.  Used here so we can run the bus end-to-end without bbolt.
type memEventStoreCounter struct {
	mu   sync.Mutex
	next uint64
}

func (m *memEventStoreCounter) AppendEvent(evt *types.Event) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	return m.next, nil
}

func newTestBoltStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestVMStatePersister_AppliesLibvirtEvent verifies the consumer updates the
// VM record in the store when it receives a libvirt-source event with a
// `state` attribute.
func TestVMStatePersister_AppliesLibvirtEvent(t *testing.T) {
	s := newTestBoltStore(t)
	if err := s.PutVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateStopped}); err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	bus := events.New(&memEventStoreCounter{})
	bus.Start()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p := NewVMStatePersister(bus, s)
	go p.Run(ctx)

	// Give the persister a moment to subscribe before publishing.
	time.Sleep(50 * time.Millisecond)

	bus.Publish(&types.Event{
		Type:       "vm.started",
		Source:     types.EventSourceLibvirt,
		VMID:       "vm-1",
		Attributes: map[string]string{"state": string(types.VMStateRunning)},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := s.GetVM("vm-1")
		if err == nil && got.State == types.VMStateRunning {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := s.GetVM("vm-1")
	t.Fatalf("VM state never updated; current state = %q", got.State)
}

// TestVMStatePersister_IgnoresNonLibvirtEvents asserts that app/system events
// (or events without a state attribute) are silently ignored — the persister
// is the single writer for libvirt-driven state transitions only.
func TestVMStatePersister_IgnoresNonLibvirtEvents(t *testing.T) {
	s := newTestBoltStore(t)
	original := &types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped}
	if err := s.PutVM(original); err != nil {
		t.Fatalf("seed VM: %v", err)
	}
	originalUpdated := original.UpdatedAt

	bus := events.New(&memEventStoreCounter{})
	bus.Start()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p := NewVMStatePersister(bus, s)
	go p.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// app event with a state attr — must be ignored.
	bus.Publish(&types.Event{
		Type:       "vm.created",
		Source:     types.EventSourceApp,
		VMID:       "vm-2",
		Attributes: map[string]string{"state": string(types.VMStateRunning)},
	})

	// libvirt event without state attr — must be ignored.
	bus.Publish(&types.Event{
		Type:   "vm.lifecycle_42",
		Source: types.EventSourceLibvirt,
		VMID:   "vm-2",
	})

	// libvirt event with empty VMID — must be ignored.
	bus.Publish(&types.Event{
		Type:       "vm.started",
		Source:     types.EventSourceLibvirt,
		Attributes: map[string]string{"state": string(types.VMStateRunning)},
	})

	// Give the bus + persister a beat to process — then assert no change.
	time.Sleep(150 * time.Millisecond)

	got, err := s.GetVM("vm-2")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if got.State != types.VMStateStopped {
		t.Errorf("state mutated unexpectedly: got %q, want %q", got.State, types.VMStateStopped)
	}
	if !got.UpdatedAt.Equal(originalUpdated) {
		t.Errorf("UpdatedAt mutated unexpectedly: got %v, want %v", got.UpdatedAt, originalUpdated)
	}
}

// TestVMStatePersister_NoStoreOrBus_NoOp ensures Run returns immediately when
// either dependency is missing, instead of panicking.
func TestVMStatePersister_NoStoreOrBus_NoOp(t *testing.T) {
	NewVMStatePersister(nil, nil).Run(context.Background())
	NewVMStatePersister(nil, newTestBoltStore(t)).Run(context.Background())
}

// TestVMStatePersister_HandlesMissingVM verifies the consumer silently drops
// events for a VM that no longer exists in the store (e.g. a `vm.undefined`
// event arriving after the VM record has already been deleted by an API
// handler).
func TestVMStatePersister_HandlesMissingVM(t *testing.T) {
	s := newTestBoltStore(t)
	bus := events.New(&memEventStoreCounter{})
	bus.Start()
	t.Cleanup(bus.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p := NewVMStatePersister(bus, s)
	go p.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	bus.Publish(&types.Event{
		Type:       "vm.undefined",
		Source:     types.EventSourceLibvirt,
		VMID:       "vm-does-not-exist",
		Attributes: map[string]string{"state": string(types.VMStateDeleted)},
	})

	// Just ensure no panic and that we can still publish a follow-up event.
	time.Sleep(100 * time.Millisecond)
}
