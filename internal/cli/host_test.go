package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeHostDaemon captures the most recent GET path + Authorization header
// against the host/quota endpoints so we can assert the CLI talked to the
// right URL with the right credentials.
type fakeHostDaemon struct {
	lastPath string
	authHdr  string
	status   int
	respBody string
}

func newFakeHostDaemon(t *testing.T, status int, body string) (*httptest.Server, *fakeHostDaemon) {
	t.Helper()
	state := &fakeHostDaemon{status: status, respBody: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.authHdr = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(state.status)
		_, _ = w.Write([]byte(state.respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

const sampleHostStatsBody = `{
  "vm_count": 7,
  "cpu":  {"used": 23, "total": 100, "available": 77, "percentage": 23},
  "ram":  {"used": 8589934592, "total": 17179869184, "available": 8589934592, "percentage": 50},
  "disk": {"used": 53687091200, "total": 1099511627776, "available": 1045824536576, "percentage": 4},
  "event_stream_connections": 2
}`

const sampleQuotasBody = `{
  "vms":    {"used": 7,  "limit": 32},
  "cpus":   {"used": 14, "limit": 64},
  "ram_mb": {"used": 8192, "limit": 65536},
  "disk_gb":{"used": 50},
  "gpus":   {"used": 2,  "limit": 4}
}`

func TestCLI_HostStats_HappyPath(t *testing.T) {
	srv, state := newFakeHostDaemon(t, http.StatusOK, sampleHostStatsBody)

	out, err := runCLI("host", "stats", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("host stats: %v\nout=%s", err, out)
	}
	if state.lastPath != "/api/v1/host/stats" {
		t.Fatalf("path = %q, want /api/v1/host/stats", state.lastPath)
	}
	for _, want := range []string{"RESOURCE", "USED", "TOTAL", "PERCENT",
		"VMs", "7", "CPU", "23%", "RAM", "Disk", "SSE clients", "2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
	// Binary-unit rendering (8 GiB / 16 GiB for RAM, 50 GiB for Disk used,
	// 1.0 TiB for Disk total — formatBytes promotes to the next unit at 1024).
	for _, want := range []string{"8.0 GiB", "16.0 GiB", "50.0 GiB", "1.0 TiB"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing humanised bytes %q: %s", want, out)
		}
	}
}

func TestCLI_HostStats_JSONFlagPassesThrough(t *testing.T) {
	srv, _ := newFakeHostDaemon(t, http.StatusOK, sampleHostStatsBody)

	out, err := runCLI("host", "stats", "--api-url", srv.URL, "--json")
	if err != nil {
		t.Fatalf("host stats --json: %v", err)
	}
	// --json should emit the body verbatim, including the integer fields the
	// table view rewrites into "8.0 GiB" etc.
	for _, want := range []string{`"vm_count": 7`, `"event_stream_connections": 2`, `"percentage": 50`} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected raw JSON to contain %q: %s", want, out)
		}
	}
	// Table headers must NOT show up in the JSON path.
	if strings.Contains(out, "RESOURCE") || strings.Contains(out, "PERCENT") {
		t.Fatalf("--json output should not contain table headers: %s", out)
	}
}

func TestCLI_HostStats_AuthorizationHeader(t *testing.T) {
	srv, state := newFakeHostDaemon(t, http.StatusOK, sampleHostStatsBody)

	if _, err := runCLI("host", "stats", "--api-url", srv.URL, "--api-key", "secret-token"); err != nil {
		t.Fatalf("host stats: %v", err)
	}
	if state.authHdr != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want Bearer secret-token", state.authHdr)
	}
}

func TestCLI_HostStats_PropagatesDaemonError(t *testing.T) {
	srv, _ := newFakeHostDaemon(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)

	_, err := runCLI("host", "stats", "--api-url", srv.URL)
	if err == nil {
		t.Fatalf("expected error for 401 response, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %q, want HTTP 401", err.Error())
	}
}

func TestCLI_HostQuotas_HappyPath(t *testing.T) {
	srv, state := newFakeHostDaemon(t, http.StatusOK, sampleQuotasBody)

	out, err := runCLI("host", "quotas", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("host quotas: %v\nout=%s", err, out)
	}
	if state.lastPath != "/api/v1/quotas/usage" {
		t.Fatalf("path = %q, want /api/v1/quotas/usage", state.lastPath)
	}
	for _, want := range []string{"QUOTA", "USED", "LIMIT",
		"VMs", "7", "32",
		"CPUs", "14", "64 vCPU",
		"RAM", "8192 MB", "65536 MB",
		"Disk", "50 GB",
		"GPUs", "2", "4",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
}

func TestCLI_HostQuotas_RendersUnlimitedWhenLimitIsZero(t *testing.T) {
	// CPUs, RAM, Disk, and GPUs all omit "limit" (== 0) so they should
	// print "unlimited"; VMs is the only configured cap.
	const body = `{
		"vms":    {"used": 1, "limit": 5},
		"cpus":   {"used": 2},
		"ram_mb": {"used": 1024},
		"disk_gb":{"used": 10},
		"gpus":   {"used": 0}
	}`
	srv, _ := newFakeHostDaemon(t, http.StatusOK, body)

	out, err := runCLI("host", "quotas", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("host quotas: %v", err)
	}
	// Exactly four "unlimited" rows: CPUs, RAM, Disk, GPUs. VMs has a configured cap.
	if count := strings.Count(out, "unlimited"); count != 4 {
		t.Fatalf("expected 4 'unlimited' rows, got %d in %s", count, out)
	}
	if !strings.Contains(out, "VMs") || !strings.Contains(out, " 5") {
		t.Fatalf("VMs row should still render its configured limit: %s", out)
	}
}

func TestCLI_HostQuotas_JSONFlagPassesThrough(t *testing.T) {
	srv, _ := newFakeHostDaemon(t, http.StatusOK, sampleQuotasBody)

	out, err := runCLI("host", "quotas", "--api-url", srv.URL, "--json")
	if err != nil {
		t.Fatalf("host quotas --json: %v", err)
	}
	for _, want := range []string{`"limit": 32`, `"used": 14`, `"used": 50`} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected raw JSON to contain %q: %s", want, out)
		}
	}
	if strings.Contains(out, "QUOTA") || strings.Contains(out, "LIMIT\n") {
		t.Fatalf("--json output should not contain table headers: %s", out)
	}
}

func TestCLI_HostQuotas_AuthorizationHeader(t *testing.T) {
	srv, state := newFakeHostDaemon(t, http.StatusOK, sampleQuotasBody)

	if _, err := runCLI("host", "quotas", "--api-url", srv.URL, "--api-key", "secret-token"); err != nil {
		t.Fatalf("host quotas: %v", err)
	}
	if state.authHdr != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want Bearer secret-token", state.authHdr)
	}
}

func TestCLI_HostQuotas_PropagatesDaemonError(t *testing.T) {
	srv, _ := newFakeHostDaemon(t, http.StatusServiceUnavailable, `{"error":"unavailable"}`)
	_, err := runCLI("host", "quotas", "--api-url", srv.URL)
	if err == nil {
		t.Fatalf("expected error for 503 response, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("error = %q, want HTTP 503", err.Error())
	}
}

func TestFormatBytes_HumanReadable(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{2048, "2.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{8 * 1024 * 1024 * 1024, "8.0 GiB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TiB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuotaLimit_RendersUnitsAndUnlimited(t *testing.T) {
	cases := []struct {
		limit int
		unit  string
		want  string
	}{
		{0, "MB", "unlimited"},
		{0, "", "unlimited"},
		{-1, "GB", "unlimited"},
		{5, "", "5"},
		{8, "vCPU", "8 vCPU"},
	}
	for _, c := range cases {
		if got := quotaLimit(c.limit, c.unit); got != c.want {
			t.Errorf("quotaLimit(%d, %q) = %q, want %q", c.limit, c.unit, got, c.want)
		}
	}
}
