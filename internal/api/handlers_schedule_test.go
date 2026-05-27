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
