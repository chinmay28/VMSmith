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

func TestLifecycleEventToTypeAndSeverity(t *testing.T) {
	tests := []struct {
		name         string
		event        *libvirt.DomainEventLifecycle
		wantType     string
		wantSeverity string
	}{
		{
			name:         "started → vm.started/info",
			event:        &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_STARTED},
			wantType:     "vm.started",
			wantSeverity: types.EventSeverityInfo,
		},
		{
			name:         "stopped → vm.stopped/info",
			event:        &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_STOPPED},
			wantType:     "vm.stopped",
			wantSeverity: types.EventSeverityInfo,
		},
		{
			name:         "shutdown → vm.shutdown/info",
			event:        &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_SHUTDOWN},
			wantType:     "vm.shutdown",
			wantSeverity: types.EventSeverityInfo,
		},
		{
			name:         "crashed → vm.crashed/error",
			event:        &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_CRASHED},
			wantType:     "vm.crashed",
			wantSeverity: types.EventSeverityError,
		},
		{
			name:         "suspended → vm.suspended/info",
			event:        &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_SUSPENDED},
			wantType:     "vm.suspended",
			wantSeverity: types.EventSeverityInfo,
		},
		{
			name:         "resumed → vm.resumed/info",
			event:        &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_RESUMED},
			wantType:     "vm.resumed",
			wantSeverity: types.EventSeverityInfo,
		},
		{
			name:         "defined → vm.defined/info",
			event:        &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_DEFINED},
			wantType:     "vm.defined",
			wantSeverity: types.EventSeverityInfo,
		},
		{
			name:         "undefined → vm.undefined/info",
			event:        &libvirt.DomainEventLifecycle{Event: libvirt.DOMAIN_EVENT_UNDEFINED},
			wantType:     "vm.undefined",
			wantSeverity: types.EventSeverityInfo,
		},
		{
			name:         "nil event has fallback type",
			event:        nil,
			wantType:     "vm.lifecycle_unknown",
			wantSeverity: types.EventSeverityInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotSeverity := lifecycleEventToTypeAndSeverity(tt.event)
			if gotType != tt.wantType {
				t.Errorf("type = %q, want %q", gotType, tt.wantType)
			}
			if gotSeverity != tt.wantSeverity {
				t.Errorf("severity = %q, want %q", gotSeverity, tt.wantSeverity)
			}
		})
	}
}
