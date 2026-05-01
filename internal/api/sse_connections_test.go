package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// TestEventStreamConnectionsCounter verifies the SSE handler increments and
// decrements Server.eventStreamConns and that GET /host/stats surfaces the
// current count via HostStats.EventStreamConnections.
func TestEventStreamConnectionsCounter(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Baseline: no streams open.
	if got := getStreamConnections(t, ts.URL); got != 0 {
		t.Fatalf("baseline event_stream_connections = %d, want 0", got)
	}

	// Open a stream and wait for the SSE headers to land.
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
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	if err := waitForStreamConnections(t, ts.URL, 1, 2*time.Second); err != nil {
		t.Fatalf("after open: %v", err)
	}

	// Close the stream and verify the counter drops.
	cancel()
	io.Copy(io.Discard, resp.Body)

	if err := waitForStreamConnections(t, ts.URL, 0, 2*time.Second); err != nil {
		t.Fatalf("after close: %v", err)
	}
}

// TestEventStreamConnectionsCounterMultipleClients verifies multiple
// concurrent SSE clients are each counted.
func TestEventStreamConnectionsCounterMultipleClients(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	const clients = 3
	cancels := make([]context.CancelFunc, 0, clients)
	bodies := make([]io.ReadCloser, 0, clients)
	defer func() {
		for _, c := range cancels {
			c()
		}
		for _, b := range bodies {
			io.Copy(io.Discard, b)
			b.Close()
		}
	}()

	for i := 0; i < clients; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		// Use a fresh client per stream so HTTP/1.1 keep-alive pooling does
		// not serialize requests over a single TCP connection.
		client := &http.Client{Transport: &http.Transport{}}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
		bodies = append(bodies, resp.Body)
	}

	if err := waitForStreamConnections(t, ts.URL, clients, 2*time.Second); err != nil {
		t.Fatalf("with %d clients: %v", clients, err)
	}

	// Close one client; counter should drop to clients-1.
	cancels[0]()
	io.Copy(io.Discard, bodies[0])
	bodies[0].Close()
	cancels = cancels[1:]
	bodies = bodies[1:]

	if err := waitForStreamConnections(t, ts.URL, clients-1, 2*time.Second); err != nil {
		t.Fatalf("after closing one: %v", err)
	}
}

func getStreamConnections(t *testing.T, baseURL string) int64 {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/v1/host/stats")
	if err != nil {
		t.Fatalf("GET host/stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("host/stats status = %d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var stats types.HostStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode host/stats: %v", err)
	}
	return stats.EventStreamConnections
}

func waitForStreamConnections(t *testing.T, baseURL string, want int64, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int64
	for time.Now().Before(deadline) {
		last = getStreamConnections(t, baseURL)
		if last == want {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("event_stream_connections did not converge: want=%d got=%d", want, last)
}
