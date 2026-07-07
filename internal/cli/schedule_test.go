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

// TestCLI_ScheduleList_ForwardsVMIDSort covers the 5.4.97 vm_id sort axis
// on the schedules list — whitespace + case normalisation, forwarded as
// `sort=vm_id` to the daemon's /schedules endpoint.
func TestCLI_ScheduleList_ForwardsVMIDSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--sort", "  VM_ID  ", "--order", "asc"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=vm_id") {
		t.Fatalf("query missing sort=vm_id: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=asc") {
		t.Fatalf("query missing order=asc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleList_InvalidSortAdvertisesVMID asserts the client-side
// rejection lists vm_id in the error envelope so operators discover the
// new 5.4.97 axis from the error path.
func TestCLI_ScheduleList_InvalidSortAdvertisesVMID(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--sort", "garbage")
	if err == nil {
		t.Fatal("expected client-side rejection of --sort garbage")
	}
	if !strings.Contains(err.Error(), "vm_id") {
		t.Fatalf("invalid --sort message should advertise vm_id, got %v", err)
	}
}

// TestCLI_ScheduleList_ForwardsActionSort covers the 5.4.99 action sort
// axis on the schedules list — whitespace + case normalisation,
// forwarded as `sort=action` to the daemon's /schedules endpoint.
func TestCLI_ScheduleList_ForwardsActionSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--sort", "  ACTION  ", "--order", "asc"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=action") {
		t.Fatalf("query missing sort=action: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=asc") {
		t.Fatalf("query missing order=asc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleList_InvalidSortAdvertisesAction asserts the
// client-side rejection lists action in the error envelope so operators
// discover the new 5.4.99 axis from the error path.
func TestCLI_ScheduleList_InvalidSortAdvertisesAction(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--sort", "garbage")
	if err == nil {
		t.Fatal("expected client-side rejection of --sort garbage")
	}
	if !strings.Contains(err.Error(), "action") {
		t.Fatalf("invalid --sort message should advertise action, got %v", err)
	}
}

// TestCLI_ScheduleList_ForwardsTimezoneSort covers the 5.4.112 timezone
// sort axis on the schedule list — whitespace + case normalisation on the
// `--sort` value (the daemon validates the stored timezone case-sensitively
// per IANA, but the sort *param* itself is lowercased before forwarding).
func TestCLI_ScheduleList_ForwardsTimezoneSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--sort", "  TIMEZONE  ", "--order", "desc"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=timezone") {
		t.Fatalf("query missing sort=timezone: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=desc") {
		t.Fatalf("query missing order=desc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleList_InvalidSortAdvertisesTimezone asserts the
// client-side rejection lists timezone in the error envelope so operators
// discover the new 5.4.112 axis from the error path.
func TestCLI_ScheduleList_InvalidSortAdvertisesTimezone(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--sort", "garbage")
	if err == nil {
		t.Fatal("expected client-side rejection of --sort garbage")
	}
	if !strings.Contains(err.Error(), "timezone") {
		t.Fatalf("invalid --sort message should advertise timezone, got %v", err)
	}
}

// TestCLI_ScheduleList_ForwardsEnabledSort covers the 5.4.113 enabled
// sort axis on the schedule list — whitespace + case normalisation on
// the `--sort` value (the daemon validates server-side; the CLI just
// lowercases + trims before forwarding).
func TestCLI_ScheduleList_ForwardsEnabledSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--sort", "  ENABLED  ", "--order", "desc"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=enabled") {
		t.Fatalf("query missing sort=enabled: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=desc") {
		t.Fatalf("query missing order=desc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleList_InvalidSortAdvertisesEnabled asserts the
// client-side rejection lists enabled in the error envelope so
// operators discover the new 5.4.113 axis from the error path.
func TestCLI_ScheduleList_InvalidSortAdvertisesEnabled(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--sort", "garbage")
	if err == nil {
		t.Fatal("expected client-side rejection of --sort garbage")
	}
	if !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("invalid --sort message should advertise enabled, got %v", err)
	}
}

// TestCLI_ScheduleList_ForwardsCatchUpPolicySort covers the 5.4.116
// catch_up_policy sort axis on the schedule list — whitespace + case
// normalisation on the `--sort` value (the daemon validates server-side;
// the CLI just lowercases + trims before forwarding).
func TestCLI_ScheduleList_ForwardsCatchUpPolicySort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--sort", "  CATCH_UP_POLICY  ", "--order", "asc"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=catch_up_policy") {
		t.Fatalf("query missing sort=catch_up_policy: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=asc") {
		t.Fatalf("query missing order=asc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleList_InvalidSortAdvertisesCatchUpPolicy asserts the
// client-side rejection lists catch_up_policy in the error envelope so
// operators discover the new 5.4.116 axis from the error path.
func TestCLI_ScheduleList_InvalidSortAdvertisesCatchUpPolicy(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--sort", "garbage")
	if err == nil {
		t.Fatal("expected client-side rejection of --sort garbage")
	}
	if !strings.Contains(err.Error(), "catch_up_policy") {
		t.Fatalf("invalid --sort message should advertise catch_up_policy, got %v", err)
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

// TestCLI_ScheduleList_ForwardsNextFireRange covers the --next-fire-since /
// --next-fire-until flags (5.4.60): whitespace-trim, both bounds forwarded
// as next_fire_since= / next_fire_until=.
func TestCLI_ScheduleList_ForwardsNextFireRange(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--next-fire-since", "  2026-06-01T12:00:00Z  ",
		"--next-fire-until", "2026-06-01T20:00:00Z"); err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"next_fire_since=2026-06-01", "next_fire_until=2026-06-01T20"} {
		if !strings.Contains(d.lastQuery, want) {
			t.Fatalf("query missing %q: %s", want, d.lastQuery)
		}
	}
}

func TestCLI_ScheduleList_EmptyNextFireOmitsParam(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--next-fire-since", "   ", "--next-fire-until", ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(d.lastQuery, "next_fire_since=") {
		t.Fatalf("whitespace-only next-fire-since should not send the param: %s", d.lastQuery)
	}
	if strings.Contains(d.lastQuery, "next_fire_until=") {
		t.Fatalf("empty next-fire-until should not send the param: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleList_RejectsInvalidNextFireSince(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--next-fire-since", "not-a-time"); err == nil {
		t.Fatal("expected invalid --next-fire-since rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid next-fire-since should not contact daemon")
	}
}

func TestCLI_ScheduleList_RejectsInvalidNextFireUntil(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--next-fire-until", "garbage"); err == nil {
		t.Fatal("expected invalid --next-fire-until rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid next-fire-until should not contact daemon")
	}
}

// TestCLI_ScheduleList_ForwardsLastFiredRange covers the --last-fired-since /
// --last-fired-until flags (5.4.74): whitespace-trim, both bounds forwarded
// as last_fired_since= / last_fired_until=.
func TestCLI_ScheduleList_ForwardsLastFiredRange(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--last-fired-since", "  2026-06-01T12:00:00Z  ",
		"--last-fired-until", "2026-06-01T20:00:00Z"); err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"last_fired_since=2026-06-01", "last_fired_until=2026-06-01T20"} {
		if !strings.Contains(d.lastQuery, want) {
			t.Fatalf("query missing %q: %s", want, d.lastQuery)
		}
	}
}

func TestCLI_ScheduleList_EmptyLastFiredOmitsParam(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--last-fired-since", "   ", "--last-fired-until", ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(d.lastQuery, "last_fired_since=") {
		t.Fatalf("whitespace-only last-fired-since should not send the param: %s", d.lastQuery)
	}
	if strings.Contains(d.lastQuery, "last_fired_until=") {
		t.Fatalf("empty last-fired-until should not send the param: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleList_RejectsInvalidLastFiredSince(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--last-fired-since", "not-a-time"); err == nil {
		t.Fatal("expected invalid --last-fired-since rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid last-fired-since should not contact daemon")
	}
}

func TestCLI_ScheduleList_RejectsInvalidLastFiredUntil(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--last-fired-until", "garbage"); err == nil {
		t.Fatal("expected invalid --last-fired-until rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid last-fired-until should not contact daemon")
	}
}

// TestCLI_ScheduleList_ForwardsPrefix covers the --prefix flag (5.4.82):
// case-sensitive HasPrefix on schedule name, whitespace-trim, empty omits.
func TestCLI_ScheduleList_ForwardsPrefix(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--prefix", "  nightly-  "); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "prefix=nightly-") {
		t.Fatalf("query missing prefix=nightly-: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleList_PrefixPreservesCase asserts the CLI forwards the
// prefix verbatim (no lowercasing) so the case-sensitive backend contract
// is preserved end-to-end.
func TestCLI_ScheduleList_PrefixPreservesCase(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--prefix", "Nightly-"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "prefix=Nightly-") {
		t.Fatalf("query lost case: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleList_EmptyPrefixOmitsParam(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--prefix", "   "); err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(d.lastQuery, "prefix=") {
		t.Fatalf("whitespace-only prefix should not send the param: %s", d.lastQuery)
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

// TestCLI_ScheduleList_ForwardsLastFiredAtSort asserts the 5.4.84 sort axis
// passes through end-to-end. Mixed-case + whitespace are normalised on the
// CLI side so the daemon always sees the lowercase canonical form.
func TestCLI_ScheduleList_ForwardsLastFiredAtSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "list", "--api-url", d.server.URL,
		"--sort", "  LAST_FIRED_AT  ", "--order", " DESC "); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=last_fired_at") {
		t.Fatalf("query missing sort=last_fired_at: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=desc") {
		t.Fatalf("query missing order=desc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleList_RejectsInvalidSort_LastFiredAtMentioned makes sure the
// CLI-side rejection message advertises the new sort key in the supported set
// so operators see it on a typo.
func TestCLI_ScheduleList_RejectsInvalidSort_LastFiredAtMentioned(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "list", "--api-url", d.server.URL, "--sort", "memory")
	if err == nil {
		t.Fatal("expected invalid --sort rejection")
	}
	if !strings.Contains(err.Error(), "last_fired_at") {
		t.Fatalf("CLI error should mention last_fired_at, got: %v", err)
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

func TestCLI_ScheduleRuns_ForwardsSearchFilter(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK,
		`[{"id":"run-1","schedule_id":"sched-1","vm_id":"vm-1","started_at":"2026-05-22T02:00:00Z","status":"error","error":"context deadline exceeded"}]`)

	out, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--search", "  deadline  ")
	if err != nil {
		t.Fatalf("runs: %v\nout=%s", err, out)
	}
	if d.lastMeth != http.MethodGet || d.lastPath != "/api/v1/schedules/sched-1/runs" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	// The CLI trims whitespace before forwarding so the daemon never sees the padding.
	if !strings.Contains(d.lastQuery, "search=deadline") {
		t.Fatalf("query missing search=deadline (trimmed): %s", d.lastQuery)
	}
	if strings.Contains(d.lastQuery, "search=%20deadline") || strings.Contains(d.lastQuery, "search=+deadline") {
		t.Fatalf("query should not retain leading whitespace, got %s", d.lastQuery)
	}
	if !strings.Contains(out, "deadline") {
		t.Fatalf("output missing run detail: %s", out)
	}
}

func TestCLI_ScheduleRuns_EmptySearchFlagSendsNothing(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--search", "   "); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if strings.Contains(d.lastQuery, "search=") {
		t.Fatalf("empty --search should not send search query param, got %q", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_ForwardsSortAndOrder(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--sort", "  FINISHED_AT  ", "--order", " DESC "); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=finished_at") {
		t.Fatalf("query missing sort=finished_at: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=desc") {
		t.Fatalf("query missing order=desc: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_EmptySortAndOrderOmitParams(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--sort", "   ", "--order", "  "); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if strings.Contains(d.lastQuery, "sort=") {
		t.Fatalf("empty --sort should not send sort query param, got %q", d.lastQuery)
	}
	if strings.Contains(d.lastQuery, "order=") {
		t.Fatalf("empty --order should not send order query param, got %q", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_RejectsInvalidSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--sort", "memory")
	if err == nil || !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("expected client-side rejection, got err=%v", err)
	}
}

func TestCLI_ScheduleRuns_ForwardsDurationSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--sort", "  DURATION  ", "--order", "desc"); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=duration") {
		t.Fatalf("query missing sort=duration: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=desc") {
		t.Fatalf("query missing order=desc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleRuns_ForwardsVMIDSort asserts the 5.4.95 vm_id sort
// axis: --sort vm_id (whitespace + case normalised client-side) reaches
// the daemon as ?sort=vm_id.
func TestCLI_ScheduleRuns_ForwardsVMIDSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--sort", "  VM_ID  ", "--order", "asc"); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=vm_id") {
		t.Fatalf("query missing sort=vm_id: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=asc") {
		t.Fatalf("query missing order=asc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleRuns_InvalidSortAdvertisesVMID asserts the
// client-side rejection of an unknown --sort value lists vm_id in the
// error envelope so operators discover the new 5.4.95 axis.
func TestCLI_ScheduleRuns_InvalidSortAdvertisesVMID(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--sort", "memory")
	if err == nil {
		t.Fatal("expected client-side rejection of --sort memory")
	}
	if !strings.Contains(err.Error(), "vm_id") {
		t.Fatalf("invalid --sort message should advertise vm_id, got %v", err)
	}
}

// TestCLI_ScheduleRuns_ForwardsSkipReasonSort covers the --sort skip_reason
// axis (5.4.96): the CLI normalises whitespace + case and forwards
// `sort=skip_reason` to the daemon's /schedules/{id}/runs endpoint.
func TestCLI_ScheduleRuns_ForwardsSkipReasonSort(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--sort", "  SKIP_REASON  ", "--order", "asc"); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(d.lastQuery, "sort=skip_reason") {
		t.Fatalf("query missing sort=skip_reason: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "order=asc") {
		t.Fatalf("query missing order=asc: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleRuns_InvalidSortAdvertisesSkipReason asserts the CLI
// client-side rejection message advertises the new skip_reason axis so
// operators discover it from the error path.
func TestCLI_ScheduleRuns_InvalidSortAdvertisesSkipReason(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--sort", "memory")
	if err == nil || !strings.Contains(err.Error(), "skip_reason") {
		t.Fatalf("expected error mentioning skip_reason, got %v", err)
	}
}

func TestCLI_ScheduleRuns_SortFlagHelpAdvertisesSkipReason(t *testing.T) {
	flag := scheduleRunsCmd.Flags().Lookup("sort")
	if flag == nil {
		t.Fatal("expected --sort flag to exist")
	}
	if !strings.Contains(flag.Usage, "skip_reason") {
		t.Fatalf("--sort flag usage should advertise skip_reason, got %q", flag.Usage)
	}
}

func TestCLI_ScheduleRuns_RejectsInvalidOrder(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--order", "sideways")
	if err == nil || !strings.Contains(err.Error(), "invalid --order") {
		t.Fatalf("expected client-side rejection, got err=%v", err)
	}
}

func TestCLI_ScheduleRuns_ForwardsFinishedRange(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK,
		`[{"id":"run-1","schedule_id":"sched-1","vm_id":"vm-1","started_at":"2026-05-22T02:00:00Z","finished_at":"2026-05-22T02:30:00Z","status":"success"}]`)

	out, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--finished-since", "  2026-05-22T02:00:00Z  ", "--finished-until", "2026-05-22T03:00:00Z")
	if err != nil {
		t.Fatalf("runs: %v\nout=%s", err, out)
	}
	if d.lastMeth != http.MethodGet || d.lastPath != "/api/v1/schedules/sched-1/runs" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	if !strings.Contains(d.lastQuery, "finished_since=2026-05-22T02") {
		t.Fatalf("query missing finished_since (whitespace-trimmed): %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "finished_until=2026-05-22T03") {
		t.Fatalf("query missing finished_until: %s", d.lastQuery)
	}
	if strings.Contains(d.lastQuery, "finished_since=%20") || strings.Contains(d.lastQuery, "finished_since=+") {
		t.Fatalf("query should not retain whitespace, got %s", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_EmptyFinishedFlagsOmitParams(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--finished-since", "   ", "--finished-until", ""); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if strings.Contains(d.lastQuery, "finished_since=") {
		t.Fatalf("empty --finished-since should not send the param, got %q", d.lastQuery)
	}
	if strings.Contains(d.lastQuery, "finished_until=") {
		t.Fatalf("empty --finished-until should not send the param, got %q", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_RejectsInvalidFinishedSince(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--finished-since", "not-a-time"); err == nil {
		t.Fatal("expected invalid --finished-since rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid finished-since should not contact the daemon")
	}
}

func TestCLI_ScheduleRuns_RejectsInvalidFinishedUntil(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--finished-until", "garbage"); err == nil {
		t.Fatal("expected invalid --finished-until rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid finished-until should not contact the daemon")
	}
}

// TestCLI_ScheduleRuns_ForwardsDurationRange covers the --min-duration-ms /
// --max-duration-ms range filter (5.4.64) — both ends round-trip through the
// query string and the CLI distinguishes "flag not set" from "explicit 0".
func TestCLI_ScheduleRuns_ForwardsDurationRange(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK,
		`[{"id":"run-1","schedule_id":"sched-1","vm_id":"vm-1","started_at":"2026-05-22T02:00:00Z","finished_at":"2026-05-22T02:30:00Z","status":"success"}]`)

	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--min-duration-ms", "600000", "--max-duration-ms", "3600000"); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(d.lastQuery, "min_duration_ms=600000") {
		t.Fatalf("query missing min_duration_ms: %s", d.lastQuery)
	}
	if !strings.Contains(d.lastQuery, "max_duration_ms=3600000") {
		t.Fatalf("query missing max_duration_ms: %s", d.lastQuery)
	}
}

// TestCLI_ScheduleRuns_DurationRangeZeroExplicit confirms that --min-duration-ms=0
// (explicit zero) IS forwarded, while the unset default is NOT — operators need
// to be able to lower-bound at zero without the CLI silently dropping the flag.
func TestCLI_ScheduleRuns_DurationRangeZeroExplicit(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL,
		"--min-duration-ms", "0"); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if !strings.Contains(d.lastQuery, "min_duration_ms=0") {
		t.Fatalf("explicit --min-duration-ms=0 should be forwarded: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_DurationRangeUnsetOmitsParams(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if strings.Contains(d.lastQuery, "min_duration_ms=") {
		t.Fatalf("unset --min-duration-ms should not send the param, got %q", d.lastQuery)
	}
	if strings.Contains(d.lastQuery, "max_duration_ms=") {
		t.Fatalf("unset --max-duration-ms should not send the param, got %q", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_RejectsNegativeDuration(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--min-duration-ms", "-1"); err == nil {
		t.Fatal("expected negative --min-duration-ms rejection")
	}
	if d.lastPath != "" {
		t.Fatal("negative duration should not contact the daemon")
	}
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--max-duration-ms", "-5"); err == nil {
		t.Fatal("expected negative --max-duration-ms rejection")
	}
}

// TestCLI_ScheduleRuns_ForwardsSkipReason covers the 5.4.65 --skip-reason
// flag: lowercase + trim is mirrored client-side, the param round-trips
// through the query string under the same `skip_reason=` key the API uses.
func TestCLI_ScheduleRuns_ForwardsSkipReason(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK,
		`[{"id":"run-1","schedule_id":"sched-1","vm_id":"vm-1","started_at":"2026-05-22T02:00:00Z","status":"skipped","skip_reason":"queue_full"}]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--skip-reason", "  QUEUE_FULL  "); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if d.lastMeth != http.MethodGet || d.lastPath != "/api/v1/schedules/sched-1/runs" {
		t.Fatalf("unexpected request: %s %s", d.lastMeth, d.lastPath)
	}
	if !strings.Contains(d.lastQuery, "skip_reason=queue_full") {
		t.Fatalf("query missing skip_reason (lowercase+trim): %s", d.lastQuery)
	}
	// Whitespace should not survive the trim.
	if strings.Contains(d.lastQuery, "skip_reason=%20") || strings.Contains(d.lastQuery, "skip_reason=+") {
		t.Fatalf("query should not retain whitespace: %s", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_EmptySkipReasonOmitsParam(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--skip-reason", "   "); err != nil {
		t.Fatalf("runs: %v", err)
	}
	if strings.Contains(d.lastQuery, "skip_reason=") {
		t.Fatalf("blank --skip-reason should not send the param, got %q", d.lastQuery)
	}
}

func TestCLI_ScheduleRuns_RejectsInvalidSkipReason(t *testing.T) {
	d := newFakeScheduleDaemon(t, http.StatusOK, `[]`)
	if _, err := runCLI("schedule", "runs", "sched-1", "--api-url", d.server.URL, "--skip-reason", "garbage"); err == nil {
		t.Fatal("expected invalid --skip-reason rejection")
	}
	if d.lastPath != "" {
		t.Fatal("invalid skip-reason should not contact the daemon")
	}
}
