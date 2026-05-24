// Package scheduler implements vmSmith's recurring VM-action scheduler. A
// single Engine drives one robfig/cron instance per distinct timezone, fans
// fired schedules onto a bounded worker pool, resolves tag-selector targets at
// fire time, and records every attempt as a ScheduleRun. See docs/SCHEDULES.md
// for the operator-facing contract.
package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// Store is the persistence surface the engine needs. *store.Store satisfies it.
type Store interface {
	ListSchedules() ([]*types.Schedule, error)
	GetSchedule(id string) (*types.Schedule, error)
	PutSchedule(*types.Schedule) error
	AppendRun(scheduleID string, run *types.ScheduleRun) error
	GetLastTick() (time.Time, error)
	SetLastTick(t time.Time) error
}

// EventSink receives lifecycle / fire events. *events.EventBus satisfies it.
// A nil sink disables event emission.
type EventSink interface {
	Publish(*types.Event)
}

// Config tunes the engine. Zero values fall back to sane defaults via
// withDefaults so callers can pass a partially-populated struct.
type Config struct {
	WorkerPoolSize int
	QueueSize      int
	MaxRetries     int
	ActionTimeout  time.Duration
	RetryBackoff   []time.Duration
	MaxCatchUp     int
	TickInterval   time.Duration

	// now and sleep are injectable for deterministic tests. Production leaves
	// them nil (time.Now / time.Sleep).
	now   func() time.Time
	sleep func(time.Duration)
}

func (c Config) withDefaults() Config {
	if c.WorkerPoolSize <= 0 {
		c.WorkerPoolSize = 4
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 64
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.ActionTimeout <= 0 {
		c.ActionTimeout = 5 * time.Minute
	}
	if c.RetryBackoff == nil {
		c.RetryBackoff = []time.Duration{30 * time.Second, 2 * time.Minute}
	}
	if c.MaxCatchUp <= 0 {
		c.MaxCatchUp = 100
	}
	if c.TickInterval <= 0 {
		c.TickInterval = 60 * time.Second
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.sleep == nil {
		c.sleep = time.Sleep
	}
	return c
}

// cronParser parses the canonical 6-field (seconds-required) cron form.
var cronParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type job struct {
	scheduleID    string
	scheduledTime time.Time
	actor         string
}

type entry struct {
	timezone string
	entryID  cron.EntryID
}

// Engine is the running scheduler. It is safe for concurrent use.
type Engine struct {
	store   Store
	vmMgr   vm.Manager
	sink    EventSink
	cfg     Config
	actions map[types.ScheduleAction]actionFunc

	mu      sync.Mutex
	crons   map[string]*cron.Cron    // timezone (location name) -> cron instance
	entries map[string]entry         // scheduleID -> cron entry
	sems    map[string]chan struct{} // scheduleID -> per-schedule concurrency semaphore

	jobs    chan job
	wg      sync.WaitGroup
	stopCh  chan struct{}
	started bool
}

// New constructs an Engine. The engine is inert until Start is called.
func New(store Store, vmMgr vm.Manager, sink EventSink, cfg Config) *Engine {
	return &Engine{
		store:   store,
		vmMgr:   vmMgr,
		sink:    sink,
		cfg:     cfg.withDefaults(),
		actions: defaultActions(),
		crons:   map[string]*cron.Cron{},
		entries: map[string]entry{},
		sems:    map[string]chan struct{}{},
		stopCh:  make(chan struct{}),
	}
}

func (e *Engine) now() time.Time { return e.cfg.now() }

// ValidateSpec verifies a cron spec and timezone, returning a typed API error
// on failure so the REST handler can surface a precise 400.
func (e *Engine) ValidateSpec(cronSpec, timezone string) error {
	if _, err := loadLocation(timezone); err != nil {
		return types.NewAPIError("invalid_timezone", fmt.Sprintf("invalid timezone %q", timezone))
	}
	if _, err := cronParser.Parse(strings.TrimSpace(cronSpec)); err != nil {
		return types.NewAPIError("invalid_cron_spec",
			"cron_spec must be a 6-field spec with seconds (e.g. \"0 30 2 * * *\"): "+err.Error())
	}
	return nil
}

// NextFireTime returns the next fire time for a spec/timezone after now.
func (e *Engine) NextFireTime(cronSpec, timezone string) (time.Time, error) {
	loc, err := loadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	sched, err := cronParser.Parse(strings.TrimSpace(cronSpec))
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(e.now().In(loc)), nil
}

func loadLocation(tz string) (*time.Location, error) {
	if strings.TrimSpace(tz) == "" {
		return time.Local, nil
	}
	return time.LoadLocation(tz)
}

// Register wires (or rewires) a schedule into the running cron set. It is
// idempotent: an existing registration for the same ID is removed first. A
// disabled schedule is de-registered and left unscheduled.
func (e *Engine) Register(sched *types.Schedule) {
	if sched == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unregisterLocked(sched.ID)

	// Always (re)size the per-schedule concurrency semaphore.
	maxConc := sched.MaxConcurrent
	if maxConc <= 0 {
		maxConc = 1
	}
	e.sems[sched.ID] = make(chan struct{}, maxConc)

	if !sched.Enabled {
		return
	}
	cronSched, err := cronParser.Parse(strings.TrimSpace(sched.CronSpec))
	if err != nil {
		logger.Warn("scheduler", "skipping schedule with invalid cron spec",
			"schedule", sched.ID, "error", err.Error())
		return
	}
	loc, err := loadLocation(sched.Timezone)
	if err != nil {
		logger.Warn("scheduler", "skipping schedule with invalid timezone",
			"schedule", sched.ID, "error", err.Error())
		return
	}
	tzKey := loc.String()
	c, ok := e.crons[tzKey]
	if !ok {
		c = cron.New(cron.WithSeconds(), cron.WithLocation(loc))
		e.crons[tzKey] = c
		if e.started {
			c.Start()
		}
	}
	id := sched.ID
	entryID := c.Schedule(cronSched, cron.FuncJob(func() {
		e.enqueue(job{scheduleID: id, scheduledTime: e.now(), actor: "scheduler"})
	}))
	e.entries[id] = entry{timezone: tzKey, entryID: entryID}
}

// Unregister removes a schedule from the cron set and drops its semaphore.
func (e *Engine) Unregister(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unregisterLocked(id)
	delete(e.sems, id)
}

func (e *Engine) unregisterLocked(id string) {
	if ent, ok := e.entries[id]; ok {
		if c, ok := e.crons[ent.timezone]; ok {
			c.Remove(ent.entryID)
		}
		delete(e.entries, id)
	}
}

// Start loads persisted schedules, runs catch-up, then begins ticking.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return nil
	}
	e.started = true
	e.jobs = make(chan job, e.cfg.QueueSize)
	e.mu.Unlock()

	scheds, err := e.store.ListSchedules()
	if err != nil {
		return fmt.Errorf("loading schedules: %w", err)
	}
	for _, s := range scheds {
		e.Register(s)
	}

	// Capture the catch-up cursor BEFORE advancing it, then start workers and
	// cron instances. Replayed fires dispatch through the worker pool.
	prevTick, _ := e.store.GetLastTick()

	for i := 0; i < e.cfg.WorkerPoolSize; i++ {
		e.wg.Add(1)
		go e.worker()
	}

	e.mu.Lock()
	for _, c := range e.crons {
		c.Start()
	}
	e.mu.Unlock()

	// Persist a fresh tick immediately so a crash before the first interval
	// still advances the catch-up cursor.
	_ = e.store.SetLastTick(e.now())
	e.wg.Add(1)
	go e.tickLoop(ctx)

	// Run catch-up in the background so a long replay never blocks daemon
	// startup. The daemon ctx cancels in-flight catch-up actions on shutdown.
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.runCatchUp(ctx, scheds, prevTick)
	}()

	logger.Info("scheduler", "scheduler started",
		"schedules", fmt.Sprintf("%d", len(scheds)),
		"workers", fmt.Sprintf("%d", e.cfg.WorkerPoolSize))
	return nil
}

// Stop halts cron instances and drains the worker pool.
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return
	}
	e.started = false
	for _, c := range e.crons {
		c.Stop()
	}
	close(e.stopCh)
	jobs := e.jobs
	e.jobs = nil
	e.mu.Unlock()

	if jobs != nil {
		close(jobs)
	}
	e.wg.Wait()
}

func (e *Engine) tickLoop(ctx context.Context) {
	defer e.wg.Done()
	t := time.NewTicker(e.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-t.C:
			if err := e.store.SetLastTick(e.now()); err != nil {
				logger.Warn("scheduler", "failed to persist last_tick", "error", err.Error())
			}
		}
	}
}

func (e *Engine) worker() {
	defer e.wg.Done()
	for j := range e.jobs {
		e.fire(context.Background(), j.scheduleID, j.scheduledTime, j.actor)
	}
}

// enqueue pushes a job onto the worker pool. A full queue records a queue_full
// skip instead of blocking the cron goroutine.
func (e *Engine) enqueue(j job) {
	e.mu.Lock()
	ch := e.jobs
	e.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- j:
	default:
		e.recordQueueFull(j)
	}
}

func (e *Engine) recordQueueFull(j job) {
	run := &types.ScheduleRun{
		ScheduleID: j.scheduleID,
		StartedAt:  e.now().UTC(),
		Status:     types.ScheduleRunStatusSkipped,
		SkipReason: types.ScheduleRunSkipReasonQueueFull,
	}
	finishRun(run, e.now())
	_ = e.store.AppendRun(j.scheduleID, run)
	e.emit(systemEvent("schedule.fire_skipped", types.EventSeverityWarn,
		fmt.Sprintf("schedule %s fire dropped: worker queue full", j.scheduleID),
		map[string]string{"schedule_id": j.scheduleID, "skip_reason": string(types.ScheduleRunSkipReasonQueueFull)}, e.now()))
}

// RunNow fires a schedule immediately (out of band of cron), attributing the
// runs to the given actor. It returns an error only when the schedule does not
// exist; per-VM outcomes are recorded as runs.
func (e *Engine) RunNow(ctx context.Context, id, actor string) error {
	if _, err := e.store.GetSchedule(id); err != nil {
		return err
	}
	if strings.TrimSpace(actor) == "" {
		actor = "api"
	}
	e.fire(ctx, id, e.now(), actor)
	return nil
}

// fire executes one schedule fire: resolve targets, enforce per-schedule
// concurrency, run the action against each target, and record the outcome.
func (e *Engine) fire(ctx context.Context, scheduleID string, scheduledTime time.Time, actor string) {
	sched, err := e.store.GetSchedule(scheduleID)
	if err != nil {
		// Schedule deleted between enqueue and dispatch — nothing to do.
		return
	}

	sem := e.semaphore(scheduleID)
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	default:
		run := &types.ScheduleRun{
			ScheduleID: scheduleID,
			StartedAt:  e.now().UTC(),
			Status:     types.ScheduleRunStatusSkipped,
			SkipReason: types.ScheduleRunSkipReasonConcurrentRun,
		}
		finishRun(run, e.now())
		_ = e.store.AppendRun(scheduleID, run)
		e.emit(appEvent("schedule.fire_skipped", "", actor,
			fmt.Sprintf("schedule %q skipped: previous run still in progress", sched.Name),
			map[string]string{"schedule_id": scheduleID, "skip_reason": string(types.ScheduleRunSkipReasonConcurrentRun)}, e.now()))
		return
	}

	e.emit(appEvent("schedule.fired", "", actor,
		fmt.Sprintf("schedule %q fired", sched.Name),
		map[string]string{"schedule_id": scheduleID, "action": string(sched.Action)}, e.now()))

	targets, missing := e.resolveTargets(ctx, sched)
	if missing {
		run := &types.ScheduleRun{
			ScheduleID: scheduleID,
			VMID:       sched.VMID,
			StartedAt:  e.now().UTC(),
			Status:     types.ScheduleRunStatusSkipped,
			SkipReason: types.ScheduleRunSkipReasonVMNotFound,
		}
		finishRun(run, e.now())
		_ = e.store.AppendRun(scheduleID, run)
		e.emit(appEvent("schedule.fire_skipped", sched.VMID, actor,
			fmt.Sprintf("schedule %q skipped: target VM not found", sched.Name),
			map[string]string{"schedule_id": scheduleID, "skip_reason": string(types.ScheduleRunSkipReasonVMNotFound)}, e.now()))
		e.finalize(sched, scheduledTime, "error: vm_not_found")
		return
	}

	var anyErr, anySuccess bool
	for _, vmID := range targets {
		status := e.runOne(ctx, sched, vmID, scheduledTime, actor)
		switch status {
		case types.ScheduleRunStatusError:
			anyErr = true
		case types.ScheduleRunStatusSuccess:
			anySuccess = true
		}
	}

	result := "success"
	switch {
	case anyErr && anySuccess:
		result = "partial"
	case anyErr:
		result = "error"
	case !anySuccess && len(targets) == 0:
		result = "no_targets"
	}
	e.finalize(sched, scheduledTime, result)
}

// runOne executes the action against a single VM with retry + timeout and
// records the resulting ScheduleRun. It returns the recorded status.
func (e *Engine) runOne(ctx context.Context, sched *types.Schedule, vmID string, scheduledTime time.Time, actor string) types.ScheduleRunStatus {
	run := &types.ScheduleRun{
		ScheduleID: sched.ID,
		VMID:       vmID,
		StartedAt:  e.now().UTC(),
		Status:     types.ScheduleRunStatusRunning,
	}

	action, ok := e.actions[sched.Action]
	if !ok {
		run.Status = types.ScheduleRunStatusError
		run.Error = fmt.Sprintf("unknown action %q", sched.Action)
		finishRun(run, e.now())
		_ = e.store.AppendRun(sched.ID, run)
		return run.Status
	}

	var lastErr error
	attempts := e.cfg.MaxRetries + 1
	for attempt := 0; attempt < attempts; attempt++ {
		actx, cancel := context.WithTimeout(ctx, e.cfg.ActionTimeout)
		err := action(actx, e.vmMgr, sched, vmID, scheduledTime)
		cancel()

		if err == nil {
			run.Status = types.ScheduleRunStatusSuccess
			run.Error = ""
			finishRun(run, e.now())
			_ = e.store.AppendRun(sched.ID, run)
			e.emit(appEvent("schedule.fire_succeeded", vmID, actor,
				fmt.Sprintf("schedule %q %s succeeded on %s", sched.Name, sched.Action, vmID),
				map[string]string{"schedule_id": sched.ID, "action": string(sched.Action)}, e.now()))
			return run.Status
		}

		if se, isSkip := err.(*skipError); isSkip {
			run.Status = types.ScheduleRunStatusSkipped
			run.SkipReason = se.reason
			finishRun(run, e.now())
			_ = e.store.AppendRun(sched.ID, run)
			e.emit(appEvent("schedule.fire_skipped", vmID, actor,
				fmt.Sprintf("schedule %q skipped on %s: %s", sched.Name, vmID, se.reason),
				map[string]string{"schedule_id": sched.ID, "skip_reason": string(se.reason)}, e.now()))
			return run.Status
		}

		lastErr = err
		if run.Error == "" {
			run.Error = fmt.Sprintf("attempt %d: %s", attempt+1, err.Error())
		} else {
			run.Error += fmt.Sprintf("; attempt %d: %s", attempt+1, err.Error())
		}
		if attempt < attempts-1 {
			e.cfg.sleep(e.backoff(attempt))
		}
	}

	run.Status = types.ScheduleRunStatusError
	finishRun(run, e.now())
	_ = e.store.AppendRun(sched.ID, run)
	e.emit(appEvent("schedule.fire_failed", vmID, actor,
		fmt.Sprintf("schedule %q %s failed on %s: %s", sched.Name, sched.Action, vmID, lastErr.Error()),
		map[string]string{"schedule_id": sched.ID, "action": string(sched.Action), "error": lastErr.Error()}, e.now()))
	return run.Status
}

func (e *Engine) backoff(attempt int) time.Duration {
	if len(e.cfg.RetryBackoff) == 0 {
		return 0
	}
	if attempt >= len(e.cfg.RetryBackoff) {
		return e.cfg.RetryBackoff[len(e.cfg.RetryBackoff)-1]
	}
	return e.cfg.RetryBackoff[attempt]
}

// finalize updates the schedule's LastFiredAt / LastResult / NextFireAt and
// persists the change.
func (e *Engine) finalize(sched *types.Schedule, scheduledTime time.Time, result string) {
	current, err := e.store.GetSchedule(sched.ID)
	if err != nil {
		return
	}
	fired := scheduledTime.UTC()
	current.LastFiredAt = &fired
	current.LastResult = result
	if next, err := e.NextFireTime(current.CronSpec, current.Timezone); err == nil && current.Enabled {
		n := next.UTC()
		current.NextFireAt = &n
	}
	_ = e.store.PutSchedule(current)
}

// resolveTargets returns the VM IDs a fire applies to. The second return value
// is true only for an explicit single-VM schedule whose VM no longer exists.
func (e *Engine) resolveTargets(ctx context.Context, sched *types.Schedule) ([]string, bool) {
	if strings.TrimSpace(sched.VMID) != "" {
		if _, err := e.vmMgr.Get(ctx, sched.VMID); err != nil {
			return nil, true
		}
		return []string{sched.VMID}, false
	}

	all, err := e.vmMgr.List(ctx)
	if err != nil {
		logger.Warn("scheduler", "tag fan-out list failed", "schedule", sched.ID, "error", err.Error())
		return nil, false
	}

	selector := types.NormalizeScheduleTags(sched.TagSelector)
	var out []string
	for _, v := range all {
		if v == nil {
			continue
		}
		if len(selector) == 0 || vmMatchesAnyTag(v, selector) {
			out = append(out, v.ID)
		}
	}
	sort.Strings(out)
	return out, false
}

func vmMatchesAnyTag(v *types.VM, selector []string) bool {
	tagset := map[string]struct{}{}
	for _, t := range v.Tags {
		tagset[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	for _, t := range v.Spec.Tags {
		tagset[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	for _, want := range selector {
		if _, ok := tagset[want]; ok {
			return true
		}
	}
	return false
}

func (e *Engine) semaphore(scheduleID string) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	sem, ok := e.sems[scheduleID]
	if !ok {
		sem = make(chan struct{}, 1)
		e.sems[scheduleID] = sem
	}
	return sem
}

// runCatchUp replays missed fires per each schedule's catch-up policy. lastTick
// is the catch-up cursor captured before it was advanced for this run.
func (e *Engine) runCatchUp(ctx context.Context, scheds []*types.Schedule, lastTick time.Time) {
	if lastTick.IsZero() {
		// Fresh install (or unreadable cursor): nothing to catch up.
		return
	}
	now := e.now()
	for _, sched := range scheds {
		if sched == nil || !sched.Enabled {
			continue
		}
		policy := sched.CatchUpPolicy
		if policy == "" || policy == types.ScheduleCatchUpSkip {
			continue
		}
		cronSched, err := cronParser.Parse(strings.TrimSpace(sched.CronSpec))
		if err != nil {
			continue
		}
		loc, err := loadLocation(sched.Timezone)
		if err != nil {
			continue
		}
		missed := missedFires(cronSched, lastTick.In(loc), now.In(loc), e.cfg.MaxCatchUp)
		if len(missed) == 0 {
			continue
		}
		switch policy {
		case types.ScheduleCatchUpRunOnce:
			e.emit(appEvent("schedule.catch_up_replayed", "", "scheduler",
				fmt.Sprintf("schedule %q replaying 1 of %d missed fire(s)", sched.Name, len(missed)),
				map[string]string{"schedule_id": sched.ID, "missed": fmt.Sprintf("%d", len(missed)), "replayed": "1"}, e.now()))
			e.fire(ctx, sched.ID, missed[len(missed)-1], "scheduler")
		case types.ScheduleCatchUpRunAll:
			e.emit(appEvent("schedule.catch_up_replayed", "", "scheduler",
				fmt.Sprintf("schedule %q replaying %d missed fire(s)", sched.Name, len(missed)),
				map[string]string{"schedule_id": sched.ID, "missed": fmt.Sprintf("%d", len(missed)), "replayed": fmt.Sprintf("%d", len(missed))}, e.now()))
			for _, t := range missed {
				e.fire(ctx, sched.ID, t, "scheduler")
			}
		}
	}
}

func (e *Engine) emit(evt *types.Event) {
	if e.sink == nil || evt == nil {
		return
	}
	e.sink.Publish(evt)
}

func finishRun(run *types.ScheduleRun, now time.Time) {
	t := now.UTC()
	run.FinishedAt = &t
}

func appEvent(evtType, vmID, actor, message string, attrs map[string]string, now time.Time) *types.Event {
	return &types.Event{
		Type:       evtType,
		Source:     types.EventSourceApp,
		VMID:       vmID,
		Severity:   types.EventSeverityInfo,
		Message:    message,
		Attributes: attrs,
		Actor:      actor,
		OccurredAt: now,
	}
}

func systemEvent(evtType, severity, message string, attrs map[string]string, now time.Time) *types.Event {
	return &types.Event{
		Type:       evtType,
		Source:     types.EventSourceSystem,
		Severity:   severity,
		Message:    message,
		Attributes: attrs,
		Actor:      "scheduler",
		OccurredAt: now,
	}
}
