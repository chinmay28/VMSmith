package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// memStore implements both events.Store (AppendEvent) and webhooks.Store.
type memStore struct {
	mu       sync.Mutex
	events   []*types.Event
	webhooks map[string]*types.Webhook
	seq      uint64
}

func newMemStore() *memStore {
	return &memStore{webhooks: map[string]*types.Webhook{}}
}

func (s *memStore) AppendEvent(evt *types.Event) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	evt.ID = fmt.Sprintf("%d", s.seq)
	cp := *evt
	s.events = append(s.events, &cp)
	return s.seq, nil
}

func (s *memStore) PutWebhook(wh *types.Webhook) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *wh
	s.webhooks[wh.ID] = &cp
	return nil
}

func (s *memStore) GetWebhook(id string) (*types.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wh, ok := s.webhooks[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *wh
	return &cp, nil
}

func (s *memStore) ListWebhooks() ([]*types.Webhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*types.Webhook, 0, len(s.webhooks))
	for _, wh := range s.webhooks {
		cp := *wh
		out = append(out, &cp)
	}
	return out, nil
}

func (s *memStore) DeleteWebhook(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.webhooks, id)
	return nil
}

func (s *memStore) eventsByType(t string) []*types.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*types.Event
	for _, e := range s.events {
		if e.Type == t {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out
}

// allowAll bypasses SSRF checks for the test httptest server (which binds
// loopback by default).
func allowAllResolver(host string) ([]net.IP, error) {
	// Not actually consulted because allowedHosts covers the host; return
	// public-looking address for safety.
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

func newTestManager(t *testing.T, store *memStore, bus *events.EventBus, allowed []string) *Manager {
	t.Helper()
	m := NewManager(store, bus, Config{
		AllowedHosts: allowed,
		HTTPTimeout:  2 * time.Second,
	})
	// Test-only: shrink the retry schedule so failed-delivery tests finish quickly.
	m.backoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	m.resolveIPs = allowAllResolver
	return m
}

func TestManager_DeliversSignedEvent(t *testing.T) {
	store := newMemStore()
	bus := events.New(store)
	bus.Start()
	defer bus.Stop()

	type capture struct {
		body  []byte
		hdr   http.Header
		count int32
	}
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.body = body
		cap.hdr = r.Header.Clone()
		atomic.AddInt32(&cap.count, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	mgr := newTestManager(t, store, bus, []string{host, "127.0.0.1"})

	wh := &types.Webhook{
		ID:        "wh-test",
		URL:       srv.URL,
		Secret:    "topsecret",
		Active:    true,
		CreatedAt: time.Now(),
	}
	if err := store.PutWebhook(wh); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	bus.Publish(&types.Event{Type: "vm.started", Source: types.EventSourceLibvirt, VMID: "vm-1"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&cap.count) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&cap.count); got != 1 {
		t.Fatalf("delivered %d, want 1", got)
	}

	sig := cap.hdr.Get(HeaderSignature)
	if !strings.HasPrefix(sig, "sha256=") {
		t.Fatalf("missing/invalid signature header: %q", sig)
	}
	want := signWith("topsecret", cap.body)
	if strings.TrimPrefix(sig, "sha256=") != want {
		t.Fatalf("signature mismatch:\n got %q\nwant %q", sig, want)
	}
	if cap.hdr.Get(HeaderEventType) != "vm.started" {
		t.Fatalf("event-type header = %q, want vm.started", cap.hdr.Get(HeaderEventType))
	}
	if cap.hdr.Get(HeaderSchemaVersion) != "1" {
		t.Fatalf("schema-version header = %q, want 1", cap.hdr.Get(HeaderSchemaVersion))
	}
}

func signWith(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestManager_EmitsDeliveryFailedAfterRetries(t *testing.T) {
	store := newMemStore()
	bus := events.New(store)
	bus.Start()
	defer bus.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "always fails", http.StatusInternalServerError)
	}))
	defer srv.Close()

	host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	mgr := newTestManager(t, store, bus, []string{host, "127.0.0.1"})

	wh := &types.Webhook{
		ID:        "wh-fail",
		URL:       srv.URL,
		Secret:    "k",
		Active:    true,
		CreatedAt: time.Now(),
	}
	_ = store.PutWebhook(wh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	bus.Publish(&types.Event{Type: "vm.deleted", Source: types.EventSourceApp, VMID: "vm-x"})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(store.eventsByType("webhook.delivery_failed")) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	failures := store.eventsByType("webhook.delivery_failed")
	if len(failures) == 0 {
		t.Fatalf("expected webhook.delivery_failed event after retries, got none (events=%v)", store.events)
	}
	attrs := failures[0].Attributes
	if attrs["webhook_id"] != "wh-fail" {
		t.Fatalf("delivery_failed missing webhook_id attr: %v", attrs)
	}
	if attrs["event_type"] != "vm.deleted" {
		t.Fatalf("delivery_failed missing event_type attr: %v", attrs)
	}
	if attrs["last_status"] != "500" {
		t.Fatalf("delivery_failed last_status = %q, want 500", attrs["last_status"])
	}
}

func TestManager_FiltersByEventType(t *testing.T) {
	store := newMemStore()
	bus := events.New(store)
	bus.Start()
	defer bus.Stop()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	mgr := newTestManager(t, store, bus, []string{host, "127.0.0.1"})

	wh := &types.Webhook{
		ID:         "wh-types",
		URL:        srv.URL,
		Secret:     "k",
		EventTypes: []string{"vm.started", "system.*"},
		Active:     true,
		CreatedAt:  time.Now(),
	}
	_ = store.PutWebhook(wh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	// Match: vm.started (exact)
	bus.Publish(&types.Event{Type: "vm.started", Source: types.EventSourceLibvirt})
	// Match: system.daemon_started (prefix)
	bus.Publish(&types.Event{Type: "system.daemon_started", Source: types.EventSourceSystem})
	// Skip: vm.stopped
	bus.Publish(&types.Event{Type: "vm.stopped", Source: types.EventSourceLibvirt})

	// Wait for two deliveries to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Allow a tiny grace period in case a third (vm.stopped) is in flight.
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("delivered %d events, want 2 (filter mismatch)", got)
	}
}

func TestManager_UnregisterStopsWorker(t *testing.T) {
	store := newMemStore()
	bus := events.New(store)
	bus.Start()
	defer bus.Stop()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host, _, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	mgr := newTestManager(t, store, bus, []string{host, "127.0.0.1"})

	wh := &types.Webhook{ID: "wh-unreg", URL: srv.URL, Secret: "k", Active: true, CreatedAt: time.Now()}
	_ = store.PutWebhook(wh)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop()

	mgr.Unregister(wh.ID)

	bus.Publish(&types.Event{Type: "vm.deleted", Source: types.EventSourceApp})
	time.Sleep(150 * time.Millisecond)

	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("expected zero deliveries after unregister, got %d", got)
	}
}
