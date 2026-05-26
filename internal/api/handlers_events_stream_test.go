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

	br := bufio.NewReader(resp.Body)
	frame, err := readSSEFrame(br)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	cancel()
	io.Copy(io.Discard, resp.Body)

	if frame.id != "2" || frame.event != "vm.stopped" {
		t.Fatalf("unexpected frame: id=%q event=%q data=%q", frame.id, frame.event, frame.data)
	}
}

func TestStreamEvents_ReplayOverflowReturnsGone(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	// Append 1002 events; ?since=1 leaves 1001 events to replay, which is
	// strictly greater than the handler's sseReplayLimit (1000) and triggers
	// the 410 short-circuit.
	now := time.Now().Truncate(time.Millisecond)
	for i := 0; i < 1002; i++ {
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
	// The 410 short-circuit must commit a JSON error response, not the SSE
	// content-type — otherwise clients hang reading an unterminated stream
	// (this was the root cause of the original handler bug).
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json on 410 path", ct)
	}
	if v := resp.Header.Get("Cache-Control"); strings.Contains(v, "text/event-stream") {
		t.Fatalf("unexpected SSE Cache-Control on 410 path: %q", v)
	}
}

// TestStreamEvents_FilterReplay_VMID verifies that ?vm_id= drops non-matching
// events from the replay window before they're written to the SSE stream so
// clients tailing a single VM don't have to discard every other event.
//
// Cursor semantics: Last-Event-ID: 1 → replay seqs 2..5; vm_id=vm-1 keeps
// seqs 3 and 5 (the two vm-1 events after the cursor).
func TestStreamEvents_FilterReplay_VMID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)
	for i, evt := range []*types.Event{
		{VMID: "vm-1", Type: "vm.started", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-5 * time.Minute)},
		{VMID: "vm-2", Type: "vm.started", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-4 * time.Minute)},
		{VMID: "vm-1", Type: "vm.stopped", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-3 * time.Minute)},
		{VMID: "vm-2", Type: "vm.stopped", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-2 * time.Minute)},
		{VMID: "vm-1", Type: "vm.deleted", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-1 * time.Minute)},
	} {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent %d: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream?vm_id=vm-1", nil)
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

	br := bufio.NewReader(resp.Body)
	ids := []string{}
	deadline := time.After(2 * time.Second)
	readDone := make(chan struct{})
	go func() {
		for {
			f, err := readSSEFrame(br)
			if err != nil {
				close(readDone)
				return
			}
			ids = append(ids, f.id)
			if len(ids) >= 2 {
				close(readDone)
				return
			}
		}
	}()
	select {
	case <-readDone:
	case <-deadline:
		t.Fatalf("timed out waiting for filtered replay frames; got %v", ids)
	}
	cancel()
	io.Copy(io.Discard, resp.Body)
	if len(ids) != 2 || ids[0] != "3" || ids[1] != "5" {
		t.Fatalf("filtered replay ids = %v, want [3 5]", ids)
	}
}

// TestStreamEvents_FilterReplay_TypePrefix verifies the case-insensitive
// prefix predicate is applied to replayed events so SSE clients can slice
// the timeline by event family (e.g. snapshot.*) without forwarding noise.
//
// Cursor semantics: Last-Event-ID: 1 → replay seqs 2..4; type_prefix=Snapshot.
// keeps seqs 2 and 4 (the two snapshot.* events after the cursor).
func TestStreamEvents_FilterReplay_TypePrefix(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)
	for i, evt := range []*types.Event{
		{Type: "vm.created", VMID: "vm-1", Source: types.EventSourceApp, OccurredAt: now.Add(-4 * time.Minute)},
		{Type: "snapshot.created", VMID: "vm-1", Source: types.EventSourceApp, OccurredAt: now.Add(-3 * time.Minute)},
		{Type: "vm.started", VMID: "vm-1", Source: types.EventSourceLibvirt, OccurredAt: now.Add(-2 * time.Minute)},
		{Type: "snapshot.deleted", VMID: "vm-1", Source: types.EventSourceApp, OccurredAt: now.Add(-1 * time.Minute)},
	} {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent %d: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream?type_prefix=Snapshot.", nil)
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

	br := bufio.NewReader(resp.Body)
	frames := []*sseFrame{}
	deadline := time.After(2 * time.Second)
	readDone := make(chan struct{})
	go func() {
		for {
			f, err := readSSEFrame(br)
			if err != nil {
				close(readDone)
				return
			}
			frames = append(frames, f)
			if len(frames) >= 2 {
				close(readDone)
				return
			}
		}
	}()
	select {
	case <-readDone:
	case <-deadline:
		t.Fatalf("timed out waiting for filtered replay frames; got %d", len(frames))
	}
	cancel()
	io.Copy(io.Discard, resp.Body)
	if len(frames) != 2 || frames[0].event != "snapshot.created" || frames[1].event != "snapshot.deleted" {
		t.Fatalf("filtered replay events = %d frames; want 2 snapshot.*", len(frames))
	}
}

// TestStreamEvents_FilterReplay_MinSeverity verifies the ?min_severity= floor
// drops below-floor events during SSE replay just like the paginated list.
func TestStreamEvents_FilterReplay_MinSeverity(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)
	for i, evt := range []*types.Event{
		{Type: "vm.created", VMID: "vm-1", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-4 * time.Minute)},
		{Type: "vm.stopped", VMID: "vm-1", Source: types.EventSourceLibvirt, Severity: types.EventSeverityWarn, OccurredAt: now.Add(-3 * time.Minute)},
		{Type: "vm.started", VMID: "vm-1", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, OccurredAt: now.Add(-2 * time.Minute)},
		{Type: "dhcp.exhausted", Source: types.EventSourceSystem, Severity: types.EventSeverityError, OccurredAt: now.Add(-1 * time.Minute)},
	} {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent %d: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream?min_severity=warn", nil)
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

	br := bufio.NewReader(resp.Body)
	frames := []*sseFrame{}
	deadline := time.After(2 * time.Second)
	readDone := make(chan struct{})
	go func() {
		for {
			f, err := readSSEFrame(br)
			if err != nil {
				close(readDone)
				return
			}
			frames = append(frames, f)
			if len(frames) >= 2 {
				close(readDone)
				return
			}
		}
	}()
	select {
	case <-readDone:
	case <-deadline:
		t.Fatalf("timed out waiting for filtered replay frames; got %d", len(frames))
	}
	cancel()
	io.Copy(io.Discard, resp.Body)
	// Replay after seq 1 → seqs 2,3,4. min_severity=warn keeps the warn
	// (vm.stopped) and the error (dhcp.exhausted), dropping the info started.
	if len(frames) != 2 || frames[0].event != "vm.stopped" || frames[1].event != "dhcp.exhausted" {
		t.Fatalf("min_severity=warn replay = %d frames; want vm.stopped + dhcp.exhausted", len(frames))
	}
}

func TestStreamEvents_InvalidMinSeverityReturns400(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/events/stream?min_severity=bogus")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestStreamEvents_FilterLive_VMID exercises the live SSE pipeline with a
// server-side filter: non-matching events published to the bus must be
// dropped before they cross the wire.
func TestStreamEvents_FilterLive_VMID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	_, _, stop := wireEventBusWithStore(t, ts, s)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream?vm_id=vm-keep", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	frameCh := make(chan *sseFrame, 1)
	errCh := make(chan error, 1)
	go func() {
		br := bufio.NewReader(resp.Body)
		f, err := readSSEFrame(br)
		if err != nil {
			errCh <- err
			return
		}
		frameCh <- f
	}()

	srv := ts.Config.Handler.(*Server)
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			// Publish a noisy non-matching event interleaved with the matching one;
			// the server-side filter must drop the noise and only emit vm-keep.
			srv.publishAppEvent("vm.stopped", "vm-drop", "noise", nil)
			srv.publishAppEvent("vm.started", "vm-keep", "signal", nil)
		case err := <-errCh:
			t.Fatalf("read live frame: %v", err)
		case frame := <-frameCh:
			if frame.event != "vm.started" {
				t.Fatalf("first live frame event = %q, want vm.started", frame.event)
			}
			// Verify that the frame is for vm-keep (the filter target), not vm-drop.
			if !strings.Contains(frame.data, `"vm_id":"vm-keep"`) {
				t.Fatalf("live frame data missing vm-keep: %s", frame.data)
			}
			if strings.Contains(frame.data, `"vm_id":"vm-drop"`) {
				t.Fatalf("vm-drop noise leaked across filter: %s", frame.data)
			}
			cancel()
			io.Copy(io.Discard, resp.Body)
			return
		case <-deadline:
			t.Fatal("timed out waiting for filtered live SSE frame")
		}
	}
}

// TestStreamEvents_FilterReplay_Search verifies the case-insensitive substring
// match works server-side for replay: clients can fetch only events whose
// haystack (message/type/source/severity/actor/vm_id/resource_id/attrs)
// contains the needle.
//
// Cursor semantics: Last-Event-ID: 1 → replay seqs 2..4; search=BASTION
// (case-insensitive) keeps seq 3 only ("bastion deploy").
func TestStreamEvents_FilterReplay_Search(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)
	for i, evt := range []*types.Event{
		{Type: "vm.created", VMID: "vm-1", Message: "boot", Source: types.EventSourceApp, OccurredAt: now.Add(-4 * time.Minute)},
		{Type: "vm.stopped", VMID: "vm-2", Message: "web1 offline", Source: types.EventSourceLibvirt, OccurredAt: now.Add(-3 * time.Minute)},
		{Type: "vm.started", VMID: "vm-1", Message: "bastion online", Source: types.EventSourceLibvirt, OccurredAt: now.Add(-2 * time.Minute)},
		{Type: "snapshot.created", VMID: "vm-1", Message: "pre-deploy", Source: types.EventSourceApp, OccurredAt: now.Add(-1 * time.Minute)},
	} {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent %d: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream?search=BASTION", nil)
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
	br := bufio.NewReader(resp.Body)

	frameCh := make(chan *sseFrame, 1)
	go func() {
		f, err := readSSEFrame(br)
		if err == nil {
			frameCh <- f
		}
	}()
	select {
	case f := <-frameCh:
		if f.id != "3" || f.event != "vm.started" {
			t.Fatalf("search matched wrong frame: id=%s event=%s data=%s", f.id, f.event, f.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for search-matched replay frame")
	}
	cancel()
	io.Copy(io.Discard, resp.Body)
}

// TestStreamEvents_LiveDeliveryAfterReplay exercises the full SSE pipeline:
// after the handler subscribes to the EventBus, a fresh event published via
// the bus is delivered to the SSE client as a vm.deleted frame.  Pins down
// the bus subscription path that 4.2.17 flagged as gap-coverage and that
// existing tests (which subscribe to the bus channel directly) do not cover.
//
// Race-robust by design: there is no synchronous signal the SSE handler emits
// when its Subscribe() returns, so we publish on a 50ms tick until the SSE
// reader picks up a frame or the overall deadline elapses.
func TestStreamEvents_ShutdownFrameOnBeginShutdown(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	srv := ts.Config.Handler.(*Server)
	srv.BeginShutdown()

	frame, err := readSSEFrame(bufio.NewReader(resp.Body))
	if err != nil {
		t.Fatalf("read shutdown frame: %v", err)
	}
	if frame.id != "" {
		t.Fatalf("shutdown frame id = %q, want empty", frame.id)
	}
	if frame.event != "shutdown" {
		t.Fatalf("shutdown frame event = %q, want shutdown", frame.event)
	}
	if frame.data != `{"message":"server is shutting down"}` {
		t.Fatalf("shutdown frame data = %q", frame.data)
	}
}

func TestStreamEvents_LiveDeliveryAfterReplay(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	_, _, stop := wireEventBusWithStore(t, ts, s)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	frameCh := make(chan *sseFrame, 1)
	errCh := make(chan error, 1)
	go func() {
		br := bufio.NewReader(resp.Body)
		f, err := readSSEFrame(br)
		if err != nil {
			errCh <- err
			return
		}
		frameCh <- f
	}()

	srv := ts.Config.Handler.(*Server)
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			srv.publishAppEvent("vm.deleted", "vm-live", "live test", nil)
		case err := <-errCh:
			t.Fatalf("read live frame: %v", err)
		case frame := <-frameCh:
			if frame.event != "vm.deleted" {
				t.Fatalf("first live frame event = %q, want vm.deleted (data=%q)", frame.event, frame.data)
			}
			cancel()
			io.Copy(io.Discard, resp.Body)
			return
		case <-deadline:
			t.Fatal("timed out waiting for live SSE frame")
		}
	}
}
