package vm

import (
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/pkg/types"
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
