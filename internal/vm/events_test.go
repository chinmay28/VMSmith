package vm

import (
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

func TestLifecycleEventToVMState(t *testing.T) {
	tests := []struct {
		name  string
		event *libvirt.DomainEventLifecycle
		want  types.VMState
	}{
		{
			name:  "started becomes running",
			event: &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_STARTED},
			want:  types.VMStateRunning,
		},
		{
			name:  "resumed becomes running",
			event: &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_RESUMED},
			want:  types.VMStateRunning,
		},
		{
			name:  "stopped becomes stopped",
			event: &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_STOPPED},
			want:  types.VMStateStopped,
		},
		{
			name:  "shutdown becomes stopped",
			event: &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_SHUTDOWN},
			want:  types.VMStateStopped,
		},
		{
			name:  "crashed becomes unknown",
			event: &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_CRASHED},
			want:  types.VMStateUnknown,
		},
		{
			name:  "undefined becomes deleted",
			event: &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_UNDEFINED},
			want:  types.VMStateDeleted,
		},
		{
			name:  "nil event becomes unknown",
			event: nil,
			want:  types.VMStateUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lifecycleEventToVMState(tt.event); got != tt.want {
				t.Fatalf("lifecycleEventToVMState() = %q, want %q", got, tt.want)
			}
		})
	}
}
