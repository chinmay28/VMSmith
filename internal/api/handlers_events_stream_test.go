package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestListEvents_FilterVariantsAndPaginationHeaders(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)
	events := []*types.Event{
		{VMID: "vm-1", Type: "vm.started", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-4 * time.Minute)},
		{VMID: "vm-2", Type: "quota.exceeded", Source: types.EventSourceSystem, Severity: types.EventSeverityWarn, OccurredAt: now.Add(-3 * time.Minute)},
		{VMID: "vm-1", Type: "vm.deleted", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-2 * time.Minute)},
		{VMID: "vm-3", Type: "webhook.delivery_failed", Source: types.EventSourceSystem, Severity: types.EventSeverityError, OccurredAt: now.Add(-1 * time.Minute)},
	}
	for i, evt := range events {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent %d: %v", i+1, err)
		}
	}

	resp, err := http.Get(ts.URL + "/api/v1/events?source=system&severity=error&type=webhook.delivery_failed&page=1&per_page=1")
	if err != nil {
		t.Fatalf("GET filtered events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1", got)
	}
	var filtered []*types.Event
	if err := json.NewDecoder(resp.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered response: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("len(filtered) = %d, want 1", len(filtered))
	}
	if filtered[0].Type != "webhook.delivery_failed" || filtered[0].ID != "4" {
		t.Fatalf("unexpected filtered event: %+v", filtered[0])
	}

	resp, err = http.Get(ts.URL + "/api/v1/events?until=4&page=1&per_page=2")
	if err != nil {
		t.Fatalf("GET until events: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("until X-Total-Count = %q, want 3", got)
	}
	filtered = nil
	if err := json.NewDecoder(resp.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode until response: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("len(until page) = %d, want 2", len(filtered))
	}
	if filtered[0].ID != "3" || filtered[1].ID != "2" {
		t.Fatalf("until page ids = [%s %s], want [3 2]", filtered[0].ID, filtered[1].ID)
	}
}

func TestStreamEvents_ReplaysFromLastEventID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)
	for i, evt := range []*types.Event{
		{VMID: "vm-1", Type: "vm.started", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-2 * time.Minute)},
		{VMID: "vm-1", Type: "vm.stopped", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-1 * time.Minute)},
	} {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent %d: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	frame := readSSEFrame(t, resp.Body)
	cancel()
	io.Copy(io.Discard, resp.Body)

	if !strings.Contains(frame, "id: 2\n") || !strings.Contains(frame, "event: vm.stopped\n") {
		t.Fatalf("unexpected frame: %q", frame)
	}
}

func TestStreamEvents_ReplayOverflowReturnsGone(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)
	for i := 0; i < 1001; i++ {
		if _, err := s.AppendEvent(&types.Event{VMID: "vm-1", Type: "vm.started", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(time.Duration(i) * time.Millisecond)}); err != nil {
			t.Fatalf("AppendEvent %d: %v", i+1, err)
		}
	}

	resp, err := http.Get(ts.URL + "/api/v1/events/stream?since=1")
	if err != nil {
		t.Fatalf("GET overflow stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 410, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var apiErr errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode 410 body: %v", err)
	}
	if apiErr.Code != "event_stream_replay_window_exceeded" {
		t.Fatalf("code = %q, want event_stream_replay_window_exceeded", apiErr.Code)
	}
}

func readSSEFrame(t *testing.T, r io.Reader) string {
	t.Helper()
	br := bufio.NewReader(r)
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		b.WriteString(line)
		if line == "\n" {
			return b.String()
		}
	}
}
