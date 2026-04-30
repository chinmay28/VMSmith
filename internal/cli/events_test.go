package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// withTestEventStore sets storeOverrideForCLI to a real store backed by a temp
// dir and returns the store + cleanup so tests can seed events directly.
func withTestEventStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = filepath.Join(dir, "test.db")
	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	storeOverrideForCLI = func() (*store.Store, func(), error) {
		return s, func() {}, nil
	}
	return s, func() {
		storeOverrideForCLI = nil
		s.Close()
	}
}

func TestCLI_EventsList_Empty(t *testing.T) {
	_, cleanup := withTestEventStore(t)
	defer cleanup()

	out, err := runCLI("events", "list")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	if !strings.Contains(out, "No events.") {
		t.Errorf("expected 'No events.' message, got %q", out)
	}
}

func TestCLI_EventsList_RendersAllFields(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{
		ID:        "evt-1",
		Type:      "vm_started",
		Source:    "libvirt",
		Severity:  "info",
		VMID:      "vm-abc",
		Message:   "VM started",
		CreatedAt: now,
	})

	out, err := runCLI("events", "list")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	for _, want := range []string{"vm_started", "libvirt", "vm-abc", "info"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestCLI_EventsList_FilterByVM(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", VMID: "vm-A", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_started", VMID: "vm-B", CreatedAt: now})

	out, err := runCLI("events", "list", "--vm", "vm-A")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	if !strings.Contains(out, "vm-A") {
		t.Errorf("output missing vm-A\n%s", out)
	}
	if strings.Contains(out, "vm-B") {
		t.Errorf("output should not contain vm-B\n%s", out)
	}
}

func TestCLI_EventsList_FilterBySource(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	s.PutEvent(&types.Event{ID: "evt-1", Type: "vm_started", Source: "libvirt", VMID: "vm-1", CreatedAt: now})
	s.PutEvent(&types.Event{ID: "evt-2", Type: "vm_created", Source: "app", VMID: "vm-1", CreatedAt: now})

	out, err := runCLI("events", "list", "--source", "libvirt")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	if !strings.Contains(out, "vm_started") || strings.Contains(out, "vm_created") {
		t.Errorf("source filter not applied:\n%s", out)
	}
}

func TestCLI_EventsList_NewestFirst(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	s.PutEvent(&types.Event{ID: "evt-old", Type: "vm_old", VMID: "vm-X", CreatedAt: older})
	s.PutEvent(&types.Event{ID: "evt-new", Type: "vm_new", VMID: "vm-X", CreatedAt: newer})

	out, err := runCLI("events", "list")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	idxNew := strings.Index(out, "vm_new")
	idxOld := strings.Index(out, "vm_old")
	if idxNew < 0 || idxOld < 0 || idxNew >= idxOld {
		t.Errorf("expected newest first; vm_new=%d vm_old=%d\n%s", idxNew, idxOld, out)
	}
}

func TestCLI_EventsList_SinceDuration(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	old := time.Now().Add(-10 * time.Minute)
	recent := time.Now().Add(-1 * time.Minute)
	s.PutEvent(&types.Event{ID: "evt-old", Type: "old_event", CreatedAt: old})
	s.PutEvent(&types.Event{ID: "evt-recent", Type: "recent_event", CreatedAt: recent})

	out, err := runCLI("events", "list", "--since", "5m")
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	if !strings.Contains(out, "recent_event") || strings.Contains(out, "old_event") {
		t.Errorf("--since 5m did not exclude old event:\n%s", out)
	}
}

func TestCLI_EventsList_LimitCaps(t *testing.T) {
	s, cleanup := withTestEventStore(t)
	defer cleanup()

	now := time.Now()
	for i := 0; i < 5; i++ {
		s.PutEvent(&types.Event{
			ID:        "evt-" + string(rune('A'+i)),
			Type:      "vm_evt_" + string(rune('A'+i)),
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	out, err := runCLI("events", "list", "--limit", "2")
	if err != nil {
		t.Fatalf("events list --limit 2: %v", err)
	}
	// Header + 2 rows = 3 newline-separated lines (plus trailing).
	dataLines := 0
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(ln, "vm_evt_") {
			dataLines++
		}
	}
	if dataLines != 2 {
		t.Errorf("expected 2 event rows, got %d:\n%s", dataLines, out)
	}
}

func TestCLI_EventsList_InvalidSince(t *testing.T) {
	_, cleanup := withTestEventStore(t)
	defer cleanup()

	_, err := runCLI("events", "list", "--since", "not-a-thing")
	if err == nil {
		t.Fatal("expected error for invalid --since")
	}
	if !strings.Contains(err.Error(), "invalid --since") {
		t.Errorf("wrong error: %v", err)
	}
}

// --- events follow tests ---

// sseEventTestServer serves a deterministic SSE stream for testing.
type sseEventTestServer struct {
	mu          sync.Mutex
	events      []*types.Event   // events to send on first connect
	replay      []*types.Event   // events to send when Last-Event-ID is set
	authHeaders []string         // captured Authorization headers
	lastIDSeen  []string         // captured Last-Event-ID headers
	statusOnce  int              // optional one-shot status code (0 = 200)
	server      *httptest.Server // backing test server
}

func newSSETestServer(t *testing.T, events []*types.Event) *sseEventTestServer {
	t.Helper()
	srv := &sseEventTestServer{events: events}
	srv.server = httptest.NewServer(http.HandlerFunc(srv.handle))
	t.Cleanup(srv.server.Close)
	return srv
}

func (s *sseEventTestServer) URL() string { return s.server.URL }

func (s *sseEventTestServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.authHeaders = append(s.authHeaders, r.Header.Get("Authorization"))
	s.lastIDSeen = append(s.lastIDSeen, r.Header.Get("Last-Event-ID"))
	statusOnce := s.statusOnce
	s.statusOnce = 0
	events := s.events
	if r.Header.Get("Last-Event-ID") != "" && s.replay != nil {
		events = s.replay
	}
	s.mu.Unlock()

	if statusOnce != 0 {
		w.WriteHeader(statusOnce)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flush", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for _, e := range events {
		data, _ := json.Marshal(e)
		fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", e.ID, e.Type, data)
		flusher.Flush()
	}
	// keep open briefly so the client doesn't immediately reconnect
	select {
	case <-r.Context().Done():
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFollow_PrintsEventsAsTheyArrive(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", Source: "libvirt", Severity: "info", VMID: "vm-A", Message: "started", OccurredAt: time.Now()},
		{ID: "2", Type: "vm.stopped", Source: "libvirt", Severity: "info", VMID: "vm-A", Message: "stopped", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "", eventFilter{}, &buf); err != nil {
		t.Fatalf("followEventsStream: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"vm.started", "vm.stopped", "vm-A"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFollow_FiltersByVMID(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "2", Type: "vm.started", VMID: "vm-B", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "", eventFilter{vmID: "vm-A"}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "vm-A") {
		t.Errorf("vm-A row missing:\n%s", out)
	}
	if strings.Contains(out, "vm-B") {
		t.Errorf("vm-B row should be filtered out:\n%s", out)
	}
}

func TestFollow_FiltersByTypeSourceSeverity(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", Source: "libvirt", Severity: "info", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "2", Type: "vm.created", Source: "app", Severity: "info", VMID: "vm-A", OccurredAt: time.Now()},
		{ID: "3", Type: "vm.crashed", Source: "libvirt", Severity: "error", VMID: "vm-A", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "",
		eventFilter{source: "libvirt", severity: "error"}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "vm.crashed") {
		t.Errorf("vm.crashed should pass libvirt+error filter:\n%s", out)
	}
	if strings.Contains(out, "vm.started") || strings.Contains(out, "vm.created") {
		t.Errorf("non-matching events should be filtered:\n%s", out)
	}
}

func TestFollow_AuthHeaderForwarded(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "secret-key", eventFilter{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.authHeaders) == 0 || srv.authHeaders[0] != "Bearer secret-key" {
		t.Errorf("expected Authorization: Bearer secret-key, got %v", srv.authHeaders)
	}
}

func TestFollow_AuthFailureIsFatal(t *testing.T) {
	srv := newSSETestServer(t, nil)
	srv.statusOnce = http.StatusUnauthorized

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var buf bytes.Buffer
	err := followEventsStream(ctx, srv.URL(), "", eventFilter{}, &buf)
	if err == nil {
		t.Fatal("expected fatal auth error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("expected auth-failed error, got: %v", err)
	}
}

func TestFollow_GoneIsFatal(t *testing.T) {
	srv := newSSETestServer(t, nil)
	srv.statusOnce = http.StatusGone

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var buf bytes.Buffer
	err := followEventsStream(ctx, srv.URL(), "", eventFilter{}, &buf)
	if err == nil {
		t.Fatal("expected 410 fatal error")
	}
	if !strings.Contains(err.Error(), "replay window exceeded") {
		t.Errorf("expected replay-window error, got: %v", err)
	}
}

func TestFollow_ReconnectsOnDisconnect(t *testing.T) {
	srv := newSSETestServer(t, []*types.Event{
		{ID: "1", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()},
	})
	srv.replay = []*types.Event{
		{ID: "2", Type: "vm.stopped", VMID: "vm-A", OccurredAt: time.Now()},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL(), "", eventFilter{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "vm.started") {
		t.Errorf("expected first connect events:\n%s", out)
	}
	if !strings.Contains(out, "vm.stopped") {
		t.Errorf("expected reconnect (replay) events:\n%s", out)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	// Must have seen at least one Last-Event-ID header on a reconnect.
	saw := false
	for _, id := range srv.lastIDSeen {
		if id == "1" {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected Last-Event-ID=1 on reconnect, headers seen: %v", srv.lastIDSeen)
	}
}

func TestFollow_HeartbeatIgnored(t *testing.T) {
	// Custom server that emits a heartbeat then one event then closes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f.Flush()
		fmt.Fprint(w, ": keepalive\n\n")
		f.Flush()
		evt := &types.Event{ID: "1", Type: "vm.started", VMID: "vm-A", OccurredAt: time.Now()}
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", evt.ID, evt.Type, data)
		f.Flush()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	if err := followEventsStream(ctx, srv.URL, "", eventFilter{}, &buf); err != nil {
		t.Fatalf("follow: %v", err)
	}
	if !strings.Contains(buf.String(), "vm.started") {
		t.Errorf("event after heartbeat missing:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "keepalive") {
		t.Errorf("heartbeat comment should not be printed:\n%s", buf.String())
	}
}

func TestLastIDToSeq(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"42", "42", false},
		{"0", "0", false},
		{"evt-1234", "", true},
		{"", "", true},
		{"abc", "", true},
	}
	for _, c := range cases {
		got, err := lastIDToSeq(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("lastIDToSeq(%q) err=%v want err=%v", c.in, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("lastIDToSeq(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchesEventFilter(t *testing.T) {
	e := &types.Event{Type: "vm.started", Source: "libvirt", Severity: "info", VMID: "vm-A"}
	cases := []struct {
		name  string
		f     eventFilter
		match bool
	}{
		{"empty filter matches", eventFilter{}, true},
		{"vm match", eventFilter{vmID: "vm-A"}, true},
		{"vm mismatch", eventFilter{vmID: "vm-B"}, false},
		{"type match", eventFilter{typeStr: "vm.started"}, true},
		{"type mismatch", eventFilter{typeStr: "vm.stopped"}, false},
		{"source case-insensitive", eventFilter{source: "LIBVIRT"}, true},
		{"severity case-insensitive", eventFilter{severity: "INFO"}, true},
		{"severity mismatch", eventFilter{severity: "error"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchesEventFilter(e, c.f); got != c.match {
				t.Errorf("got %v, want %v", got, c.match)
			}
		})
	}
}
