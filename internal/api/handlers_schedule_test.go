package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/scheduler"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func testScheduleServer(t *testing.T) (*httptest.Server, *vm.MockManager, func()) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = filepath.Join(dir, "test.db")
	cfg.Storage.ImagesDir = dir

	mockMgr := vm.NewMockManager()
	apiServer := NewServer(mockMgr, storage.NewManager(cfg, s), network.NewPortForwarder(s), s)
	engine := scheduler.New(s, mockMgr, nil, scheduler.Config{})
	apiServer.SetScheduleSubsystem(s, engine)
	ts := httptest.NewServer(apiServer)

	return ts, mockMgr, func() {
		ts.Close()
		s.Close()
	}
}

func schedDo(t *testing.T, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func createSchedule(t *testing.T, base string, req types.CreateScheduleRequest) *types.Schedule {
	t.Helper()
	resp, data := schedDo(t, http.MethodPost, base+"/api/v1/schedules", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create schedule: HTTP %d: %s", resp.StatusCode, data)
	}
	var sched types.Schedule
	if err := json.Unmarshal(data, &sched); err != nil {
		t.Fatal(err)
	}
	return &sched
}

func validCreate() types.CreateScheduleRequest {
	return types.CreateScheduleRequest{
		Name:     "nightly-snapshot",
		VMID:     "vm-1",
		Action:   types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *",
		Timezone: "UTC",
	}
}

func TestCreateSchedule_Success(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()

	sched := createSchedule(t, ts.URL, validCreate())
	if sched.ID == "" || sched.NextFireAt == nil {
		t.Fatalf("expected populated id and next_fire_at, got %+v", sched)
	}
	if !sched.Enabled {
		t.Fatal("schedule should default to enabled")
	}
	if sched.CatchUpPolicy != types.ScheduleCatchUpSkip {
		t.Fatalf("catch_up_policy should default to skip, got %q", sched.CatchUpPolicy)
	}

	resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/"+sched.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: HTTP %d: %s", resp.StatusCode, data)
	}
}

func TestCreateSchedule_Validation(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()

	cases := []struct {
		name string
		mut  func(*types.CreateScheduleRequest)
		code string
	}{
		{"missing name", func(r *types.CreateScheduleRequest) { r.Name = "" }, "invalid_name"},
		{"invalid action", func(r *types.CreateScheduleRequest) { r.Action = "explode" }, "invalid_action"},
		{"missing cron", func(r *types.CreateScheduleRequest) { r.CronSpec = "" }, "invalid_cron_spec"},
		{"bad cron", func(r *types.CreateScheduleRequest) { r.CronSpec = "nope" }, "invalid_cron_spec"},
		{"5-field cron", func(r *types.CreateScheduleRequest) { r.CronSpec = "0 2 * * *" }, "invalid_cron_spec"},
		{"bad timezone", func(r *types.CreateScheduleRequest) { r.Timezone = "Nowhere/Nope" }, "invalid_timezone"},
		{"mutually exclusive target", func(r *types.CreateScheduleRequest) { r.TagSelector = []string{"prod"} }, "invalid_target"},
		{"bad catch up", func(r *types.CreateScheduleRequest) { r.CatchUpPolicy = "sometimes" }, "invalid_catch_up_policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validCreate()
			tc.mut(&req)
			resp, data := schedDo(t, http.MethodPost, ts.URL+"/api/v1/schedules", req)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", resp.StatusCode, data)
			}
			var er errorResponse
			json.Unmarshal(data, &er)
			if er.Code != tc.code {
				t.Fatalf("expected code %q, got %q (%s)", tc.code, er.Code, data)
			}
		})
	}
}

func TestListSchedules_FiltersAndSort(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()

	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "alpha-snap", VMID: "vm-1", Action: types.ScheduleActionSnapshot, CronSpec: "0 0 2 * * *"})
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "beta-start", VMID: "vm-2", Action: types.ScheduleActionStart, CronSpec: "0 0 6 * * *"})
	disabled := false
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "gamma-stop", VMID: "vm-2", Action: types.ScheduleActionStop, CronSpec: "0 0 22 * * *", Enabled: &disabled})

	list := func(q string) ([]*types.Schedule, int) {
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list%s: HTTP %d: %s", q, resp.StatusCode, data)
		}
		var out []*types.Schedule
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total
	}

	if out, total := list("?action=snapshot"); len(out) != 1 || total != 1 || out[0].Name != "alpha-snap" {
		t.Fatalf("action filter wrong: %+v total=%d", out, total)
	}
	if out, total := list("?vm_id=vm-2"); len(out) != 2 || total != 2 {
		t.Fatalf("vm_id filter wrong: %+v total=%d", out, total)
	}
	if out, _ := list("?enabled=false"); len(out) != 1 || out[0].Name != "gamma-stop" {
		t.Fatalf("enabled=false filter wrong: %+v", out)
	}
	if out, _ := list("?enabled=true"); len(out) != 2 {
		t.Fatalf("enabled=true should match 2, got %d", len(out))
	}
	if out, _ := list("?search=beta"); len(out) != 1 || out[0].Name != "beta-start" {
		t.Fatalf("search filter wrong: %+v", out)
	}
	// sort by name desc
	if out, _ := list("?sort=name&order=desc"); len(out) != 3 || out[0].Name != "gamma-stop" {
		t.Fatalf("name desc sort wrong: %+v", out)
	}
	// invalid sort / order
	if resp, _ := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules?sort=bogus", nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid sort should 400, got %d", resp.StatusCode)
	}
	if resp, _ := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules?order=sideways", nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid order should 400, got %d", resp.StatusCode)
	}
	if resp, _ := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules?enabled=maybe", nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid enabled should 400, got %d", resp.StatusCode)
	}
}

func TestGetSchedule_NotFound(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()
	resp, _ := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/missing", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestUpdateSchedule(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()
	sched := createSchedule(t, ts.URL, validCreate())

	// noop
	if resp, _ := schedDo(t, http.MethodPatch, ts.URL+"/api/v1/schedules/"+sched.ID, map[string]any{}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty patch should 400 noop_update, got %d", resp.StatusCode)
	}

	// disable -> NextFireAt cleared
	enabled := false
	resp, data := schedDo(t, http.MethodPatch, ts.URL+"/api/v1/schedules/"+sched.ID, types.ScheduleUpdateSpec{Enabled: &enabled})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: HTTP %d: %s", resp.StatusCode, data)
	}
	var got types.Schedule
	json.Unmarshal(data, &got)
	if got.Enabled || got.NextFireAt != nil {
		t.Fatalf("disabled schedule should have nil next_fire_at: %+v", got)
	}

	// invalid cron in patch
	bad := "garbage"
	if resp, _ := schedDo(t, http.MethodPatch, ts.URL+"/api/v1/schedules/"+sched.ID, types.ScheduleUpdateSpec{CronSpec: &bad}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid cron patch should 400, got %d", resp.StatusCode)
	}

	// 404 on unknown
	newName := "x"
	if resp, _ := schedDo(t, http.MethodPatch, ts.URL+"/api/v1/schedules/missing", types.ScheduleUpdateSpec{Name: &newName}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch unknown should 404, got %d", resp.StatusCode)
	}
}

func TestDeleteSchedule(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()
	sched := createSchedule(t, ts.URL, validCreate())

	resp, _ := schedDo(t, http.MethodDelete, ts.URL+"/api/v1/schedules/"+sched.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete should 204, got %d", resp.StatusCode)
	}
	resp, _ = schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/"+sched.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted schedule should 404, got %d", resp.StatusCode)
	}
	resp, _ = schedDo(t, http.MethodDelete, ts.URL+"/api/v1/schedules/missing", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete unknown should 404, got %d", resp.StatusCode)
	}
}

func TestRunNowAndRuns(t *testing.T) {
	ts, mockMgr, cleanup := testScheduleServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "vm-1", State: types.VMStateStopped, Spec: types.VMSpec{Name: "vm-1"}})

	sched := createSchedule(t, ts.URL, types.CreateScheduleRequest{
		Name: "starter", VMID: "vm-1", Action: types.ScheduleActionStart, CronSpec: "0 0 2 * * *",
	})

	resp, data := schedDo(t, http.MethodPost, ts.URL+"/api/v1/schedules/"+sched.ID+"/run-now", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("run-now: HTTP %d: %s", resp.StatusCode, data)
	}

	v, _ := mockMgr.Get(context.Background(), "vm-1")
	if v.State != types.VMStateRunning {
		t.Fatalf("run-now should have started the VM, state=%s", v.State)
	}

	resp, data = schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/"+sched.ID+"/runs", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runs: HTTP %d: %s", resp.StatusCode, data)
	}
	var runs []*types.ScheduleRun
	json.Unmarshal(data, &runs)
	if len(runs) != 1 || runs[0].Status != types.ScheduleRunStatusSuccess {
		t.Fatalf("expected 1 success run, got %+v", runs)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("expected X-Total-Count 1, got %q", got)
	}

	// run-now on unknown schedule
	resp, _ = schedDo(t, http.MethodPost, ts.URL+"/api/v1/schedules/missing/run-now", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("run-now unknown should 404, got %d", resp.StatusCode)
	}
}

// testScheduleServerStore mirrors testScheduleServer but also returns the
// backing store so a test can persist schedules with controlled CreatedAt
// timestamps (the create endpoint always stamps CreatedAt = now).
func testScheduleServerStore(t *testing.T) (*httptest.Server, *store.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = filepath.Join(dir, "test.db")
	cfg.Storage.ImagesDir = dir

	mockMgr := vm.NewMockManager()
	apiServer := NewServer(mockMgr, storage.NewManager(cfg, s), network.NewPortForwarder(s), s)
	engine := scheduler.New(s, mockMgr, nil, scheduler.Config{})
	apiServer.SetScheduleSubsystem(s, engine)
	ts := httptest.NewServer(apiServer)
	return ts, s, func() {
		ts.Close()
		s.Close()
	}
}

func TestListSchedules_FilterByTimeRange(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	mk := func(id, name, created string) {
		t.Helper()
		var ca time.Time
		if created != "" {
			parsed, err := time.Parse(time.RFC3339, created)
			if err != nil {
				t.Fatalf("parse %q: %v", created, err)
			}
			ca = parsed
		}
		if err := s.PutSchedule(&types.Schedule{
			ID:        id,
			Name:      name,
			Action:    types.ScheduleActionSnapshot,
			CronSpec:  "0 0 2 * * *",
			Enabled:   true,
			CreatedAt: ca,
		}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	mk("sched-1", "early", "2026-05-05T00:00:00Z")
	mk("sched-2", "mid", "2026-05-10T00:00:00Z")
	mk("sched-3", "late", "2026-05-15T00:00:00Z")
	mk("sched-4", "zerotime", "") // zero created_at

	list := func(q string) ([]*types.Schedule, int, int) {
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		var out []*types.Schedule
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	// No bounds: all four (including zero-time) returned.
	if out, total, code := list(""); code != http.StatusOK || len(out) != 4 || total != 4 {
		t.Fatalf("no-bounds: code=%d len=%d total=%d", code, len(out), total)
	}
	// since only — inclusive lower bound drops the early one; zero-time excluded.
	if out, total, _ := list("?since=2026-05-10T00:00:00Z"); len(out) != 2 || total != 2 {
		t.Fatalf("since inclusive lower bound wrong: %+v total=%d", out, total)
	}
	// until only — inclusive upper bound; zero-time excluded.
	if out, total, _ := list("?until=2026-05-10T00:00:00Z"); len(out) != 2 || total != 2 {
		t.Fatalf("until inclusive upper bound wrong: %+v total=%d", out, total)
	}
	// both bounds — only the mid schedule falls in the window.
	if out, total, _ := list("?since=2026-05-08T00:00:00Z&until=2026-05-12T00:00:00Z"); len(out) != 1 || total != 1 || out[0].Name != "mid" {
		t.Fatalf("since+until window wrong: %+v total=%d", out, total)
	}
	// zero-time schedule is filtered OUT whenever any bound is set.
	if out, _, _ := list("?since=2000-01-01T00:00:00Z&until=2100-01-01T00:00:00Z"); len(out) != 3 {
		t.Fatalf("zero-time should be excluded under bounds, got %d", len(out))
	}
	// composes with action filter.
	if out, total, _ := list("?action=snapshot&since=2026-05-12T00:00:00Z"); len(out) != 1 || total != 1 || out[0].Name != "late" {
		t.Fatalf("action+since compose wrong: %+v total=%d", out, total)
	}
	// invalid since / until → 400.
	if _, _, code := list("?since=nope"); code != http.StatusBadRequest {
		t.Fatalf("invalid since should 400, got %d", code)
	}
	if _, _, code := list("?until=garbage"); code != http.StatusBadRequest {
		t.Fatalf("invalid until should 400, got %d", code)
	}
}

func TestListSchedules_FilterByTagSelector(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()

	// prod: tags [prod, web]; data: tag [data]; single: vm_id-targeted (no tags).
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "prod-snap", TagSelector: []string{"prod", "web"}, Action: types.ScheduleActionSnapshot, CronSpec: "0 0 2 * * *"})
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "data-snap", TagSelector: []string{"data"}, Action: types.ScheduleActionSnapshot, CronSpec: "0 0 3 * * *"})
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "single-start", VMID: "vm-9", Action: types.ScheduleActionStart, CronSpec: "0 0 4 * * *"})

	list := func(q string) ([]*types.Schedule, int) {
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list%s: HTTP %d: %s", q, resp.StatusCode, data)
		}
		var out []*types.Schedule
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total
	}

	// exact membership: matches a schedule when any tag equals the value.
	if out, total := list("?tag_selector=prod"); len(out) != 1 || total != 1 || out[0].Name != "prod-snap" {
		t.Fatalf("tag_selector=prod wrong: %+v total=%d", out, total)
	}
	if out, total := list("?tag_selector=web"); len(out) != 1 || total != 1 || out[0].Name != "prod-snap" {
		t.Fatalf("tag_selector=web wrong: %+v total=%d", out, total)
	}
	if out, total := list("?tag_selector=data"); len(out) != 1 || total != 1 || out[0].Name != "data-snap" {
		t.Fatalf("tag_selector=data wrong: %+v total=%d", out, total)
	}
	// case-insensitive + whitespace-trimmed.
	if out, total := list("?tag_selector=%20PROD%20"); len(out) != 1 || total != 1 || out[0].Name != "prod-snap" {
		t.Fatalf("tag_selector case/trim wrong: %+v total=%d", out, total)
	}
	// substring is NOT a match — exact membership only (`pro` != `prod`).
	if out, total := list("?tag_selector=pro"); len(out) != 0 || total != 0 {
		t.Fatalf("tag_selector should be exact, not substring: %+v total=%d", out, total)
	}
	// empty disables the filter — all three returned.
	if out, total := list("?tag_selector="); len(out) != 3 || total != 3 {
		t.Fatalf("empty tag_selector should be no-op: %+v total=%d", out, total)
	}
	// vm_id-targeted schedule (empty tag_selector) is never matched.
	if out, total := list("?tag_selector=vm-9"); len(out) != 0 || total != 0 {
		t.Fatalf("vm_id-targeted schedule should not match tag_selector: %+v total=%d", out, total)
	}
	// composes additively with action; X-Total-Count reflects post-filter.
	if out, total := list("?tag_selector=prod&action=snapshot"); len(out) != 1 || total != 1 || out[0].Name != "prod-snap" {
		t.Fatalf("tag_selector+action compose wrong: %+v total=%d", out, total)
	}
	if out, total := list("?tag_selector=prod&action=start"); len(out) != 0 || total != 0 {
		t.Fatalf("tag_selector+non-matching action should be empty: %+v total=%d", out, total)
	}
}

func TestListSchedules_FilterByCatchUpPolicy(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()

	// skip-default omits catch_up_policy, so the handler defaults it to "skip"
	// — exercising the empty-stored-treated-as-skip path. once / all are
	// explicit. once-snap is a snapshot action; all-start is a start action.
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "skip-default", VMID: "vm-1", Action: types.ScheduleActionStop, CronSpec: "0 0 2 * * *"})
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "once-snap", VMID: "vm-2", Action: types.ScheduleActionSnapshot, CronSpec: "0 0 3 * * *", CatchUpPolicy: types.ScheduleCatchUpRunOnce})
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "all-start", VMID: "vm-3", Action: types.ScheduleActionStart, CronSpec: "0 0 4 * * *", CatchUpPolicy: types.ScheduleCatchUpRunAll})

	list := func(q string) ([]*types.Schedule, int, int) {
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		var out []*types.Schedule
		if resp.StatusCode == http.StatusOK {
			json.Unmarshal(data, &out)
		}
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	// empty stored policy is treated as "skip".
	if out, total, code := list("?catch_up_policy=skip"); code != http.StatusOK || len(out) != 1 || total != 1 || out[0].Name != "skip-default" {
		t.Fatalf("catch_up_policy=skip wrong: %+v total=%d code=%d", out, total, code)
	}
	if out, total, _ := list("?catch_up_policy=run_once"); len(out) != 1 || total != 1 || out[0].Name != "once-snap" {
		t.Fatalf("catch_up_policy=run_once wrong: %+v total=%d", out, total)
	}
	if out, total, _ := list("?catch_up_policy=run_all"); len(out) != 1 || total != 1 || out[0].Name != "all-start" {
		t.Fatalf("catch_up_policy=run_all wrong: %+v total=%d", out, total)
	}
	// case-insensitive + whitespace-trimmed.
	if out, total, _ := list("?catch_up_policy=%20RUN_ONCE%20"); len(out) != 1 || total != 1 || out[0].Name != "once-snap" {
		t.Fatalf("catch_up_policy case/trim wrong: %+v total=%d", out, total)
	}
	// empty disables the filter — all three returned.
	if out, total, _ := list("?catch_up_policy="); len(out) != 3 || total != 3 {
		t.Fatalf("empty catch_up_policy should be no-op: %+v total=%d", out, total)
	}
	// invalid value returns 400.
	if _, _, code := list("?catch_up_policy=bogus"); code != http.StatusBadRequest {
		t.Fatalf("invalid catch_up_policy should 400, got %d", code)
	}
	// composes additively with action; X-Total-Count reflects post-filter.
	if out, total, _ := list("?catch_up_policy=run_once&action=snapshot"); len(out) != 1 || total != 1 || out[0].Name != "once-snap" {
		t.Fatalf("catch_up_policy+action compose wrong: %+v total=%d", out, total)
	}
	if out, total, _ := list("?catch_up_policy=run_all&action=snapshot"); len(out) != 0 || total != 0 {
		t.Fatalf("catch_up_policy+non-matching action should be empty: %+v total=%d", out, total)
	}
}

// TestListSchedules_FilterByTimezone covers the ?timezone= filter (5.4.55):
// case-sensitive exact-match against the stored Timezone field, whitespace-
// trimmed, empty disables, no default-fallback for unset values (mirrors the
// ?vm_id= / ?actor= / ?resource_id= exact-match contracts).
func TestListSchedules_FilterByTimezone(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()

	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "utc-snap", VMID: "vm-1", Action: types.ScheduleActionSnapshot, CronSpec: "0 0 2 * * *", Timezone: "UTC"})
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "ny-stop", VMID: "vm-2", Action: types.ScheduleActionStop, CronSpec: "0 0 3 * * *", Timezone: "America/New_York"})
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "tokyo-start", VMID: "vm-3", Action: types.ScheduleActionStart, CronSpec: "0 0 4 * * *", Timezone: "Asia/Tokyo"})
	// No timezone set: the engine treats empty as time.Local. Stored value is "".
	createSchedule(t, ts.URL, types.CreateScheduleRequest{Name: "default-tz", VMID: "vm-4", Action: types.ScheduleActionRestart, CronSpec: "0 0 5 * * *"})

	list := func(q string) ([]*types.Schedule, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		var out []*types.Schedule
		if resp.StatusCode == http.StatusOK {
			json.Unmarshal(data, &out)
		}
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	// Baseline: no filter returns all four.
	if out, total, code := list(""); code != http.StatusOK || len(out) != 4 || total != 4 {
		t.Fatalf("baseline: code=%d len=%d total=%d", code, len(out), total)
	}

	// Exact match: timezone=UTC returns only utc-snap. The default-tz schedule
	// has an empty stored timezone and must NOT match UTC (no default-fallback).
	if out, total, _ := list("?timezone=UTC"); len(out) != 1 || total != 1 || out[0].Name != "utc-snap" {
		t.Fatalf("timezone=UTC: %+v total=%d", out, total)
	}
	if out, total, _ := list("?timezone=America/New_York"); len(out) != 1 || total != 1 || out[0].Name != "ny-stop" {
		t.Fatalf("timezone=America/New_York: %+v total=%d", out, total)
	}
	if out, total, _ := list("?timezone=Asia/Tokyo"); len(out) != 1 || total != 1 || out[0].Name != "tokyo-start" {
		t.Fatalf("timezone=Asia/Tokyo: %+v total=%d", out, total)
	}

	// Case-sensitive: lowercase variants do not match (IANA names are case-sensitive).
	if out, total, _ := list("?timezone=utc"); len(out) != 0 || total != 0 {
		t.Fatalf("timezone=utc (case-sensitive): %+v total=%d", out, total)
	}
	if out, total, _ := list("?timezone=america/new_york"); len(out) != 0 || total != 0 {
		t.Fatalf("timezone=america/new_york (case-sensitive): %+v total=%d", out, total)
	}

	// Whitespace-trimmed.
	if out, total, _ := list("?timezone=%20UTC%20"); len(out) != 1 || total != 1 || out[0].Name != "utc-snap" {
		t.Fatalf("timezone trim: %+v total=%d", out, total)
	}

	// Empty value disables the filter — all four returned.
	if out, total, _ := list("?timezone="); len(out) != 4 || total != 4 {
		t.Fatalf("empty timezone should be no-op: %+v total=%d", out, total)
	}

	// No match yields an empty array + total 0.
	if out, total, code := list("?timezone=Europe/Berlin"); code != http.StatusOK || len(out) != 0 || total != 0 {
		t.Fatalf("timezone=Europe/Berlin (unknown): code=%d %+v total=%d", code, out, total)
	}

	// Composes additively with action: utc-snap is the only snapshot in UTC.
	if out, total, _ := list("?timezone=UTC&action=snapshot"); len(out) != 1 || total != 1 || out[0].Name != "utc-snap" {
		t.Fatalf("timezone+action compose: %+v total=%d", out, total)
	}
	// timezone=UTC + action=stop → empty (ny-stop is in America/New_York).
	if out, total, _ := list("?timezone=UTC&action=stop"); len(out) != 0 || total != 0 {
		t.Fatalf("timezone+non-matching action should be empty: %+v total=%d", out, total)
	}

	// Composes with catch_up_policy (all use default skip): UTC+skip → utc-snap.
	if out, total, _ := list("?timezone=UTC&catch_up_policy=skip"); len(out) != 1 || total != 1 || out[0].Name != "utc-snap" {
		t.Fatalf("timezone+catch_up_policy compose: %+v total=%d", out, total)
	}

	// X-Total-Count reflects the post-filter population even when paginated.
	if out, total, _ := list("?timezone=UTC&per_page=1"); len(out) != 1 || total != 1 {
		t.Fatalf("timezone paginated: len=%d total=%d", len(out), total)
	}
}

// TestListSchedules_FilterByNextFire covers the ?next_fire_since= /
// ?next_fire_until= filter (5.4.60): inclusive RFC3339 bounds on the
// cron-computed NextFireAt, schedules with a nil NextFireAt filtered OUT
// whenever any bound is set, 400 on garbage values, composes additively
// with action / created_at since/until / pagination.
func TestListSchedules_FilterByNextFire(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	mk := func(id, name, nextFire string) {
		t.Helper()
		sched := &types.Schedule{
			ID:        id,
			Name:      name,
			Action:    types.ScheduleActionSnapshot,
			CronSpec:  "0 0 2 * * *",
			Enabled:   true,
			CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		}
		if nextFire != "" {
			parsed, err := time.Parse(time.RFC3339, nextFire)
			if err != nil {
				t.Fatalf("parse %q: %v", nextFire, err)
			}
			sched.NextFireAt = &parsed
		}
		if err := s.PutSchedule(sched); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	mk("sched-1", "early", "2026-06-01T12:00:00Z")
	mk("sched-2", "mid", "2026-06-01T15:00:00Z")
	mk("sched-3", "late", "2026-06-01T20:00:00Z")
	mk("sched-4", "nilfire", "") // disabled / stalled — nil NextFireAt

	list := func(q string) ([]*types.Schedule, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		var out []*types.Schedule
		if resp.StatusCode == http.StatusOK {
			json.Unmarshal(data, &out)
		}
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	// Baseline: no filter returns all four (including nil-NextFireAt).
	if out, total, code := list(""); code != http.StatusOK || len(out) != 4 || total != 4 {
		t.Fatalf("baseline: code=%d len=%d total=%d", code, len(out), total)
	}

	// next_fire_since alone: inclusive lower bound drops the early one.
	// The nilfire schedule must be filtered OUT under any bound.
	if out, total, _ := list("?next_fire_since=2026-06-01T15:00:00Z"); len(out) != 2 || total != 2 {
		t.Fatalf("next_fire_since lower bound wrong: %+v total=%d", out, total)
	}

	// next_fire_until alone: inclusive upper bound.
	if out, total, _ := list("?next_fire_until=2026-06-01T15:00:00Z"); len(out) != 2 || total != 2 {
		t.Fatalf("next_fire_until upper bound wrong: %+v total=%d", out, total)
	}

	// Both bounds: only mid falls in the window.
	if out, total, _ := list("?next_fire_since=2026-06-01T13:00:00Z&next_fire_until=2026-06-01T18:00:00Z"); len(out) != 1 || total != 1 || out[0].Name != "mid" {
		t.Fatalf("range window wrong: %+v total=%d", out, total)
	}

	// nil-NextFireAt schedule is excluded under any bound.
	if out, _, _ := list("?next_fire_since=2000-01-01T00:00:00Z&next_fire_until=2100-01-01T00:00:00Z"); len(out) != 3 {
		t.Fatalf("nil NextFireAt should be excluded under bounds, got %d", len(out))
	}

	// Empty values disable both bounds (no-op).
	if out, total, _ := list("?next_fire_since=&next_fire_until="); len(out) != 4 || total != 4 {
		t.Fatalf("empty bounds should be no-op: %+v total=%d", out, total)
	}

	// Whitespace-trimmed.
	if out, total, _ := list("?next_fire_since=%202026-06-01T15:00:00Z%20"); len(out) != 2 || total != 2 {
		t.Fatalf("whitespace-trim wrong: %+v total=%d", out, total)
	}

	// Composes additively with action (all four are snapshot).
	if out, total, _ := list("?action=snapshot&next_fire_since=2026-06-01T17:00:00Z"); len(out) != 1 || total != 1 || out[0].Name != "late" {
		t.Fatalf("action+next_fire_since compose wrong: %+v total=%d", out, total)
	}

	// Composes additively with the existing since (on created_at) — every schedule
	// has created_at on 2026-05-01, so since=2026-04-01 leaves all in scope but
	// the next_fire bound still narrows.
	if out, total, _ := list("?since=2026-04-01T00:00:00Z&next_fire_until=2026-06-01T13:00:00Z"); len(out) != 1 || total != 1 || out[0].Name != "early" {
		t.Fatalf("since(created_at)+next_fire_until compose wrong: %+v total=%d", out, total)
	}

	// Invalid bounds → 400 with typed error codes.
	if _, _, code := list("?next_fire_since=nope"); code != http.StatusBadRequest {
		t.Fatalf("invalid next_fire_since should 400, got %d", code)
	}
	if _, _, code := list("?next_fire_until=garbage"); code != http.StatusBadRequest {
		t.Fatalf("invalid next_fire_until should 400, got %d", code)
	}

	// X-Total-Count reflects the post-filter population under pagination.
	if out, total, _ := list("?next_fire_since=2026-06-01T13:00:00Z&per_page=1"); len(out) != 1 || total != 2 {
		t.Fatalf("paginated post-filter total wrong: len=%d total=%d", len(out), total)
	}
}

func TestScheduleEndpoints_503WhenDisabled(t *testing.T) {
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.DefaultConfig()
	apiServer := NewServer(vm.NewMockManager(), storage.NewManager(cfg, s), network.NewPortForwarder(s), s)
	// Intentionally NOT calling SetScheduleSubsystem.
	ts := httptest.NewServer(apiServer)
	defer ts.Close()

	for _, path := range []string{"/api/v1/schedules", "/api/v1/schedules/x", "/api/v1/schedules/x/runs"} {
		resp, data := schedDo(t, http.MethodGet, ts.URL+path, nil)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s should 503 when disabled, got %d: %s", path, resp.StatusCode, data)
		}
		var er errorResponse
		json.Unmarshal(data, &er)
		if er.Code != "schedules_disabled" {
			t.Fatalf("%s: expected schedules_disabled, got %q", path, er.Code)
		}
	}
}

func TestListScheduleRuns_Filters(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-runs", Name: "runs", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(started string, status types.ScheduleRunStatus) {
		t.Helper()
		at, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		if err := s.AppendRun("sched-runs", &types.ScheduleRun{
			VMID: "vm-1", StartedAt: at, Status: status,
		}); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	mk("2026-05-20T02:00:00Z", types.ScheduleRunStatusError)
	mk("2026-05-21T02:00:00Z", types.ScheduleRunStatusSkipped)
	mk("2026-05-22T02:00:00Z", types.ScheduleRunStatusSuccess)

	list := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-runs/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	// No filter: all three, newest first.
	if runs, total, code := list(""); code != http.StatusOK || len(runs) != 3 || total != 3 {
		t.Fatalf("no filter: code=%d len=%d total=%d", code, len(runs), total)
	}

	// status exact match.
	if runs, total, _ := list("?status=error"); len(runs) != 1 || total != 1 || runs[0].Status != types.ScheduleRunStatusError {
		t.Fatalf("status=error: %+v total=%d", runs, total)
	}
	// status is case-insensitive + trimmed.
	if runs, _, _ := list("?status=%20SUCCESS%20"); len(runs) != 1 || runs[0].Status != types.ScheduleRunStatusSuccess {
		t.Fatalf("status=SUCCESS (case/trim): %+v", runs)
	}
	// unknown status -> 400 invalid_status.
	if _, _, code := list("?status=bogus"); code != http.StatusBadRequest {
		t.Fatalf("status=bogus should 400, got %d", code)
	}

	// since is inclusive on started_at.
	if runs, total, _ := list("?since=2026-05-21T02:00:00Z"); len(runs) != 2 || total != 2 {
		t.Fatalf("since: len=%d total=%d", len(runs), total)
	}
	// until is inclusive on started_at.
	if runs, total, _ := list("?until=2026-05-21T02:00:00Z"); len(runs) != 2 || total != 2 {
		t.Fatalf("until: len=%d total=%d", len(runs), total)
	}
	// invalid since/until -> 400.
	if _, _, code := list("?since=garbage"); code != http.StatusBadRequest {
		t.Fatalf("invalid since should 400, got %d", code)
	}
	if _, _, code := list("?until=garbage"); code != http.StatusBadRequest {
		t.Fatalf("invalid until should 400, got %d", code)
	}

	// Filters compose: status + since.
	if runs, total, _ := list("?status=success&since=2026-05-21T02:00:00Z"); len(runs) != 1 || total != 1 {
		t.Fatalf("status+since compose: len=%d total=%d", len(runs), total)
	}
	// status with no match yields an empty (non-null) array + total 0.
	if runs, total, code := list("?status=running"); code != http.StatusOK || runs == nil || len(runs) != 0 || total != 0 {
		t.Fatalf("status=running: code=%d runs=%v total=%d", code, runs, total)
	}

	// X-Total-Count reflects the post-filter population even when paginated.
	if runs, total, _ := list("?since=2026-05-21T02:00:00Z&per_page=1"); len(runs) != 1 || total != 2 {
		t.Fatalf("filtered pagination: len=%d total=%d", len(runs), total)
	}

	// Unknown schedule still 404s even with a filter present.
	resp, _ := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/missing/runs?status=error", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown schedule should 404, got %d", resp.StatusCode)
	}
}

func TestListScheduleRuns_FilterByVMID(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-multi", Name: "multi", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
		TagSelector: []string{"prod"},
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(started, vmID string, status types.ScheduleRunStatus) {
		t.Helper()
		at, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		if err := s.AppendRun("sched-multi", &types.ScheduleRun{
			VMID: vmID, StartedAt: at, Status: status,
		}); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	mk("2026-05-20T02:00:00Z", "vm-1", types.ScheduleRunStatusSuccess)
	mk("2026-05-21T02:00:00Z", "vm-2", types.ScheduleRunStatusError)
	mk("2026-05-22T02:00:00Z", "vm-1", types.ScheduleRunStatusSuccess)
	mk("2026-05-23T02:00:00Z", "vm-3", types.ScheduleRunStatusSkipped)

	list := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-multi/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	// Baseline: 4 runs, no filter.
	if runs, total, code := list(""); code != http.StatusOK || len(runs) != 4 || total != 4 {
		t.Fatalf("baseline: code=%d len=%d total=%d", code, len(runs), total)
	}

	// Exact match: vm_id=vm-1 returns 2 runs, post-filter X-Total-Count=2.
	runs, total, _ := list("?vm_id=vm-1")
	if len(runs) != 2 || total != 2 {
		t.Fatalf("vm_id=vm-1: len=%d total=%d", len(runs), total)
	}
	for _, r := range runs {
		if r.VMID != "vm-1" {
			t.Fatalf("vm_id=vm-1 leaked %q", r.VMID)
		}
	}

	// Exact match: vm_id=vm-3 returns the single skipped run.
	if runs, total, _ := list("?vm_id=vm-3"); len(runs) != 1 || total != 1 || runs[0].VMID != "vm-3" {
		t.Fatalf("vm_id=vm-3: %+v total=%d", runs, total)
	}

	// No match yields an empty (non-null) array + total 0.
	runs, total, code := list("?vm_id=vm-missing")
	if code != http.StatusOK || runs == nil || len(runs) != 0 || total != 0 {
		t.Fatalf("vm_id=vm-missing: code=%d runs=%v total=%d", code, runs, total)
	}

	// vm_id is whitespace-trimmed.
	if runs, _, _ := list("?vm_id=%20vm-2%20"); len(runs) != 1 || runs[0].VMID != "vm-2" {
		t.Fatalf("vm_id trim: %+v", runs)
	}

	// vm_id matching is case-sensitive: VM IDs are opaque vm-<unix-nano> strings.
	if runs, total, _ := list("?vm_id=VM-1"); len(runs) != 0 || total != 0 {
		t.Fatalf("vm_id case-sensitive: %+v total=%d", runs, total)
	}

	// Empty vm_id is treated as no filter (mirrors event handler vm_id contract).
	if runs, total, _ := list("?vm_id="); len(runs) != 4 || total != 4 {
		t.Fatalf("empty vm_id: len=%d total=%d", len(runs), total)
	}

	// Composes with status: vm-1 + success → 2 runs.
	if runs, total, _ := list("?vm_id=vm-1&status=success"); len(runs) != 2 || total != 2 {
		t.Fatalf("vm-1+success: len=%d total=%d", len(runs), total)
	}
	// vm-1 + status=error → 0 (only vm-2 errored).
	if runs, total, _ := list("?vm_id=vm-1&status=error"); len(runs) != 0 || total != 0 {
		t.Fatalf("vm-1+error: len=%d total=%d", len(runs), total)
	}

	// Composes with since/until: vm-1 + since 2026-05-22 → 1 run.
	if runs, total, _ := list("?vm_id=vm-1&since=2026-05-22T02:00:00Z"); len(runs) != 1 || total != 1 {
		t.Fatalf("vm-1+since: len=%d total=%d", len(runs), total)
	}

	// X-Total-Count reflects post-filter population under pagination.
	if runs, total, _ := list("?vm_id=vm-1&per_page=1"); len(runs) != 1 || total != 2 {
		t.Fatalf("vm-1 paginated: len=%d total=%d", len(runs), total)
	}
}

func TestListScheduleRuns_FilterBySearch(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-srch", Name: "srch", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(started string, status types.ScheduleRunStatus, errMsg string, skip types.ScheduleRunSkipReason) {
		t.Helper()
		at, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		if err := s.AppendRun("sched-srch", &types.ScheduleRun{
			VMID: "vm-1", StartedAt: at, Status: status, Error: errMsg, SkipReason: skip,
		}); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	// 4 runs with different error/skip-reason content the search filter
	// should be able to slice without leaking across statuses.
	mk("2026-05-20T02:00:00Z", types.ScheduleRunStatusError, "context deadline exceeded waiting for VM", "")
	mk("2026-05-21T02:00:00Z", types.ScheduleRunStatusError, "libvirt connection refused", "")
	mk("2026-05-22T02:00:00Z", types.ScheduleRunStatusSkipped, "", types.ScheduleRunSkipReasonVMAlreadyStopped)
	mk("2026-05-23T02:00:00Z", types.ScheduleRunStatusSuccess, "", "")

	list := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-srch/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	// Baseline: 4 runs, no filter.
	if runs, total, code := list(""); code != http.StatusOK || len(runs) != 4 || total != 4 {
		t.Fatalf("baseline: code=%d len=%d total=%d", code, len(runs), total)
	}

	// Empty search disables the filter (mirrors the events handler contract).
	if runs, total, _ := list("?search="); len(runs) != 4 || total != 4 {
		t.Fatalf("empty search: len=%d total=%d", len(runs), total)
	}

	// Substring match against the error field.
	if runs, total, _ := list("?search=deadline"); len(runs) != 1 || total != 1 || runs[0].Error == "" {
		t.Fatalf("search=deadline: %+v total=%d", runs, total)
	}

	// Case-insensitive ("LIBVIRT" matches lowercase "libvirt").
	if runs, total, _ := list("?search=LIBVIRT"); len(runs) != 1 || total != 1 {
		t.Fatalf("search=LIBVIRT: %+v total=%d", runs, total)
	}

	// Whitespace-trimmed (URL-encoded spaces around the needle).
	if runs, total, _ := list("?search=%20libvirt%20"); len(runs) != 1 || total != 1 {
		t.Fatalf("search whitespace: %+v total=%d", runs, total)
	}

	// Substring match against the skip_reason field (a typed
	// ScheduleRunSkipReason — round-tripped via string(...)).
	if runs, total, _ := list("?search=vm_already_stopped"); len(runs) != 1 || total != 1 {
		t.Fatalf("search=vm_already_stopped: %+v total=%d", runs, total)
	}

	// id / schedule_id / vm_id / status are intentionally NOT in the haystack.
	// vm-1 is shared by every run so a vm-1 search would match all four if it
	// were in the haystack — assert it returns 0.
	if runs, total, _ := list("?search=vm-1"); len(runs) != 0 || total != 0 {
		t.Fatalf("vm-1 should be excluded from search haystack: %+v total=%d", runs, total)
	}
	// "success" is a status string — it should NOT match (status is also excluded).
	if runs, total, _ := list("?search=success"); len(runs) != 0 || total != 0 {
		t.Fatalf("success should be excluded from search haystack: %+v total=%d", runs, total)
	}

	// Composes with status: search=context + status=error → 1 run.
	if runs, total, _ := list("?status=error&search=context"); len(runs) != 1 || total != 1 {
		t.Fatalf("status+search compose: %+v total=%d", runs, total)
	}
	// Composes with status: search=deadline + status=skipped → 0 (deadline is on an error).
	if runs, total, _ := list("?status=skipped&search=deadline"); len(runs) != 0 || total != 0 {
		t.Fatalf("status+search no-cross-bleed: %+v total=%d", runs, total)
	}

	// X-Total-Count reflects post-filter population under pagination.
	if runs, total, _ := list("?search=connection%20refused&per_page=1"); len(runs) != 1 || total != 1 {
		t.Fatalf("search paginated: %+v total=%d", runs, total)
	}
}

func TestListScheduleRuns_Sort(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-sort", Name: "sort", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(id, started, finished string, status types.ScheduleRunStatus) {
		t.Helper()
		startAt, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse started %q: %v", started, err)
		}
		run := &types.ScheduleRun{ID: id, VMID: "vm-1", StartedAt: startAt, Status: status}
		if finished != "" {
			finAt, err := time.Parse(time.RFC3339, finished)
			if err != nil {
				t.Fatalf("parse finished %q: %v", finished, err)
			}
			run.FinishedAt = &finAt
		}
		if err := s.AppendRun("sched-sort", run); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	mk("run-a", "2026-05-20T02:00:00Z", "2026-05-20T02:00:05Z", types.ScheduleRunStatusError)
	mk("run-b", "2026-05-21T02:00:00Z", "2026-05-21T02:00:02Z", types.ScheduleRunStatusSuccess)
	mk("run-c", "2026-05-22T02:00:00Z", "", types.ScheduleRunStatusRunning)

	list := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-sort/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	ids := func(runs []*types.ScheduleRun) []string {
		out := make([]string, 0, len(runs))
		for _, r := range runs {
			out = append(out, r.ID)
		}
		return out
	}

	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	if runs, total, code := list(""); code != http.StatusOK || total != 3 || !eq(ids(runs), []string{"run-c", "run-b", "run-a"}) {
		t.Fatalf("default order: code=%d ids=%v total=%d", code, ids(runs), total)
	}

	if runs, _, _ := list("?sort=started_at&order=asc"); !eq(ids(runs), []string{"run-a", "run-b", "run-c"}) {
		t.Fatalf("sort=started_at,asc: %v", ids(runs))
	}

	if runs, _, _ := list("?sort=finished_at&order=asc"); !eq(ids(runs), []string{"run-a", "run-b", "run-c"}) {
		t.Fatalf("sort=finished_at,asc nil-trailing: %v", ids(runs))
	}

	if runs, _, _ := list("?sort=finished_at&order=desc"); !eq(ids(runs), []string{"run-c", "run-b", "run-a"}) {
		t.Fatalf("sort=finished_at,desc nil-leading: %v", ids(runs))
	}

	if runs, _, _ := list("?sort=status&order=asc"); !eq(ids(runs), []string{"run-a", "run-c", "run-b"}) {
		t.Fatalf("sort=status,asc: %v", ids(runs))
	}

	if runs, _, _ := list("?sort=id&order=asc"); !eq(ids(runs), []string{"run-a", "run-b", "run-c"}) {
		t.Fatalf("sort=id,asc: %v", ids(runs))
	}

	if _, _, code := list("?sort=memory"); code != http.StatusBadRequest {
		t.Fatalf("sort=memory should 400, got %d", code)
	}

	if _, _, code := list("?sort=started_at&order=sideways"); code != http.StatusBadRequest {
		t.Fatalf("order=sideways should 400, got %d", code)
	}

	if runs, _, _ := list("?sort=%20STATUS%20&order=ASC"); !eq(ids(runs), []string{"run-a", "run-c", "run-b"}) {
		t.Fatalf("sort STATUS trimmed: %v", ids(runs))
	}

	if runs, total, _ := list("?status=error&sort=id&order=asc"); total != 1 || !eq(ids(runs), []string{"run-a"}) {
		t.Fatalf("sort+status compose: %v total=%d", ids(runs), total)
	}

	if runs, total, _ := list("?sort=started_at&order=asc&per_page=1"); total != 3 || !eq(ids(runs), []string{"run-a"}) {
		t.Fatalf("sort + per_page=1: %v total=%d", ids(runs), total)
	}
}

// TestListScheduleRuns_SortByDuration covers the ?sort=duration axis (5.4.63):
// runs are ordered by (finished_at - started_at). Runs with a nil
// finished_at have unknown duration and sink to the tail in ascending order
// (mirroring the finished_at nil-trailing semantics). The 400 invalid_sort
// message now lists "duration" alongside the other axes.
func TestListScheduleRuns_SortByDuration(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-dur", Name: "dur", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(id, started string, durationSeconds int, status types.ScheduleRunStatus, running bool) {
		t.Helper()
		startedAt, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		run := &types.ScheduleRun{ID: id, VMID: "vm-1", StartedAt: startedAt, Status: status}
		if !running {
			finishedAt := startedAt.Add(time.Duration(durationSeconds) * time.Second)
			run.FinishedAt = &finishedAt
		}
		if err := s.AppendRun("sched-dur", run); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	// Three completed runs with distinct durations (30s, 2m, 1h) and one
	// still-running run with no finished_at.
	mk("run-short", "2026-05-20T02:00:00Z", 30, types.ScheduleRunStatusSuccess, false)
	mk("run-medium", "2026-05-20T02:05:00Z", 120, types.ScheduleRunStatusSuccess, false)
	mk("run-long", "2026-05-20T02:10:00Z", 3600, types.ScheduleRunStatusError, false)
	mk("run-running", "2026-05-20T02:15:00Z", 0, types.ScheduleRunStatusRunning, true)

	list := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-dur/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	ids := func(runs []*types.ScheduleRun) []string {
		out := make([]string, 0, len(runs))
		for _, r := range runs {
			out = append(out, r.ID)
		}
		return out
	}

	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// sort=duration,asc: shortest concrete duration first; still-running sinks to the tail.
	if runs, _, code := list("?sort=duration&order=asc"); code != http.StatusOK || !eq(ids(runs), []string{"run-short", "run-medium", "run-long", "run-running"}) {
		t.Fatalf("duration asc: code=%d ids=%v", code, ids(runs))
	}

	// sort=duration,desc flips: nil-duration leads, then longest concrete.
	if runs, _, _ := list("?sort=duration&order=desc"); !eq(ids(runs), []string{"run-running", "run-long", "run-medium", "run-short"}) {
		t.Fatalf("duration desc: %v", ids(runs))
	}

	// Whitespace + case-insensitive sort=duration.
	if runs, _, _ := list("?sort=%20DURATION%20&order=asc"); !eq(ids(runs), []string{"run-short", "run-medium", "run-long", "run-running"}) {
		t.Fatalf("duration uppercase/whitespace: %v", ids(runs))
	}

	// Composes with status filter — only error/success runs remain, ordered by duration.
	if runs, total, _ := list("?sort=duration&order=asc&status=success"); total != 2 || !eq(ids(runs), []string{"run-short", "run-medium"}) {
		t.Fatalf("duration asc + status=success: %v total=%d", ids(runs), total)
	}

	// Pagination preserves post-filter total count.
	if runs, total, _ := list("?sort=duration&order=asc&per_page=2"); total != 4 || !eq(ids(runs), []string{"run-short", "run-medium"}) {
		t.Fatalf("duration asc paginated: %v total=%d", ids(runs), total)
	}

	// The invalid_sort sentinel must still reject unknown values now that
	// "duration" is a valid axis.
	if _, _, code := list("?sort=memory"); code != http.StatusBadRequest {
		t.Fatalf("sort=memory should 400, got %d", code)
	}
}

// TestListScheduleRuns_FilterByFinishedAt covers the ?finished_since= /
// ?finished_until= filter (5.4.62): inclusive RFC3339 bounds on the run's
// nullable FinishedAt. Runs with a nil FinishedAt (still-running) are
// filtered OUT when either bound is set, mirroring the next_fire_at filter
// on /schedules.
func TestListScheduleRuns_FilterByFinishedAt(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-fin", Name: "fin", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(started, finished string, status types.ScheduleRunStatus) {
		t.Helper()
		startedAt, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		run := &types.ScheduleRun{VMID: "vm-1", StartedAt: startedAt, Status: status}
		if finished != "" {
			finishedAt, err := time.Parse(time.RFC3339, finished)
			if err != nil {
				t.Fatalf("parse %q: %v", finished, err)
			}
			run.FinishedAt = &finishedAt
		}
		if err := s.AppendRun("sched-fin", run); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	mk("2026-05-20T02:00:00Z", "2026-05-20T02:05:00Z", types.ScheduleRunStatusSuccess)
	mk("2026-05-20T02:01:00Z", "2026-05-20T02:30:00Z", types.ScheduleRunStatusSuccess)
	mk("2026-05-20T02:02:00Z", "2026-05-20T03:15:00Z", types.ScheduleRunStatusError)
	mk("2026-05-20T02:03:00Z", "", types.ScheduleRunStatusRunning)

	list := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-fin/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	if runs, total, code := list(""); code != http.StatusOK || len(runs) != 4 || total != 4 {
		t.Fatalf("baseline: code=%d len=%d total=%d", code, len(runs), total)
	}

	if runs, total, _ := list("?finished_since=2026-05-20T02:10:00Z"); len(runs) != 2 || total != 2 {
		t.Fatalf("finished_since lower bound wrong: %+v total=%d", runs, total)
	}

	if runs, total, _ := list("?finished_until=2026-05-20T03:00:00Z"); len(runs) != 2 || total != 2 {
		t.Fatalf("finished_until upper bound wrong: %+v total=%d", runs, total)
	}

	if runs, total, _ := list("?finished_since=2026-05-20T02:10:00Z&finished_until=2026-05-20T03:00:00Z"); len(runs) != 1 || total != 1 {
		t.Fatalf("both bounds window wrong: %+v total=%d", runs, total)
	}

	if runs, total, _ := list("?finished_since=2000-01-01T00:00:00Z&finished_until=2100-01-01T00:00:00Z"); len(runs) != 3 || total != 3 {
		t.Fatalf("wide-open should exclude still-running: %+v total=%d", runs, total)
	}

	if runs, total, _ := list("?finished_since=&finished_until="); len(runs) != 4 || total != 4 {
		t.Fatalf("empty disables: %+v total=%d", runs, total)
	}

	if runs, total, _ := list("?finished_since=%202026-05-20T02:10:00Z%20"); len(runs) != 2 || total != 2 {
		t.Fatalf("whitespace-trim: %+v total=%d", runs, total)
	}

	if runs, total, _ := list("?status=success&finished_since=2026-05-20T02:10:00Z"); len(runs) != 1 || total != 1 {
		t.Fatalf("status+finished_since compose: %+v total=%d", runs, total)
	}

	if runs, total, _ := list("?since=2026-05-20T00:00:00Z&finished_since=2026-05-20T02:10:00Z&finished_until=2026-05-20T03:00:00Z"); len(runs) != 1 || total != 1 {
		t.Fatalf("started_at+finished compose: %+v total=%d", runs, total)
	}

	if _, _, code := list("?finished_since=garbage"); code != http.StatusBadRequest {
		t.Fatalf("invalid finished_since should 400, got %d", code)
	}
	if _, _, code := list("?finished_until=nope"); code != http.StatusBadRequest {
		t.Fatalf("invalid finished_until should 400, got %d", code)
	}

	if runs, total, _ := list("?finished_since=2026-05-20T02:10:00Z&per_page=1"); len(runs) != 1 || total != 2 {
		t.Fatalf("filtered pagination: %+v total=%d", runs, total)
	}
}

// TestListScheduleRuns_FilterByDuration covers the ?min_duration_ms= /
// ?max_duration_ms= filter (5.4.64): inclusive non-negative integer bounds
// on the run's finished_at - started_at duration in milliseconds. Runs with
// a nil FinishedAt (still-running) are filtered OUT when either bound is set,
// mirroring the finished_at range filter's nil-handling.
func TestListScheduleRuns_FilterByDuration(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-dur", Name: "dur", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(started, finished string, status types.ScheduleRunStatus) {
		t.Helper()
		startedAt, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		run := &types.ScheduleRun{VMID: "vm-1", StartedAt: startedAt, Status: status}
		if finished != "" {
			finishedAt, err := time.Parse(time.RFC3339, finished)
			if err != nil {
				t.Fatalf("parse %q: %v", finished, err)
			}
			run.FinishedAt = &finishedAt
		}
		if err := s.AppendRun("sched-dur", run); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	// Four runs: 5min, 29min, 73min, and one still-running.
	mk("2026-05-20T02:00:00Z", "2026-05-20T02:05:00Z", types.ScheduleRunStatusSuccess)
	mk("2026-05-20T02:01:00Z", "2026-05-20T02:30:00Z", types.ScheduleRunStatusSuccess)
	mk("2026-05-20T02:02:00Z", "2026-05-20T03:15:00Z", types.ScheduleRunStatusError)
	mk("2026-05-20T02:03:00Z", "", types.ScheduleRunStatusRunning)

	list := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-dur/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	if runs, total, code := list(""); code != http.StatusOK || len(runs) != 4 || total != 4 {
		t.Fatalf("baseline: code=%d len=%d total=%d", code, len(runs), total)
	}

	// 10 min lower bound: 29min + 73min match, the 5min and still-running are out.
	if runs, total, _ := list("?min_duration_ms=600000"); len(runs) != 2 || total != 2 {
		t.Fatalf("min_duration_ms lower bound wrong: %+v total=%d", runs, total)
	}

	// 30 min upper bound: 5min + 29min match (29min hits inclusive 30min), the 73min and still-running are out.
	if runs, total, _ := list("?max_duration_ms=1800000"); len(runs) != 2 || total != 2 {
		t.Fatalf("max_duration_ms upper bound wrong: %+v total=%d", runs, total)
	}

	// Window 10..60 min: only 29min matches.
	if runs, total, _ := list("?min_duration_ms=600000&max_duration_ms=3600000"); len(runs) != 1 || total != 1 {
		t.Fatalf("both bounds window wrong: %+v total=%d", runs, total)
	}

	// Inclusive boundaries: 5min = 300000ms, exact match included on both sides.
	if runs, total, _ := list("?min_duration_ms=300000&max_duration_ms=300000"); len(runs) != 1 || total != 1 {
		t.Fatalf("inclusive boundary wrong: %+v total=%d", runs, total)
	}

	// Wide-open should exclude the still-running run.
	if runs, total, _ := list("?min_duration_ms=0&max_duration_ms=2147483647"); len(runs) != 3 || total != 3 {
		t.Fatalf("wide-open should exclude still-running: %+v total=%d", runs, total)
	}

	// Empty values disable the filter — all four rows return.
	if runs, total, _ := list("?min_duration_ms=&max_duration_ms="); len(runs) != 4 || total != 4 {
		t.Fatalf("empty disables: %+v total=%d", runs, total)
	}

	// Whitespace is trimmed.
	if runs, total, _ := list("?min_duration_ms=%20600000%20"); len(runs) != 2 || total != 2 {
		t.Fatalf("whitespace-trim: %+v total=%d", runs, total)
	}

	// Composes with status: only the success run with ≥10min duration.
	if runs, total, _ := list("?status=success&min_duration_ms=600000"); len(runs) != 1 || total != 1 {
		t.Fatalf("status+min_duration_ms compose: %+v total=%d", runs, total)
	}

	// Composes with started_at range + finished_at range.
	if runs, total, _ := list("?since=2026-05-20T00:00:00Z&min_duration_ms=600000&max_duration_ms=3600000"); len(runs) != 1 || total != 1 {
		t.Fatalf("started_at+duration compose: %+v total=%d", runs, total)
	}

	// Invalid (non-numeric or negative) returns 400.
	if _, _, code := list("?min_duration_ms=garbage"); code != http.StatusBadRequest {
		t.Fatalf("invalid min_duration_ms should 400, got %d", code)
	}
	if _, _, code := list("?max_duration_ms=nope"); code != http.StatusBadRequest {
		t.Fatalf("invalid max_duration_ms should 400, got %d", code)
	}
	if _, _, code := list("?min_duration_ms=-1"); code != http.StatusBadRequest {
		t.Fatalf("negative min_duration_ms should 400, got %d", code)
	}

	// Filtered pagination: total reflects the post-filter count, not page size.
	if runs, total, _ := list("?min_duration_ms=600000&per_page=1"); len(runs) != 1 || total != 2 {
		t.Fatalf("filtered pagination: %+v total=%d", runs, total)
	}
}

// TestListScheduleRuns_FilterBySkipReason covers the ?skip_reason= filter
// (5.4.65): case-insensitive exact-match on the run's skip_reason field.
// Runs with an empty skip_reason (every non-skipped run, and any skipped
// run persisted without a reason) are filtered OUT whenever the filter is
// set — the symmetric categorical sub-axis to ?status=skipped.
func TestListScheduleRuns_FilterBySkipReason(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-skip", Name: "skip", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(started string, status types.ScheduleRunStatus, reason types.ScheduleRunSkipReason) {
		t.Helper()
		at, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		if err := s.AppendRun("sched-skip", &types.ScheduleRun{
			VMID: "vm-1", StartedAt: at, Status: status, SkipReason: reason,
		}); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	// 5 runs:
	// - 2 success runs (no skip_reason)
	// - 1 skipped/queue_full
	// - 1 skipped/vm_already_stopped
	// - 1 skipped/concurrent_run
	mk("2026-05-20T02:00:00Z", types.ScheduleRunStatusSuccess, "")
	mk("2026-05-21T02:00:00Z", types.ScheduleRunStatusSkipped, types.ScheduleRunSkipReasonQueueFull)
	mk("2026-05-22T02:00:00Z", types.ScheduleRunStatusSkipped, types.ScheduleRunSkipReasonVMAlreadyStopped)
	mk("2026-05-23T02:00:00Z", types.ScheduleRunStatusSuccess, "")
	mk("2026-05-24T02:00:00Z", types.ScheduleRunStatusSkipped, types.ScheduleRunSkipReasonConcurrentRun)

	list := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-skip/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	// Baseline: 5 runs, no filter.
	if runs, total, code := list(""); code != http.StatusOK || len(runs) != 5 || total != 5 {
		t.Fatalf("baseline: code=%d len=%d total=%d", code, len(runs), total)
	}

	// Exact match: queue_full → 1 run.
	runs, total, _ := list("?skip_reason=queue_full")
	if len(runs) != 1 || total != 1 || runs[0].SkipReason != types.ScheduleRunSkipReasonQueueFull {
		t.Fatalf("queue_full: %+v total=%d", runs, total)
	}

	// vm_already_stopped → 1 run.
	if runs, total, _ := list("?skip_reason=vm_already_stopped"); len(runs) != 1 || total != 1 || runs[0].SkipReason != types.ScheduleRunSkipReasonVMAlreadyStopped {
		t.Fatalf("vm_already_stopped: %+v total=%d", runs, total)
	}

	// Case-insensitive: VM_ALREADY_STOPPED.
	if runs, total, _ := list("?skip_reason=VM_ALREADY_STOPPED"); len(runs) != 1 || total != 1 {
		t.Fatalf("case-insensitive: %+v total=%d", runs, total)
	}

	// Whitespace is trimmed.
	if runs, total, _ := list("?skip_reason=%20queue_full%20"); len(runs) != 1 || total != 1 {
		t.Fatalf("whitespace-trim: %+v total=%d", runs, total)
	}

	// Empty disables the filter — all 5 rows return.
	if runs, total, _ := list("?skip_reason="); len(runs) != 5 || total != 5 {
		t.Fatalf("empty disables: %+v total=%d", runs, total)
	}

	// Recognized-but-unused reason returns 0 (no leak from other reasons).
	if runs, total, _ := list("?skip_reason=catch_up_skipped"); len(runs) != 0 || total != 0 {
		t.Fatalf("catch_up_skipped: %+v total=%d", runs, total)
	}

	// Composes with status: status=skipped + skip_reason=queue_full → 1 run.
	if runs, total, _ := list("?status=skipped&skip_reason=queue_full"); len(runs) != 1 || total != 1 {
		t.Fatalf("status+skip_reason compose: %+v total=%d", runs, total)
	}

	// Composes with status: status=success + skip_reason=queue_full → 0 runs
	// (success runs have empty skip_reason, so the skip_reason filter excludes them).
	if runs, total, _ := list("?status=success&skip_reason=queue_full"); len(runs) != 0 || total != 0 {
		t.Fatalf("status=success+skip_reason should be empty: %+v total=%d", runs, total)
	}

	// Composes with started_at since: skip_reason=concurrent_run + since 2026-05-23 → 1 run.
	if runs, total, _ := list("?skip_reason=concurrent_run&since=2026-05-23T00:00:00Z"); len(runs) != 1 || total != 1 {
		t.Fatalf("skip_reason+since compose: %+v total=%d", runs, total)
	}

	// Invalid value returns 400 invalid_skip_reason.
	if _, _, code := list("?skip_reason=garbage"); code != http.StatusBadRequest {
		t.Fatalf("invalid skip_reason should 400, got %d", code)
	}

	// Filtered pagination: total reflects the post-filter count.
	// Seed an extra queue_full skip to exercise the paginated path.
	mk("2026-05-25T02:00:00Z", types.ScheduleRunStatusSkipped, types.ScheduleRunSkipReasonQueueFull)
	if runs, total, _ := list("?skip_reason=queue_full&per_page=1"); len(runs) != 1 || total != 2 {
		t.Fatalf("filtered pagination: %+v total=%d", runs, total)
	}
}
