package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// --- test doubles -----------------------------------------------------------

type fakeStore struct {
	mu       sync.Mutex
	scheds   map[string]*types.Schedule
	runs     map[string][]*types.ScheduleRun
	lastTick time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		scheds: map[string]*types.Schedule{},
		runs:   map[string][]*types.ScheduleRun{},
	}
}

func (f *fakeStore) ListSchedules() ([]*types.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*types.Schedule, 0, len(f.scheds))
	for _, s := range f.scheds {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeStore) GetSchedule(id string) (*types.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.scheds[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return s, nil
}

func (f *fakeStore) PutSchedule(s *types.Schedule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scheds[s.ID] = s
	return nil
}

func (f *fakeStore) AppendRun(scheduleID string, run *types.ScheduleRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[scheduleID] = append(f.runs[scheduleID], run)
	return nil
}

func (f *fakeStore) GetLastTick() (time.Time, error) { return f.lastTick, nil }
func (f *fakeStore) SetLastTick(t time.Time) error   { f.lastTick = t; return nil }

func (f *fakeStore) runsFor(id string) []*types.ScheduleRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*types.ScheduleRun(nil), f.runs[id]...)
}

type fakeSink struct {
	mu     sync.Mutex
	events []*types.Event
}

func (s *fakeSink) Publish(e *types.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *fakeSink) typeCount(t string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.Type == t {
			n++
		}
	}
	return n
}

// --- helpers ----------------------------------------------------------------

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func testEngine(store Store, mgr vm.Manager, sink EventSink) *Engine {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return New(store, mgr, sink, Config{
		now:          fixedClock(now),
		sleep:        func(time.Duration) {},
		MaxRetries:   2,
		RetryBackoff: []time.Duration{0, 0},
	})
}

func seedVM(mgr *vm.MockManager, id string, state types.VMState, tags ...string) {
	mgr.SeedVM(&types.VM{
		ID:    id,
		Name:  id,
		State: state,
		Tags:  tags,
		Spec:  types.VMSpec{Name: id, Tags: tags},
	})
}

const dailySpec = "0 0 2 * * *"

// --- tests ------------------------------------------------------------------

func TestValidateSpec(t *testing.T) {
	e := testEngine(newFakeStore(), vm.NewMockManager(), &fakeSink{})

	if err := e.ValidateSpec(dailySpec, "UTC"); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	if err := e.ValidateSpec("0 0 2 * *", ""); err == nil {
		t.Fatal("5-field spec should be rejected (seconds required)")
	}
	if err := e.ValidateSpec("not a cron", ""); err == nil {
		t.Fatal("garbage spec should be rejected")
	}
	if err := e.ValidateSpec(dailySpec, "Nowhere/Notreal"); err == nil {
		t.Fatal("invalid timezone should be rejected")
	}
	var apiErr *types.APIError
	if err := e.ValidateSpec(dailySpec, "Nowhere/Notreal"); !errors.As(err, &apiErr) || apiErr.Code != "invalid_timezone" {
		t.Fatalf("expected invalid_timezone APIError, got %v", err)
	}
}

func TestNextFireTime(t *testing.T) {
	e := testEngine(newFakeStore(), vm.NewMockManager(), &fakeSink{})
	// now is 2026-01-02 03:04:05 UTC; next 02:00 daily fire is the 3rd at 02:00.
	next, err := e.NextFireTime(dailySpec, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 1, 3, 2, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next fire = %v, want %v", next, want)
	}
}

func TestFire_StartAction_StartsStoppedVM(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	sink := &fakeSink{}
	store.PutSchedule(&types.Schedule{ID: "sched-1", Name: "starter", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true})

	e := testEngine(store, mgr, sink)
	e.fire(context.Background(), "sched-1", e.now(), "scheduler")

	v, _ := mgr.Get(context.Background(), "vm-1")
	if v.State != types.VMStateRunning {
		t.Fatalf("vm should be running, got %s", v.State)
	}
	runs := store.runsFor("sched-1")
	if len(runs) != 1 || runs[0].Status != types.ScheduleRunStatusSuccess {
		t.Fatalf("expected 1 success run, got %+v", runs)
	}
	if sink.typeCount("schedule.fired") != 1 || sink.typeCount("schedule.fire_succeeded") != 1 {
		t.Fatalf("missing fire events: %+v", sink.events)
	}
}

func TestFire_StartAction_SkipsRunningVM(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateRunning)
	store.PutSchedule(&types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true})

	e := testEngine(store, mgr, &fakeSink{})
	e.fire(context.Background(), "s", e.now(), "scheduler")

	runs := store.runsFor("s")
	if len(runs) != 1 || runs[0].Status != types.ScheduleRunStatusSkipped || runs[0].SkipReason != types.ScheduleRunSkipReasonVMAlreadyRunning {
		t.Fatalf("expected vm_already_running skip, got %+v", runs[0])
	}
}

func TestFire_StopAction_SkipsStoppedVM(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	store.PutSchedule(&types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStop, VMID: "vm-1", CronSpec: dailySpec, Enabled: true})

	e := testEngine(store, mgr, &fakeSink{})
	e.fire(context.Background(), "s", e.now(), "scheduler")

	runs := store.runsFor("s")
	if len(runs) != 1 || runs[0].SkipReason != types.ScheduleRunSkipReasonVMAlreadyStopped {
		t.Fatalf("expected vm_already_stopped skip, got %+v", runs[0])
	}
}

func TestFire_VMNotFound_Skips(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	store.PutSchedule(&types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "ghost", CronSpec: dailySpec, Enabled: true})

	e := testEngine(store, mgr, &fakeSink{})
	e.fire(context.Background(), "s", e.now(), "scheduler")

	runs := store.runsFor("s")
	if len(runs) != 1 || runs[0].SkipReason != types.ScheduleRunSkipReasonVMNotFound {
		t.Fatalf("expected vm_not_found skip, got %+v", runs)
	}
	sched, _ := store.GetSchedule("s")
	if sched.LastResult != "error: vm_not_found" {
		t.Fatalf("unexpected last result %q", sched.LastResult)
	}
}

func TestFire_SnapshotAction_CreatesAndTrims(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateRunning)
	sched := &types.Schedule{ID: "s", Name: "backup", Action: types.ScheduleActionSnapshot, VMID: "vm-1", CronSpec: dailySpec, Enabled: true, RetentionCount: 2}
	store.PutSchedule(sched)

	// Pre-seed three auto-named snapshots so the new one trips retention=2.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, suffix := range []string{"20260101T000000Z", "20260101T010000Z", "20260101T020000Z"} {
		mgr.SeedSnapshot(&types.Snapshot{VMID: "vm-1", Name: "auto-backup-" + suffix, CreatedAt: base.Add(time.Duration(i) * time.Hour)})
	}

	e := testEngine(store, mgr, &fakeSink{})
	e.fire(context.Background(), "s", e.now(), "scheduler")

	snaps, _ := mgr.ListSnapshots(context.Background(), "vm-1")
	if len(snaps) != 2 {
		t.Fatalf("expected retention to leave 2 snapshots, got %d", len(snaps))
	}
	for _, s := range snaps {
		if s.Name == "auto-backup-20260101T000000Z" || s.Name == "auto-backup-20260101T010000Z" {
			t.Fatalf("oldest snapshot %s should have been trimmed", s.Name)
		}
	}
}

func TestFire_TagFanOut(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateRunning, "prod")
	seedVM(mgr, "vm-2", types.VMStateRunning, "prod", "web")
	seedVM(mgr, "vm-3", types.VMStateRunning, "dev")
	store.PutSchedule(&types.Schedule{ID: "s", Name: "fleet", Action: types.ScheduleActionSnapshot, TagSelector: []string{"prod"}, CronSpec: dailySpec, Enabled: true})

	e := testEngine(store, mgr, &fakeSink{})
	e.fire(context.Background(), "s", e.now(), "scheduler")

	runs := store.runsFor("s")
	if len(runs) != 2 {
		t.Fatalf("expected 2 fan-out runs, got %d: %+v", len(runs), runs)
	}
	got := map[string]bool{}
	for _, r := range runs {
		got[r.VMID] = true
	}
	if !got["vm-1"] || !got["vm-2"] || got["vm-3"] {
		t.Fatalf("fan-out targeted wrong VMs: %+v", got)
	}
}

func TestFire_NoSelector_TargetsAllVMs(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	seedVM(mgr, "vm-2", types.VMStateStopped)
	store.PutSchedule(&types.Schedule{ID: "s", Name: "all", Action: types.ScheduleActionStart, CronSpec: dailySpec, Enabled: true})

	e := testEngine(store, mgr, &fakeSink{})
	e.fire(context.Background(), "s", e.now(), "scheduler")

	if len(store.runsFor("s")) != 2 {
		t.Fatalf("expected runs for all 2 VMs")
	}
}

func TestFire_ConcurrentRun_Skips(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	store.PutSchedule(&types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true})

	e := testEngine(store, mgr, &fakeSink{})
	// Hold the only concurrency slot so the fire must skip.
	sem := e.semaphore("s")
	sem <- struct{}{}

	e.fire(context.Background(), "s", e.now(), "scheduler")

	runs := store.runsFor("s")
	if len(runs) != 1 || runs[0].SkipReason != types.ScheduleRunSkipReasonConcurrentRun {
		t.Fatalf("expected concurrent_run skip, got %+v", runs)
	}
}

func TestEnqueue_QueueFull_RecordsSkip(t *testing.T) {
	store := newFakeStore()
	e := testEngine(store, vm.NewMockManager(), &fakeSink{})
	// Simulate a saturated queue with no draining worker.
	e.jobs = make(chan job, 1)
	e.jobs <- job{scheduleID: "s"}

	e.enqueue(job{scheduleID: "s", scheduledTime: e.now()})

	runs := store.runsFor("s")
	if len(runs) != 1 || runs[0].SkipReason != types.ScheduleRunSkipReasonQueueFull {
		t.Fatalf("expected queue_full skip, got %+v", runs)
	}
}

// TestEnqueue_ConcurrentWithStop_NoPanic exercises the shutdown race where a
// cron goroutine is mid-enqueue while Stop() closes the jobs channel. The send
// happens under e.mu (which Stop() also holds before closing), so enqueue
// either wins before the close or observes a nil channel — never "send on
// closed channel". Before the fix this test panics and crashes the process.
func TestEnqueue_ConcurrentWithStop_NoPanic(t *testing.T) {
	store := newFakeStore()
	e := testEngine(store, vm.NewMockManager(), &fakeSink{})
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	var producers sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					e.enqueue(job{scheduleID: "s", scheduledTime: e.now(), actor: "scheduler"})
				}
			}
		}()
	}

	// Let producers saturate the queue, then stop concurrently with enqueues.
	time.Sleep(5 * time.Millisecond)
	e.Stop()
	close(stop)
	producers.Wait()
}

func TestRunOne_RetryThenSuccess(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	sched := &types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true}
	store.PutSchedule(sched)

	e := testEngine(store, mgr, &fakeSink{})
	calls := 0
	e.actions[types.ScheduleActionStart] = func(_ context.Context, _ vm.Manager, _ *types.Schedule, _ string, _ time.Time) error {
		calls++
		if calls < 2 {
			return errors.New("transient")
		}
		return nil
	}

	status := e.runOne(context.Background(), sched, "vm-1", e.now(), "scheduler")
	if status != types.ScheduleRunStatusSuccess {
		t.Fatalf("expected success after retry, got %s", status)
	}
	if calls != 2 {
		t.Fatalf("expected 2 attempts, got %d", calls)
	}
}

func TestRunOne_RetryExhausted_Error(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	sched := &types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true}
	store.PutSchedule(sched)

	e := testEngine(store, mgr, &fakeSink{})
	calls := 0
	e.actions[types.ScheduleActionStart] = func(_ context.Context, _ vm.Manager, _ *types.Schedule, _ string, _ time.Time) error {
		calls++
		return errors.New("always fails")
	}

	status := e.runOne(context.Background(), sched, "vm-1", e.now(), "scheduler")
	if status != types.ScheduleRunStatusError {
		t.Fatalf("expected error status, got %s", status)
	}
	if calls != 3 { // MaxRetries=2 -> 3 attempts
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
	runs := store.runsFor("s")
	if len(runs) != 1 || runs[0].Error == "" {
		t.Fatalf("expected error run with detail, got %+v", runs)
	}
}

func TestRegisterUnregister(t *testing.T) {
	store := newFakeStore()
	e := testEngine(store, vm.NewMockManager(), &fakeSink{})
	sched := &types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true, Timezone: "UTC"}

	e.Register(sched)
	e.mu.Lock()
	_, hasEntry := e.entries["s"]
	_, hasSem := e.sems["s"]
	e.mu.Unlock()
	if !hasEntry || !hasSem {
		t.Fatal("register should create cron entry and semaphore")
	}

	e.Unregister("s")
	e.mu.Lock()
	_, hasEntry = e.entries["s"]
	_, hasSem = e.sems["s"]
	e.mu.Unlock()
	if hasEntry || hasSem {
		t.Fatal("unregister should drop entry and semaphore")
	}
}

func TestRegister_DisabledScheduleNotScheduled(t *testing.T) {
	store := newFakeStore()
	e := testEngine(store, vm.NewMockManager(), &fakeSink{})
	e.Register(&types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, CronSpec: dailySpec, Enabled: false})

	e.mu.Lock()
	_, hasEntry := e.entries["s"]
	e.mu.Unlock()
	if hasEntry {
		t.Fatal("disabled schedule should not be scheduled")
	}
}

func TestRunNow_UnknownSchedule(t *testing.T) {
	e := testEngine(newFakeStore(), vm.NewMockManager(), &fakeSink{})
	if err := e.RunNow(context.Background(), "missing", "api"); err == nil {
		t.Fatal("RunNow on unknown schedule should error")
	}
}

func TestRunCatchUp_RunOnce(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	sched := &types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true, Timezone: "UTC", CatchUpPolicy: types.ScheduleCatchUpRunOnce}
	store.PutSchedule(sched)
	// last_tick three days before "now" (2026-01-02 03:04:05) -> several missed 02:00 fires.
	store.lastTick = time.Date(2025, 12, 30, 0, 0, 0, 0, time.UTC)

	sink := &fakeSink{}
	e := testEngine(store, mgr, sink)
	e.runCatchUp(context.Background(), []*types.Schedule{sched}, store.lastTick)

	if n := len(store.runsFor("s")); n != 1 {
		t.Fatalf("run_once should produce exactly 1 run, got %d", n)
	}
	if sink.typeCount("schedule.catch_up_replayed") != 1 {
		t.Fatal("expected catch_up_replayed event")
	}
}

func TestRunCatchUp_Skip(t *testing.T) {
	store := newFakeStore()
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	sched := &types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true, Timezone: "UTC", CatchUpPolicy: types.ScheduleCatchUpSkip}
	store.PutSchedule(sched)
	store.lastTick = time.Date(2025, 12, 30, 0, 0, 0, 0, time.UTC)

	e := testEngine(store, mgr, &fakeSink{})
	e.runCatchUp(context.Background(), []*types.Schedule{sched}, store.lastTick)

	if n := len(store.runsFor("s")); n != 0 {
		t.Fatalf("skip policy should produce 0 runs, got %d", n)
	}
}

func TestRunCatchUp_FreshInstallNoCatchUp(t *testing.T) {
	store := newFakeStore() // lastTick is zero
	mgr := vm.NewMockManager()
	seedVM(mgr, "vm-1", types.VMStateStopped)
	sched := &types.Schedule{ID: "s", Name: "x", Action: types.ScheduleActionStart, VMID: "vm-1", CronSpec: dailySpec, Enabled: true, Timezone: "UTC", CatchUpPolicy: types.ScheduleCatchUpRunAll}
	store.PutSchedule(sched)

	e := testEngine(store, mgr, &fakeSink{})
	e.runCatchUp(context.Background(), []*types.Schedule{sched}, store.lastTick)

	if n := len(store.runsFor("s")); n != 0 {
		t.Fatalf("fresh install should not catch up, got %d runs", n)
	}
}
