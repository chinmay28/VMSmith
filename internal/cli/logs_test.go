package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeLogsDaemon captures the most recent GET path + query against a stub
// /api/v1/logs endpoint so we can assert the CLI forwarded the query
// parameters the API actually expects.
type fakeLogsDaemon struct {
	lastPath  string
	lastQuery string
	authHdr   string
	status    int
	respBody  string
}

func newFakeLogsDaemon(t *testing.T, status int, body string) (*httptest.Server, *fakeLogsDaemon) {
	t.Helper()
	state := &fakeLogsDaemon{status: status, respBody: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.lastQuery = r.URL.RawQuery
		state.authHdr = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(state.status)
		_, _ = w.Write([]byte(state.respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

func TestCLI_LogsList_HappyPath(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK,
		`{"entries":[
			{"ts":"2026-05-14T08:00:00Z","level":"info","source":"api","msg":"GET /vms","fields":{"vm_id":"vm-1"}},
			{"ts":"2026-05-14T08:00:01Z","level":"warn","source":"daemon","msg":"slow handler","fields":null}
		],"total":2}`)

	out, err := runCLI("logs", "list", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("logs list: %v\nout=%s", err, out)
	}
	if state.lastPath != "/api/v1/logs" {
		t.Fatalf("path = %q, want /api/v1/logs", state.lastPath)
	}
	// Default invocation passes no filters.
	if state.lastQuery != "" {
		t.Fatalf("unexpected query for default invocation: %q", state.lastQuery)
	}
	for _, want := range []string{"TIME", "LEVEL", "SOURCE", "MESSAGE", "GET /vms", "slow handler", "INFO", "WARN"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
	// Default omits the FIELDS column.
	if strings.Contains(out, "FIELDS") {
		t.Fatalf("default output should not include FIELDS column: %s", out)
	}
}

func TestCLI_LogsList_FieldsFlagShowsStructuredColumn(t *testing.T) {
	srv, _ := newFakeLogsDaemon(t, http.StatusOK,
		`{"entries":[
			{"ts":"2026-05-14T08:00:00Z","level":"error","source":"api","msg":"VM not found","fields":{"vm_id":"vm-9","error":"resource_not_found"}}
		],"total":1}`)

	out, err := runCLI("logs", "list", "--api-url", srv.URL, "--fields")
	if err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if !strings.Contains(out, "FIELDS") {
		t.Fatalf("expected FIELDS column header, got %s", out)
	}
	// Keys are sorted so the test is deterministic.
	if !strings.Contains(out, "error=resource_not_found vm_id=vm-9") {
		t.Fatalf("expected sorted k=v pairs, got %s", out)
	}
}

func TestCLI_LogsList_ForwardsFilters(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)

	_, err := runCLI("logs", "list",
		"--api-url", srv.URL,
		"--level", "WARN",
		"--source", "API",
		"--search", "  CONNECT  ",
		"--limit", "50",
		"--page", "2",
	)
	if err != nil {
		t.Fatalf("logs list: %v", err)
	}
	for _, want := range []string{
		"level=warn",
		"source=api",
		"search=connect",
		"per_page=50",
		"page=2",
	} {
		if !strings.Contains(state.lastQuery, want) {
			t.Fatalf("query missing %q: %q", want, state.lastQuery)
		}
	}
}

func TestCLI_LogsList_LowercasesAndTrimsSearch(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL, "--search", "  CONNECT  "); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "search=connect") {
		t.Fatalf("expected normalised search=connect, got %q", state.lastQuery)
	}
	if strings.Contains(state.lastQuery, "CONNECT") || strings.Contains(state.lastQuery, "+++") {
		t.Fatalf("query should not retain whitespace or uppercase: %q", state.lastQuery)
	}
}

func TestCLI_LogsList_EmptyFlagsOmitParams(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL,
		"--level", "", "--source", "", "--since", "", "--search", ""); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	for _, banned := range []string{"level=", "source=", "since=", "search=", "per_page=", "page="} {
		if strings.Contains(state.lastQuery, banned) {
			t.Fatalf("empty flags must not be forwarded; query=%q contained %q", state.lastQuery, banned)
		}
	}
}

func TestCLI_LogsList_SinceDurationBecomesRFC3339(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL, "--since", "5m"); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	// The CLI converts the duration to an RFC3339 timestamp before forwarding
	// — the daemon parses RFC3339Nano, not Go duration strings.
	if !strings.Contains(state.lastQuery, "since=") {
		t.Fatalf("expected ?since= in query: %q", state.lastQuery)
	}
	// The encoded form should be URL-encoded RFC3339 (`T...Z`) — assert that
	// shape rather than the exact timestamp (which is now-relative).
	if !strings.Contains(state.lastQuery, "T") || !strings.Contains(state.lastQuery, "Z") {
		t.Fatalf("expected RFC3339 since= value, got %q", state.lastQuery)
	}
}

func TestCLI_LogsList_SinceInvalidIsRejected(t *testing.T) {
	srv, _ := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	_, err := runCLI("logs", "list", "--api-url", srv.URL, "--since", "not-a-time")
	if err == nil {
		t.Fatalf("expected error for invalid --since, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --since") {
		t.Fatalf("error = %q, want it to mention 'invalid --since'", err.Error())
	}
}

func TestCLI_LogsList_RejectsInvalidLevel(t *testing.T) {
	srv, _ := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	_, err := runCLI("logs", "list", "--api-url", srv.URL, "--level", "shout")
	if err == nil {
		t.Fatalf("expected error for invalid --level, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --level") {
		t.Fatalf("error = %q, want it to mention 'invalid --level'", err.Error())
	}
}

func TestCLI_LogsList_WarningNormalisesToWarn(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL, "--level", "warning"); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "level=warn") {
		t.Fatalf("expected level=warn normalisation, got %q", state.lastQuery)
	}
	if strings.Contains(state.lastQuery, "level=warning") {
		t.Fatalf("warning should have been canonicalised to warn: %q", state.lastQuery)
	}
}

func TestCLI_LogsList_RejectsNegativeLimit(t *testing.T) {
	srv, _ := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	_, err := runCLI("logs", "list", "--api-url", srv.URL, "--limit", "-1")
	if err == nil {
		t.Fatalf("expected error for negative --limit, got nil")
	}
	if !strings.Contains(err.Error(), "--limit") {
		t.Fatalf("error = %q, want it to mention --limit", err.Error())
	}
}

func TestCLI_LogsList_NoEntriesMessage(t *testing.T) {
	srv, _ := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	out, err := runCLI("logs", "list", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if !strings.Contains(out, "No log entries.") {
		t.Fatalf("expected 'No log entries.' message, got %q", out)
	}
}

func TestCLI_LogsList_PropagatesDaemonError(t *testing.T) {
	srv, _ := newFakeLogsDaemon(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	_, err := runCLI("logs", "list", "--api-url", srv.URL)
	if err == nil {
		t.Fatalf("expected error for 401 response, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %q, want HTTP 401", err.Error())
	}
}

func TestCLI_LogsList_AuthorizationHeader(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL, "--api-key", "secret-token"); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if state.authHdr != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want Bearer secret-token", state.authHdr)
	}
}

func TestCLI_LogsList_ForwardsSortAndOrder(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL,
		"--sort", "level", "--order", "desc"); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	for _, want := range []string{"sort=level", "order=desc"} {
		if !strings.Contains(state.lastQuery, want) {
			t.Fatalf("query missing %q: %q", want, state.lastQuery)
		}
	}
}

func TestCLI_LogsList_NormalisesSortAndOrderCase(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL,
		"--sort", "  SOURCE  ", "--order", " DESC "); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "sort=source") {
		t.Fatalf("expected lowercase trimmed sort=source, got %q", state.lastQuery)
	}
	if !strings.Contains(state.lastQuery, "order=desc") {
		t.Fatalf("expected lowercase trimmed order=desc, got %q", state.lastQuery)
	}
}

func TestCLI_LogsList_RejectsInvalidSort(t *testing.T) {
	srv, _ := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	_, err := runCLI("logs", "list", "--api-url", srv.URL, "--sort", "bogus")
	if err == nil {
		t.Fatal("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("error = %q, want 'invalid --sort'", err.Error())
	}
}

func TestCLI_LogsList_RejectsInvalidOrder(t *testing.T) {
	srv, _ := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	_, err := runCLI("logs", "list", "--api-url", srv.URL, "--order", "sideways")
	if err == nil {
		t.Fatal("expected error for invalid --order, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --order") {
		t.Fatalf("error = %q, want 'invalid --order'", err.Error())
	}
}

func TestCLI_LogsList_EmptySortAndOrderOmitParams(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL,
		"--sort", "", "--order", ""); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	for _, banned := range []string{"sort=", "order="} {
		if strings.Contains(state.lastQuery, banned) {
			t.Fatalf("empty sort/order must not be forwarded; query=%q contained %q", state.lastQuery, banned)
		}
	}
}

func TestCLI_LogsList_ForwardsVMIDFilter(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL, "--vm-id", "vm-1741234567890"); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "vm_id=vm-1741234567890") {
		t.Fatalf("expected vm_id=vm-1741234567890 in query, got %q", state.lastQuery)
	}
}

func TestCLI_LogsList_VMIDFilterIsTrimmed(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL, "--vm-id", "   vm-trimmed   "); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "vm_id=vm-trimmed") {
		t.Fatalf("expected trimmed vm_id, got %q", state.lastQuery)
	}
	if strings.Contains(state.lastQuery, "vm_id=+++vm-trimmed") || strings.Contains(state.lastQuery, "vm_id=%20") {
		t.Fatalf("query should not retain whitespace: %q", state.lastQuery)
	}
}

func TestCLI_LogsList_EmptyVMIDOmitsParam(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL, "--vm-id", "   "); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	if strings.Contains(state.lastQuery, "vm_id=") {
		t.Fatalf("whitespace-only --vm-id must not be forwarded; query=%q", state.lastQuery)
	}
}

func TestCLI_LogsList_VMIDFilterComposesWithOtherFilters(t *testing.T) {
	srv, state := newFakeLogsDaemon(t, http.StatusOK, `{"entries":[],"total":0}`)
	if _, err := runCLI("logs", "list", "--api-url", srv.URL,
		"--vm-id", "vm-compose", "--level", "warn", "--source", "daemon", "--search", "boot"); err != nil {
		t.Fatalf("logs list: %v", err)
	}
	for _, want := range []string{"vm_id=vm-compose", "level=warn", "source=daemon", "search=boot"} {
		if !strings.Contains(state.lastQuery, want) {
			t.Fatalf("expected %q in query, got %q", want, state.lastQuery)
		}
	}
}
