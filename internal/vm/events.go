package vm

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

var libvirtEventLoopOnce sync.Once

func ensureLibvirtEventLoop() error {
	var err error
	libvirtEventLoopOnce.Do(func() {
		err = libvirt.EventRegisterDefaultImpl()
	})
	return err
}

func (m *LibvirtManager) startLifecycleMonitor() error {
	if err := ensureLibvirtEventLoop(); err != nil {
		return fmt.Errorf("registering libvirt event loop: %w", err)
	}

	callbackID, err := m.conn.DomainEventLifecycleRegister(nil, func(c *libvirt.Connect, d *libvirt.Domain, event *libvirt.DomainEventLifecycle) {
		m.handleLifecycleEvent(d, event)
	})
	if err != nil {
		return fmt.Errorf("registering libvirt lifecycle callback: %w", err)
	}

	m.lifecycleCallbackID = callbackID
	m.lifecycleRegistered = true
	m.lifecycleStopCh = make(chan struct{})
	go m.runLifecycleEventLoop()
	return nil
}

func (m *LibvirtManager) runLifecycleEventLoop() {
	for {
		select {
		case <-m.lifecycleStopCh:
			return
		default:
		}
		if err := libvirt.EventRunDefaultImpl(); err != nil {
			select {
			case <-m.lifecycleStopCh:
				return
			default:
			}
			logger.Warn("daemon", "libvirt event loop iteration failed", "error", err.Error())
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func (m *LibvirtManager) stopLifecycleMonitor() {
	if m.lifecycleRegistered {
		if err := m.conn.DomainEventDeregister(m.lifecycleCallbackID); err != nil {
			logger.Warn("daemon", "failed to deregister libvirt lifecycle callback", "error", err.Error())
		}
		m.lifecycleCallbackID = 0
		m.lifecycleRegistered = false
	}
	if m.lifecycleStopCh != nil {
		close(m.lifecycleStopCh)
		m.lifecycleStopCh = nil
	}
}

// handleLifecycleEvent translates a libvirt domain lifecycle callback into a
// typed Event and publishes it to the bus.  State persistence is intentionally
// kept out of this goroutine — a separate consumer (StartVMStatePersister)
// subscribes to the libvirt-source stream and applies the state mutation to
// bbolt.  This decouples the libvirt event loop from the store transaction so
// a slow or contended bbolt write can never stall libvirt callbacks.
func (m *LibvirtManager) handleLifecycleEvent(dom *libvirt.Domain, event *libvirt.DomainEventLifecycle) {
	if dom == nil || event == nil {
		return
	}
	name, err := dom.GetName()
	if err != nil {
		logger.Warn("daemon", "received libvirt lifecycle event for unnamed domain", "error", err.Error())
		return
	}

	vmRecord, err := m.findStoredVMByName(name)
	if err != nil {
		logger.Warn("daemon", "failed to match libvirt lifecycle event to stored VM", "vm", name, "error", err.Error())
		return
	}
	if vmRecord == nil {
		logger.Debug("daemon", "ignoring lifecycle event for unmanaged domain", "vm", name, "event", event.String())
		return
	}

	state := lifecycleEventToVMState(event)
	evtType, severity := lifecycleEventToTypeAndSeverity(event)

	logger.Info("daemon", "vm lifecycle event received",
		"vm", name, "event", event.String(), "type", evtType, "state", string(state))

	if m.eventBus == nil {
		// Bus not wired (test harness or early-startup path): fall back to
		// directly persisting the state change so the daemon never silently
		// loses a libvirt-driven state transition.  This preserves the
		// pre-refactor behavior for callers that haven't migrated yet.
		vmRecord.State = state
		vmRecord.UpdatedAt = time.Now()
		if err := m.store.PutVM(vmRecord); err != nil {
			logger.Warn("daemon", "failed to persist lifecycle event state (no bus)",
				"vm", name, "event", event.String(), "error", err.Error())
		}
		return
	}

	m.eventBus.Publish(&types.Event{
		Type:     evtType,
		Source:   types.EventSourceLibvirt,
		VMID:     vmRecord.ID,
		Severity: severity,
		Message:  fmt.Sprintf("VM %s state changed to %s", name, string(state)),
		Attributes: map[string]string{
			"vm_name":         name,
			"state":           string(state),
			"libvirt_event":   event.String(),
			"libvirt_event_n": fmt.Sprintf("%d", event.Event),
		},
		OccurredAt: time.Now(),
	})
}

func (m *LibvirtManager) findStoredVMByName(name string) (*types.VM, error) {
	vms, err := m.store.ListVMs()
	if err != nil {
		return nil, err
	}
	for _, vm := range vms {
		if vm != nil && strings.EqualFold(vm.Name, name) {
			return vm, nil
		}
	}
	return nil, nil
}

func lifecycleEventToVMState(event *libvirt.DomainEventLifecycle) types.VMState {
	if event == nil {
		return types.VMStateUnknown
	}

	switch event.Event {
	case libvirt.DOMAIN_EVENT_STARTED, libvirt.DOMAIN_EVENT_RESUMED:
		return types.VMStateRunning
	case libvirt.DOMAIN_EVENT_STOPPED, libvirt.DOMAIN_EVENT_SHUTDOWN, libvirt.DOMAIN_EVENT_SUSPENDED, libvirt.DOMAIN_EVENT_PMSUSPENDED:
		return types.VMStateStopped
	case libvirt.DOMAIN_EVENT_CRASHED:
		return types.VMStateUnknown
	case libvirt.DOMAIN_EVENT_UNDEFINED:
		return types.VMStateDeleted
	default:
		return types.VMStateUnknown
	}
}

// lifecycleEventToTypeAndSeverity maps a libvirt lifecycle code to a stable
// dotted event-type string and severity.  Crashes are reported at "error"
// severity so dashboards / alerting can distinguish them from clean stops.
func lifecycleEventToTypeAndSeverity(event *libvirt.DomainEventLifecycle) (string, string) {
	if event == nil {
		return "vm.lifecycle_unknown", types.EventSeverityInfo
	}
	switch event.Event {
	case libvirt.DOMAIN_EVENT_DEFINED:
		return "vm.defined", types.EventSeverityInfo
	case libvirt.DOMAIN_EVENT_UNDEFINED:
		return "vm.undefined", types.EventSeverityInfo
	case libvirt.DOMAIN_EVENT_STARTED:
		return "vm.started", types.EventSeverityInfo
	case libvirt.DOMAIN_EVENT_SUSPENDED, libvirt.DOMAIN_EVENT_PMSUSPENDED:
		return "vm.suspended", types.EventSeverityInfo
	case libvirt.DOMAIN_EVENT_RESUMED:
		return "vm.resumed", types.EventSeverityInfo
	case libvirt.DOMAIN_EVENT_STOPPED:
		return "vm.stopped", types.EventSeverityInfo
	case libvirt.DOMAIN_EVENT_SHUTDOWN:
		return "vm.shutdown", types.EventSeverityInfo
	case libvirt.DOMAIN_EVENT_CRASHED:
		return "vm.crashed", types.EventSeverityError
	default:
		return fmt.Sprintf("vm.lifecycle_%d", event.Event), types.EventSeverityInfo
	}
}

