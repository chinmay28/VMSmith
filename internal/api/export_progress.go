package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// exportProgressMsg is one progress frame for an in-flight image export. It is
// streamed to the GUI over a dedicated SSE channel (not the event bus) so the
// high-frequency frames never reach the persistent event log or webhooks.
type exportProgressMsg struct {
	Name    string  `json:"name"`
	Percent float64 `json:"percent"`
	Done    bool    `json:"done"`
}

// exportProgressBroker is a tiny in-memory pub/sub for image-export progress,
// keyed by VM ID. It remembers the most recent frame per key so a subscriber
// that connects mid-export receives the current percentage immediately instead
// of waiting for the next qemu-img tick.
type exportProgressBroker struct {
	mu   sync.Mutex
	subs map[string]map[int]chan exportProgressMsg
	last map[string]exportProgressMsg
	next int
}

func newExportProgressBroker() *exportProgressBroker {
	return &exportProgressBroker{
		subs: make(map[string]map[int]chan exportProgressMsg),
		last: make(map[string]exportProgressMsg),
	}
}

// publish fans a frame out to every subscriber on key. A terminal Done frame
// clears the remembered last-frame so a future export of the same VM starts
// clean.
func (b *exportProgressBroker) publish(key string, msg exportProgressMsg) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if msg.Done {
		delete(b.last, key)
	} else {
		b.last[key] = msg
	}
	for _, ch := range b.subs[key] {
		select {
		case ch <- msg:
		default:
			// Drop on a full buffer; the next frame (or the terminal Done) will
			// catch a slow subscriber up.
		}
	}
}

// subscribe registers a subscriber for key and returns its channel plus a
// cancel func. The last-known frame, if any, is delivered immediately.
func (b *exportProgressBroker) subscribe(key string) (<-chan exportProgressMsg, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan exportProgressMsg, 8)
	if b.subs[key] == nil {
		b.subs[key] = make(map[int]chan exportProgressMsg)
	}
	b.subs[key][id] = ch
	if last, ok := b.last[key]; ok {
		ch <- last
	}
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if m := b.subs[key]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(b.subs, key)
			}
		}
		close(ch)
	}
	return ch, cancel
}

// StreamExportProgress handles GET /api/v1/vms/{vmID}/export/progress — an SSE
// stream of image-export progress for the VM. Frames carry {name, percent,
// done}; the stream closes when a Done frame arrives or the client disconnects.
func (s *Server) StreamExportProgress(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")
	if _, err := s.vmManager.Get(r.Context(), vmID); err != nil {
		writeAPIError(w, http.StatusNotFound, types.NewAPIError("resource_not_found",
			fmt.Sprintf("vm %q not found", vmID)))
		return
	}
	if s.exportProgress == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "export_progress_disabled",
			"export progress streaming is unavailable")
		return
	}

	ch, cancel := s.exportProgress.subscribe(vmID)
	defer cancel()

	sw := newSSEWriter(w)
	if sw == nil {
		return
	}

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if err := sw.Heartbeat(); err != nil {
				return
			}
		case msg, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			if sw.WriteEvent("", "export.progress", string(data)) != nil {
				return
			}
			if msg.Done {
				return
			}
		}
	}
}
