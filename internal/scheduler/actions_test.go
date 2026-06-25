package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestLifecycleScheduleActions(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		seedState  types.VMState
		action     func(context.Context, vm.Manager, *types.Schedule, string, time.Time) error
		wantState  types.VMState
		wantSkip   types.ScheduleRunSkipReason
	}{
		{name: "force-stop running vm", seedState: types.VMStateRunning, action: forceStopAction, wantState: types.VMStateStopped},
		{name: "force-stop stopped vm skips", seedState: types.VMStateStopped, action: forceStopAction, wantSkip: types.ScheduleRunSkipReasonVMAlreadyStopped},
		{name: "reboot running vm", seedState: types.VMStateRunning, action: rebootAction, wantState: types.VMStateRunning},
		{name: "reboot stopped vm skips", seedState: types.VMStateStopped, action: rebootAction, wantSkip: types.ScheduleRunSkipReasonVMAlreadyStopped},
		{name: "suspend running vm", seedState: types.VMStateRunning, action: suspendAction, wantState: types.VMStatePaused},
		{name: "suspend paused vm skips", seedState: types.VMStatePaused, action: suspendAction, wantSkip: types.ScheduleRunSkipReasonVMAlreadyStopped},
		{name: "resume paused vm", seedState: types.VMStatePaused, action: resumeAction, wantState: types.VMStateRunning},
		{name: "resume running vm skips", seedState: types.VMStateRunning, action: resumeAction, wantSkip: types.ScheduleRunSkipReasonVMAlreadyRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := vm.NewMockManager()
			vmObj, err := mgr.Create(ctx, types.VMSpec{Name: tt.name})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			switch tt.seedState {
			case types.VMStateStopped:
				if err := mgr.Stop(ctx, vmObj.ID); err != nil {
					t.Fatalf("Stop: %v", err)
				}
			case types.VMStatePaused:
				if err := mgr.Suspend(ctx, vmObj.ID); err != nil {
					t.Fatalf("Suspend: %v", err)
				}
			}
			err = tt.action(ctx, mgr, &types.Schedule{Name: "sched"}, vmObj.ID, time.Now())
			if tt.wantSkip != "" {
				se, ok := err.(*skipError)
				if !ok || se.reason != tt.wantSkip {
					t.Fatalf("err = %v, want skip %q", err, tt.wantSkip)
				}
				return
			}
			if err != nil {
				t.Fatalf("action error: %v", err)
			}
			got, err := mgr.Get(ctx, vmObj.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.State != tt.wantState {
				t.Fatalf("state = %q, want %q", got.State, tt.wantState)
			}
		})
	}
}
