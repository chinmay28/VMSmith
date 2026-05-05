package vm

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

// memEventStore is a minimal events.Store stub for unit tests.
type memEventStore struct{ next uint64 }

func (m *memEventStore) AppendEvent(evt *types.Event) (uint64, error) {
	m.next++
	return m.next, nil
}

// TestEmitDHCPExhausted_PublishesSystemEvent verifies that the helper emits
// a `dhcp.exhausted` system event with the expected attributes when an event
// bus is wired.
func TestEmitDHCPExhausted_PublishesSystemEvent(t *testing.T) {
	bus := events.New(&memEventStore{})
	bus.Start()
	defer bus.Stop()

	ch, cancel := bus.Subscribe("test")
	defer cancel()

	m := &LibvirtManager{}
	m.SetEventBus(bus)
	m.emitDHCPExhausted("vmA", "no available IPs in 192.168.100.50-200")

	select {
	case evt := <-ch:
		if evt.Type != "dhcp.exhausted" {
			t.Errorf("Type = %q, want dhcp.exhausted", evt.Type)
		}
		if evt.Source != types.EventSourceSystem {
			t.Errorf("Source = %q, want system", evt.Source)
		}
		if evt.Severity != types.EventSeverityWarn {
			t.Errorf("Severity = %q, want warn", evt.Severity)
		}
		if evt.Attributes["vm_name"] != "vmA" {
			t.Errorf("attributes.vm_name = %q, want vmA", evt.Attributes["vm_name"])
		}
		if evt.Attributes["reason"] == "" {
			t.Errorf("attributes.reason should not be empty")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dhcp.exhausted event")
	}
}

// TestEmitDHCPExhausted_NoBus must be a no-op (and not panic) when no bus
// is wired into the manager.
func TestEmitDHCPExhausted_NoBus(t *testing.T) {
	m := &LibvirtManager{}
	m.emitDHCPExhausted("vm", "reason") // must not panic
}

func TestHandleStoredLifecycleEvent_NoBus_DoesNotMutateStoreState(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	original := &types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateStopped}
	if err := s.PutVM(original); err != nil {
		t.Fatalf("seed VM: %v", err)
	}

	m := &LibvirtManager{store: s}
	m.handleStoredLifecycleEvent("alpha", original, &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_STARTED})

	got, err := s.GetVM("vm-1")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if got.State != types.VMStateStopped {
		t.Fatalf("state mutated without event bus: got %q want %q", got.State, types.VMStateStopped)
	}
}

func TestHandleStoredLifecycleEvent_WithBus_PublishesLibvirtEvent(t *testing.T) {
	bus := events.New(&memEventStore{})
	bus.Start()
	defer bus.Stop()

	ch, cancel := bus.Subscribe("test")
	defer cancel()

	m := &LibvirtManager{}
	m.SetEventBus(bus)

	vmRecord := &types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped}
	m.handleStoredLifecycleEvent("beta", vmRecord, &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_STARTED})

	select {
	case evt := <-ch:
		if evt.Type != "vm.started" {
			t.Fatalf("Type = %q, want vm.started", evt.Type)
		}
		if evt.Source != types.EventSourceLibvirt {
			t.Fatalf("Source = %q, want %q", evt.Source, types.EventSourceLibvirt)
		}
		if evt.VMID != "vm-2" {
			t.Fatalf("VMID = %q, want vm-2", evt.VMID)
		}
		if evt.Attributes["state"] != string(types.VMStateRunning) {
			t.Fatalf("state attr = %q, want %q", evt.Attributes["state"], types.VMStateRunning)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle event")
	}
}
