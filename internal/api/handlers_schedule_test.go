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
	"strings"
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

// TestListSchedules_FilterByLastFired covers the ?last_fired_since= /
// ?last_fired_until= filter (5.4.74): inclusive RFC3339 bounds on the
// stored LastFiredAt, schedules with a nil LastFiredAt filtered OUT
// whenever any bound is set, 400 on garbage values, composes additively
// with action / created_at since/until / next_fire range / pagination.
func TestListSchedules_FilterByLastFired(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	mk := func(id, name, lastFired string) {
		t.Helper()
		sched := &types.Schedule{
			ID:        id,
			Name:      name,
			Action:    types.ScheduleActionSnapshot,
			CronSpec:  "0 0 2 * * *",
			Enabled:   true,
			CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		}
		if lastFired != "" {
			parsed, err := time.Parse(time.RFC3339, lastFired)
			if err != nil {
				t.Fatalf("parse %q: %v", lastFired, err)
			}
			sched.LastFiredAt = &parsed
		}
		if err := s.PutSchedule(sched); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	mk("sched-1", "early", "2026-06-01T12:00:00Z")
	mk("sched-2", "mid", "2026-06-01T15:00:00Z")
	mk("sched-3", "late", "2026-06-01T20:00:00Z")
	mk("sched-4", "neverfired", "") // never-fired — nil LastFiredAt

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

	// Baseline: no filter returns all four (including nil-LastFiredAt).
	if out, total, code := list(""); code != http.StatusOK || len(out) != 4 || total != 4 {
		t.Fatalf("baseline: code=%d len=%d total=%d", code, len(out), total)
	}

	// last_fired_since alone: inclusive lower bound drops the early one.
	// The neverfired schedule must be filtered OUT under any bound.
	if out, total, _ := list("?last_fired_since=2026-06-01T15:00:00Z"); len(out) != 2 || total != 2 {
		t.Fatalf("last_fired_since lower bound wrong: %+v total=%d", out, total)
	}

	// last_fired_until alone: inclusive upper bound.
	if out, total, _ := list("?last_fired_until=2026-06-01T15:00:00Z"); len(out) != 2 || total != 2 {
		t.Fatalf("last_fired_until upper bound wrong: %+v total=%d", out, total)
	}

	// Both bounds: only mid falls in the window.
	if out, total, _ := list("?last_fired_since=2026-06-01T13:00:00Z&last_fired_until=2026-06-01T18:00:00Z"); len(out) != 1 || total != 1 || out[0].Name != "mid" {
		t.Fatalf("range window wrong: %+v total=%d", out, total)
	}

	// nil-LastFiredAt schedule is excluded under any bound.
	if out, _, _ := list("?last_fired_since=2000-01-01T00:00:00Z&last_fired_until=2100-01-01T00:00:00Z"); len(out) != 3 {
		t.Fatalf("nil LastFiredAt should be excluded under bounds, got %d", len(out))
	}

	// Empty values disable both bounds (no-op).
	if out, total, _ := list("?last_fired_since=&last_fired_until="); len(out) != 4 || total != 4 {
		t.Fatalf("empty bounds should be no-op: %+v total=%d", out, total)
	}

	// Whitespace-trimmed.
	if out, total, _ := list("?last_fired_since=%202026-06-01T15:00:00Z%20"); len(out) != 2 || total != 2 {
		t.Fatalf("whitespace-trim wrong: %+v total=%d", out, total)
	}

	// Composes additively with action (all four are snapshot).
	if out, total, _ := list("?action=snapshot&last_fired_since=2026-06-01T17:00:00Z"); len(out) != 1 || total != 1 || out[0].Name != "late" {
		t.Fatalf("action+last_fired_since compose wrong: %+v total=%d", out, total)
	}

	// Composes additively with the existing since (on created_at) — every schedule
	// has created_at on 2026-05-01, so since=2026-04-01 leaves all in scope but
	// the last_fired bound still narrows.
	if out, total, _ := list("?since=2026-04-01T00:00:00Z&last_fired_until=2026-06-01T13:00:00Z"); len(out) != 1 || total != 1 || out[0].Name != "early" {
		t.Fatalf("since(created_at)+last_fired_until compose wrong: %+v total=%d", out, total)
	}

	// Invalid bounds → 400 with typed error codes.
	if _, _, code := list("?last_fired_since=nope"); code != http.StatusBadRequest {
		t.Fatalf("invalid last_fired_since should 400, got %d", code)
	}
	if _, _, code := list("?last_fired_until=garbage"); code != http.StatusBadRequest {
		t.Fatalf("invalid last_fired_until should 400, got %d", code)
	}

	// X-Total-Count reflects the post-filter population under pagination.
	if out, total, _ := list("?last_fired_since=2026-06-01T13:00:00Z&per_page=1"); len(out) != 1 || total != 2 {
		t.Fatalf("paginated post-filter total wrong: len=%d total=%d", len(out), total)
	}
}

// TestListSchedules_SortByLastFired covers the 5.4.84 sort axis: ascending puts
// the earliest concrete LastFiredAt first and pushes never-fired schedules to
// the tail; descending flips that — never-fired schedules come first, then
// concrete last-fires from newest to oldest. All comparators tiebreak on ID
// for deterministic pagination. Mirrors the next_fire_at sort axis exactly.
func TestListSchedules_SortByLastFired(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	mk := func(id, name, lastFired string) {
		t.Helper()
		sched := &types.Schedule{
			ID:        id,
			Name:      name,
			Action:    types.ScheduleActionSnapshot,
			CronSpec:  "0 0 2 * * *",
			Enabled:   true,
			CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		}
		if lastFired != "" {
			parsed, err := time.Parse(time.RFC3339, lastFired)
			if err != nil {
				t.Fatalf("parse %q: %v", lastFired, err)
			}
			sched.LastFiredAt = &parsed
		}
		if err := s.PutSchedule(sched); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	mk("sched-1", "early", "2026-06-01T12:00:00Z")
	mk("sched-2", "mid", "2026-06-01T15:00:00Z")
	mk("sched-3", "late", "2026-06-01T20:00:00Z")
	mk("sched-4", "neverfired", "")

	list := func(q string) ([]*types.Schedule, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list%s: HTTP %d: %s", q, resp.StatusCode, data)
		}
		var out []*types.Schedule
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total
	}
	ids := func(items []*types.Schedule) []string {
		out := make([]string, 0, len(items))
		for _, s := range items {
			out = append(out, s.ID)
		}
		return out
	}
	eq := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	// asc — earliest concrete last_fired first, never-fired sinks to tail.
	if out, total := list("?sort=last_fired_at&order=asc"); !eq(ids(out), []string{"sched-1", "sched-2", "sched-3", "sched-4"}) || total != 4 {
		t.Fatalf("last_fired_at asc nil-trailing wrong: %v total=%d", ids(out), total)
	}

	// desc — never-fired surfaces first, concrete last_fires newest first.
	if out, total := list("?sort=last_fired_at&order=desc"); !eq(ids(out), []string{"sched-4", "sched-3", "sched-2", "sched-1"}) || total != 4 {
		t.Fatalf("last_fired_at desc nil-leading wrong: %v total=%d", ids(out), total)
	}

	// Default order is asc — matches the schedule list contract.
	if out, _ := list("?sort=last_fired_at"); !eq(ids(out), []string{"sched-1", "sched-2", "sched-3", "sched-4"}) {
		t.Fatalf("last_fired_at default-order wrong: %v", ids(out))
	}

	// Composes with the existing last_fired range filter: narrow to two
	// concrete-fire schedules, then sort within the cohort.
	if out, total := list("?last_fired_since=2026-06-01T13:00:00Z&sort=last_fired_at&order=asc"); !eq(ids(out), []string{"sched-2", "sched-3"}) || total != 2 {
		t.Fatalf("filter+sort compose wrong: %v total=%d", ids(out), total)
	}
}

// TestListSchedules_SortByLastFired_TiebreakOnID asserts the deterministic id
// tiebreak holds when multiple schedules share the same LastFiredAt — the same
// paginated-determinism contract every other sort axis upholds.
func TestListSchedules_SortByLastFired_TiebreakOnID(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	stamp := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"sched-z", "sched-a", "sched-m"} {
		sched := &types.Schedule{
			ID:          id,
			Name:        id,
			Action:      types.ScheduleActionSnapshot,
			CronSpec:    "0 0 2 * * *",
			Enabled:     true,
			CreatedAt:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			LastFiredAt: &stamp,
		}
		if err := s.PutSchedule(sched); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}

	resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules?sort=last_fired_at&order=asc", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d: %s", resp.StatusCode, data)
	}
	var out []*types.Schedule
	json.Unmarshal(data, &out)
	wantOrder := []string{"sched-a", "sched-m", "sched-z"}
	for i, w := range wantOrder {
		if out[i].ID != w {
			t.Fatalf("equal-keys tiebreak: idx %d got %s, want %s; full=%+v", i, out[i].ID, w, out)
		}
	}
}

// TestListSchedules_SortByLastFired_400InvalidSortRejected verifies the new
// sort axis is wired through the existing 400 path. The error message must
// also enumerate last_fired_at so clients see the supported set.
func TestListSchedules_SortByLastFired_400InvalidSortRejected(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()

	resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules?sort=nope", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid sort should 400, got %d", resp.StatusCode)
	}
	var er errorResponse
	json.Unmarshal(data, &er)
	if er.Code != "invalid_sort" {
		t.Fatalf("expected invalid_sort, got %q", er.Code)
	}
	// The error message must include last_fired_at so clients learn the
	// full supported set on rejection.
	if !bytes.Contains(data, []byte("last_fired_at")) {
		t.Fatalf("error message should advertise last_fired_at, got: %s", data)
	}
}

// TestListSchedules_SortByVMID covers the 5.4.97 vm_id sort axis on the
// schedules list — the symmetric sort counterpart to the existing
// case-sensitive ?vm_id= exact-match filter on the same column. Mirrors
// the events vm_id sort axis (5.4.93), the logs vm_id sort axis (5.4.94),
// and the schedule-runs vm_id sort axis (5.4.95): case-sensitive ASCII
// compare on the vm_id field with empty vm_id (tag_selector-targeted or
// all-VMs schedules) sinking to the tail in asc / head in desc.
func TestListSchedules_SortByVMID(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	// Four schedules: empty vm_id (all-VMs or tag_selector), two distinct
	// VMs case-sensitively offset, and one duplicate that exercises the
	// id tiebreak.
	mk := func(id, vmID string) {
		t.Helper()
		if err := s.PutSchedule(&types.Schedule{
			ID: id, Name: id, VMID: vmID, Action: types.ScheduleActionSnapshot,
			CronSpec: "0 0 2 * * *", Enabled: true,
		}); err != nil {
			t.Fatalf("put schedule: %v", err)
		}
	}
	mk("sched-empty", "")
	mk("sched-up", "vm-A")
	mk("sched-mid", "vm-b")
	mk("sched-low", "vm-c")

	list := func(q string) ([]*types.Schedule, int, int, string) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		var out []*types.Schedule
		_ = json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode, string(data)
	}

	ids := func(items []*types.Schedule) []string {
		out := make([]string, 0, len(items))
		for _, x := range items {
			out = append(out, x.ID)
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

	// sort=vm_id,asc: case-sensitive ASCII order ('A' < 'b' < 'c'); empty
	// vm_id sinks to the tail.
	if items, _, code, body := list("?sort=vm_id&order=asc"); code != http.StatusOK || !eq(ids(items), []string{"sched-up", "sched-mid", "sched-low", "sched-empty"}) {
		t.Fatalf("vm_id asc: code=%d ids=%v body=%s", code, ids(items), body)
	}

	// sort=vm_id,desc flips: empty leads, then descending ASCII.
	if items, _, _, _ := list("?sort=vm_id&order=desc"); !eq(ids(items), []string{"sched-empty", "sched-low", "sched-mid", "sched-up"}) {
		t.Fatalf("vm_id desc: %v", ids(items))
	}

	// Whitespace + case-insensitive normalisation on the sort param value.
	if items, _, _, _ := list("?sort=%20VM_ID%20&order=asc"); !eq(ids(items), []string{"sched-up", "sched-mid", "sched-low", "sched-empty"}) {
		t.Fatalf("vm_id whitespace+upper: %v", ids(items))
	}

	// Pagination preserves post-filter total count.
	if items, total, _, _ := list("?sort=vm_id&order=asc&per_page=2"); total != 4 || !eq(ids(items), []string{"sched-up", "sched-mid"}) {
		t.Fatalf("vm_id paginated: %v total=%d", ids(items), total)
	}

	// Composes with the ?vm_id= filter: filter narrows to one VM, sort
	// tiebreaks on id deterministically among duplicates.
	mk("sched-dup1", "vm-b")
	mk("sched-dup2", "vm-b")
	if items, total, _, _ := list("?sort=vm_id&order=asc&vm_id=vm-b"); total != 3 || !eq(ids(items), []string{"sched-dup1", "sched-dup2", "sched-mid"}) {
		t.Fatalf("vm_id asc + ?vm_id= filter: %v total=%d", ids(items), total)
	}
}

// TestListSchedules_SortByVMID_400InvalidSortAdvertisesVMID asserts the 400
// envelope mentions vm_id so operators discover the new axis from the
// daemon's error text (5.4.97 ergonomic contract — mirrors the equivalent
// check on events / logs / schedule-runs sort axes).
func TestListSchedules_SortByVMID_400InvalidSortAdvertisesVMID(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()
	resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules?sort=garbage", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if !bytes.Contains(data, []byte("vm_id")) {
		t.Fatalf("invalid_sort envelope must mention vm_id: %s", data)
	}
}

// TestListSchedules_SortByAction covers the 5.4.99 action sort axis on
// the schedules list — the symmetric sort counterpart to the existing
// case-insensitive ?action= exact-match filter on the same column.
// Diverges from the nil-trailing convention because action is closed
// and total (every schedule resolves to exactly one of the four values
// at create time), mirroring the webhook delivery_status sort axis
// (5.4.98) divergence rationale.
func TestListSchedules_SortByAction(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	mk := func(id string, action types.ScheduleAction) {
		t.Helper()
		if err := s.PutSchedule(&types.Schedule{
			ID: id, Name: id, VMID: "vm-1", Action: action,
			CronSpec: "0 0 2 * * *", Enabled: true,
		}); err != nil {
			t.Fatalf("put schedule: %v", err)
		}
	}
	mk("sched-stop", types.ScheduleActionStop)
	mk("sched-start", types.ScheduleActionStart)
	mk("sched-snapshot", types.ScheduleActionSnapshot)
	mk("sched-restart", types.ScheduleActionRestart)

	list := func(q string) ([]*types.Schedule, int, int, string) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules"+q, nil)
		var out []*types.Schedule
		_ = json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode, string(data)
	}

	ids := func(items []*types.Schedule) []string {
		out := make([]string, 0, len(items))
		for _, x := range items {
			out = append(out, x.ID)
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

	// sort=action,asc: case-folded alphabetical order
	// (restart < snapshot < start < stop).
	if items, _, code, body := list("?sort=action&order=asc"); code != http.StatusOK || !eq(ids(items), []string{"sched-restart", "sched-snapshot", "sched-start", "sched-stop"}) {
		t.Fatalf("action asc: code=%d ids=%v body=%s", code, ids(items), body)
	}

	// sort=action,desc flips the asc ordering.
	if items, _, _, _ := list("?sort=action&order=desc"); !eq(ids(items), []string{"sched-stop", "sched-start", "sched-snapshot", "sched-restart"}) {
		t.Fatalf("action desc: %v", ids(items))
	}

	// Whitespace + case-insensitive normalisation on the sort param value.
	if items, _, _, _ := list("?sort=%20ACTION%20&order=asc"); !eq(ids(items), []string{"sched-restart", "sched-snapshot", "sched-start", "sched-stop"}) {
		t.Fatalf("action whitespace+upper: %v", ids(items))
	}

	// Pagination preserves post-filter total count.
	if items, total, _, _ := list("?sort=action&order=asc&per_page=2"); total != 4 || !eq(ids(items), []string{"sched-restart", "sched-snapshot"}) {
		t.Fatalf("action paginated: %v total=%d", ids(items), total)
	}

	// Composes with the ?action= filter: filter narrows to one action,
	// sort tiebreaks deterministically on id among duplicates.
	mk("sched-snap-1", types.ScheduleActionSnapshot)
	mk("sched-snap-2", types.ScheduleActionSnapshot)
	if items, total, _, _ := list("?sort=action&order=asc&action=snapshot"); total != 3 || !eq(ids(items), []string{"sched-snap-1", "sched-snap-2", "sched-snapshot"}) {
		t.Fatalf("action asc + ?action= filter: %v total=%d", ids(items), total)
	}
}

// TestListSchedules_SortByAction_400InvalidSortAdvertisesAction asserts
// the 400 envelope mentions action so operators discover the new axis
// from the daemon's error text (5.4.99 ergonomic contract — mirrors the
// equivalent check on the vm_id / delivery_status sort axes).
func TestListSchedules_SortByAction_400InvalidSortAdvertisesAction(t *testing.T) {
	ts, _, cleanup := testScheduleServer(t)
	defer cleanup()
	resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules?sort=garbage", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if !bytes.Contains(data, []byte("action")) {
		t.Fatalf("invalid_sort envelope must mention action: %s", data)
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

// TestListScheduleRuns_SortByVMID covers the ?sort=vm_id axis (5.4.95):
// the symmetric sort counterpart to the case-sensitive ?vm_id= exact-match
// filter on /schedules/{id}/runs. Mirrors the events vm_id sort axis
// (5.4.93) and the logs vm_id sort axis (5.4.94) — case-sensitive ASCII
// compare with empty vm_id sinking to the tail in asc / head in desc. The
// 400 invalid_sort envelope must advertise "vm_id".
func TestListScheduleRuns_SortByVMID(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-vm", Name: "vm", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(id, vmID, started string) {
		t.Helper()
		startedAt, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		finishedAt := startedAt.Add(time.Second)
		if err := s.AppendRun("sched-vm", &types.ScheduleRun{
			ID: id, VMID: vmID, StartedAt: startedAt, FinishedAt: &finishedAt,
			Status: types.ScheduleRunStatusSuccess,
		}); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	// Four runs: empty vm_id (e.g. queue_full skip on all-VMs), two distinct
	// VMs case-sensitively offset, and one duplicate that exercises the
	// id tiebreak.
	mk("run-empty", "", "2026-05-20T02:00:00Z")
	mk("run-up", "vm-A", "2026-05-20T02:01:00Z")
	mk("run-mid", "vm-b", "2026-05-20T02:02:00Z")
	mk("run-low", "vm-c", "2026-05-20T02:03:00Z")

	list := func(q string) ([]*types.ScheduleRun, int, int, string) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-vm/runs"+q, nil)
		var out []*types.ScheduleRun
		_ = json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode, string(data)
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

	// sort=vm_id,asc: case-sensitive ASCII ordering ('A' < 'b' < 'c');
	// empty vm_id sinks to the tail.
	if runs, _, code, body := list("?sort=vm_id&order=asc"); code != http.StatusOK || !eq(ids(runs), []string{"run-up", "run-mid", "run-low", "run-empty"}) {
		t.Fatalf("vm_id asc: code=%d ids=%v body=%s", code, ids(runs), body)
	}

	// sort=vm_id,desc flips: empty vm_id leads, then descending ASCII.
	if runs, _, _, _ := list("?sort=vm_id&order=desc"); !eq(ids(runs), []string{"run-empty", "run-low", "run-mid", "run-up"}) {
		t.Fatalf("vm_id desc: %v", ids(runs))
	}

	// Whitespace + case-insensitive normalisation on the sort param.
	if runs, _, _, _ := list("?sort=%20VM_ID%20&order=asc"); !eq(ids(runs), []string{"run-up", "run-mid", "run-low", "run-empty"}) {
		t.Fatalf("vm_id whitespace+upper: %v", ids(runs))
	}

	// Pagination preserves the post-filter total count.
	if runs, total, _, _ := list("?sort=vm_id&order=asc&per_page=2"); total != 4 || !eq(ids(runs), []string{"run-up", "run-mid"}) {
		t.Fatalf("vm_id asc paginated: %v total=%d", ids(runs), total)
	}

	// Composes with the ?vm_id= filter: filter narrows to one VM, then
	// the sort tiebreak on id orders deterministically among duplicates.
	mk("run-dup1", "vm-b", "2026-05-20T02:04:00Z")
	mk("run-dup2", "vm-b", "2026-05-20T02:05:00Z")
	if runs, total, _, _ := list("?sort=vm_id&order=asc&vm_id=vm-b"); total != 3 || !eq(ids(runs), []string{"run-dup1", "run-dup2", "run-mid"}) {
		t.Fatalf("vm_id asc + ?vm_id= filter: %v total=%d", ids(runs), total)
	}
}

// TestListScheduleRuns_InvalidSortAdvertisesVMID asserts the 400
// envelope mentions vm_id so operators discover the new axis from the
// daemon's error text (5.4.95 ergonomic contract — mirrors the
// equivalent check on logs / events sort axes).
func TestListScheduleRuns_InvalidSortAdvertisesVMID(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()
	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-err", Name: "err", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-err/runs?sort=garbage", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(data))
	}
	if !strings.Contains(string(data), "vm_id") {
		t.Fatalf("invalid_sort message should advertise vm_id, got %s", string(data))
	}
	if !strings.Contains(string(data), "invalid_sort") {
		t.Fatalf("expected invalid_sort code, got %s", string(data))
	}
}

// TestListScheduleRuns_SortBySkipReason covers the ?sort=skip_reason axis
// (5.4.96): runs are ordered alphabetically by their skip_reason field. Runs
// with an empty skip_reason (every non-skipped run, plus skipped runs
// persisted without a reason) sink to the tail in asc and head in desc,
// mirroring the finished_at / duration nil-trailing semantics. The 400
// invalid_sort message now lists "skip_reason" alongside the other axes.
func TestListScheduleRuns_SortBySkipReason(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-sr", Name: "sr", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}

	mk := func(id, started string, status types.ScheduleRunStatus, reason types.ScheduleRunSkipReason) {
		t.Helper()
		startedAt, err := time.Parse(time.RFC3339, started)
		if err != nil {
			t.Fatalf("parse %q: %v", started, err)
		}
		run := &types.ScheduleRun{ID: id, VMID: "vm-1", StartedAt: startedAt, Status: status, SkipReason: reason}
		if err := s.AppendRun("sched-sr", run); err != nil {
			t.Fatalf("append run: %v", err)
		}
	}
	mk("run-queue", "2026-05-20T02:00:00Z", types.ScheduleRunStatusSkipped, types.ScheduleRunSkipReasonQueueFull)
	mk("run-success", "2026-05-20T02:01:00Z", types.ScheduleRunStatusSuccess, "")
	mk("run-catchup", "2026-05-20T02:02:00Z", types.ScheduleRunStatusSkipped, types.ScheduleRunSkipReasonCatchUpSkipped)
	mk("run-error", "2026-05-20T02:03:00Z", types.ScheduleRunStatusError, "")

	listSR := func(q string) ([]*types.ScheduleRun, int, int) {
		t.Helper()
		resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-sr/runs"+q, nil)
		var out []*types.ScheduleRun
		json.Unmarshal(data, &out)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return out, total, resp.StatusCode
	}

	idsSR := func(runs []*types.ScheduleRun) []string {
		out := make([]string, 0, len(runs))
		for _, r := range runs {
			out = append(out, r.ID)
		}
		return out
	}

	eqSR := func(got, want []string) bool {
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

	// sort=skip_reason,asc: populated reasons alphabetical (catch_up_skipped < queue_full);
	// empty-reason runs sink to the tail tiebroken by id.
	if runs, _, code := listSR("?sort=skip_reason&order=asc"); code != http.StatusOK || !eqSR(idsSR(runs), []string{"run-catchup", "run-queue", "run-error", "run-success"}) {
		t.Fatalf("skip_reason asc: code=%d ids=%v", code, idsSR(runs))
	}

	// sort=skip_reason,desc flips: empty-reason runs head the list (tiebroken
	// by id descending — run-success > run-error), then populated reasons
	// descending alphabetically.
	if runs, _, _ := listSR("?sort=skip_reason&order=desc"); !eqSR(idsSR(runs), []string{"run-success", "run-error", "run-queue", "run-catchup"}) {
		t.Fatalf("skip_reason desc: %v", idsSR(runs))
	}

	// Whitespace + case-insensitive sort=skip_reason.
	if runs, _, _ := listSR("?sort=%20SKIP_REASON%20&order=asc"); !eqSR(idsSR(runs), []string{"run-catchup", "run-queue", "run-error", "run-success"}) {
		t.Fatalf("skip_reason uppercase/whitespace: %v", idsSR(runs))
	}

	// Composes with the ?skip_reason= filter — only the catch_up_skipped run
	// remains, ordered by skip_reason (one row).
	if runs, total, _ := listSR("?sort=skip_reason&order=asc&skip_reason=catch_up_skipped"); total != 1 || !eqSR(idsSR(runs), []string{"run-catchup"}) {
		t.Fatalf("skip_reason asc + filter: %v total=%d", idsSR(runs), total)
	}

	// Pagination preserves post-filter total count.
	if runs, total, _ := listSR("?sort=skip_reason&order=asc&per_page=2"); total != 4 || !eqSR(idsSR(runs), []string{"run-catchup", "run-queue"}) {
		t.Fatalf("skip_reason asc paginated: %v total=%d", idsSR(runs), total)
	}
}

// TestListScheduleRuns_InvalidSortAdvertisesSkipReason asserts the 400
// invalid_sort envelope advertises `skip_reason` in the supported set so
// operators discover the new axis from the error path.
func TestListScheduleRuns_InvalidSortAdvertisesSkipReason(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()
	if err := s.PutSchedule(&types.Schedule{
		ID: "sched-x", Name: "x", Action: types.ScheduleActionSnapshot,
		CronSpec: "0 0 2 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("put schedule: %v", err)
	}
	resp, data := schedDo(t, http.MethodGet, ts.URL+"/api/v1/schedules/sched-x/runs?sort=memory", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(data), "skip_reason") {
		t.Fatalf("invalid_sort envelope must mention skip_reason: %s", string(data))
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

// TestListSchedules_FilterByPrefix covers the ?prefix= filter (5.4.82): the
// fifth and final name-prefix axis after snapshots (5.4.75), VMs (5.4.76),
// images (5.4.77), and templates (5.4.78). Case-sensitive HasPrefix on the
// schedule name; whitespace-trimmed; empty disables; composes additively with
// every other schedule filter and reflects correctly in X-Total-Count under
// pagination.
func TestListSchedules_FilterByPrefix(t *testing.T) {
	ts, s, cleanup := testScheduleServerStore(t)
	defer cleanup()

	mk := func(id, name string, enabled bool) {
		t.Helper()
		sched := &types.Schedule{
			ID:        id,
			Name:      name,
			Action:    types.ScheduleActionSnapshot,
			CronSpec:  "0 0 2 * * *",
			Enabled:   enabled,
			CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		}
		if err := s.PutSchedule(sched); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	mk("sched-1", "nightly-snapshot", true)
	mk("sched-2", "nightly-restart", true)
	mk("sched-3", "weekly-backup", false)
	mk("sched-4", "Nightly-Audit", true) // capital-N — must NOT match `nightly-`

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

	// Prefix matches the two `nightly-` schedules. Capital-N is excluded
	// (case-sensitive).
	if out, total, _ := list("?prefix=nightly-"); len(out) != 2 || total != 2 {
		t.Fatalf("prefix=nightly-: %+v total=%d", out, total)
	}
	// Verify capital-N is not in the matched set.
	for _, sched := range func() []*types.Schedule {
		out, _, _ := list("?prefix=nightly-")
		return out
	}() {
		if sched.Name == "Nightly-Audit" {
			t.Fatalf("case-sensitive prefix must not match capital-N")
		}
	}

	// Case-sensitive: capital prefix only matches the capital-N schedule.
	if out, total, _ := list("?prefix=Nightly-"); len(out) != 1 || total != 1 || out[0].Name != "Nightly-Audit" {
		t.Fatalf("prefix=Nightly-: %+v total=%d", out, total)
	}

	// Empty value is a no-op (returns all).
	if out, total, _ := list("?prefix="); len(out) != 4 || total != 4 {
		t.Fatalf("empty prefix should be no-op: %+v total=%d", out, total)
	}

	// Whitespace-trimmed: " nightly-" matches the same set as "nightly-".
	if out, total, _ := list("?prefix=%20nightly-%20"); len(out) != 2 || total != 2 {
		t.Fatalf("whitespace-trim wrong: %+v total=%d", out, total)
	}

	// Substring-only matches are excluded (HasPrefix, not Contains).
	// "snapshot" is in the middle of "nightly-snapshot" but not a prefix.
	if out, total, _ := list("?prefix=snapshot"); len(out) != 0 || total != 0 {
		t.Fatalf("prefix=snapshot should match none: %+v total=%d", out, total)
	}

	// No match returns an empty list with zero total (not 404).
	if out, total, code := list("?prefix=zzz"); code != http.StatusOK || len(out) != 0 || total != 0 {
		t.Fatalf("no-match: code=%d %+v total=%d", code, out, total)
	}

	// Composes additively with ?enabled= (only nightly-snapshot + nightly-restart
	// are enabled and start with `nightly-`).
	if out, total, _ := list("?prefix=nightly-&enabled=true"); len(out) != 2 || total != 2 {
		t.Fatalf("prefix+enabled compose: %+v total=%d", out, total)
	}
	if out, total, _ := list("?prefix=nightly-&enabled=false"); len(out) != 0 || total != 0 {
		t.Fatalf("prefix+enabled=false compose: %+v total=%d", out, total)
	}

	// Composes additively with ?search= (search is substring + lowercased; the
	// combined filter must intersect — `prefix=nightly-` AND `search=restart`
	// (which hits only the name) leaves only the nightly-restart schedule).
	if out, total, _ := list("?prefix=nightly-&search=restart"); len(out) != 1 || total != 1 || out[0].Name != "nightly-restart" {
		t.Fatalf("prefix+search compose: %+v total=%d", out, total)
	}

	// Filtered pagination: X-Total-Count reflects the post-filter count.
	if out, total, _ := list("?prefix=nightly-&per_page=1"); len(out) != 1 || total != 2 {
		t.Fatalf("paginated post-filter total wrong: len=%d total=%d", len(out), total)
	}
}
