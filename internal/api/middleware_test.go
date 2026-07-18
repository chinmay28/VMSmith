package api

import (
	"bytes"
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

func TestRequestLogger_RedactsSensitiveJSONBodyFields(t *testing.T) {
	if err := logger.Init("", logger.LevelDebug); err != nil {
		t.Fatalf("init logger: %v", err)
	}
	defer logger.Close()

	h := requestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	body := []byte(`{"name":"vm-1","vnc_password":"hunter2","ssh_pub_key":"ssh-rsa AAAA test","nested":{"secret":"token"},"items":[{"admin_password":"rootpw"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/vms", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	entries := logger.Get().Entries("debug", logger.Entry{}.Timestamp, 10)
	if len(entries) == 0 {
		t.Fatal("expected at least one log entry")
	}

	loggedBody := entries[len(entries)-1].Fields["body"]
	for _, leaked := range []string{"hunter2", "ssh-rsa AAAA test", "token", "rootpw"} {
		if strings.Contains(loggedBody, leaked) {
			t.Fatalf("sensitive value leaked in body log: %q", loggedBody)
		}
	}
	for _, marker := range []string{`"vnc_password":"REDACTED"`, `"ssh_pub_key":"REDACTED"`, `"secret":"REDACTED"`, `"admin_password":"REDACTED"`} {
		if !strings.Contains(loggedBody, marker) {
			t.Fatalf("expected %s in redacted body log, got %q", marker, loggedBody)
		}
	}
	if !strings.Contains(loggedBody, `"name":"vm-1"`) {
		t.Fatalf("expected non-sensitive fields to remain visible, got %q", loggedBody)
	}
}

func TestRedactSensitiveBody_PreservesNonJSONPayload(t *testing.T) {
	raw := "name=vm-1&secret=hunter2"
	if got := redactSensitiveBody([]byte(raw)); got != raw {
		t.Fatalf("redactSensitiveBody() = %q, want %q", got, raw)
	}
}
