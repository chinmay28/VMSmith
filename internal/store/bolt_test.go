package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func tempDB(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	return s, func() { s.Close(); os.Remove(path) }
}

// --- VM tests ---

func TestPutGetVM(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	vm := &types.VM{
		ID:   "vm-1",
		Name: "test-vm",
		Spec: types.VMSpec{
			Name:   "test-vm",
			Image:  "ubuntu-22.04",
			CPUs:   4,
			RAMMB:  4096,
			DiskGB: 50,
		},
		State:     types.VMStateRunning,
		IP:        "192.168.100.10",
		DiskPath:  "/var/lib/vmsmith/vms/vm-1/disk.qcow2",
		CreatedAt: time.Now().Truncate(time.Millisecond),
	}

	if err := s.PutVM(vm); err != nil {
		t.Fatalf("PutVM: %v", err)
	}

	got, err := s.GetVM("vm-1")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}

	if got.Name != vm.Name {
		t.Errorf("Name = %q, want %q", got.Name, vm.Name)
	}
	if got.Spec.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", got.Spec.CPUs)
	}
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want %q", got.State, types.VMStateRunning)
	}
	if got.IP != "192.168.100.10" {
		t.Errorf("IP = %q, want 192.168.100.10", got.IP)
	}
}

func TestGetVM_NotFound(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	_, err := s.GetVM("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent VM")
	}
}

func TestListVMs(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	// Empty list
	vms, err := s.ListVMs()
	if err != nil {
		t.Fatalf("ListVMs empty: %v", err)
	}
	if len(vms) != 0 {
		t.Errorf("expected 0 VMs, got %d", len(vms))
	}

	// Add two VMs
	s.PutVM(&types.VM{ID: "vm-1", Name: "first"})
	s.PutVM(&types.VM{ID: "vm-2", Name: "second"})

	vms, err = s.ListVMs()
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	if len(vms) != 2 {
		t.Errorf("expected 2 VMs, got %d", len(vms))
	}
}

func TestDeleteVM(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	s.PutVM(&types.VM{ID: "vm-1", Name: "doomed"})

	if err := s.DeleteVM("vm-1"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}

	_, err := s.GetVM("vm-1")
	if err == nil {
		t.Fatal("expected VM to be deleted")
	}
}

func TestPutVM_Update(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	s.PutVM(&types.VM{ID: "vm-1", Name: "original", State: types.VMStateRunning})
	s.PutVM(&types.VM{ID: "vm-1", Name: "updated", State: types.VMStateStopped})

	got, _ := s.GetVM("vm-1")
	if got.Name != "updated" {
		t.Errorf("Name = %q, want updated", got.Name)
	}
	if got.State != types.VMStateStopped {
		t.Errorf("State = %q, want stopped", got.State)
	}
}

// --- Image tests ---

func TestImageCRUD(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	img := &types.Image{
		ID:        "img-1",
		Name:      "ubuntu-base",
		Path:      "/var/lib/vmsmith/images/ubuntu-base.qcow2",
		SizeBytes: 1073741824,
		Format:    "qcow2",
		SourceVM:  "vm-1",
		CreatedAt: time.Now(),
	}

	// Create
	if err := s.PutImage(img); err != nil {
		t.Fatalf("PutImage: %v", err)
	}

	// Read
	got, err := s.GetImage("img-1")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if got.Name != "ubuntu-base" {
		t.Errorf("Name = %q, want ubuntu-base", got.Name)
	}
	if got.SizeBytes != 1073741824 {
		t.Errorf("SizeBytes = %d, want 1073741824", got.SizeBytes)
	}

	// List
	imgs, _ := s.ListImages()
	if len(imgs) != 1 {
		t.Errorf("expected 1 image, got %d", len(imgs))
	}

	// Delete
	s.DeleteImage("img-1")
	_, err = s.GetImage("img-1")
	if err == nil {
		t.Fatal("expected image to be deleted")
	}
}

// --- Template tests ---

func TestTemplateCRUD(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	tpl := &types.VMTemplate{
		ID:          "tmpl-1",
		Name:        "small-linux",
		Image:       "ubuntu-22.04",
		CPUs:        2,
		RAMMB:       2048,
		DiskGB:      20,
		Description: "small preset",
		Tags:        []string{"prod", "web"},
		DefaultUser: "ubuntu",
		CreatedAt:   time.Now().Truncate(time.Millisecond),
		UpdatedAt:   time.Now().Truncate(time.Millisecond),
	}

	if err := s.PutTemplate(tpl); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}

	got, err := s.GetTemplate("tmpl-1")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if got.Name != tpl.Name {
		t.Fatalf("Name = %q, want %q", got.Name, tpl.Name)
	}
	if got.DefaultUser != tpl.DefaultUser {
		t.Fatalf("DefaultUser = %q, want %q", got.DefaultUser, tpl.DefaultUser)
	}

	list, err := s.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(list) != 1 || list[0].ID != tpl.ID {
		t.Fatalf("ListTemplates = %#v, want template %#v", list, tpl)
	}

	if err := s.DeleteTemplate(tpl.ID); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if _, err := s.GetTemplate(tpl.ID); err == nil {
		t.Fatal("expected template to be deleted")
	}
}

// --- Schedule tests ---

func TestScheduleCRUD(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	schedule := &types.Schedule{
		ID:            "sched-1",
		Name:          "nightly snapshot",
		VMID:          "vm-1",
		Action:        types.ScheduleActionSnapshot,
		CronSpec:      "0 0 2 * * *",
		Timezone:      "UTC",
		Enabled:       true,
		CatchUpPolicy: types.ScheduleCatchUpRunOnce,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.PutSchedule(schedule); err != nil {
		t.Fatalf("PutSchedule: %v", err)
	}

	got, err := s.GetSchedule(schedule.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got.Name != schedule.Name || got.Action != schedule.Action {
		t.Fatalf("GetSchedule = %#v, want %#v", got, schedule)
	}

	list, err := s.ListSchedules()
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(list) != 1 || list[0].ID != schedule.ID {
		t.Fatalf("ListSchedules = %#v, want %#v", list, schedule)
	}

	if err := s.DeleteSchedule(schedule.ID); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	if _, err := s.GetSchedule(schedule.ID); err == nil {
		t.Fatal("expected schedule to be deleted")
	}
}

func TestAppendRunAndListRuns(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	base := time.Date(2026, 5, 5, 16, 0, 0, 0, time.UTC)
	runs := []*types.ScheduleRun{
		{ID: "run-1", ScheduleID: "sched-1", VMID: "vm-1", StartedAt: base.Add(1 * time.Minute), Status: types.ScheduleRunStatusSuccess},
		{ID: "run-2", ScheduleID: "sched-1", VMID: "vm-1", StartedAt: base.Add(2 * time.Minute), Status: types.ScheduleRunStatusError, Error: "boom"},
		{ID: "run-3", ScheduleID: "sched-1", VMID: "vm-2", StartedAt: base.Add(3 * time.Minute), Status: types.ScheduleRunStatusSkipped, SkipReason: types.ScheduleRunSkipReasonConcurrentRun},
	}
	for _, run := range runs {
		if err := s.AppendRun("sched-1", run); err != nil {
			t.Fatalf("AppendRun: %v", err)
		}
	}

	stored, err := s.ListRuns("sched-1", 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("ListRuns length = %d, want 3", len(stored))
	}
	if stored[0].ID != "run-3" || stored[1].ID != "run-2" || stored[2].ID != "run-1" {
		t.Fatalf("ListRuns order = [%s %s %s], want [run-3 run-2 run-1]", stored[0].ID, stored[1].ID, stored[2].ID)
	}

	limited, err := s.ListRuns("sched-1", 2)
	if err != nil {
		t.Fatalf("ListRuns limit: %v", err)
	}
	if len(limited) != 2 || limited[0].ID != "run-3" || limited[1].ID != "run-2" {
		t.Fatalf("ListRuns limit = %#v, want newest two runs", limited)
	}
}

func TestAppendRunTrimsHistoryAndDeleteScheduleRemovesRuns(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	for i := 0; i < defaultScheduleRunHistory+5; i++ {
		run := &types.ScheduleRun{
			ID:         fmt.Sprintf("run-%03d", i),
			ScheduleID: "sched-1",
			VMID:       "vm-1",
			StartedAt:  time.Unix(0, int64(i+1)).UTC(),
			Status:     types.ScheduleRunStatusSuccess,
		}
		if err := s.AppendRun("sched-1", run); err != nil {
			t.Fatalf("AppendRun %d: %v", i, err)
		}
	}

	runs, err := s.ListRuns("sched-1", 0)
	if err != nil {
		t.Fatalf("ListRuns after trim: %v", err)
	}
	if len(runs) != defaultScheduleRunHistory {
		t.Fatalf("trimmed run count = %d, want %d", len(runs), defaultScheduleRunHistory)
	}
	if runs[len(runs)-1].ID != "run-005" {
		t.Fatalf("oldest retained run = %s, want run-005", runs[len(runs)-1].ID)
	}

	if err := s.PutSchedule(&types.Schedule{ID: "sched-1", Name: "cleanup"}); err != nil {
		t.Fatalf("PutSchedule: %v", err)
	}
	if err := s.DeleteSchedule("sched-1"); err != nil {
		t.Fatalf("DeleteSchedule: %v", err)
	}
	runs, err = s.ListRuns("sched-1", 0)
	if err != nil {
		t.Fatalf("ListRuns after delete: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs after delete = %d, want 0", len(runs))
	}
}

func TestLastTickRoundTrip(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	zero, err := s.GetLastTick()
	if err != nil {
		t.Fatalf("GetLastTick empty: %v", err)
	}
	if !zero.IsZero() {
		t.Fatalf("GetLastTick empty = %v, want zero", zero)
	}

	want := time.Date(2026, 5, 5, 16, 4, 0, 123456789, time.UTC)
	if err := s.SetLastTick(want); err != nil {
		t.Fatalf("SetLastTick: %v", err)
	}
	got, err := s.GetLastTick()
	if err != nil {
		t.Fatalf("GetLastTick: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("GetLastTick = %v, want %v", got, want)
	}
}

// --- Port forward tests ---

func TestPortForwardCRUD(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	pf := &types.PortForward{
		ID:        "pf-1",
		VMID:      "vm-1",
		HostPort:  2222,
		GuestPort: 22,
		GuestIP:   "192.168.100.10",
		Protocol:  types.ProtocolTCP,
	}

	// Create
	if err := s.PutPortForward(pf); err != nil {
		t.Fatalf("PutPortForward: %v", err)
	}

	// List all
	pfs, _ := s.ListPortForwards("")
	if len(pfs) != 1 {
		t.Fatalf("expected 1 port forward, got %d", len(pfs))
	}
	if pfs[0].HostPort != 2222 {
		t.Errorf("HostPort = %d, want 2222", pfs[0].HostPort)
	}

	// List filtered by VM
	s.PutPortForward(&types.PortForward{ID: "pf-2", VMID: "vm-2", HostPort: 3333})
	filtered, _ := s.ListPortForwards("vm-1")
	if len(filtered) != 1 {
		t.Errorf("expected 1 filtered port forward, got %d", len(filtered))
	}

	// Delete
	s.DeletePortForward("pf-1")
	all, _ := s.ListPortForwards("")
	if len(all) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(all))
	}
}

func TestStoreReopenPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persist.db")

	// Write data
	s1, _ := New(path)
	s1.PutVM(&types.VM{ID: "vm-persist", Name: "survivor"})
	s1.Close()

	// Reopen and verify
	s2, _ := New(path)
	defer s2.Close()

	got, err := s2.GetVM("vm-persist")
	if err != nil {
		t.Fatalf("data not persisted: %v", err)
	}
	if got.Name != "survivor" {
		t.Errorf("Name = %q, want survivor", got.Name)
	}
}

// --- VM with Networks (round-trip JSON) ---

func TestVMWithNetworksRoundTrip(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	vm := &types.VM{
		ID:   "vm-net",
		Name: "multi-net",
		Spec: types.VMSpec{
			Name:  "multi-net",
			Image: "ubuntu",
			Networks: []types.NetworkAttachment{
				{Name: "data", Mode: types.NetworkModeMacvtap, HostInterface: "eth1", StaticIP: "192.168.1.100/24"},
				{Name: "storage", Mode: types.NetworkModeBridge, Bridge: "br-eth2"},
			},
		},
		State: types.VMStateRunning,
	}

	s.PutVM(vm)
	got, _ := s.GetVM("vm-net")

	if len(got.Spec.Networks) != 2 {
		t.Fatalf("expected 2 networks, got %d", len(got.Spec.Networks))
	}
	if got.Spec.Networks[0].StaticIP != "192.168.1.100/24" {
		t.Errorf("network[0] StaticIP = %q", got.Spec.Networks[0].StaticIP)
	}
	if got.Spec.Networks[1].Mode != types.NetworkModeBridge {
		t.Errorf("network[1] Mode = %q, want bridge", got.Spec.Networks[1].Mode)
	}
}

// --- Event tests ---

func TestEventCRUD(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)

	evt1 := &types.Event{
		ID:        "evt-1",
		VMID:      "vm-1",
		Type:      "vm_started",
		Message:   "VM started",
		CreatedAt: now,
	}
	evt2 := &types.Event{
		ID:        "evt-2",
		VMID:      "vm-1",
		Type:      "vm_stopped",
		Message:   "VM stopped",
		CreatedAt: now.Add(time.Hour),
	}

	if err := s.PutEvent(evt1); err != nil {
		t.Fatalf("PutEvent 1 failed: %v", err)
	}
	if err := s.PutEvent(evt2); err != nil {
		t.Fatalf("PutEvent 2 failed: %v", err)
	}

	events, err := s.ListEvents()
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ListEvents returned %d items, want 2", len(events))
	}
}

func TestPruneEventsByAge(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	now := time.Now()
	old := &types.Event{Type: "vm.started", Message: "old", OccurredAt: now.Add(-48 * time.Hour)}
	mid := &types.Event{Type: "vm.stopped", Message: "mid", OccurredAt: now.Add(-25 * time.Hour)}
	fresh := &types.Event{Type: "vm.created", Message: "fresh", OccurredAt: now.Add(-1 * time.Hour)}

	for _, evt := range []*types.Event{old, mid, fresh} {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	// Cutoff = now - 24h: should delete old + mid (both older than 24h), keep fresh.
	deleted, err := s.PruneEventsByAge(24 * time.Hour)
	if err != nil {
		t.Fatalf("PruneEventsByAge: %v", err)
	}
	if deleted != 2 {
		t.Errorf("PruneEventsByAge deleted=%d, want 2", deleted)
	}

	count, err := s.CountEvents()
	if err != nil {
		t.Fatalf("CountEvents: %v", err)
	}
	if count != 1 {
		t.Errorf("after prune: count=%d, want 1", count)
	}

	got, _, err := s.ListEventsFiltered(EventFilter{})
	if err != nil {
		t.Fatalf("ListEventsFiltered: %v", err)
	}
	if len(got) != 1 || got[0].Message != "fresh" {
		t.Fatalf("expected only the fresh event to survive, got %+v", got)
	}
}

func TestPruneEventsByAge_ZeroDisables(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	stale := &types.Event{Type: "vm.started", OccurredAt: time.Now().Add(-365 * 24 * time.Hour)}
	if _, err := s.AppendEvent(stale); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	deleted, err := s.PruneEventsByAge(0)
	if err != nil {
		t.Fatalf("PruneEventsByAge(0): %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected zero deletes when maxAge=0, got %d", deleted)
	}

	deleted, err = s.PruneEventsByAge(-1 * time.Hour)
	if err != nil {
		t.Fatalf("PruneEventsByAge(negative): %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected zero deletes when maxAge<0, got %d", deleted)
	}
}

func TestPruneEventsByAge_KeepsAllWhenAllFresh(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	now := time.Now()
	for i := 0; i < 5; i++ {
		evt := &types.Event{Type: "vm.heartbeat", OccurredAt: now.Add(-time.Duration(i) * time.Minute)}
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	deleted, err := s.PruneEventsByAge(24 * time.Hour)
	if err != nil {
		t.Fatalf("PruneEventsByAge: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected no deletes when all events fresh, got %d", deleted)
	}

	count, _ := s.CountEvents()
	if count != 5 {
		t.Errorf("count=%d, want 5", count)
	}
}

func TestPruneEventsByAge_StopsAtFirstFresh(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	now := time.Now()
	// Mix: stale, stale, fresh, stale (out of order timestamps but in append order).
	// Pruning walks chronologically by sequence ID and stops at first fresh
	// event.  The trailing stale entry should NOT be deleted.
	events := []*types.Event{
		{Type: "a", OccurredAt: now.Add(-5 * time.Hour)},
		{Type: "b", OccurredAt: now.Add(-4 * time.Hour)},
		{Type: "c", OccurredAt: now.Add(-1 * time.Minute)}, // fresh
		{Type: "d", OccurredAt: now.Add(-3 * time.Hour)},   // stale by clock, but inserted after fresh
	}
	for _, evt := range events {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	deleted, err := s.PruneEventsByAge(2 * time.Hour)
	if err != nil {
		t.Fatalf("PruneEventsByAge: %v", err)
	}
	// Only the first two stale events should be deleted; the walk stops at "c".
	if deleted != 2 {
		t.Errorf("PruneEventsByAge deleted=%d, want 2", deleted)
	}

	count, _ := s.CountEvents()
	if count != 2 {
		t.Errorf("count=%d, want 2", count)
	}
}

// --- Webhook tests ---

func TestPutGetListDeleteWebhook(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	wh := &types.Webhook{
		ID:        "wh-1",
		URL:       "https://example.com/hook",
		Secret:    "topsecret",
		Active:    true,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := s.PutWebhook(wh); err != nil {
		t.Fatalf("PutWebhook: %v", err)
	}

	got, err := s.GetWebhook("wh-1")
	if err != nil {
		t.Fatalf("GetWebhook: %v", err)
	}
	if got.URL != wh.URL || got.Secret != wh.Secret || !got.Active {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, wh)
	}

	hooks, err := s.ListWebhooks()
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].ID != "wh-1" {
		t.Fatalf("ListWebhooks = %+v, want one webhook with ID wh-1", hooks)
	}

	if err := s.DeleteWebhook("wh-1"); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}

	if _, err := s.GetWebhook("wh-1"); err == nil {
		t.Fatalf("expected not-found after DeleteWebhook")
	}
	hooks, _ = s.ListWebhooks()
	if len(hooks) != 0 {
		t.Fatalf("ListWebhooks after delete = %d, want 0", len(hooks))
	}
}

// --- Snapshot tag metadata tests ---

func TestSnapshotTags_PutGetRoundTrip(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	if err := s.PutSnapshotTags("vm-1", "snap-a", []string{"audit", "production"}); err != nil {
		t.Fatalf("PutSnapshotTags: %v", err)
	}
	tags, err := s.GetSnapshotTags("vm-1", "snap-a")
	if err != nil {
		t.Fatalf("GetSnapshotTags: %v", err)
	}
	if len(tags) != 2 || tags[0] != "audit" || tags[1] != "production" {
		t.Fatalf("round-trip mismatch: got %v", tags)
	}
}

func TestSnapshotTags_GetMissingReturnsNil(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	tags, err := s.GetSnapshotTags("vm-1", "missing")
	if err != nil {
		t.Fatalf("GetSnapshotTags on missing: %v", err)
	}
	if tags != nil {
		t.Fatalf("expected nil tags on missing record, got %v", tags)
	}
}

func TestSnapshotTags_PutEmptyClearsRecord(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	if err := s.PutSnapshotTags("vm-1", "snap-a", []string{"audit"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Empty slice clears the record entirely.
	if err := s.PutSnapshotTags("vm-1", "snap-a", []string{}); err != nil {
		t.Fatalf("PutSnapshotTags empty: %v", err)
	}
	tags, err := s.GetSnapshotTags("vm-1", "snap-a")
	if err != nil {
		t.Fatalf("GetSnapshotTags after clear: %v", err)
	}
	if tags != nil {
		t.Fatalf("expected nil after clear, got %v", tags)
	}
}

func TestSnapshotTags_DeleteRemovesRecord(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	if err := s.PutSnapshotTags("vm-1", "snap-a", []string{"audit"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.DeleteSnapshotTags("vm-1", "snap-a"); err != nil {
		t.Fatalf("DeleteSnapshotTags: %v", err)
	}
	tags, err := s.GetSnapshotTags("vm-1", "snap-a")
	if err != nil {
		t.Fatalf("GetSnapshotTags after delete: %v", err)
	}
	if tags != nil {
		t.Fatalf("expected nil after delete, got %v", tags)
	}
}

func TestSnapshotTags_DeleteMissingIsNoOp(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	if err := s.DeleteSnapshotTags("vm-1", "never-existed"); err != nil {
		t.Fatalf("DeleteSnapshotTags on missing should be idempotent: %v", err)
	}
}

func TestSnapshotTags_ListByVMReturnsMatchingOnly(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	if err := s.PutSnapshotTags("vm-1", "snap-a", []string{"audit"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutSnapshotTags("vm-1", "snap-b", []string{"production", "backup"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutSnapshotTags("vm-2", "snap-c", []string{"staging"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := s.ListSnapshotTagsByVM("vm-1")
	if err != nil {
		t.Fatalf("ListSnapshotTagsByVM: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected two entries for vm-1, got %v", out)
	}
	if got := out["snap-a"]; len(got) != 1 || got[0] != "audit" {
		t.Fatalf("snap-a tags wrong: %v", got)
	}
	if got := out["snap-b"]; len(got) != 2 || got[0] != "production" || got[1] != "backup" {
		t.Fatalf("snap-b tags wrong: %v", got)
	}
	// vm-2's snap-c must not leak into vm-1's response.
	if _, leaked := out["snap-c"]; leaked {
		t.Fatalf("ListSnapshotTagsByVM leaked vm-2 record into vm-1 response")
	}
}

func TestSnapshotTags_RejectsEmptyKeys(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	if err := s.PutSnapshotTags("", "snap-a", []string{"x"}); err == nil {
		t.Fatalf("expected error for empty vmID")
	}
	if err := s.PutSnapshotTags("vm-1", "", []string{"x"}); err == nil {
		t.Fatalf("expected error for empty snapshot name")
	}
}

func TestSnapshotTags_ListByVMOnEmptyStoreReturnsEmpty(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	out, err := s.ListSnapshotTagsByVM("vm-1")
	if err != nil {
		t.Fatalf("ListSnapshotTagsByVM on empty bucket: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty map, got %v", out)
	}
}

// Two VMs with similar IDs (`vm-1` and `vm-10`) must not bleed into each
// other when their tag rows share a bucket and key prefix.  The cursor walk
// in ListSnapshotTagsByVM seeks on `vmID + "/"` so the trailing slash
// disambiguates the prefix.
func TestSnapshotTags_ListByVMPrefixIsolation(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()

	if err := s.PutSnapshotTags("vm-1", "snap-a", []string{"audit"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutSnapshotTags("vm-10", "snap-b", []string{"staging"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := s.ListSnapshotTagsByVM("vm-1")
	if err != nil {
		t.Fatalf("ListSnapshotTagsByVM: %v", err)
	}
	if _, leaked := out["snap-b"]; leaked {
		t.Fatalf("ListSnapshotTagsByVM(vm-1) leaked vm-10's snap-b into the response: %v", out)
	}
}

// --- EventFilter.ResourceID tests ---

func seedResourceIDEventsForStore(t *testing.T, s *Store) {
	t.Helper()
	base := time.Now().Truncate(time.Millisecond)
	seeds := []*types.Event{
		{Type: "snapshot.created", Source: "app", Severity: "info", VMID: "vm-1", ResourceID: "snap-pre", Message: "made", OccurredAt: base.Add(-30 * time.Minute)},
		{Type: "snapshot.deleted", Source: "app", Severity: "warn", VMID: "vm-1", ResourceID: "snap-pre", Message: "dropped", OccurredAt: base.Add(-25 * time.Minute)},
		{Type: "image.uploaded", Source: "app", Severity: "info", ResourceID: "img-other", Message: "uploaded", OccurredAt: base.Add(-20 * time.Minute)},
		{Type: "vm.started", Source: "libvirt", Severity: "info", VMID: "vm-2", Message: "started", OccurredAt: base.Add(-15 * time.Minute)},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func TestListEventsFiltered_ResourceID_ExactMatch(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()
	seedResourceIDEventsForStore(t, s)

	got, total, err := s.ListEventsFiltered(EventFilter{ResourceID: "snap-pre"})
	if err != nil {
		t.Fatalf("ListEventsFiltered: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Fatalf("ResourceID=snap-pre returned total=%d len=%d, want 2/2", total, len(got))
	}
	for _, e := range got {
		if e.ResourceID != "snap-pre" {
			t.Errorf("filter leaked event %+v", e)
		}
	}
}

func TestListEventsFiltered_ResourceID_CaseSensitive(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()
	seedResourceIDEventsForStore(t, s)

	got, _, err := s.ListEventsFiltered(EventFilter{ResourceID: "SNAP-pre"})
	if err != nil {
		t.Fatalf("ListEventsFiltered: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("case-mismatched ResourceID should return 0 events, got %d", len(got))
	}
}

func TestListEventsFiltered_ResourceID_EmptyDisablesFilter(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()
	seedResourceIDEventsForStore(t, s)

	got, _, err := s.ListEventsFiltered(EventFilter{ResourceID: ""})
	if err != nil {
		t.Fatalf("ListEventsFiltered: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("empty ResourceID should disable filter, got %d events", len(got))
	}
}

func TestListEventsFiltered_ResourceID_NoMatchReturnsEmpty(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()
	seedResourceIDEventsForStore(t, s)

	got, _, err := s.ListEventsFiltered(EventFilter{ResourceID: "snap-not-here"})
	if err != nil {
		t.Fatalf("ListEventsFiltered: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 events for unknown ResourceID, got %d", len(got))
	}
}

func TestListEventsFiltered_ResourceID_ComposesWithSeverity(t *testing.T) {
	s, cleanup := tempDB(t)
	defer cleanup()
	seedResourceIDEventsForStore(t, s)

	// snap-pre carries an info create and a warn delete — narrowing by
	// severity=warn should leave only the deletion.
	got, total, err := s.ListEventsFiltered(EventFilter{ResourceID: "snap-pre", Severity: "warn"})
	if err != nil {
		t.Fatalf("ListEventsFiltered: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].Type != "snapshot.deleted" {
		t.Fatalf("ResourceID+Severity returned %+v (total=%d), want one snapshot.deleted", got, total)
	}
}

func TestListEventsFiltered_ResourceID_MatchesEmptyResourceIDOnly(t *testing.T) {
	// When ResourceID is non-empty but no event carries it, the filter
	// returns zero events even though the store has events with empty
	// ResourceID. This is the inverse contract of EmptyDisablesFilter.
	s, cleanup := tempDB(t)
	defer cleanup()

	if _, err := s.AppendEvent(&types.Event{Type: "vm.started", VMID: "vm-1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, _, err := s.ListEventsFiltered(EventFilter{ResourceID: "snap-something"})
	if err != nil {
		t.Fatalf("ListEventsFiltered: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("non-empty ResourceID against events with empty ResourceID should return 0, got %d", len(got))
	}
}
