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

// operationProgressMsg is one progress frame for a long-running VM operation
// (image export, clone, …). It is streamed to the GUI over a dedicated SSE
// channel — not the event bus — so the high-frequency frames never reach the
// persistent event log or webhooks.
type operationProgressMsg struct {
	Op      string  `json:"op"`             // "export", "clone", …
	Name    string  `json:"name,omitempty"` // target name (image / clone)
	Percent float64 `json:"percent"`
	Done    bool    `json:"done"`
}

// operationProgressBroker is a tiny in-memory pub/sub for VM operation
// progress, keyed by VM ID. It remembers the most recent frame per key so a
// subscriber that connects mid-operation receives the current percentage
// immediately instead of waiting for the next tick.
type operationProgressBroker struct {
	mu   sync.Mutex
	subs map[string]map[int]chan operationProgressMsg
	last map[string]operationProgressMsg
	next int
}

func newOperationProgressBroker() *operationProgressBroker {
	return &operationProgressBroker{
		subs: make(map[string]map[int]chan operationProgressMsg),
		last: make(map[string]operationProgressMsg),
	}
}

// publish fans a frame out to every subscriber on key. A terminal Done frame
// clears the remembered last-frame so a future operation on the same VM starts
// clean.
func (b *operationProgressBroker) publish(key string, msg operationProgressMsg) {
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
func (b *operationProgressBroker) subscribe(key string) (<-chan operationProgressMsg, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan operationProgressMsg, 8)
	if b.subs[key] == nil {
		b.subs[key] = make(map[int]chan operationProgressMsg)
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

// progressCallback returns a throttled callback that publishes whole-percent
// advances for op on the given VM key, so a fast disk can't flood subscribers.
func (b *operationProgressBroker) progressCallback(key, op, name string) func(float64) {
	var lastPct float64 = -1
	return func(p float64) {
		if p-lastPct >= 1 || p >= 100 {
			lastPct = p
			b.publish(key, operationProgressMsg{Op: op, Name: name, Percent: p})
		}
	}
}

// ReadinessReporter returns a callback the VM manager can invoke to stream VM
// readiness progress (create / start / restart waiting for the guest to become
// pingable) onto the per-VM operation-progress SSE channel. Returns nil when
// the broker is unavailable, which the manager treats as "no reporting".
func (s *Server) ReadinessReporter() func(vmID, op string, percent float64, done bool) {
	if s.operationProgress == nil {
		return nil
	}
	return func(vmID, op string, percent float64, done bool) {
		s.operationProgress.publish(vmID, operationProgressMsg{Op: op, Percent: percent, Done: done})
	}
}

// StreamOperationProgress handles GET /api/v1/vms/{vmID}/operations/progress —
// an SSE stream of long-operation progress for the VM. Frames carry {op, name,
// percent, done}; the stream closes when a Done frame arrives or the client
// disconnects.
func (s *Server) StreamOperationProgress(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")
	if _, err := s.vmManager.Get(r.Context(), vmID); err != nil {
		writeAPIError(w, http.StatusNotFound, types.NewAPIError("resource_not_found",
			fmt.Sprintf("vm %q not found", vmID)))
		return
	}
	if s.operationProgress == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "operation_progress_disabled",
			"operation progress streaming is unavailable")
		return
	}

	ch, cancel := s.operationProgress.subscribe(vmID)
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
			if sw.WriteEvent("", "operation.progress", string(data)) != nil {
				return
			}
			if msg.Done {
				return
			}
		}
	}
}
