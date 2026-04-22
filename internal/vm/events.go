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

	vmRecord.State = lifecycleEventToVMState(event)
	vmRecord.UpdatedAt = time.Now()
	if err := m.store.PutVM(vmRecord); err != nil {
		logger.Warn("daemon", "failed to persist lifecycle event state", "vm", name, "event", event.String(), "error", err.Error())
		return
	}

	logger.Info("daemon", "vm lifecycle event received", "vm", name, "event", event.String(), "state", string(vmRecord.State))
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
