package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/logger"
)

func TestRequestLogger_RedactsSensitiveQueryParams(t *testing.T) {
	if err := logger.Init("", logger.LevelDebug); err != nil {
		t.Fatalf("init logger: %v", err)
	}
	defer logger.Close()

	h := requestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/vms/vm-1/console?ticket=secret-ticket&api_key=secret-key&since=42", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	entries := logger.Get().Entries("debug", logger.Entry{}.Timestamp, 10)
	if len(entries) == 0 {
		t.Fatal("expected at least one log entry")
	}

	query := entries[len(entries)-1].Fields["query"]
	if strings.Contains(query, "secret-ticket") {
		t.Fatalf("ticket leaked in query log: %q", query)
	}
	if strings.Contains(query, "secret-key") {
		t.Fatalf("api_key leaked in query log: %q", query)
	}
	if !strings.Contains(query, "ticket=REDACTED") {
		t.Fatalf("expected redacted ticket in query log, got %q", query)
	}
	if !strings.Contains(query, "api_key=REDACTED") {
		t.Fatalf("expected redacted api_key in query log, got %q", query)
	}
	if !strings.Contains(query, "since=42") {
		t.Fatalf("expected non-sensitive params to remain visible, got %q", query)
	}
}

func TestRedactSensitiveRawQuery_PreservesNonSensitiveAndMalformedParts(t *testing.T) {
	raw := "ticket=abc123&empty=&flag&name=vm-1&api_key=xyz"
	got := redactSensitiveRawQuery(raw)
	want := "ticket=REDACTED&empty=&flag&name=vm-1&api_key=REDACTED"
	if got != want {
		t.Fatalf("redactSensitiveRawQuery() = %q, want %q", got, want)
	}
}
