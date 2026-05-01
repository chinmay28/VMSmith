package vm

import (
	"context"
	"time"

	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// VMStatePersister consumes libvirt-source `vm.*` events from the bus and
// applies the resulting state to the bbolt store.  It exists so that the
// libvirt event-loop goroutine never blocks on a store write — every
// state mutation flows through the same fan-out as SSE/CLI subscribers.
type VMStatePersister struct {
	bus   *events.EventBus
	store *store.Store

	subName string
}

// NewVMStatePersister creates a persister wired to the given bus + store.
// Call Run from a goroutine; it returns when the supplied context is cancelled.
func NewVMStatePersister(bus *events.EventBus, s *store.Store) *VMStatePersister {
	return &VMStatePersister{
		bus:     bus,
		store:   s,
		subName: "vm-state-persister",
	}
}

// Run subscribes to the bus and applies VM state changes carried in libvirt
// events.  Returns when ctx is Done, the bus is stopped, or the subscription
// channel is closed.
func (p *VMStatePersister) Run(ctx context.Context) {
	if p.bus == nil || p.store == nil {
		return
	}
	ch, cancel := p.bus.Subscribe(p.subName)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			p.apply(evt)
		}
	}
}

// apply persists the new state for one libvirt-source event.  Non-libvirt
// events and events without a vm_id are ignored — the persister is the
// single writer for libvirt-driven state transitions.
func (p *VMStatePersister) apply(evt *types.Event) {
	if evt == nil || evt.Source != types.EventSourceLibvirt || evt.VMID == "" {
		return
	}
	stateStr := evt.Attributes["state"]
	if stateStr == "" {
		return
	}

	vmRecord, err := p.store.GetVM(evt.VMID)
	if err != nil || vmRecord == nil {
		// VM may have been deleted between the libvirt callback firing and
		// this consumer running.  Nothing to do.
		return
	}

	newState := types.VMState(stateStr)
	if vmRecord.State == newState {
		return
	}
	vmRecord.State = newState
	vmRecord.UpdatedAt = time.Now()
	if err := p.store.PutVM(vmRecord); err != nil {
		logger.Warn("daemon", "failed to persist lifecycle event state",
			"vm", vmRecord.Name, "vm_id", evt.VMID, "state", stateStr, "error", err.Error())
	}
}
