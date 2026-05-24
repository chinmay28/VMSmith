package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// skipError signals that an action declined to run for an expected,
// non-error reason (e.g. the VM is already in the target state). Skips are
// never retried and are recorded as ScheduleRunStatusSkipped with the carried
// reason.
type skipError struct {
	reason types.ScheduleRunSkipReason
}

func (e *skipError) Error() string { return string(e.reason) }

func skip(reason types.ScheduleRunSkipReason) error { return &skipError{reason: reason} }

// actionFunc executes a single schedule action against one resolved VM.
// Returning a *skipError marks the run skipped (no retry); any other error is
// retried per the engine's retry policy and ultimately recorded as an error.
type actionFunc func(ctx context.Context, mgr vm.Manager, sched *types.Schedule, vmID string, scheduledTime time.Time) error

// defaultActions returns the v1 action registry.
func defaultActions() map[types.ScheduleAction]actionFunc {
	return map[types.ScheduleAction]actionFunc{
		types.ScheduleActionSnapshot: snapshotAction,
		types.ScheduleActionStart:    startAction,
		types.ScheduleActionStop:     stopAction,
		types.ScheduleActionRestart:  restartAction,
	}
}

func startAction(ctx context.Context, mgr vm.Manager, _ *types.Schedule, vmID string, _ time.Time) error {
	v, err := mgr.Get(ctx, vmID)
	if err != nil {
		return skip(types.ScheduleRunSkipReasonVMNotFound)
	}
	if v.State == types.VMStateRunning || v.State == types.VMStatePaused {
		return skip(types.ScheduleRunSkipReasonVMAlreadyRunning)
	}
	return mgr.Start(ctx, vmID)
}

func stopAction(ctx context.Context, mgr vm.Manager, _ *types.Schedule, vmID string, _ time.Time) error {
	v, err := mgr.Get(ctx, vmID)
	if err != nil {
		return skip(types.ScheduleRunSkipReasonVMNotFound)
	}
	if v.State == types.VMStateStopped {
		return skip(types.ScheduleRunSkipReasonVMAlreadyStopped)
	}
	return mgr.Stop(ctx, vmID)
}

func restartAction(ctx context.Context, mgr vm.Manager, _ *types.Schedule, vmID string, _ time.Time) error {
	if _, err := mgr.Get(ctx, vmID); err != nil {
		return skip(types.ScheduleRunSkipReasonVMNotFound)
	}
	return mgr.Restart(ctx, vmID)
}

// snapshotPrefix is the auto-generated snapshot name prefix scoped to a
// schedule. RetentionCount trimming only ever touches names with this prefix
// so operator-created snapshots are never deleted.
func snapshotPrefix(sched *types.Schedule) string {
	return fmt.Sprintf("auto-%s-", sanitizeName(sched.Name))
}

func snapshotAction(ctx context.Context, mgr vm.Manager, sched *types.Schedule, vmID string, scheduledTime time.Time) error {
	if _, err := mgr.Get(ctx, vmID); err != nil {
		return skip(types.ScheduleRunSkipReasonVMNotFound)
	}
	name := fmt.Sprintf("%s%s", snapshotPrefix(sched), scheduledTime.UTC().Format("20060102T150405Z"))
	if _, err := mgr.CreateSnapshot(ctx, vmID, types.SnapshotSpec{
		Name:        name,
		Description: fmt.Sprintf("auto snapshot from schedule %q", sched.Name),
		Tags:        []string{"auto", "schedule"},
	}); err != nil {
		return err
	}
	if sched.RetentionCount > 0 {
		if err := trimSnapshots(ctx, mgr, sched, vmID); err != nil {
			// Retention failure is non-fatal: the snapshot succeeded, so the
			// run is a success; surface the trim failure as an error only when
			// the create itself failed.
			return nil
		}
	}
	return nil
}

// trimSnapshots deletes the oldest auto-named snapshots for this schedule on
// the given VM until at most RetentionCount remain.
func trimSnapshots(ctx context.Context, mgr vm.Manager, sched *types.Schedule, vmID string) error {
	snaps, err := mgr.ListSnapshots(ctx, vmID)
	if err != nil {
		return err
	}
	prefix := snapshotPrefix(sched)
	owned := make([]*types.Snapshot, 0, len(snaps))
	for _, s := range snaps {
		if strings.HasPrefix(s.Name, prefix) {
			owned = append(owned, s)
		}
	}
	if len(owned) <= sched.RetentionCount {
		return nil
	}
	// Oldest first: sort by CreatedAt, then name (the timestamped suffix makes
	// name ordering chronological even when CreatedAt is unavailable).
	sort.Slice(owned, func(i, j int) bool {
		if owned[i].CreatedAt.Equal(owned[j].CreatedAt) {
			return owned[i].Name < owned[j].Name
		}
		return owned[i].CreatedAt.Before(owned[j].CreatedAt)
	})
	excess := len(owned) - sched.RetentionCount
	for i := 0; i < excess; i++ {
		if err := mgr.DeleteSnapshot(ctx, vmID, owned[i].Name); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeName lowercases a schedule name and replaces any character outside
// [a-z0-9._-] with a hyphen so the resulting snapshot name is libvirt-safe.
func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		return "schedule"
	}
	return out
}
