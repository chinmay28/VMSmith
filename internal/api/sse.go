package api

import (
	"fmt"
	"net/http"
	"time"
)

const sseHeartbeatInterval = 30 * time.Second

// sseWriter wraps an http.ResponseWriter to write Server-Sent Event frames.
// Flushing is delegated to an http.ResponseController so middleware that
// wraps the ResponseWriter (and exposes Unwrap) does not break streaming.
type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// newSSEWriter sets the required SSE response headers and returns a writer.
// Returns nil (and writes a 500) if flushing is not supported on this writer.
func newSSEWriter(w http.ResponseWriter) *sseWriter {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disables nginx response buffering
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		// Status is already committed; nothing useful to send back.  Return
		// nil so the caller exits its loop.
		return nil
	}
	return &sseWriter{w: w, rc: rc}
}

// WriteEvent sends a named SSE frame with the given id and JSON-encoded data.
//
//	id: <id>\nevent: <name>\ndata: <data>\n\n
func (s *sseWriter) WriteEvent(id, name, data string) error {
	_, err := fmt.Fprintf(s.w, "id: %s\nevent: %s\ndata: %s\n\n", id, name, data)
	if err != nil {
		return err
	}
	return s.rc.Flush()
}

// WriteComment sends a keepalive comment frame (": keepalive\n\n").
// Proxies that strip idle connections reset their timer on receipt.
func (s *sseWriter) WriteComment(comment string) error {
	_, err := fmt.Fprintf(s.w, ": %s\n\n", comment)
	if err != nil {
		return err
	}
	return s.rc.Flush()
}

// Heartbeat sends a keepalive comment.
func (s *sseWriter) Heartbeat() error {
	return s.WriteComment("keepalive")
}
