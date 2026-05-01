package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestTrackSSEConnection_IncrementsAndReleases(t *testing.T) {
	s := &Server{}

	if got := s.ActiveSSEConnections(); got != 0 {
		t.Fatalf("initial count = %d, want 0", got)
	}

	r1 := s.trackSSEConnection()
	r2 := s.trackSSEConnection()
	if got := s.ActiveSSEConnections(); got != 2 {
		t.Fatalf("after 2 connects, count = %d, want 2", got)
	}

	r1()
	if got := s.ActiveSSEConnections(); got != 1 {
		t.Fatalf("after 1 release, count = %d, want 1", got)
	}

	// Calling release twice must not double-decrement.
	r1()
	if got := s.ActiveSSEConnections(); got != 1 {
		t.Fatalf("after duplicate release, count = %d, want 1", got)
	}

	r2()
	if got := s.ActiveSSEConnections(); got != 0 {
		t.Fatalf("after final release, count = %d, want 0", got)
	}
}

func TestTrackSSEConnection_IsConcurrencySafe(t *testing.T) {
	s := &Server{}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			release := s.trackSSEConnection()
			release()
		}()
	}
	wg.Wait()

	if got := s.ActiveSSEConnections(); got != 0 {
		t.Fatalf("after %d connect/release pairs, count = %d, want 0", goroutines, got)
	}
}

// TestStreamEvents_TracksActiveSSEConnections opens an SSE stream and
// confirms GET /api/v1/host/stats reports active_sse_streams == 1 while the
// stream is open, and 0 after the client disconnects.
func TestStreamEvents_TracksActiveSSEConnections(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	streamCtx, cancelStream := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, ts.URL+"/api/v1/events/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("stream Content-Type = %q, want text/event-stream", ct)
	}

	// Wait briefly for the StreamEvents handler to register the connection.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats := getHostStats(t, ts.URL)
		if stats.ActiveSSEStreams == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stats := getHostStats(t, ts.URL)
	if stats.ActiveSSEStreams != 1 {
		t.Fatalf("active_sse_streams while open = %d, want 1", stats.ActiveSSEStreams)
	}

	cancelStream()
	resp.Body.Close()

	// Wait for the handler to release the counter.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats := getHostStats(t, ts.URL)
		if stats.ActiveSSEStreams == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stats = getHostStats(t, ts.URL)
	if stats.ActiveSSEStreams != 0 {
		t.Fatalf("active_sse_streams after close = %d, want 0", stats.ActiveSSEStreams)
	}
}

func getHostStats(t *testing.T, baseURL string) types.HostStats {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/v1/host/stats")
	if err != nil {
		t.Fatalf("GET /host/stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("host/stats status = %d, want 200", resp.StatusCode)
	}
	var stats types.HostStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode host stats: %v", err)
	}
	return stats
}

// TestStreamEvents_DoesNotTrackOnFlusherFailure ensures the counter is not
// incremented when streaming is unsupported (newSSEWriter returns nil).
func TestStreamEvents_DoesNotTrackOnFlusherFailure(t *testing.T) {
	s := &Server{}

	rec := &nonFlushingRecorder{ResponseWriter: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream", nil)

	s.StreamEvents(rec, req)

	if got := s.ActiveSSEConnections(); got != 0 {
		t.Fatalf("active connections after non-flushing 500 = %d, want 0", got)
	}
}

// nonFlushingRecorder wraps a ResponseWriter and intentionally does NOT
// implement http.Flusher so newSSEWriter aborts with 500.
type nonFlushingRecorder struct {
	http.ResponseWriter
}
