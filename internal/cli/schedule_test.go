package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeScheduleDaemon struct {
	server    *httptest.Server
	lastPath  string
	lastQuery string
	lastBody  string
	lastMeth  string
	status    int
	respBody  string
}

func newFakeScheduleDaemon(t *testing.T, status int, respBody string) *fakeScheduleDaemon {
	d := &fakeScheduleDaemon{status: status, respBody: respBody}
	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.lastPath = r.URL.Path
		d.lastQuery = r.URL.RawQuery
		d.lastMeth = r.Method
		body, _ := io.ReadAll(r.Body)
		d.lastBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(d.status)
		w.Write([]byte(d.respBody))
	}))
	t.Cleanup(d.server.Close)
	return d
}

func TestCLI_ScheduleCreate_Success(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusCreated,
		`{"id":"sched-1","name":"nightly","action":"snapshot","vm_id":"vm-1","cron_spec":"0 0 2 * * *","enabled":true}`)

	out, err := runCLI("schedule", "create", "--api-url", d.server.URL,
		"--name", "nightly", "--vm", "vm-1", "--action", "snapshot", "--cron", "0 0 2 * * *")
	if err != nil {
		t.Fatalf("create: %v\nout=%s", err, out)
	}
	if d.lastMeth != http.MethodPost || d.lastPath != "/api/v1/schedules" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(d.lastBody), &sent); err != nil {
		t.Fatalf("body not JSON: %s", d.lastBody)
	}
	if sent["action"] != "snapshot" || sent["cron_spec"] != "0 0 2 * * *" {
		t.Fatalf("body missing fields: %s", d.lastBody)
	}
	if !strings.Contains(out, "sched-1") {
		t.Fatalf("output missing id: %s", out)
	}
}

func TestCLI_ScheduleCreate_RejectsInvalidAction(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusCreated, `{}`)
	_, err := runCLI("schedule", "create", "--api-url", d.server.URL,
		"--name", "x", "--action", "explode", "--cron", "0 0 2 * * *")
	if err == nil {
		t.Fatal("expected client-side rejection of invalid action")
	}
	if d.lastPath != "" {
		t.Fatal("invalid action should not contact the daemon")
	}
}

func TestCLI_ScheduleCreate_RejectsMutuallyExclusiveTarget(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusCreated, `{}`)
	_, err := runCLI("schedule", "create", "--api-url", d.server.URL,
		"--name", "x", "--action", "snapshot", "--cron", "0 0 2 * * *", "--vm", "vm-1", "--tag", "prod")
	if err == nil {
		t.Fatal("expected --vm + --tag rejection")
	}
}

func TestCLI_ScheduleCreate_RequiresName(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusCreated, `{}`)
	if _, err := runCLI("schedule", "create", "--api-url", d.server.URL,
		"--action", "snapshot", "--cron", "0 0 2 * * *"); err == nil {
		t.Fatal("expected missing --name rejection")
	}
}

func TestCLI_ScheduleList_ForwardsFilters(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK,
		`[{"id":"sched-1","name":"nightly","action":"snapshot","vm_id":"vm-1","cron_spec":"0 0 2 * * *","enabled":true}]`)

	out, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--action", "Snapshot", "--enabled", "true", "--vm", "vm-1", "--search", "  Nightly  ", "--sort", "name", "--order", "desc")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"action=snapshot", "enabled=true", "vm_id=vm-1", "search=nightly", "sort=name", "order=desc"} {
		if !strings.Contains(d.lastQuery, want) {
			t.Fatalf("query missing %q: %s", want, d.lastQuery)
		}
	}
	if !strings.Contains(out, "sched-1") {
		t.Fatalf("output missing schedule: %s", out)
	}
}

func TestCLI_ScheduleList_ForwardsTagSelector(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--tag-selector", "  PROD  "); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "tag_selector=prod") {
		t.Fatalf("query missing trimmed+lowercased tag_selector: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleList_ForwardsCatchUpPolicy(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--catch-up", "  RUN_ONCE  "); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "catch_up_policy=run_once") {
		t.Fatalf("query missing trimmed+lowercased catch_up_policy: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleList_ForwardsTimezone(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--timezone", "  America/New_York  "); err != nil {
		t.Fatalf("list: %v", err)
	}
	// IANA timezone names are case-sensitive — forwarded verbatim (only trim).
	if !strings.Contains(d.lastQuery, "timezone=America%2FNew_York") {
		t.Fatalf("query missing trimmed timezone: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleList_EmptyTimezoneOmitsParam(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--timezone", "   "); err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(d.lastQuery, "timezone=") {
		t.Fatalf("whitespace-only timezone should not send the param: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleList_RejectsInvalidCatchUp(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--catch-up", "bogus"); err == nil {
		t.Fatal("expected invalid --catch-up rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid catch-up should not contact daemon")
	}
}

func TestCLI_ScheduleList_ForwardsTimeRange(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--since", "2026-05-01T00:00:00Z", "--until", "2026-05-31T00:00:00Z"); err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"since=2026-05-01", "until=2026-05-31"} {
		if !strings.Contains(d.lastQuery, want) {
			t.Fatalf("query missing %q: %s", want, d.lastQuery)
		}
	}
}

func TestCLI_ScheduleList_RejectsInvalidSince(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--since", "not-a-time"); err == nil {
		t.Fatal("expected invalid --since rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid since should not contact daemon")
	}
}

func TestCLI_ScheduleList_RejectsInvalidUntil(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--until", "garbage"); err == nil {
		t.Fatal("expected invalid --until rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid until should not contact daemon")
	}
}

func TestCLI_ScheduleList_RejectsInvalidSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--sort", "bogus"); err == nil {
		t.Fatal("expected invalid --sort rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid sort should not contact daemon")
	}
}

func TestCLI_ScheduleList_RejectsInvalidEnabled(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--enabled", "maybe"); err == nil {
		t.Fatal("expected invalid --enabled rejection")
	}
}

func TestCLI_ScheduleList_Empty(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	out, err := runCLI("schedule", "list", "--api-url", d.server.URL)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No schedules found.") {
		t.Fatalf("expected empty message, got %s", out)
	}
}

func TestCLI_ScheduleEdit_ForwardsChangedFields(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK,
		`{"id":"sched-1","name":"renamed","action":"snapshot","cron_spec":"0 0 3 * * *","enabled":false}`)

	out, err := runCLI("schedule", "edit", "sched-1", "--api-url", d.server.URL,
		"--name", "renamed", "--enabled=false", "--cron", "0 0 3 * * *")
	if err != nil {
		t.Fatalf("edit: %v\nout=%s", err, out)
	}
	if d.lastMeth != http.MethodPatch || d.lastPath != "/api/v1/schedules/sched-1" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	var spec map[string]any
	json.Unmarshal([]byte(d.lastBody), &spec)
	if spec["name"] != "renamed" {
		t.Fatalf("missing name in patch: %s", d.lastBody)
	}
	if v, ok := spec["enabled"].(bool); !ok || v {
		t.Fatalf("expected enabled=false in patch: %s", d.lastBody)
	}
	if spec["cron_spec"] != "0 0 3 * * *" {
		t.Fatalf("missing cron_spec in patch: %s", d.lastBody)
	}
}

func TestCLI_ScheduleEdit_NoFields(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `{}`)
	if _, err := runCLI("schedule", "edit", "sched-1", "--api-url", d.server.URL); err == nil {
		t.Fatal("expected no-fields rejection")
	}
	if d.lastPath != "" {
		t.Fatal("no-fields edit should not contact daemon")
	}
}

func TestCLI_ScheduleEdit_TagAndClearTagsExclusive(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `{}`)
	if _, err := runCLI("schedule", "edit", "sched-1", "--api-url", d.server.URL, "--tag", "prod", "--clear-tags"); err == nil {
		t.Fatal("expected --tag + --clear-tags rejection")
	}
}

func TestCLI_ScheduleDelete(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusNoContent, ``)
	out, err := runCLI("schedule", "delete", "sched-1", "--api-url", d.server.URL)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if d.lastMeth != http.MethodDelete || d.lastPath != "/api/v1/schedules/sched-1" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	if !strings.Contains(out, "deleted") {
		t.Fatalf("expected deleted message: %s", out)
	}
}

func TestCLI_ScheduleRunNow(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `{"id":"sched-1","name":"x"}`)
	out, err := runCLI("schedule", "run-now", "sched-1", "--api-url", d.server.URL)
	if err != nil {
		t.Fatalf("run-now: %v", err)
	}
	if d.lastMeth != http.MethodPost || d.lastPath != "/api/v1/schedules/sched-1/run-now" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	if !strings.Contains(out, "fired") {
		t.Fatalf("expected fired message: %s", out)
	}
}

func TestCLI_ScheduleRuns_ForwardsFilters(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK,
		`[{"id":"run-1","schedule_id":"sched-1","vm_id":"vm-1","started_at":"2026-05-22T02:00:00Z","status":"error","error":"boom"}]`)

	out, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--status", "  ERROR  ", "--since", "2026-05-01T00:00:00Z", "--until", "2026-05-31T00:00:00Z", "--limit", "10", "--page", "2")
	if err != nil {
		t.Fatalf("runs: %v\nout=%s", err, out)
	}
	if d.lastMeth != http.MethodGet || d.lastPath != "/api/v1/schedules/sched-1/runs" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	for _, want := range []string{"status=error", "since=2026-05-01", "until=2026-05-31", "per_page=10", "page=2"} {
		if !strings.Contains(d.lastQuery, want) {
			t.Fatalf("query missing %q: %s", want, d.lastQuery)
		}
	}
	if !strings.Contains(out, "error") || !strings.Contains(out, "boom") {
		t.Fatalf("output missing run detail: %s", out)
	}
}

func TestCLI_ScheduleRuns_RejectsInvalidStatus(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--status", "queued"); err == nil {
		t.Fatal("expected invalid --status rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid status should not contact the daemon")
	}
}

func TestCLI_ScheduleRuns_RejectsInvalidSince(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--since", "not-a-time"); err == nil {
		t.Fatal("expected invalid --since rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid since should not contact the daemon")
	}
}

func TestCLI_ScheduleRuns_ForwardsVMFilter(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK,
		`[{"id":"run-1","schedule_id":"sched-1","vm_id":"vm-42","started_at":"2026-05-22T02:00:00Z","status":"success"}]`)

	out, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--vm", "  vm-42  ")
	if err != nil {
		t.Fatalf("runs: %v\nout=%s", err, out)
	}
	if d.lastMeth != http.MethodGet || d.lastPath != "/api/v1/schedules/sched-1/runs" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	if !strings.Contains(d.lastQuery, "vm_id=vm-42") {
		t.Fatalf("query missing vm_id=vm-42: %s", d.lastQuery)
	}
	if !strings.Contains(out, "vm-42") {
		t.Fatalf("output missing run detail: %s", out)
	}
}

func TestCLI_ScheduleRuns_EmptyVMFlagSendsNothing(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--vm", "   "); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if strings.Contains(d.lastQuery, "vm_id=") {
		t.Fatalf("empty --vm should not send vm_id query param, got %q", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_Empty(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	out, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if d.lastQuery != "" {
		t.Fatalf("no flags should send no query params, got %q", d.lastQuery)
	}
	if !strings.Contains(out, "No runs found") {
		t.Fatalf("expected empty message: %s", out)
	}
}
