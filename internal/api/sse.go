package api

import (
	"fmt"
	"net/http"
	"time"
)

const sseHeartbeatInterval = 30 * time.Second

// sseWriter wraps an http.ResponseWriter to write Server-Sent Event frames.
// It requires the underlying ResponseWriter to implement http.Flusher.
type sseWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

// newSSEWriter sets the required SSE response headers and returns a writer.
// Returns nil (and writes a 500) if the ResponseWriter doesn't support flushing.
func newSSEWriter(w http.ResponseWriter) *sseWriter {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return nil
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disables nginx response buffering
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &sseWriter{w: w, f: f}
}

// WriteEvent sends a named SSE frame with the given id and JSON-encoded data.
//
//	id: <id>\nevent: <name>\ndata: <data>\n\n
func (s *sseWriter) WriteEvent(id, name, data string) error {
	_, err := fmt.Fprintf(s.w, "id: %s\nevent: %s\ndata: %s\n\n", id, name, data)
	if err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

// WriteComment sends a keepalive comment frame (": keepalive\n\n").
// Proxies that strip idle connections reset their timer on receipt.
func (s *sseWriter) WriteComment(comment string) error {
	_, err := fmt.Fprintf(s.w, ": %s\n\n", comment)
	if err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

// Heartbeat sends a keepalive comment.
func (s *sseWriter) Heartbeat() error {
	return s.WriteComment("keepalive")
}
