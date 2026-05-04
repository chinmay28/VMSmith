// Package webhooks delivers event-bus events to externally registered HTTP
// receivers.  Each registered Webhook becomes a goroutine subscriber; matching
// events are signed (HMAC-SHA256) and POSTed to the target URL with a fixed
// retry schedule.  After all retries are exhausted a `webhook.delivery_failed`
// system event is emitted so delivery health is visible on the events stream.
//
// See docs/ARCHITECTURE.md "Event System" -> "Webhook contract" for the wire
// format and SSRF rules.
package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// HeaderSignature is the HMAC-SHA256 signature header.  Format: "sha256=<hex>".
const HeaderSignature = "X-VMSmith-Signature"

// HeaderEventID, HeaderEventType, and HeaderSchemaVersion let receivers route
// and dedupe without parsing the body.
const (
	HeaderEventID       = "X-VMSmith-Event-Id"
	HeaderEventType     = "X-VMSmith-Event-Type"
	HeaderSchemaVersion = "X-VMSmith-Schema-Version"
)

// queueSize is the per-webhook bounded delivery queue depth.  A producer that
// finds the queue full emits a `webhook.queue_overflow` event and drops the
// event for that webhook only.
const queueSize = 1000

// defaultBackoff is the retry schedule.  Each entry is the delay BEFORE the
// next attempt, so the first entry is the wait before the first retry (i.e.
// the second attempt overall).  The slice length therefore caps the total
// number of attempts at len(defaultBackoff)+1.
var defaultBackoff = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
}

// Store is the persistence interface the manager requires.  PutWebhook is
// used to record last-delivery state on each attempt.
type Store interface {
	ListWebhooks() ([]*types.Webhook, error)
	PutWebhook(*types.Webhook) error
	GetWebhook(id string) (*types.Webhook, error)
	DeleteWebhook(id string) error
}

// Manager owns the set of active webhook delivery goroutines and the
// subscription on the event bus.  It is safe for concurrent use.
type Manager struct {
	store        Store
	bus          *events.EventBus
	httpTimeout  time.Duration
	allowedHosts []string

	mu        sync.Mutex
	workers   map[string]*worker
	stopped   bool
	wg        sync.WaitGroup
	rootCtx   context.Context
	cancelAll context.CancelFunc

	// Test hooks.
	resolveIPs func(string) ([]net.IP, error)
	backoff    []time.Duration
	// disableJitter skips the random jitter on retries so tests are
	// deterministic.
	disableJitter bool
}

// Config carries operator-tunable knobs.
type Config struct {
	// AllowedHosts is a case-insensitive allow-list of hostnames that bypass
	// the SSRF deny-list (loopback, link-local, metadata, VM NAT range).
	// Intended for local testing.
	AllowedHosts []string
	// HTTPTimeout is the per-request timeout.  Zero defaults to 10s.
	HTTPTimeout time.Duration
}

// NewManager constructs a Manager.  The bus may be nil, in which case events
// will not be received and delivery_failed events will not be published.
func NewManager(store Store, bus *events.EventBus, cfg Config) *Manager {
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &Manager{
		store:        store,
		bus:          bus,
		httpTimeout:  timeout,
		allowedHosts: cfg.AllowedHosts,
		workers:      make(map[string]*worker),
		backoff:      defaultBackoff,
	}
}

// Start launches a goroutine per registered webhook.  Re-registering an
// already-started Manager is a no-op.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.rootCtx != nil {
		m.mu.Unlock()
		return nil
	}
	m.rootCtx, m.cancelAll = context.WithCancel(ctx)
	m.mu.Unlock()

	if m.store == nil {
		return nil
	}
	hooks, err := m.store.ListWebhooks()
	if err != nil {
		return fmt.Errorf("listing webhooks: %w", err)
	}
	for _, wh := range hooks {
		if wh != nil && wh.Active {
			m.startWorkerLocked(wh)
		}
	}
	return nil
}

// Stop signals every worker to drain and exit, then waits for them.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	if m.cancelAll != nil {
		m.cancelAll()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Register adds a new webhook.  The caller is responsible for persisting
// the row (Manager only maintains in-memory delivery state).  When wh.Active
// is true a worker is launched immediately.
func (m *Manager) Register(wh *types.Webhook) {
	if wh == nil || !wh.Active {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped || m.rootCtx == nil {
		return
	}
	m.startWorkerLocked(wh)
}

// Unregister tears down the worker for a webhook ID.  Pending queued events
// are dropped.  Idempotent.
func (m *Manager) Unregister(id string) {
	m.mu.Lock()
	w, ok := m.workers[id]
	if ok {
		delete(m.workers, id)
	}
	m.mu.Unlock()
	if ok {
		w.stop()
	}
}

func (m *Manager) startWorkerLocked(wh *types.Webhook) {
	if _, exists := m.workers[wh.ID]; exists {
		return
	}
	w := newWorker(m, *wh)
	m.workers[wh.ID] = w
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		w.run(m.rootCtx)
	}()
}

// worker owns the bus subscription, queue, and delivery loop for one webhook.
type worker struct {
	mgr       *Manager
	hook      types.Webhook
	queue     chan *types.Event
	sub       <-chan *types.Event
	cancelSub func()
	stopOnce  sync.Once
	stopped   chan struct{}
}

func newWorker(m *Manager, wh types.Webhook) *worker {
	w := &worker{
		mgr:     m,
		hook:    wh,
		queue:   make(chan *types.Event, queueSize),
		stopped: make(chan struct{}),
	}
	// Subscribe synchronously so events published immediately after worker
	// construction are delivered.  Without this, run()'s deferred subscribe
	// races the next Publish.
	if m.bus != nil {
		w.sub, w.cancelSub = m.bus.Subscribe("webhook-" + wh.ID)
	}
	return w
}

func (w *worker) stop() {
	w.stopOnce.Do(func() { close(w.stopped) })
}

func (w *worker) run(ctx context.Context) {
	if w.cancelSub != nil {
		defer w.cancelSub()
	}

	// Producer: filter and enqueue.  Runs in a separate goroutine so the
	// delivery loop below can spend arbitrary time on a slow receiver
	// without blocking bus fan-out (the bus drops a subscriber when its
	// channel fills).
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopped:
				return
			case evt, ok := <-w.sub:
				if !ok {
					return
				}
				if !w.matchesType(evt.Type) {
					continue
				}
				select {
				case w.queue <- evt:
				default:
					w.publishOverflow(evt)
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopped:
			return
		case evt := <-w.queue:
			w.deliver(ctx, evt)
		}
	}
}

func (w *worker) matchesType(eventType string) bool {
	if len(w.hook.EventTypes) == 0 {
		return true
	}
	for _, want := range w.hook.EventTypes {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		// Exact or "vm.*" prefix glob.
		if strings.HasSuffix(want, ".*") {
			if strings.HasPrefix(eventType, strings.TrimSuffix(want, "*")) {
				return true
			}
		} else if want == eventType {
			return true
		}
	}
	return false
}

func (w *worker) deliver(ctx context.Context, evt *types.Event) {
	body, err := json.Marshal(evt)
	if err != nil {
		logger.Warn("webhooks", "marshal event failed", "webhook_id", w.hook.ID, "error", err.Error())
		return
	}

	target, verifiedIPs, err := validateTarget(w.hook.URL, w.mgr.allowedHosts, w.mgr.resolveIPs)
	if err != nil {
		w.recordFailure(evt, err.Error(), -1)
		return
	}

	// Build a one-shot HTTP client whose dialer is pinned to the IPs we just
	// verified.  This closes the DNS-rebinding window between validation and
	// connect: even if the upstream resolver is hostile and switches to a
	// blocked IP at connect time, the dialer refuses anything outside the
	// verified set.  When the host bypassed the check via allowedHosts,
	// verifiedIPs is nil and we trust the system resolver.
	client := w.mgr.clientForDelivery(verifiedIPs)

	attempts := len(w.mgr.backoff) + 1
	var lastErr error
	var lastStatus int
	for i := 0; i < attempts; i++ {
		if i > 0 {
			delay := w.mgr.backoff[i-1]
			if !w.mgr.disableJitter {
				// Add up to 25% positive jitter so multiple webhooks targeting a
				// flaky receiver don't retry in lockstep.
				delay += time.Duration(rand.Int63n(int64(delay/4) + 1))
			}
			select {
			case <-ctx.Done():
				return
			case <-w.stopped:
				return
			case <-time.After(delay):
			}
		}
		status, err := w.send(ctx, client, target.String(), body, evt)
		lastStatus = status
		if err == nil && status >= 200 && status < 300 {
			w.recordSuccess(status)
			return
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("HTTP %d", status)
		}
	}

	msg := "delivery failed"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	w.recordFailure(evt, msg, lastStatus)
}

func (w *worker) send(ctx context.Context, client *http.Client, url string, body []byte, evt *types.Event) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vmsmith-webhook/1")
	req.Header.Set(HeaderSignature, "sha256="+Sign(w.hook.Secret, body))
	req.Header.Set(HeaderEventID, evt.ID)
	req.Header.Set(HeaderEventType, evt.Type)
	req.Header.Set(HeaderSchemaVersion, strconv.Itoa(types.EventSchemaVersion))

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// clientForDelivery returns an http.Client whose dialer refuses to connect to
// any IP outside the supplied set.  Pass nil to allow the default resolver
// (used only when validateTarget bypassed the deny-list via allowedHosts).
func (m *Manager) clientForDelivery(verifiedIPs []net.IP) *http.Client {
	if len(verifiedIPs) == 0 {
		return &http.Client{Timeout: m.httpTimeout}
	}
	allowed := make(map[string]struct{}, len(verifiedIPs))
	for _, ip := range verifiedIPs {
		allowed[ip.String()] = struct{}{}
	}
	dialer := &net.Dialer{Timeout: m.httpTimeout}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				// Re-resolve and pick the first verified address.
				resolved, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
				if err != nil {
					return nil, err
				}
				for _, candidate := range resolved {
					if _, ok := allowed[candidate.String()]; ok {
						return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
					}
				}
				return nil, ErrSSRFBlocked
			}
			if _, ok := allowed[ip.String()]; !ok {
				return nil, ErrSSRFBlocked
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Timeout: m.httpTimeout, Transport: transport}
}

func (w *worker) recordSuccess(status int) {
	w.hook.LastDeliveryAt = time.Now().UTC()
	w.hook.LastStatus = status
	w.hook.LastError = ""
	if err := w.mgr.store.PutWebhook(&w.hook); err != nil {
		logger.Warn("webhooks", "persist last-delivery state failed", "webhook_id", w.hook.ID, "error", err.Error())
	}
}

func (w *worker) recordFailure(evt *types.Event, errMsg string, lastStatus int) {
	w.hook.LastDeliveryAt = time.Now().UTC()
	w.hook.LastStatus = 0
	w.hook.LastError = errMsg
	if err := w.mgr.store.PutWebhook(&w.hook); err != nil {
		logger.Warn("webhooks", "persist last-delivery state failed", "webhook_id", w.hook.ID, "error", err.Error())
	}
	logger.Warn("webhooks", "delivery failed",
		"webhook_id", w.hook.ID, "url", w.hook.URL, "event_id", evt.ID, "error", errMsg)

	if w.mgr.bus == nil {
		return
	}
	attrs := map[string]string{
		"webhook_id": w.hook.ID,
		"url":        w.hook.URL,
		"event_id":   evt.ID,
		"event_type": evt.Type,
		"error":      errMsg,
	}
	if lastStatus > 0 {
		attrs["last_status"] = strconv.Itoa(lastStatus)
	}
	failure := events.NewSystemEventWithAttrs(
		"webhook.delivery_failed",
		types.EventSeverityWarn,
		"webhook delivery failed after retries",
		attrs,
	)
	w.mgr.bus.Publish(failure)
}

func (w *worker) publishOverflow(evt *types.Event) {
	logger.Warn("webhooks", "queue full, dropping event",
		"webhook_id", w.hook.ID, "event_id", evt.ID, "event_type", evt.Type)
	if w.mgr.bus == nil {
		return
	}
	w.mgr.bus.Publish(events.NewSystemEventWithAttrs(
		"webhook.queue_overflow",
		types.EventSeverityWarn,
		"webhook delivery queue overflowed; event dropped",
		map[string]string{
			"webhook_id": w.hook.ID,
			"event_id":   evt.ID,
			"event_type": evt.Type,
		},
	))
}

// Errors returned by validateTarget are surfaced to API callers as 400s.
var (
	ErrInvalidWebhook = errors.New("invalid webhook")
	// ErrWebhookNotFound is returned by TestDeliver when no webhook with the
	// given ID is registered.  Callers map this to HTTP 404.
	ErrWebhookNotFound = errors.New("webhook not found")
)

// TestDeliver synthesises a `system.webhook_test` event and delivers it to
// the webhook with the given ID exactly once (no retries).  The result
// records the receiver's HTTP status, the duration, and any error so the UI
// can surface a quick "test succeeded / failed" answer to the operator.
//
// LastDelivery* fields on the persisted webhook are updated to reflect this
// attempt so the same telemetry the regular delivery path reports is visible
// after a manual test.  The synthetic event is NOT published to the bus —
// that would fan it out to every other subscriber too.
func (m *Manager) TestDeliver(ctx context.Context, webhookID string) (*types.WebhookTestResult, error) {
	if m.store == nil {
		return nil, ErrInvalidWebhook
	}
	hook, err := m.store.GetWebhook(webhookID)
	if err != nil || hook == nil {
		return nil, ErrWebhookNotFound
	}

	evt := &types.Event{
		ID:         fmt.Sprintf("wh-test-%d", time.Now().UnixNano()),
		Type:       "system.webhook_test",
		Source:     types.EventSourceSystem,
		Severity:   types.EventSeverityInfo,
		Message:    "synthetic test event from POST /api/v1/webhooks/{id}/test",
		Attributes: map[string]string{"webhook_id": webhookID},
		OccurredAt: time.Now().UTC(),
	}

	body, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("marshal test event: %w", err)
	}

	result := &types.WebhookTestResult{AttemptedAt: time.Now().UTC(), EventID: evt.ID}

	target, verifiedIPs, err := validateTarget(hook.URL, m.allowedHosts, m.resolveIPs)
	if err != nil {
		result.Error = err.Error()
		m.recordExternalDeliveryResult(hook, result)
		return result, nil
	}

	client := m.clientForDelivery(verifiedIPs)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		result.Error = err.Error()
		m.recordExternalDeliveryResult(hook, result)
		return result, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vmsmith-webhook/1")
	req.Header.Set(HeaderSignature, "sha256="+Sign(hook.Secret, body))
	req.Header.Set(HeaderEventID, evt.ID)
	req.Header.Set(HeaderEventType, evt.Type)
	req.Header.Set(HeaderSchemaVersion, strconv.Itoa(types.EventSchemaVersion))

	start := time.Now()
	resp, err := client.Do(req)
	result.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		m.recordExternalDeliveryResult(hook, result)
		return result, nil
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Success = true
	} else {
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	m.recordExternalDeliveryResult(hook, result)
	return result, nil
}

// recordExternalDeliveryResult mirrors the bookkeeping done by worker.recordSuccess /
// recordFailure but is callable from the synchronous test path.  It updates
// the persisted webhook with the latest delivery telemetry.  Persistence
// errors are logged but do not affect the API response.
func (m *Manager) recordExternalDeliveryResult(hook *types.Webhook, result *types.WebhookTestResult) {
	hook.LastDeliveryAt = result.AttemptedAt
	if result.Success {
		hook.LastStatus = result.StatusCode
		hook.LastError = ""
	} else {
		hook.LastStatus = 0
		hook.LastError = result.Error
	}
	if err := m.store.PutWebhook(hook); err != nil {
		logger.Warn("webhooks", "persist last-delivery state failed (test)",
			"webhook_id", hook.ID, "error", err.Error())
	}
}
