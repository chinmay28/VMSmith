package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/internal/webhooks"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// fakeWebhookStore implements both WebhookStore and WebhookRegistrar in
// memory.  It captures Register / Unregister calls so tests can assert that
// the runtime manager is notified on CRUD changes.
type fakeWebhookStore struct {
	mu           sync.Mutex
	hooks        map[string]*types.Webhook
	registered   []string
	unregistered []string
}

func newFakeWebhookStore() *fakeWebhookStore {
	return &fakeWebhookStore{hooks: map[string]*types.Webhook{}}
}

func (f *fakeWebhookStore) PutWebhook(wh *types.Webhook) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *wh
	f.hooks[wh.ID] = &cp
	return nil
}

func (f *fakeWebhookStore) GetWebhook(id string) (*types.Webhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wh, ok := f.hooks[id]
	if !ok {
		return nil, errFakeNotFound
	}
	cp := *wh
	return &cp, nil
}

func (f *fakeWebhookStore) ListWebhooks() ([]*types.Webhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*types.Webhook, 0, len(f.hooks))
	for _, h := range f.hooks {
		cp := *h
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeWebhookStore) DeleteWebhook(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hooks, id)
	return nil
}

func (f *fakeWebhookStore) Register(wh *types.Webhook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered = append(f.registered, wh.ID)
}

func (f *fakeWebhookStore) Unregister(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregistered = append(f.unregistered, id)
}

type errString string

func (e errString) Error() string { return string(e) }

const errFakeNotFound = errString("not found")

// webhookTestServer creates an httptest.Server backed by a real *Server, plus
// a fake webhook subsystem.  Returns everything tests need to make HTTP calls
// and assert on the in-memory store / registrar.
func webhookTestServer(t *testing.T) (*httptest.Server, *Server, *fakeWebhookStore, func()) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath

	mockMgr := vm.NewMockManager()
	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)

	apiServer := NewServerWithConfig(mockMgr, storageMgr, portFwd, s, cfg, nil)
	fake := newFakeWebhookStore()
	apiServer.SetWebhookSubsystem(fake, fake)

	ts := httptest.NewServer(apiServer)
	cleanup := func() {
		ts.Close()
		s.Close()
	}
	return ts, apiServer, fake, cleanup
}

func webhookRuntimeTestServer(t *testing.T) (*httptest.Server, *store.Store, *events.EventBus, *webhooks.Manager, func()) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath

	mockMgr := vm.NewMockManager()
	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)
	apiServer := NewServerWithConfig(mockMgr, storageMgr, portFwd, s, cfg, nil)

	bus := events.New(s)
	bus.Start()
	apiServer.SetEventBus(bus)

	mgr := webhooks.NewManager(s, bus, webhooks.Config{AllowedHosts: []string{"127.0.0.1"}, HTTPTimeout: time.Second, Backoff: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}})
	apiServer.SetWebhookSubsystem(s, mgr)

	ctx, cancel := context.WithCancel(context.Background())
	if err := mgr.Start(ctx); err != nil {
		cancel()
		bus.Stop()
		s.Close()
		t.Fatalf("Start webhook manager: %v", err)
	}

	ts := httptest.NewServer(apiServer)
	cleanup := func() {
		ts.Close()
		cancel()
		mgr.Stop()
		bus.Stop()
		s.Close()
	}
	return ts, s, bus, mgr, cleanup
}

func TestCreateWebhook_Success(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()

	body := jsonBody(t, types.WebhookCreateRequest{
		URL:        "https://example.com/hook",
		Secret:     "topsecret",
		EventTypes: []string{"vm.started", "system.*"},
	})
	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, b)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if got.ID == "" {
		t.Fatalf("response missing ID: %+v", got)
	}
	if got.Secret != "" {
		t.Fatalf("response leaked secret: %q", got.Secret)
	}
	if !got.Active {
		t.Fatalf("created webhook should be active")
	}
	if len(got.EventTypes) != 2 || got.EventTypes[0] != "vm.started" {
		t.Fatalf("event types not preserved: %v", got.EventTypes)
	}

	fake.mu.Lock()
	registered := append([]string(nil), fake.registered...)
	persisted, ok := fake.hooks[got.ID]
	fake.mu.Unlock()

	if len(registered) != 1 || registered[0] != got.ID {
		t.Fatalf("manager.Register not called for %s, got %v", got.ID, registered)
	}
	if !ok || persisted.Secret != "topsecret" {
		t.Fatalf("secret not persisted server-side")
	}
}

func TestCreateWebhook_RejectsInvalidScheme(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json",
		jsonBody(t, types.WebhookCreateRequest{URL: "ftp://example.com", Secret: "k"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateWebhook_WithDescription(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()

	body := jsonBody(t, types.WebhookCreateRequest{
		URL:         "https://example.com/hook",
		Secret:      "topsecret",
		Description: "  Slack notifier for production VM crashes  ",
	})
	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, b)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if got.Description != "Slack notifier for production VM crashes" {
		t.Fatalf("description not trimmed/persisted: %q", got.Description)
	}

	fake.mu.Lock()
	persisted := fake.hooks[got.ID]
	fake.mu.Unlock()
	if persisted.Description != "Slack notifier for production VM crashes" {
		t.Fatalf("persisted description: %q", persisted.Description)
	}
}

func TestCreateWebhook_RejectsLongDescription(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()

	long := make([]byte, 1025)
	for i := range long {
		long[i] = 'a'
	}
	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json",
		jsonBody(t, types.WebhookCreateRequest{
			URL:         "https://example.com/hook",
			Secret:      "k",
			Description: string(long),
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, b)
	}
}

func TestCreateWebhook_RejectsMissingSecret(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json",
		jsonBody(t, types.WebhookCreateRequest{URL: "https://example.com/x"}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestListWebhooks_RedactsSecrets(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()

	fake.PutWebhook(&types.Webhook{ID: "wh-1", URL: "https://a", Secret: "s1", Active: true})
	fake.PutWebhook(&types.Webhook{ID: "wh-2", URL: "https://b", Secret: "s2", Active: true})

	resp, err := http.Get(ts.URL + "/api/v1/webhooks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 2 {
		t.Fatalf("got %d hooks, want 2", len(hooks))
	}
	for _, h := range hooks {
		if h.Secret != "" {
			t.Fatalf("hook %s leaked secret", h.ID)
		}
	}
}

func TestDeleteWebhook_NotifiesManager(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()

	fake.PutWebhook(&types.Webhook{ID: "wh-del", URL: "https://x", Secret: "k", Active: true})

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/webhooks/wh-del", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, exists := fake.hooks["wh-del"]; exists {
		t.Fatalf("hook still in store after delete")
	}
	if len(fake.unregistered) != 1 || fake.unregistered[0] != "wh-del" {
		t.Fatalf("manager.Unregister not called: %v", fake.unregistered)
	}
}

func TestWebhooksDisabledWhenStoreUnset(t *testing.T) {
	// Build a server WITHOUT calling SetWebhookSubsystem and assert 503.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer s.Close()

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath

	apiServer := NewServerWithConfig(vm.NewMockManager(),
		storage.NewManager(cfg, s), network.NewPortForwarder(s), s, cfg, nil)
	ts := httptest.NewServer(apiServer)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/webhooks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func readAllBody(r *http.Response) ([]byte, error) {
	defer r.Body.Close()
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

// fakeWebhookTester captures the most recent TestDeliver call and returns the
// pre-configured result.  When err != nil it is returned instead of the
// result, simulating ErrWebhookNotFound or transport failures.
type fakeWebhookTester struct {
	mu     sync.Mutex
	called []string
	result *types.WebhookTestResult
	err    error
}

func (f *fakeWebhookTester) TestDeliver(_ context.Context, id string) (*types.WebhookTestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = append(f.called, id)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// fakeWebhookFull combines store, registrar and tester so SetWebhookSubsystem
// installs all three on a single value (mirroring the real Manager wiring).
type fakeWebhookFull struct {
	*fakeWebhookStore
	*fakeWebhookTester
}

func TestTestWebhook_Success(t *testing.T) {
	ts, apiServer, store, cleanup := webhookTestServer(t)
	defer cleanup()

	store.PutWebhook(&types.Webhook{ID: "wh-ok", URL: "https://example.com/x", Secret: "k", Active: true})
	tester := &fakeWebhookTester{
		result: &types.WebhookTestResult{
			Success:     true,
			StatusCode:  204,
			DurationMs:  42,
			AttemptedAt: time.Now().UTC(),
			EventID:     "wh-test-1",
		},
	}
	apiServer.SetWebhookSubsystem(&fakeWebhookFull{store, tester}, &fakeWebhookFull{store, tester})

	resp, err := http.Post(ts.URL+"/api/v1/webhooks/wh-ok/test", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var got types.WebhookTestResult
	decodeJSON(t, resp, &got)
	if !got.Success || got.StatusCode != 204 {
		t.Fatalf("unexpected result: %+v", got)
	}
	tester.mu.Lock()
	defer tester.mu.Unlock()
	if len(tester.called) != 1 || tester.called[0] != "wh-ok" {
		t.Fatalf("TestDeliver called with %v, want [wh-ok]", tester.called)
	}
}

func TestTestWebhook_NotFound(t *testing.T) {
	ts, apiServer, store, cleanup := webhookTestServer(t)
	defer cleanup()
	tester := &fakeWebhookTester{err: webhooks.ErrWebhookNotFound}
	apiServer.SetWebhookSubsystem(&fakeWebhookFull{store, tester}, &fakeWebhookFull{store, tester})

	resp, err := http.Post(ts.URL+"/api/v1/webhooks/wh-missing/test", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestTestWebhook_NoTesterReturns503(t *testing.T) {
	ts, apiServer, store, cleanup := webhookTestServer(t)
	defer cleanup()
	// Re-wire so the registrar has no TestDeliver method.
	apiServer.SetWebhookSubsystem(store, store)

	resp, err := http.Post(ts.URL+"/api/v1/webhooks/whatever/test", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestTestWebhook_EndToEndSuccess(t *testing.T) {
	ts, s, _, _, cleanup := webhookRuntimeTestServer(t)
	defer cleanup()

	var hits int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json", jsonBody(t, types.WebhookCreateRequest{
		URL:    receiver.URL,
		Secret: "topsecret",
	}))
	if err != nil {
		t.Fatalf("POST /webhooks: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.Webhook
	decodeJSON(t, resp, &created)

	resp, err = http.Post(ts.URL+"/api/v1/webhooks/"+created.ID+"/test", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /webhooks/{id}/test: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result types.WebhookTestResult
	decodeJSON(t, resp, &result)
	if !result.Success || result.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected test result: %+v", result)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("receiver hits = %d, want 1", got)
	}

	persisted, err := s.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("GetWebhook: %v", err)
	}
	if persisted.LastStatus != http.StatusNoContent {
		t.Fatalf("LastStatus = %d, want 204", persisted.LastStatus)
	}
	if persisted.LastError != "" {
		t.Fatalf("LastError = %q, want empty", persisted.LastError)
	}
	if persisted.LastDeliveryAt.IsZero() {
		t.Fatal("LastDeliveryAt was not updated")
	}
}

func TestTestWebhook_EndToEndFailure(t *testing.T) {
	ts, s, _, _, cleanup := webhookRuntimeTestServer(t)
	defer cleanup()

	var hits int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "always fails", http.StatusInternalServerError)
	}))
	defer receiver.Close()

	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json", jsonBody(t, types.WebhookCreateRequest{
		URL:    receiver.URL,
		Secret: "topsecret",
	}))
	if err != nil {
		t.Fatalf("POST /webhooks: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.Webhook
	decodeJSON(t, resp, &created)

	resp, err = http.Post(ts.URL+"/api/v1/webhooks/"+created.ID+"/test", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /webhooks/{id}/test: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result types.WebhookTestResult
	decodeJSON(t, resp, &result)
	if result.Success || result.StatusCode != http.StatusInternalServerError {
		t.Fatalf("unexpected test result: %+v", result)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("receiver hits = %d, want 1", got)
	}

	persisted, err := s.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("GetWebhook: %v", err)
	}
	if persisted.LastStatus != http.StatusInternalServerError {
		t.Fatalf("LastStatus = %d, want 500", persisted.LastStatus)
	}
	if persisted.LastError != "HTTP 500" {
		t.Fatalf("LastError = %q, want HTTP 500", persisted.LastError)
	}
	if persisted.LastDeliveryAt.IsZero() {
		t.Fatal("LastDeliveryAt was not updated")
	}
}

func TestWebhookDelivery_EndToEndFailurePublishesFinalEventAfterRetries(t *testing.T) {
	ts, s, bus, _, cleanup := webhookRuntimeTestServer(t)
	defer cleanup()

	var hits int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "always fails", http.StatusBadGateway)
	}))
	defer receiver.Close()

	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json", jsonBody(t, types.WebhookCreateRequest{
		URL:        receiver.URL,
		Secret:     "topsecret",
		EventTypes: []string{"vm.deleted"},
	}))
	if err != nil {
		t.Fatalf("POST /webhooks: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.Webhook
	decodeJSON(t, resp, &created)

	bus.Publish(&types.Event{Type: "vm.deleted", Source: types.EventSourceApp, VMID: "vm-e2e-fail"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		persisted, err := s.GetWebhook(created.ID)
		if err != nil {
			t.Fatalf("GetWebhook: %v", err)
		}
		events, _, err := s.ListEventsFiltered(store.EventFilter{Type: "webhook.delivery_failed"})
		if err != nil {
			t.Fatalf("ListEventsFiltered: %v", err)
		}
		if persisted.LastStatus == http.StatusBadGateway && persisted.LastError == "HTTP 502" && len(events) == 1 {
			if got := atomic.LoadInt32(&hits); got != 3 {
				t.Fatalf("receiver hits = %d, want 3 attempts", got)
			}
			if events[0].Attributes["webhook_id"] != created.ID {
				t.Fatalf("delivery_failed webhook_id = %q, want %q", events[0].Attributes["webhook_id"], created.ID)
			}
			if events[0].Attributes["event_type"] != "vm.deleted" {
				t.Fatalf("delivery_failed event_type = %q, want vm.deleted", events[0].Attributes["event_type"])
			}
			if events[0].Attributes["last_status"] != "502" {
				t.Fatalf("delivery_failed last_status = %q, want 502", events[0].Attributes["last_status"])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	persisted, err := s.GetWebhook(created.ID)
	if err != nil {
		t.Fatalf("GetWebhook: %v", err)
	}
	events, _, err := s.ListEventsFiltered(store.EventFilter{Type: "webhook.delivery_failed"})
	if err != nil {
		t.Fatalf("ListEventsFiltered: %v", err)
	}
	t.Fatalf("timed out waiting for delivery_failed event: hits=%d LastStatus=%d LastError=%q events=%d", atomic.LoadInt32(&hits), persisted.LastStatus, persisted.LastError, len(events))
}

// patchWebhook is a small helper for PATCH /api/v1/webhooks/{id} tests.
// It marshals the spec, sends the request, and returns the response so each
// test can decode + assert on status and body.
func patchWebhook(t *testing.T, baseURL, id string, spec types.WebhookUpdateSpec) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, baseURL+"/api/v1/webhooks/"+id, jsonBody(t, spec))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	return resp
}

func ptrStr(s string) *string          { return &s }
func ptrBool(b bool) *bool             { return &b }
func ptrStrSlice(s []string) *[]string { return &s }

func TestUpdateWebhook_SetsURL(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-u1", URL: "https://old.example.com/x", Secret: "k", Active: true})

	resp := patchWebhook(t, ts.URL, "wh-u1", types.WebhookUpdateSpec{URL: ptrStr("https://new.example.com/x")})
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if got.URL != "https://new.example.com/x" {
		t.Fatalf("url not updated: %q", got.URL)
	}
	if got.Secret != "" {
		t.Fatalf("response leaked secret: %q", got.Secret)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.hooks["wh-u1"].URL != "https://new.example.com/x" {
		t.Fatalf("persisted url: %q", fake.hooks["wh-u1"].URL)
	}
	if len(fake.unregistered) != 1 || fake.unregistered[0] != "wh-u1" {
		t.Fatalf("manager.Unregister not called: %v", fake.unregistered)
	}
	if len(fake.registered) != 1 || fake.registered[0] != "wh-u1" {
		t.Fatalf("manager.Register not called after update: %v", fake.registered)
	}
}

func TestUpdateWebhook_RotatesSecret(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-s", URL: "https://x", Secret: "old", Active: true})

	resp := patchWebhook(t, ts.URL, "wh-s", types.WebhookUpdateSpec{Secret: ptrStr("  newsecret  ")})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if got.Secret != "" {
		t.Fatalf("response leaked secret: %q", got.Secret)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.hooks["wh-s"].Secret != "newsecret" {
		t.Fatalf("persisted secret = %q, want trimmed 'newsecret'", fake.hooks["wh-s"].Secret)
	}
}

func TestUpdateWebhook_SetsEventTypes(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-e", URL: "https://x", Secret: "k", Active: true})

	resp := patchWebhook(t, ts.URL, "wh-e", types.WebhookUpdateSpec{
		EventTypes: ptrStrSlice([]string{"vm.started", " vm.stopped ", "vm.started"}),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if len(got.EventTypes) != 2 || got.EventTypes[0] != "vm.started" || got.EventTypes[1] != "vm.stopped" {
		t.Fatalf("event_types normalisation failed: %v", got.EventTypes)
	}
	fake.mu.Lock()
	persisted := fake.hooks["wh-e"].EventTypes
	fake.mu.Unlock()
	if len(persisted) != 2 {
		t.Fatalf("persisted event_types = %v, want 2 entries", persisted)
	}
}

func TestUpdateWebhook_ClearsEventTypes(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-clr", URL: "https://x", Secret: "k", Active: true,
		EventTypes: []string{"vm.started", "vm.stopped"},
	})

	resp := patchWebhook(t, ts.URL, "wh-clr", types.WebhookUpdateSpec{
		EventTypes: ptrStrSlice([]string{}),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if len(got.EventTypes) != 0 {
		t.Fatalf("event_types not cleared: %v", got.EventTypes)
	}
	fake.mu.Lock()
	persisted := fake.hooks["wh-clr"].EventTypes
	fake.mu.Unlock()
	if persisted != nil {
		t.Fatalf("persisted event_types not nil: %v", persisted)
	}
}

func TestUpdateWebhook_TogglesActive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-tog", URL: "https://x", Secret: "k", Active: true})

	resp := patchWebhook(t, ts.URL, "wh-tog", types.WebhookUpdateSpec{Active: ptrBool(false)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if got.Active {
		t.Fatalf("expected active=false")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.hooks["wh-tog"].Active {
		t.Fatalf("persisted active still true")
	}
	if len(fake.unregistered) != 1 || fake.unregistered[0] != "wh-tog" {
		t.Fatalf("manager.Unregister not called after disable: %v", fake.unregistered)
	}
}

func TestUpdateWebhook_RejectsEmptyBody(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-noop", URL: "https://x", Secret: "k", Active: true})

	resp := patchWebhook(t, ts.URL, "wh-noop", types.WebhookUpdateSpec{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateWebhook_RejectsInvalidURL(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-bad", URL: "https://x", Secret: "k", Active: true})

	resp := patchWebhook(t, ts.URL, "wh-bad", types.WebhookUpdateSpec{URL: ptrStr("ftp://nope.example")})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	resp2 := patchWebhook(t, ts.URL, "wh-bad", types.WebhookUpdateSpec{URL: ptrStr("   ")})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty url: status = %d, want 400", resp2.StatusCode)
	}
}

func TestUpdateWebhook_RejectsEmptySecret(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-es", URL: "https://x", Secret: "k", Active: true})

	resp := patchWebhook(t, ts.URL, "wh-es", types.WebhookUpdateSpec{Secret: ptrStr("   ")})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateWebhook_NotFound(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()

	resp := patchWebhook(t, ts.URL, "wh-missing", types.WebhookUpdateSpec{URL: ptrStr("https://x")})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUpdateWebhook_NoOpWhenValueEqualsCurrent(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-eq", URL: "https://x", Secret: "k", Active: true, EventTypes: []string{"vm.started"}})

	resp := patchWebhook(t, ts.URL, "wh-eq", types.WebhookUpdateSpec{
		URL:    ptrStr("https://x"),
		Active: ptrBool(true),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// Manager should not be bounced when there's nothing to do.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.unregistered) != 0 {
		t.Fatalf("manager.Unregister called for no-op update: %v", fake.unregistered)
	}
}

func TestUpdateWebhook_RejectsBadJSON(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-bj", URL: "https://x", Secret: "k", Active: true})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/webhooks/wh-bj",
		bytes.NewBufferString("{this-is-not-json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateWebhook_EventTypeReorderIsNoOp(t *testing.T) {
	// Filter lists are semantically sets — a PATCH that only reorders the
	// existing entries should not churn the in-memory worker.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-reorder", URL: "https://x", Secret: "k", Active: true,
		EventTypes: []string{"vm.started", "vm.stopped"},
	})

	resp := patchWebhook(t, ts.URL, "wh-reorder", types.WebhookUpdateSpec{
		EventTypes: ptrStrSlice([]string{"vm.stopped", "vm.started"}),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.unregistered) != 0 {
		t.Fatalf("manager.Unregister called for reorder-only patch: %v", fake.unregistered)
	}
	// The persisted slice should retain the *original* order (no write happened).
	persisted := fake.hooks["wh-reorder"].EventTypes
	if len(persisted) != 2 || persisted[0] != "vm.started" || persisted[1] != "vm.stopped" {
		t.Fatalf("persisted order should be unchanged on reorder-only no-op, got %v", persisted)
	}
}

func TestUpdateWebhook_SetsDescription(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-d1", URL: "https://x", Secret: "k", Active: true})

	resp := patchWebhook(t, ts.URL, "wh-d1", types.WebhookUpdateSpec{
		Description: ptrStr("  PagerDuty escalation hook  "),
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if got.Description != "PagerDuty escalation hook" {
		t.Fatalf("description not trimmed/persisted: %q", got.Description)
	}
	fake.mu.Lock()
	persisted := fake.hooks["wh-d1"].Description
	fake.mu.Unlock()
	if persisted != "PagerDuty escalation hook" {
		t.Fatalf("persisted description: %q", persisted)
	}
}

func TestUpdateWebhook_ClearsDescription(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-d2", URL: "https://x", Secret: "k", Active: true,
		Description: "previous description",
	})

	resp := patchWebhook(t, ts.URL, "wh-d2", types.WebhookUpdateSpec{
		Description: ptrStr(""),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if got.Description != "" {
		t.Fatalf("expected cleared description, got %q", got.Description)
	}
}

func TestUpdateWebhook_OmittedDescriptionIsNoOp(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-d3", URL: "https://x", Secret: "k", Active: true,
		Description: "keep me",
	})

	// Patch a different field; the description must survive untouched.
	resp := patchWebhook(t, ts.URL, "wh-d3", types.WebhookUpdateSpec{
		Active: ptrBool(false),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	fake.mu.Lock()
	persisted := fake.hooks["wh-d3"].Description
	fake.mu.Unlock()
	if persisted != "keep me" {
		t.Fatalf("description should be unchanged, got %q", persisted)
	}
}

func TestUpdateWebhook_RejectsLongDescription(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-d4", URL: "https://x", Secret: "k", Active: true})

	long := make([]byte, 1025)
	for i := range long {
		long[i] = 'a'
	}
	desc := string(long)
	resp := patchWebhook(t, ts.URL, "wh-d4", types.WebhookUpdateSpec{Description: &desc})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateWebhook_DescriptionNoOpDoesNotBounceWorker(t *testing.T) {
	// Setting description to its current value must not bounce the worker.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-d5", URL: "https://x", Secret: "k", Active: true,
		Description: "stable",
	})

	resp := patchWebhook(t, ts.URL, "wh-d5", types.WebhookUpdateSpec{
		Description: ptrStr("stable"),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.unregistered) != 0 {
		t.Fatalf("manager.Unregister called for description no-op: %v", fake.unregistered)
	}
}

func TestCreateWebhook_WithTags(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()

	body := jsonBody(t, types.WebhookCreateRequest{
		URL:    "https://example.com/hook",
		Secret: "topsecret",
		// Intentionally mixed-case, duplicated, and whitespace-padded — the
		// API must normalise + dedupe + sort + lowercase.
		Tags: []string{" Production ", "audit", "production"},
	})
	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, b)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if !reflect.DeepEqual(got.Tags, []string{"audit", "production"}) {
		t.Fatalf("tags not normalised: %#v", got.Tags)
	}
	fake.mu.Lock()
	persisted := fake.hooks[got.ID].Tags
	fake.mu.Unlock()
	if !reflect.DeepEqual(persisted, []string{"audit", "production"}) {
		t.Fatalf("persisted tags: %#v", persisted)
	}
}

func TestCreateWebhook_RejectsInvalidTagCharacters(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()
	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json",
		jsonBody(t, types.WebhookCreateRequest{
			URL: "https://example.com/x", Secret: "k",
			Tags: []string{"has spaces"},
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, b)
	}
}

func TestCreateWebhook_RejectsEmptyTag(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()
	resp, err := http.Post(ts.URL+"/api/v1/webhooks", "application/json",
		jsonBody(t, types.WebhookCreateRequest{
			URL: "https://example.com/x", Secret: "k",
			Tags: []string{"valid", "   "},
		}))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateWebhook_SetsTags(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-t1", URL: "https://x", Secret: "k", Active: true})

	tags := []string{"Audit", "  Production  "}
	resp := patchWebhook(t, ts.URL, "wh-t1", types.WebhookUpdateSpec{Tags: &tags})
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if !reflect.DeepEqual(got.Tags, []string{"audit", "production"}) {
		t.Fatalf("tags not normalised/persisted: %#v", got.Tags)
	}
}

func TestUpdateWebhook_ClearsTags(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-t2", URL: "https://x", Secret: "k", Active: true,
		Tags: []string{"audit", "production"},
	})

	empty := []string{}
	resp := patchWebhook(t, ts.URL, "wh-t2", types.WebhookUpdateSpec{Tags: &empty})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got types.Webhook
	decodeJSON(t, resp, &got)
	if len(got.Tags) != 0 {
		t.Fatalf("expected cleared tags, got %#v", got.Tags)
	}
	fake.mu.Lock()
	persisted := fake.hooks["wh-t2"].Tags
	fake.mu.Unlock()
	if len(persisted) != 0 {
		t.Fatalf("persisted tags should be empty/nil, got %#v", persisted)
	}
}

func TestUpdateWebhook_OmittedTagsIsNoOp(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-t3", URL: "https://x", Secret: "k", Active: true,
		Tags: []string{"keep", "me"},
	})
	// Patch a different field; the tags must survive untouched.
	resp := patchWebhook(t, ts.URL, "wh-t3", types.WebhookUpdateSpec{
		Active: ptrBool(false),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	fake.mu.Lock()
	persisted := fake.hooks["wh-t3"].Tags
	fake.mu.Unlock()
	if !reflect.DeepEqual(persisted, []string{"keep", "me"}) {
		t.Fatalf("tags should be unchanged, got %#v", persisted)
	}
}

func TestUpdateWebhook_TagsNoOpDoesNotBounceWorker(t *testing.T) {
	// Setting tags to a permutation of their current value must not bounce
	// the worker — tags are normalised on persistence so the PATCH path
	// detects equivalence after normalisation.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-t4", URL: "https://x", Secret: "k", Active: true,
		Tags: []string{"audit", "production"},
	})

	permutation := []string{"PRODUCTION", "audit"}
	resp := patchWebhook(t, ts.URL, "wh-t4", types.WebhookUpdateSpec{Tags: &permutation})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.unregistered) != 0 {
		t.Fatalf("manager.Unregister called for tags no-op: %v", fake.unregistered)
	}
}

func TestUpdateWebhook_RejectsInvalidTag(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-t5", URL: "https://x", Secret: "k", Active: true})

	bad := []string{"has spaces"}
	resp := patchWebhook(t, ts.URL, "wh-t5", types.WebhookUpdateSpec{Tags: &bad})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateWebhook_ReactivatesStoppedWorker(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-react", URL: "https://x", Secret: "k", Active: false})

	resp := patchWebhook(t, ts.URL, "wh-react", types.WebhookUpdateSpec{Active: ptrBool(true)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.registered) != 1 || fake.registered[0] != "wh-react" {
		t.Fatalf("manager.Register not called on reactivation: %v", fake.registered)
	}
}

// seedSearchableWebhooks pre-populates the fake store with three hooks that
// cover the major haystack fields (URL substring, event-type substring,
// mixed-case URL) so the filter tests can assert hits and misses without
// re-seeding each test.
func seedSearchableWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	now := time.Now().UTC()
	hooks := []*types.Webhook{
		{
			ID:         "wh-audit",
			URL:        "https://hooks.example.com/audit",
			Secret:     "audit-secret-token",
			EventTypes: []string{"vm.started", "vm.stopped"},
			Active:     true,
			CreatedAt:  now,
			// LastError is operator-noise and intentionally excluded from
			// the haystack — assert that the predicate does not match it.
			LastError: "dial tcp 198.51.100.7: connection refused",
		},
		{
			ID:          "wh-metrics",
			URL:         "https://metrics.example.com/in",
			Secret:      "k",
			EventTypes:  []string{"vm.created", "image.created"},
			Description: "Prometheus Alertmanager fan-out",
			Active:      true,
			CreatedAt:   now.Add(time.Second),
		},
		{
			ID:         "wh-CASE",
			URL:        "https://CamelCase.Example.com/Webhook",
			Secret:     "k",
			EventTypes: nil,
			Active:     true,
			CreatedAt:  now.Add(2 * time.Second),
		},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed PutWebhook: %v", err)
		}
	}
}

func listWebhooksWithQuery(t *testing.T, baseURL, rawQuery string) []*types.Webhook {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/v1/webhooks?" + rawQuery)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	return hooks
}

func TestListWebhooks_FilterBySearch_MatchesURL(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "search=audit")
	if len(hooks) != 1 || hooks[0].ID != "wh-audit" {
		t.Fatalf("expected only wh-audit, got %v", hooks)
	}
	if hooks[0].Secret != "" {
		t.Fatalf("filter response leaked secret: %q", hooks[0].Secret)
	}
}

func TestListWebhooks_FilterBySearch_MatchesEventType(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	// "image.created" is only carried by wh-metrics.
	hooks := listWebhooksWithQuery(t, ts.URL, "search=image.created")
	if len(hooks) != 1 || hooks[0].ID != "wh-metrics" {
		t.Fatalf("expected only wh-metrics, got %v", hooks)
	}
}

func TestListWebhooks_FilterBySearch_IsCaseInsensitive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	// Uppercase needle against a mixed-case URL must hit because the
	// handler lowercases the needle once before calling the predicate.
	hooks := listWebhooksWithQuery(t, ts.URL, "search=CAMELCASE")
	if len(hooks) != 1 || hooks[0].ID != "wh-CASE" {
		t.Fatalf("expected wh-CASE, got %v", hooks)
	}
}

func TestListWebhooks_FilterBySearch_TrimsWhitespace(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "search=%20%20audit%20%20")
	if len(hooks) != 1 || hooks[0].ID != "wh-audit" {
		t.Fatalf("expected only wh-audit after trim, got %v", hooks)
	}
}

func TestListWebhooks_FilterBySearch_EmptyQueryReturnsAll(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "search=")
	if len(hooks) != 3 {
		t.Fatalf("empty search must match all webhooks, got %d", len(hooks))
	}
}

func TestListWebhooks_FilterBySearch_NoMatchReturnsEmpty(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "search=needle-not-present-anywhere")
	if len(hooks) != 0 {
		t.Fatalf("expected empty result, got %d hooks: %+v", len(hooks), hooks)
	}
}

func TestListWebhooks_FilterBySearch_MatchesDescription(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	// "prometheus" only appears in wh-metrics' description.
	hooks := listWebhooksWithQuery(t, ts.URL, "search=prometheus")
	if len(hooks) != 1 || hooks[0].ID != "wh-metrics" {
		t.Fatalf("expected only wh-metrics via description, got %v", hooks)
	}
	// And case-insensitively:
	hooks = listWebhooksWithQuery(t, ts.URL, "search=ALERTMANAGER")
	if len(hooks) != 1 || hooks[0].ID != "wh-metrics" {
		t.Fatalf("expected case-insensitive description match, got %v", hooks)
	}
}

func TestListWebhooks_FilterBySearch_MatchesTag(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)
	// Add a tag to wh-metrics so the predicate has something to hit.
	fake.PutWebhook(&types.Webhook{
		ID: "wh-tagged", URL: "https://example.com/t", Secret: "k", Active: true,
		Tags: []string{"production"},
	})

	hooks := listWebhooksWithQuery(t, ts.URL, "search=production")
	if len(hooks) != 1 || hooks[0].ID != "wh-tagged" {
		t.Fatalf("expected only wh-tagged via tag search, got %v", hooks)
	}
	// Case-insensitive needle hits a lowercase-normalised tag.
	hooks = listWebhooksWithQuery(t, ts.URL, "search=PROD")
	if len(hooks) != 1 || hooks[0].ID != "wh-tagged" {
		t.Fatalf("expected case-insensitive partial tag match, got %v", hooks)
	}
}

func TestListWebhooks_FilterByTag(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-tag1", URL: "https://a", Secret: "k", Active: true,
		Tags: []string{"production", "audit"},
	})
	fake.PutWebhook(&types.Webhook{
		ID: "wh-tag2", URL: "https://b", Secret: "k", Active: true,
		Tags: []string{"production"},
	})
	fake.PutWebhook(&types.Webhook{
		ID: "wh-tag3", URL: "https://c", Secret: "k", Active: true,
		Tags: []string{"staging"},
	})
	fake.PutWebhook(&types.Webhook{
		ID: "wh-tag4", URL: "https://d", Secret: "k", Active: true,
		// No tags at all — must not appear in tag-filtered results.
	})

	hooks := listWebhooksWithQuery(t, ts.URL, "tag=production")
	if len(hooks) != 2 {
		t.Fatalf("expected 2 production webhooks, got %d: %+v", len(hooks), hooks)
	}
	for _, h := range hooks {
		if h.ID != "wh-tag1" && h.ID != "wh-tag2" {
			t.Fatalf("unexpected webhook in production set: %s", h.ID)
		}
	}
}

func TestListWebhooks_FilterByTag_CaseInsensitive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-ci", URL: "https://x", Secret: "k", Active: true,
		Tags: []string{"production"},
	})

	hooks := listWebhooksWithQuery(t, ts.URL, "tag=PRODUCTION")
	if len(hooks) != 1 || hooks[0].ID != "wh-ci" {
		t.Fatalf("expected case-insensitive tag match, got %v", hooks)
	}
}

func TestListWebhooks_FilterByTag_TrimsWhitespace(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-trim", URL: "https://x", Secret: "k", Active: true,
		Tags: []string{"audit"},
	})
	hooks := listWebhooksWithQuery(t, ts.URL, "tag=%20%20audit%20%20")
	if len(hooks) != 1 || hooks[0].ID != "wh-trim" {
		t.Fatalf("expected whitespace-trimmed tag filter to match, got %v", hooks)
	}
}

func TestListWebhooks_FilterByTag_NoMatchReturnsEmpty(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-x", URL: "https://x", Secret: "k", Active: true,
		Tags: []string{"audit"},
	})
	hooks := listWebhooksWithQuery(t, ts.URL, "tag=nonexistent")
	if len(hooks) != 0 {
		t.Fatalf("expected empty result, got %d hooks", len(hooks))
	}
}

func TestListWebhooks_FilterByTag_ComposesWithSearch(t *testing.T) {
	// `?tag=` and `?search=` compose additively (AND) — only a webhook that
	// passes both filters appears in the response.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-a", URL: "https://hooks.example.com/audit", Secret: "k", Active: true,
		Tags: []string{"audit", "production"},
	})
	fake.PutWebhook(&types.Webhook{
		ID: "wh-b", URL: "https://hooks.example.com/audit", Secret: "k", Active: true,
		Tags: []string{"staging"},
	})

	hooks := listWebhooksWithQuery(t, ts.URL, "tag=production&search=audit")
	if len(hooks) != 1 || hooks[0].ID != "wh-a" {
		t.Fatalf("expected only wh-a (audit URL + production tag), got %v", hooks)
	}
}

// =====================================================
// `?event_type=` filter (5.4.26)
// =====================================================
//
// Mirrors the bulk_delete `event_type` selector: case-insensitive exact-match
// against entries in `event_types`. Catch-all webhooks (empty event_types)
// are NOT matched.

func seedEventTypeWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	now := time.Now().UTC()
	hooks := []*types.Webhook{
		{
			ID: "wh-vm-only", URL: "https://a.example.com", Secret: "k", Active: true,
			EventTypes: []string{"vm.started", "vm.stopped"},
			CreatedAt:  now,
		},
		{
			ID: "wh-vm-and-image", URL: "https://b.example.com", Secret: "k", Active: true,
			EventTypes: []string{"vm.created", "image.created"},
			CreatedAt:  now.Add(time.Second),
		},
		{
			ID: "wh-image-only", URL: "https://c.example.com", Secret: "k", Active: true,
			EventTypes: []string{"image.created"},
			CreatedAt:  now.Add(2 * time.Second),
		},
		{
			ID: "wh-catchall", URL: "https://d.example.com", Secret: "k", Active: true,
			EventTypes: nil, // matches every event behaviourally, but NOT this filter
			CreatedAt:  now.Add(3 * time.Second),
		},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed PutWebhook: %v", err)
		}
	}
}

func TestListWebhooks_FilterByEventType_ExactMatch(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedEventTypeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "event_type=image.created")
	if len(hooks) != 2 {
		t.Fatalf("expected 2 image.created subscribers, got %d: %+v", len(hooks), hooks)
	}
	got := map[string]bool{}
	for _, h := range hooks {
		got[h.ID] = true
	}
	if !got["wh-vm-and-image"] || !got["wh-image-only"] {
		t.Fatalf("missing expected subscribers in %v", got)
	}
}

func TestListWebhooks_FilterByEventType_CaseInsensitive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedEventTypeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "event_type=IMAGE.CREATED")
	if len(hooks) != 2 {
		t.Fatalf("expected case-insensitive match to find 2, got %d", len(hooks))
	}
}

func TestListWebhooks_FilterByEventType_WhitespaceTrimmed(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedEventTypeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "event_type=%20%20vm.started%20%20")
	if len(hooks) != 1 || hooks[0].ID != "wh-vm-only" {
		t.Fatalf("expected whitespace-trimmed filter to match wh-vm-only, got %v", hooks)
	}
}

func TestListWebhooks_FilterByEventType_EmptyIsNoOp(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedEventTypeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "event_type=")
	if len(hooks) != 4 {
		t.Fatalf("expected empty filter to return all 4, got %d", len(hooks))
	}
}

func TestListWebhooks_FilterByEventType_NoMatch(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedEventTypeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "event_type=snapshot.taken")
	if len(hooks) != 0 {
		t.Fatalf("expected no matches, got %d", len(hooks))
	}
}

func TestListWebhooks_FilterByEventType_CatchAllExcluded(t *testing.T) {
	// Catch-all webhooks (nil/empty event_types) match every event
	// behaviourally but are NOT swept by the explicit-membership filter.
	// Mirrors the bulk_delete `event_type` selector semantics.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedEventTypeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "event_type=vm.created")
	if len(hooks) != 1 || hooks[0].ID != "wh-vm-and-image" {
		t.Fatalf("catch-all webhook leaked into explicit-membership filter: %v", hooks)
	}
}

func TestListWebhooks_FilterByEventType_ComposesWithTag(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-prod-vm", URL: "https://a", Secret: "k", Active: true,
		Tags:       []string{"production"},
		EventTypes: []string{"vm.created"},
	})
	fake.PutWebhook(&types.Webhook{
		ID: "wh-staging-vm", URL: "https://b", Secret: "k", Active: true,
		Tags:       []string{"staging"},
		EventTypes: []string{"vm.created"},
	})
	fake.PutWebhook(&types.Webhook{
		ID: "wh-prod-image", URL: "https://c", Secret: "k", Active: true,
		Tags:       []string{"production"},
		EventTypes: []string{"image.created"},
	})

	hooks := listWebhooksWithQuery(t, ts.URL, "tag=production&event_type=vm.created")
	if len(hooks) != 1 || hooks[0].ID != "wh-prod-vm" {
		t.Fatalf("expected intersection of tag=production AND event_type=vm.created to be wh-prod-vm only, got %v", hooks)
	}
}

func TestListWebhooks_FilterByEventType_ComposesWithSearch(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{
		ID: "wh-audit-vm", URL: "https://hooks.example.com/audit", Secret: "k", Active: true,
		EventTypes: []string{"vm.created"},
	})
	fake.PutWebhook(&types.Webhook{
		ID: "wh-metrics-vm", URL: "https://hooks.example.com/metrics", Secret: "k", Active: true,
		EventTypes: []string{"vm.created"},
	})

	hooks := listWebhooksWithQuery(t, ts.URL, "event_type=vm.created&search=audit")
	if len(hooks) != 1 || hooks[0].ID != "wh-audit-vm" {
		t.Fatalf("expected intersection of event_type=vm.created AND search=audit to be wh-audit-vm only, got %v", hooks)
	}
}

func TestListWebhooks_FilterByEventType_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedEventTypeWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?event_type=image.created")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (post-filter population)", got)
	}
}

// seedTimeRangeWebhooks pins three webhooks at deterministic CreatedAt
// timestamps (2026-05-01, 2026-05-15, 2026-05-30) so the ?since= / ?until=
// boundary tests below split them cleanly. Mirrors the snapshot (5.4.28),
// image (5.4.29), VM (5.4.30), and template (5.4.31) time-range fixtures.
func seedTimeRangeWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	hooks := []*types.Webhook{
		{ID: "wh-early", URL: "https://a.example.com", Secret: "k", Active: true, CreatedAt: day(1)},
		{ID: "wh-mid", URL: "https://b.example.com", Secret: "k", Active: true, CreatedAt: day(15)},
		{ID: "wh-late", URL: "https://c.example.com", Secret: "k", Active: true, CreatedAt: day(30)},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed PutWebhook: %v", err)
		}
	}
}

func TestListWebhooks_FilterBySince(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedTimeRangeWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?since=2026-05-10T00:00:00Z")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	ids := map[string]bool{}
	for _, h := range hooks {
		ids[h.ID] = true
	}
	if !ids["wh-mid"] || !ids["wh-late"] || ids["wh-early"] {
		t.Fatalf("expected wh-mid + wh-late, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByUntil(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedTimeRangeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "until=2026-05-20T00:00:00Z")
	if len(hooks) != 2 {
		t.Fatalf("expected 2 webhooks <= until, got %+v", hooks)
	}
}

func TestListWebhooks_FilterBySinceAndUntil(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedTimeRangeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "since=2026-05-10T00:00:00Z&until=2026-05-20T00:00:00Z")
	if len(hooks) != 1 || hooks[0].ID != "wh-mid" {
		t.Fatalf("expected only wh-mid, got %+v", hooks)
	}
}

func TestListWebhooks_FilterBySince_Inclusive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	boundary := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-edge", URL: "https://edge.example.com", Secret: "k", Active: true, CreatedAt: boundary}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hooks := listWebhooksWithQuery(t, ts.URL, "since=2026-05-01T00:00:00Z")
	if len(hooks) != 1 || hooks[0].ID != "wh-edge" {
		t.Fatalf("expected boundary match, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByInvalidSince(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?since=last-tuesday")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_since")
}

func TestListWebhooks_FilterByInvalidUntil(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?until=2026-13-99")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_until")
}

func TestListWebhooks_FilterBySince_EmptyIsNoOp(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedTimeRangeWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "since=%20%20")
	if len(hooks) != 3 {
		t.Fatalf("whitespace-only since should be a no-op; got %+v", hooks)
	}
}

func TestListWebhooks_FilterByTimeRange_ExcludesZeroCreatedAt(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-zero", URL: "https://zero.example.com", Secret: "k", Active: true}); err != nil { // zero CreatedAt
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-dated", URL: "https://dated.example.com", Secret: "k", Active: true, CreatedAt: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hooks := listWebhooksWithQuery(t, ts.URL, "since=2026-05-01T00:00:00Z")
	if len(hooks) != 1 || hooks[0].ID != "wh-dated" {
		t.Fatalf("expected only wh-dated (zero-time excluded), got %+v", hooks)
	}
}

func TestListWebhooks_FilterBySince_ComposesWithTagAndSearch(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-prod-old", URL: "https://hooks.example.com/audit-old", Secret: "k", Active: true, CreatedAt: day(1), Tags: []string{"production"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-prod-new", URL: "https://hooks.example.com/audit-new", Secret: "k", Active: true, CreatedAt: day(20), Tags: []string{"production"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-staging-new", URL: "https://hooks.example.com/audit-stg", Secret: "k", Active: true, CreatedAt: day(20), Tags: []string{"staging"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-prod-other", URL: "https://hooks.example.com/metrics", Secret: "k", Active: true, CreatedAt: day(20), Tags: []string{"production"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?since=2026-05-10T00:00:00Z&tag=production&search=audit")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 1 || hooks[0].ID != "wh-prod-new" {
		t.Fatalf("expected only wh-prod-new, got %+v", hooks)
	}
}

func TestListWebhooks_FilterBySearch_SecretAndLastErrorNotInHaystack(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	// Even though wh-audit's Secret contains "audit-secret-token", the
	// matcher must never consult the secret. The "audit" needle still
	// matches via the URL ("…/audit"), but a substring that only exists
	// in the secret must not.
	hooks := listWebhooksWithQuery(t, ts.URL, "search=secret-token")
	if len(hooks) != 0 {
		t.Fatalf("expected secret substring to not match, got %d hooks", len(hooks))
	}

	// LastError carries "dial tcp 198.51.100.7…"; the IP is exclusive to
	// last_error so a positive match would indicate a contract regression.
	hooks = listWebhooksWithQuery(t, ts.URL, "search=198.51.100.7")
	if len(hooks) != 0 {
		t.Fatalf("expected last_error substring to not match, got %d hooks", len(hooks))
	}
}

// seedLastDeliveryWebhooks pins four webhooks with deterministic
// LastDeliveryAt timestamps (and one never-delivered fixture) so the
// ?last_delivery_since= / ?last_delivery_until= boundary tests below
// split them cleanly. Mirrors the seedTimeRangeWebhooks fixture used by
// the created_at time-range filter (5.4.32).
func seedLastDeliveryWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	hooks := []*types.Webhook{
		{ID: "wh-early", URL: "https://a.example.com", Secret: "k", Active: true, CreatedAt: day(1), LastDeliveryAt: day(1)},
		{ID: "wh-mid", URL: "https://b.example.com", Secret: "k", Active: true, CreatedAt: day(2), LastDeliveryAt: day(15)},
		{ID: "wh-late", URL: "https://c.example.com", Secret: "k", Active: true, CreatedAt: day(3), LastDeliveryAt: day(30)},
		// wh-never is never-delivered (zero LastDeliveryAt). Boundary tests
		// must filter it OUT whenever either bound is set, matching the
		// snapshotInTimeRange zero-time-exclusion contract.
		{ID: "wh-never", URL: "https://d.example.com", Secret: "k", Active: true, CreatedAt: day(4)},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed PutWebhook: %v", err)
		}
	}
}

func TestListWebhooks_FilterByLastDeliverySince(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedLastDeliveryWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?last_delivery_since=2026-05-10T00:00:00Z")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (wh-mid + wh-late; wh-never excluded by zero-time rule)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	ids := map[string]bool{}
	for _, h := range hooks {
		ids[h.ID] = true
	}
	if !ids["wh-mid"] || !ids["wh-late"] || ids["wh-early"] || ids["wh-never"] {
		t.Fatalf("expected wh-mid + wh-late only, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByLastDeliveryUntil(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedLastDeliveryWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "last_delivery_until=2026-05-20T00:00:00Z")
	// wh-early (delivered day 1) + wh-mid (delivered day 15); wh-late after
	// the bound, wh-never excluded by zero-time rule.
	if len(hooks) != 2 {
		t.Fatalf("expected 2 webhooks <= last_delivery_until, got %+v", hooks)
	}
	ids := map[string]bool{}
	for _, h := range hooks {
		ids[h.ID] = true
	}
	if !ids["wh-early"] || !ids["wh-mid"] || ids["wh-late"] || ids["wh-never"] {
		t.Fatalf("expected wh-early + wh-mid only, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByLastDeliveryRange(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedLastDeliveryWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL,
		"last_delivery_since=2026-05-10T00:00:00Z&last_delivery_until=2026-05-20T00:00:00Z")
	if len(hooks) != 1 || hooks[0].ID != "wh-mid" {
		t.Fatalf("expected only wh-mid in [day 10, day 20], got %+v", hooks)
	}
}

func TestListWebhooks_FilterByLastDeliverySince_Inclusive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	boundary := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-edge", URL: "https://edge.example.com", Secret: "k", Active: true, LastDeliveryAt: boundary}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hooks := listWebhooksWithQuery(t, ts.URL, "last_delivery_since=2026-05-01T00:00:00Z")
	if len(hooks) != 1 || hooks[0].ID != "wh-edge" {
		t.Fatalf("expected boundary match, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByLastDeliveryUntil_Inclusive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	boundary := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-edge", URL: "https://edge.example.com", Secret: "k", Active: true, LastDeliveryAt: boundary}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hooks := listWebhooksWithQuery(t, ts.URL, "last_delivery_until=2026-05-15T12:00:00Z")
	if len(hooks) != 1 || hooks[0].ID != "wh-edge" {
		t.Fatalf("expected boundary match, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByInvalidLastDeliverySince(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?last_delivery_since=last-tuesday")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_last_delivery_since")
}

func TestListWebhooks_FilterByInvalidLastDeliveryUntil(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?last_delivery_until=2026-13-99")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_last_delivery_until")
}

func TestListWebhooks_FilterByLastDeliverySince_EmptyIsNoOp(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedLastDeliveryWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "last_delivery_since=%20%20")
	if len(hooks) != 4 {
		t.Fatalf("whitespace-only last_delivery_since should be a no-op; got %+v", hooks)
	}
}

func TestListWebhooks_FilterByLastDeliveryRange_ExcludesNeverDelivered(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedLastDeliveryWebhooks(t, fake)

	// Wide-open range that covers every dated webhook: wh-never (zero
	// LastDeliveryAt) must still be filtered out because the predicate is
	// "show me deliveries in a window".
	hooks := listWebhooksWithQuery(t, ts.URL,
		"last_delivery_since=2025-01-01T00:00:00Z&last_delivery_until=2027-01-01T00:00:00Z")
	if len(hooks) != 3 {
		t.Fatalf("expected 3 dated webhooks (never-delivered excluded), got %+v", hooks)
	}
	for _, h := range hooks {
		if h.ID == "wh-never" {
			t.Fatalf("never-delivered webhook leaked through wide range filter: %+v", h)
		}
	}
}

func TestListWebhooks_FilterByLastDelivery_ComposesWithDeliveryStatus(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	// Three webhooks with deliveries in [day 10, day 20]: one healthy
	// (200, no LastError), one failing (500), and one failing (200 + stale
	// LastError). The status filter must narrow to the healthy one.
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-healthy", URL: "https://h.example.com", Secret: "k", Active: true, LastDeliveryAt: day(15), LastStatus: 200}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-fail-500", URL: "https://f.example.com", Secret: "k", Active: true, LastDeliveryAt: day(15), LastStatus: 500, LastError: "boom"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-fail-stale", URL: "https://s.example.com", Secret: "k", Active: true, LastDeliveryAt: day(15), LastStatus: 200, LastError: "earlier"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Out-of-range healthy hook so the range filter has work to do too.
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-out-of-range", URL: "https://o.example.com", Secret: "k", Active: true, LastDeliveryAt: day(1), LastStatus: 200}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?last_delivery_since=2026-05-10T00:00:00Z&last_delivery_until=2026-05-20T00:00:00Z&delivery_status=healthy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 1 || hooks[0].ID != "wh-healthy" {
		t.Fatalf("expected only wh-healthy, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByLastDelivery_PaginatedTotalCountReflectsFiltered(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedLastDeliveryWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?last_delivery_since=2026-05-10T00:00:00Z&per_page=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	// Two webhooks satisfy the range filter (wh-mid + wh-late). Pagination
	// caps the response at one row, but X-Total-Count must reflect the
	// post-filter / pre-pagination population.
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (post-filter / pre-pagination)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 1 {
		t.Fatalf("expected 1 row (per_page=1), got %d", len(hooks))
	}
}

// seedSortableWebhooks seeds a deterministic set of webhooks with distinct
// URLs, creation timestamps, and last-delivery timestamps so the sort tests
// can assert exact orderings without depending on insertion order.
func seedSortableWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	hooks := []*types.Webhook{
		{
			ID:             "wh-3",
			URL:            "https://Charlie.example.com/h",
			Secret:         "k",
			Active:         true,
			CreatedAt:      base.Add(3 * time.Hour),
			LastDeliveryAt: base.Add(30 * time.Hour),
		},
		{
			ID:             "wh-1",
			URL:            "https://alpha.example.com/h",
			Secret:         "k",
			Active:         true,
			CreatedAt:      base.Add(1 * time.Hour),
			LastDeliveryAt: base.Add(10 * time.Hour),
		},
		{
			ID:        "wh-2",
			URL:       "https://Bravo.example.com/h",
			Secret:    "k",
			Active:    true,
			CreatedAt: base.Add(2 * time.Hour),
			// LastDeliveryAt zero — never delivered
		},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed PutWebhook: %v", err)
		}
	}
}

func webhookIDsInOrder(hooks []*types.Webhook) []string {
	out := make([]string, len(hooks))
	for i, h := range hooks {
		out[i] = h.ID
	}
	return out
}

func TestListWebhooks_SortDefaultIsIDAsc(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "")
	want := []string{"wh-1", "wh-2", "wh-3"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("default sort: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByURL_CaseInsensitive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "sort=url")
	// case-insensitive: alpha < Bravo < Charlie
	want := []string{"wh-1", "wh-2", "wh-3"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=url: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByURLDesc(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "sort=url&order=desc")
	want := []string{"wh-3", "wh-2", "wh-1"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=url&order=desc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByCreatedAtDesc(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "sort=created_at&order=desc")
	// newest first
	want := []string{"wh-3", "wh-2", "wh-1"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=created_at desc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByLastDelivery_NeverDeliveredSortsLastAsc(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "sort=last_delivery_at")
	// asc: oldest delivery first, never-delivered (wh-2) at tail
	want := []string{"wh-1", "wh-3", "wh-2"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=last_delivery_at asc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByLastDelivery_NeverDeliveredSortsFirstDesc(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "sort=last_delivery_at&order=desc")
	// desc: never-delivered head, then newest-first
	want := []string{"wh-2", "wh-3", "wh-1"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=last_delivery_at desc: got %v, want %v", got, want)
	}
}

// 5.4.98 — delivery_status sort axis. Alphabetical: failing < healthy <
// never. Tiebreak on `id`. Symmetric sort counterpart to the
// case-insensitive `?delivery_status=` exact-match filter (5.4.35).
func seedClassifiedWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	hooks := []*types.Webhook{
		// never — LastDeliveryAt zero takes precedence
		{ID: "wh-never", URL: "https://never.example.com/h", Secret: "k", Active: true, CreatedAt: base},
		// healthy — last attempt 2xx + empty LastError
		{ID: "wh-healthy", URL: "https://healthy.example.com/h", Secret: "k", Active: true,
			CreatedAt: base, LastDeliveryAt: base.Add(time.Hour), LastStatus: 200},
		// failing — non-2xx
		{ID: "wh-failing", URL: "https://failing.example.com/h", Secret: "k", Active: true,
			CreatedAt: base, LastDeliveryAt: base.Add(2 * time.Hour), LastStatus: 500},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed PutWebhook: %v", err)
		}
	}
}

func TestListWebhooks_SortByDeliveryStatus_AscAlphabetical(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedClassifiedWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "sort=delivery_status")
	want := []string{"wh-failing", "wh-healthy", "wh-never"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=delivery_status asc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByDeliveryStatus_DescAlphabetical(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedClassifiedWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "sort=delivery_status&order=desc")
	want := []string{"wh-never", "wh-healthy", "wh-failing"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=delivery_status desc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByDeliveryStatus_NormalisesCaseAndWhitespace(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedClassifiedWebhooks(t, fake)

	// Mixed-case + whitespace must normalise to the canonical lowercase axis.
	hooks := listWebhooksWithQuery(t, ts.URL, "sort=%20Delivery_Status%20")
	want := []string{"wh-failing", "wh-healthy", "wh-never"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=Delivery_Status: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByDeliveryStatus_ComposesWithFilter(t *testing.T) {
	// Filter narrows the cohort to "failing"; sort still orders within it.
	// Add a second failing webhook so there's a deterministic id tiebreak.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedClassifiedWebhooks(t, fake)
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	if err := fake.PutWebhook(&types.Webhook{
		ID: "wh-failing-2", URL: "https://failing2.example.com/h", Secret: "k", Active: true,
		CreatedAt: base, LastDeliveryAt: base.Add(3 * time.Hour), LastStatus: 502,
	}); err != nil {
		t.Fatalf("seed PutWebhook: %v", err)
	}

	hooks := listWebhooksWithQuery(t, ts.URL, "delivery_status=failing&sort=delivery_status")
	// All survivors classify as failing; comparator falls through to id-asc.
	want := []string{"wh-failing", "wh-failing-2"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("filter+sort: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByDeliveryStatus_PaginationPreservesTotalCount(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedClassifiedWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?sort=delivery_status&per_page=2&page=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	if total := resp.Header.Get("X-Total-Count"); total != "3" {
		t.Fatalf("X-Total-Count = %q, want 3 (post-filter pre-pagination population)", total)
	}
	var page1 []*types.Webhook
	if err := json.NewDecoder(resp.Body).Decode(&page1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// page 1 takes the first 2 of [wh-failing, wh-healthy, wh-never]
	want := []string{"wh-failing", "wh-healthy"}
	if got := webhookIDsInOrder(page1); !equalStringSlice(got, want) {
		t.Fatalf("page1: got %v, want %v", got, want)
	}
}

func TestListWebhooks_InvalidSortAdvertisesDeliveryStatus(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?sort=bogus")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, b)
	}
	body, _ := readAllBody(resp)
	if !strings.Contains(string(body), "delivery_status") {
		t.Fatalf("400 envelope %q should advertise the new delivery_status axis so operators discover it", body)
	}
}

// 5.4.114 — active sort axis. Boolean compare false < true with id tiebreak.
// Closed-and-total: every webhook resolves to true or false so there is no
// nil-trailing bucket — mirrors the VM auto_start (5.4.108) / locked
// (5.4.109) and schedule enabled (5.4.113) axes.
//
// seedActiveSortWebhooks lays out two active and two inactive webhooks in
// non-sorted ID order so the tests assert the comparator's effect, not the
// input order. Distinct from `seedActiveWebhooks` (5.4.37 filter fixture)
// which only has 3 entries and asymmetric cohorts.
func seedActiveSortWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	hooks := []*types.Webhook{
		{ID: "wh-sort-1", URL: "https://a.example.com/h", Secret: "k", Active: true, CreatedAt: base},
		{ID: "wh-sort-2", URL: "https://b.example.com/h", Secret: "k", Active: false, CreatedAt: base},
		{ID: "wh-sort-3", URL: "https://c.example.com/h", Secret: "k", Active: true, CreatedAt: base},
		{ID: "wh-sort-4", URL: "https://d.example.com/h", Secret: "k", Active: false, CreatedAt: base},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed PutWebhook: %v", err)
		}
	}
}

func TestListWebhooks_SortByActive_AscPutsInactiveFirst(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveSortWebhooks(t, fake)

	// asc: inactive cohort heads (id tiebreak: wh-sort-2, wh-sort-4),
	// then active cohort (id tiebreak: wh-sort-1, wh-sort-3).
	hooks := listWebhooksWithQuery(t, ts.URL, "sort=active&order=asc")
	want := []string{"wh-sort-2", "wh-sort-4", "wh-sort-1", "wh-sort-3"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=active asc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByActive_DescPutsActiveFirst(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveSortWebhooks(t, fake)

	// desc flips the entire compare result including the id tiebreak, so
	// within each cohort the higher id comes first.
	hooks := listWebhooksWithQuery(t, ts.URL, "sort=active&order=desc")
	want := []string{"wh-sort-3", "wh-sort-1", "wh-sort-4", "wh-sort-2"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=active desc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByActive_NormalisesCaseAndWhitespace(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveSortWebhooks(t, fake)

	// Mixed-case + whitespace must normalise to the canonical lowercase
	// axis (the parser trims + lowercases before validating).
	hooks := listWebhooksWithQuery(t, ts.URL, "sort=%20ACTIVE%20&order=asc")
	want := []string{"wh-sort-2", "wh-sort-4", "wh-sort-1", "wh-sort-3"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=ACTIVE: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByActive_ComposesWithFilter(t *testing.T) {
	// Filter narrows the cohort to active=true; sort still orders within
	// it. The comparator falls through to id-asc because every survivor
	// has Active=true.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveSortWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "active=true&sort=active&order=asc")
	want := []string{"wh-sort-1", "wh-sort-3"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("filter+sort: got %v, want %v", got, want)
	}
}

func TestListWebhooks_InvalidSortAdvertisesActive(t *testing.T) {
	// 5.4.114 — the 400 envelope must advertise active so operators discover
	// the new axis from the daemon's error text.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?sort=bogus-active-axis")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, b)
	}
	body, _ := readAllBody(resp)
	if !strings.Contains(string(body), "active") {
		t.Fatalf("400 envelope %q should advertise the new active axis so operators discover it", body)
	}
}

// 5.4.122 — description sort axis. Case-insensitive compare on
// Webhook.Description with empty-trailing nil-handling. Mirrors the VM
// (5.4.120) / template (5.4.119) / image (5.4.118) / snapshot (5.4.121)
// description axes one resource over.
//
// seedDescriptionSortWebhooks lays out four webhooks in non-sorted ID
// order: two with descriptions (mixed case) and two without. Tests assert
// the comparator's effect, not the input order.
func seedDescriptionSortWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	hooks := []*types.Webhook{
		{ID: "wh-desc-1", URL: "https://a.example.com/h", Secret: "k", Description: "Slack #ops", CreatedAt: base},
		{ID: "wh-desc-2", URL: "https://b.example.com/h", Secret: "k", Description: "", CreatedAt: base},
		{ID: "wh-desc-3", URL: "https://c.example.com/h", Secret: "k", Description: "alpha", CreatedAt: base},
		{ID: "wh-desc-4", URL: "https://d.example.com/h", Secret: "k", Description: "", CreatedAt: base},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed PutWebhook: %v", err)
		}
	}
}

func TestListWebhooks_SortByDescription_AscCaseInsensitive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDescriptionSortWebhooks(t, fake)

	// asc: "alpha" < "slack #ops" (case-folded), then the two empty
	// descriptions sink to the tail (id-tiebreak: wh-desc-2 < wh-desc-4).
	hooks := listWebhooksWithQuery(t, ts.URL, "sort=description&order=asc")
	want := []string{"wh-desc-3", "wh-desc-1", "wh-desc-2", "wh-desc-4"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=description asc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByDescription_DescEmptyHeads(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDescriptionSortWebhooks(t, fake)

	// desc: empty descriptions head (id-tiebreak inverted by outer desc
	// wrapper: wh-desc-4 > wh-desc-2), then "slack #ops" > "alpha".
	hooks := listWebhooksWithQuery(t, ts.URL, "sort=description&order=desc")
	want := []string{"wh-desc-4", "wh-desc-2", "wh-desc-1", "wh-desc-3"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=description desc: got %v, want %v", got, want)
	}
}

func TestListWebhooks_SortByDescription_NormalisesCaseAndWhitespace(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDescriptionSortWebhooks(t, fake)

	// Mixed-case + whitespace must normalise to the canonical lowercase
	// axis (the parser trims + lowercases before validating).
	hooks := listWebhooksWithQuery(t, ts.URL, "sort=%20DESCRIPTION%20&order=asc")
	want := []string{"wh-desc-3", "wh-desc-1", "wh-desc-2", "wh-desc-4"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("sort=DESCRIPTION: got %v, want %v", got, want)
	}
}

func TestListWebhooks_InvalidSortAdvertisesDescription(t *testing.T) {
	// 5.4.122 — the 400 envelope must advertise description so operators
	// discover the new axis from the daemon's error text.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?sort=bogus-description-axis")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, b)
	}
	body, _ := readAllBody(resp)
	if !strings.Contains(string(body), "description") {
		t.Fatalf("400 envelope %q should advertise the new description axis so operators discover it", body)
	}
}

func TestListWebhooks_SortComposesWithSearch(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	// "example.com" matches all three; sort=url asc orders alpha < Bravo < Charlie.
	hooks := listWebhooksWithQuery(t, ts.URL, "search=example.com&sort=url")
	want := []string{"wh-1", "wh-2", "wh-3"}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, want) {
		t.Fatalf("search+sort: got %v, want %v", got, want)
	}
}

func TestListWebhooks_RejectsInvalidSort(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?sort=secret")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, b)
	}
}

func TestListWebhooks_RejectsInvalidOrder(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?order=sideways")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 400; body=%s", resp.StatusCode, b)
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- Bulk-delete (2.3.10) ---

func postJSON(t *testing.T, urlStr string, body any) *http.Response {
	t.Helper()
	resp, err := http.Post(urlStr, "application/json", jsonBody(t, body))
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	return resp
}

func TestBulkDeleteWebhooks_ByIDs(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()

	fake.PutWebhook(&types.Webhook{ID: "wh-a", URL: "https://a", Secret: "s", Active: true})
	fake.PutWebhook(&types.Webhook{ID: "wh-b", URL: "https://b", Secret: "s", Active: true})
	fake.PutWebhook(&types.Webhook{ID: "wh-c", URL: "https://c", Secret: "s", Active: true})

	resp := postJSON(t, ts.URL+"/api/v1/webhooks/bulk_delete",
		map[string]any{"ids": []string{"wh-a", "wh-c"}})
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var got bulkDeleteWebhooksResponse
	decodeJSON(t, resp, &got)
	if len(got.Results) != 2 {
		t.Fatalf("results = %v, want 2 entries", got.Results)
	}
	for _, r := range got.Results {
		if !r.Success {
			t.Fatalf("id %s expected success, got %+v", r.ID, r)
		}
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ok := fake.hooks["wh-a"]; ok {
		t.Fatalf("wh-a not removed")
	}
	if _, ok := fake.hooks["wh-c"]; ok {
		t.Fatalf("wh-c not removed")
	}
	if _, ok := fake.hooks["wh-b"]; !ok {
		t.Fatalf("wh-b should still exist (not in selection)")
	}
	if len(fake.unregistered) != 2 {
		t.Fatalf("manager.Unregister called %d times, want 2: %v", len(fake.unregistered), fake.unregistered)
	}
}

func TestBulkDeleteWebhooks_ByEventType(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()

	fake.PutWebhook(&types.Webhook{ID: "wh-1", URL: "https://1", Secret: "s", Active: true,
		EventTypes: []string{"vm.started", "vm.deleted"}})
	fake.PutWebhook(&types.Webhook{ID: "wh-2", URL: "https://2", Secret: "s", Active: true,
		EventTypes: []string{"vm.deleted"}})
	fake.PutWebhook(&types.Webhook{ID: "wh-3", URL: "https://3", Secret: "s", Active: true,
		EventTypes: []string{"vm.started"}})
	// Catch-all: should NOT be swept by the categorical selector. Tested explicitly below.
	fake.PutWebhook(&types.Webhook{ID: "wh-catchall", URL: "https://all", Secret: "s", Active: true})

	resp := postJSON(t, ts.URL+"/api/v1/webhooks/bulk_delete",
		map[string]any{"event_type": "vm.deleted"})
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var got bulkDeleteWebhooksResponse
	decodeJSON(t, resp, &got)
	if len(got.Results) != 2 {
		t.Fatalf("results = %d, want 2 (wh-1 and wh-2 only)", len(got.Results))
	}
	gotIDs := map[string]bool{}
	for _, r := range got.Results {
		if !r.Success {
			t.Fatalf("id %s expected success, got %+v", r.ID, r)
		}
		gotIDs[r.ID] = true
	}
	if !gotIDs["wh-1"] || !gotIDs["wh-2"] {
		t.Fatalf("expected wh-1 and wh-2 deleted, got %v", gotIDs)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ok := fake.hooks["wh-3"]; !ok {
		t.Fatalf("wh-3 (vm.started only) should not have been deleted")
	}
	if _, ok := fake.hooks["wh-catchall"]; !ok {
		t.Fatalf("wh-catchall (empty event_types) should not have been swept by event_type selector")
	}
}

func TestBulkDeleteWebhooks_PartialFailure(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-real", URL: "https://x", Secret: "s", Active: true})

	resp := postJSON(t, ts.URL+"/api/v1/webhooks/bulk_delete",
		map[string]any{"ids": []string{"wh-real", "wh-missing"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got bulkDeleteWebhooksResponse
	decodeJSON(t, resp, &got)
	if len(got.Results) != 2 {
		t.Fatalf("results = %v", got.Results)
	}
	results := map[string]bulkDeleteWebhookResult{}
	for _, r := range got.Results {
		results[r.ID] = r
	}
	if !results["wh-real"].Success {
		t.Fatalf("wh-real should have succeeded: %+v", results["wh-real"])
	}
	if results["wh-missing"].Success {
		t.Fatalf("wh-missing should have failed")
	}
	if results["wh-missing"].Code != "resource_not_found" {
		t.Fatalf("wh-missing.code = %q, want resource_not_found", results["wh-missing"].Code)
	}
}

func TestBulkDeleteWebhooks_EmptyRequestRejected(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()
	resp := postJSON(t, ts.URL+"/api/v1/webhooks/bulk_delete", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestBulkDeleteWebhooks_BothIDsAndEventTypeRejected(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()
	resp := postJSON(t, ts.URL+"/api/v1/webhooks/bulk_delete",
		map[string]any{"ids": []string{"wh-1"}, "event_type": "vm.deleted"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestBulkDeleteWebhooks_EventTypeNoMatchEmptyResponse(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	fake.PutWebhook(&types.Webhook{ID: "wh-1", URL: "https://x", Secret: "s", Active: true,
		EventTypes: []string{"vm.started"}})
	resp := postJSON(t, ts.URL+"/api/v1/webhooks/bulk_delete",
		map[string]any{"event_type": "vm.deleted"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got bulkDeleteWebhooksResponse
	decodeJSON(t, resp, &got)
	if len(got.Results) != 0 {
		t.Fatalf("results = %v, want empty (no event_type match)", got.Results)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, ok := fake.hooks["wh-1"]; !ok {
		t.Fatalf("wh-1 should not have been deleted")
	}
}

func TestBulkDeleteWebhooks_BadJSON(t *testing.T) {
	ts, _, _, cleanup := webhookTestServer(t)
	defer cleanup()
	resp, err := http.Post(ts.URL+"/api/v1/webhooks/bulk_delete", "application/json",
		bytes.NewBufferString("not-json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestBulkDeleteWebhooks_DisabledWhenStoreUnset(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer s.Close()

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath

	apiServer := NewServerWithConfig(vm.NewMockManager(),
		storage.NewManager(cfg, s), network.NewPortForwarder(s), s, cfg, nil)
	ts := httptest.NewServer(apiServer)
	defer ts.Close()

	resp := postJSON(t, ts.URL+"/api/v1/webhooks/bulk_delete",
		map[string]any{"ids": []string{"wh-1"}})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// --- Pagination on GET /api/v1/webhooks (roadmap 5.4.19) ---

// listWebhooksRaw returns both the decoded body and the raw response so tests
// can assert on the X-Total-Count header alongside the payload.
func listWebhooksRaw(t *testing.T, baseURL, rawQuery string) ([]*types.Webhook, *http.Response) {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/v1/webhooks?" + rawQuery)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := readAllBody(resp)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	return hooks, resp
}

func TestListWebhooks_Pagination_PerPagePage(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks, resp := listWebhooksRaw(t, ts.URL, "sort=id&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Errorf("page 1 X-Total-Count = %q, want 3", got)
	}
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, []string{"wh-1", "wh-2"}) {
		t.Fatalf("page 1 = %v, want [wh-1 wh-2]", got)
	}

	hooks2, resp2 := listWebhooksRaw(t, ts.URL, "sort=id&per_page=2&page=2")
	if got := resp2.Header.Get("X-Total-Count"); got != "3" {
		t.Errorf("page 2 X-Total-Count = %q, want 3", got)
	}
	if got := webhookIDsInOrder(hooks2); !equalStringSlice(got, []string{"wh-3"}) {
		t.Fatalf("page 2 = %v, want [wh-3]", got)
	}
}

func TestListWebhooks_Pagination_PageBeyondEndReturnsEmpty(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks, resp := listWebhooksRaw(t, ts.URL, "per_page=2&page=99")
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Errorf("X-Total-Count = %q, want 3", got)
	}
	if len(hooks) != 0 {
		t.Errorf("hooks = %v, want empty slice for out-of-range page", hooks)
	}
}

func TestListWebhooks_Pagination_NoParamsReturnsAll(t *testing.T) {
	// Without pagination params, ListWebhooks returns the full filtered set —
	// preserves the existing zero-perPage contract from parsePagination.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks, resp := listWebhooksRaw(t, ts.URL, "")
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Errorf("X-Total-Count = %q, want 3", got)
	}
	if len(hooks) != 3 {
		t.Errorf("len = %d, want 3 (full set)", len(hooks))
	}
}

func TestListWebhooks_Pagination_TotalCountReflectsFilter(t *testing.T) {
	// X-Total-Count must reflect the post-filter / pre-pagination count so
	// the GUI can paginate over the filtered population. Mirrors the
	// contract documented for VMs / images / templates / events.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSearchableWebhooks(t, fake)

	_, resp := listWebhooksRaw(t, ts.URL, "search=audit&per_page=10&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1 (only wh-audit matches)", got)
	}
}

func TestListWebhooks_Pagination_LimitAlias(t *testing.T) {
	// parsePagination accepts `limit` as a synonym for `per_page`.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks, _ := listWebhooksRaw(t, ts.URL, "sort=id&limit=1")
	if got := webhookIDsInOrder(hooks); !equalStringSlice(got, []string{"wh-1"}) {
		t.Fatalf("limit=1 = %v, want [wh-1]", got)
	}
}

func TestListWebhooks_Pagination_CapsAtMaxPerPage(t *testing.T) {
	// per_page > maxPerPage (2000) is clamped, but for tests we just verify
	// a huge per_page returns the full set without erroring.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedSortableWebhooks(t, fake)

	hooks, resp := listWebhooksRaw(t, ts.URL, "per_page=10000")
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Errorf("X-Total-Count = %q, want 3", got)
	}
	if len(hooks) != 3 {
		t.Errorf("len = %d, want 3", len(hooks))
	}
}

// seedDeliveryStatusWebhooks seeds one webhook in each delivery-status bucket
// (never, healthy, failing) so the 5.4.35 enum filter tests can assert which
// bucket each value selects.
func seedDeliveryStatusWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	// never — no LastDeliveryAt
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-never", URL: "https://never.example.com", Secret: "k", Active: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// healthy — 2xx + no LastError
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-healthy", URL: "https://healthy.example.com", Secret: "k", Active: true, LastDeliveryAt: now, LastStatus: 200}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// failing transport — LastStatus == 0 with a non-empty error
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-fail-tx", URL: "https://fail-tx.example.com", Secret: "k", Active: true, LastDeliveryAt: now, LastStatus: 0, LastError: "connection refused"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// failing 5xx
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-fail-5xx", URL: "https://fail-5xx.example.com", Secret: "k", Active: true, LastDeliveryAt: now, LastStatus: 503, LastError: "service unavailable"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestListWebhooks_FilterByDeliveryStatus_Never(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDeliveryStatusWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "delivery_status=never")
	if len(hooks) != 1 || hooks[0].ID != "wh-never" {
		t.Fatalf("expected only wh-never, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByDeliveryStatus_Healthy(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDeliveryStatusWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "delivery_status=healthy")
	if len(hooks) != 1 || hooks[0].ID != "wh-healthy" {
		t.Fatalf("expected only wh-healthy, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByDeliveryStatus_Failing(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDeliveryStatusWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "delivery_status=failing")
	if len(hooks) != 2 {
		t.Fatalf("expected 2 failing webhooks (transport + 5xx), got %d: %+v", len(hooks), hooks)
	}
	for _, h := range hooks {
		if h.ID != "wh-fail-tx" && h.ID != "wh-fail-5xx" {
			t.Fatalf("unexpected webhook in failing set: %s", h.ID)
		}
	}
}

func TestListWebhooks_FilterByDeliveryStatus_CaseInsensitive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDeliveryStatusWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "delivery_status=HEALTHY")
	if len(hooks) != 1 || hooks[0].ID != "wh-healthy" {
		t.Fatalf("expected case-insensitive match, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByDeliveryStatus_TrimsWhitespace(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDeliveryStatusWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "delivery_status=%20%20never%20%20")
	if len(hooks) != 1 || hooks[0].ID != "wh-never" {
		t.Fatalf("expected whitespace-trimmed match, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByDeliveryStatus_EmptyIsNoOp(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDeliveryStatusWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "delivery_status=")
	if len(hooks) != 4 {
		t.Fatalf("expected all 4 webhooks (empty filter no-op), got %d", len(hooks))
	}
}

func TestListWebhooks_FilterByDeliveryStatus_Invalid(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedDeliveryStatusWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?delivery_status=unknown")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_delivery_status")
}

func TestListWebhooks_FilterByDeliveryStatus_ComposesWithTag(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-prod-fail", URL: "https://a", Secret: "k", Active: true, LastDeliveryAt: now, LastStatus: 500, LastError: "internal", Tags: []string{"production"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-stg-fail", URL: "https://b", Secret: "k", Active: true, LastDeliveryAt: now, LastStatus: 500, LastError: "internal", Tags: []string{"staging"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-prod-ok", URL: "https://c", Secret: "k", Active: true, LastDeliveryAt: now, LastStatus: 200, Tags: []string{"production"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?delivery_status=failing&tag=production")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 1 || hooks[0].ID != "wh-prod-fail" {
		t.Fatalf("expected only wh-prod-fail, got %+v", hooks)
	}
}

// seedActiveWebhooks seeds two active + one inactive webhook for the 5.4.37
// active-filter tests.
func seedActiveWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-on-1", URL: "https://on1.example.com", Secret: "k", Active: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-on-2", URL: "https://on2.example.com", Secret: "k", Active: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-off", URL: "https://off.example.com", Secret: "k", Active: false}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestListWebhooks_FilterByActive_True(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "active=true")
	if len(hooks) != 2 {
		t.Fatalf("expected 2 active webhooks, got %d: %+v", len(hooks), hooks)
	}
	for _, h := range hooks {
		if !h.Active {
			t.Fatalf("inactive webhook %s leaked into active=true result", h.ID)
		}
	}
}

func TestListWebhooks_FilterByActive_False(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "active=false")
	if len(hooks) != 1 || hooks[0].ID != "wh-off" {
		t.Fatalf("expected only wh-off, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByActive_NumericAliases(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveWebhooks(t, fake)

	if hooks := listWebhooksWithQuery(t, ts.URL, "active=1"); len(hooks) != 2 {
		t.Fatalf("active=1 should match the 2 active webhooks, got %d", len(hooks))
	}
	if hooks := listWebhooksWithQuery(t, ts.URL, "active=0"); len(hooks) != 1 || hooks[0].ID != "wh-off" {
		t.Fatalf("active=0 should match only wh-off, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByActive_CaseInsensitive(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "active=FALSE")
	if len(hooks) != 1 || hooks[0].ID != "wh-off" {
		t.Fatalf("expected case-insensitive match, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByActive_TrimsWhitespace(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "active=%20true%20")
	if len(hooks) != 2 {
		t.Fatalf("expected whitespace-trimmed match (2 active), got %d", len(hooks))
	}
}

func TestListWebhooks_FilterByActive_EmptyIsNoOp(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "active=")
	if len(hooks) != 3 {
		t.Fatalf("expected all 3 webhooks (empty filter no-op), got %d", len(hooks))
	}
}

func TestListWebhooks_FilterByActive_Invalid(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedActiveWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?active=maybe")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_active")
}

// =====================================================
// url_prefix filter tests (5.4.83)
//
// Case-insensitive HasPrefix(wh.URL, value) filter. Whitespace-trimmed;
// empty disables. Mirrors the case-insensitive URL haystack in
// WebhookMatchesSearch — operators paste mixed-case URLs verbatim from
// the address bar and expect a match per RFC 3986 (scheme/host are
// case-insensitive).
// =====================================================

// seedURLPrefixWebhooks seeds four receivers spanning two distinct
// receiver hosts so the prefix filter has something to narrow.
func seedURLPrefixWebhooks(t *testing.T, fake *fakeWebhookStore) {
	t.Helper()
	hooks := []*types.Webhook{
		{ID: "wh-slack-1", URL: "https://hooks.slack.com/services/T01/B01/abc", Secret: "k", Active: true},
		{ID: "wh-slack-2", URL: "https://hooks.slack.com/services/T02/B02/def", Secret: "k", Active: true},
		{ID: "wh-pd-1", URL: "https://events.pagerduty.com/v2/enqueue", Secret: "k", Active: true},
		{ID: "wh-test-1", URL: "http://test.internal/hook", Secret: "k", Active: false},
	}
	for _, h := range hooks {
		if err := fake.PutWebhook(h); err != nil {
			t.Fatalf("seed %s: %v", h.ID, err)
		}
	}
}

func TestListWebhooks_FilterByURLPrefix_Match(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedURLPrefixWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "url_prefix=https://hooks.slack.com/")
	if len(hooks) != 2 {
		t.Fatalf("expected 2 Slack receivers, got %d: %+v", len(hooks), hooks)
	}
	for _, h := range hooks {
		if !strings.HasPrefix(strings.ToLower(h.URL), "https://hooks.slack.com/") {
			t.Fatalf("non-matching URL leaked into result: %s", h.URL)
		}
	}
}

func TestListWebhooks_FilterByURLPrefix_CaseInsensitive(t *testing.T) {
	// Mirrors the case-insensitive URL haystack in WebhookMatchesSearch and
	// the RFC 3986 contract that scheme + host are case-insensitive. An
	// operator pasting `HTTPS://HOOKS.SLACK.COM/` from the address bar must
	// match the two `https://hooks.slack.com/...` receivers seeded above.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedURLPrefixWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "url_prefix=HTTPS://HOOKS.SLACK.COM/")
	if len(hooks) != 2 {
		t.Fatalf("expected case-insensitive match (2 Slack receivers), got %d: %+v", len(hooks), hooks)
	}
}

func TestListWebhooks_FilterByURLPrefix_TrimsWhitespace(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedURLPrefixWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "url_prefix=%20https://events.pagerduty.com%20")
	if len(hooks) != 1 || hooks[0].ID != "wh-pd-1" {
		t.Fatalf("expected whitespace-trimmed match (wh-pd-1), got %+v", hooks)
	}
}

func TestListWebhooks_FilterByURLPrefix_EmptyIsNoOp(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedURLPrefixWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "url_prefix=")
	if len(hooks) != 4 {
		t.Fatalf("expected all 4 webhooks (empty filter no-op), got %d", len(hooks))
	}
}

func TestListWebhooks_FilterByURLPrefix_NoMatch(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedURLPrefixWebhooks(t, fake)

	hooks := listWebhooksWithQuery(t, ts.URL, "url_prefix=https://unknown.example.com/")
	if len(hooks) != 0 {
		t.Fatalf("expected no matches, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByURLPrefix_SubstringExcluded(t *testing.T) {
	// The filter is HasPrefix, not Contains. A receiver path appearing as a
	// substring deeper in the URL must NOT match — that's what `?search=` is
	// for. Closes the operator query that `?search=hooks.slack.com` cannot
	// answer cleanly because a description mentioning Slack also matches.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-relay", URL: "https://relay.example.com/forward?to=hooks.slack.com", Secret: "k", Active: true}); err != nil {
		t.Fatalf("seed wh-relay: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-slack", URL: "https://hooks.slack.com/services/T1/B1/x", Secret: "k", Active: true}); err != nil {
		t.Fatalf("seed wh-slack: %v", err)
	}

	hooks := listWebhooksWithQuery(t, ts.URL, "url_prefix=https://hooks.slack.com/")
	if len(hooks) != 1 || hooks[0].ID != "wh-slack" {
		t.Fatalf("expected only wh-slack (substring on relay must not match), got %+v", hooks)
	}
}

func TestListWebhooks_FilterByURLPrefix_ComposesWithActive(t *testing.T) {
	// `?url_prefix=` is applied right after `?active=`. The two filters
	// compose additively — `?url_prefix=http://test.internal/&active=true`
	// must drop the inactive `wh-test-1`. Mirrors the well-tested
	// `?active=true&tag=production` pattern from the active filter (5.4.37).
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedURLPrefixWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?url_prefix=http://test.internal/&active=true")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("X-Total-Count = %q, want 0 (active=true filter drops wh-test-1)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 0 {
		t.Fatalf("expected 0 webhooks (active filter drops the only test.internal receiver), got %+v", hooks)
	}
}

func TestListWebhooks_FilterByURLPrefix_ComposesWithSearch(t *testing.T) {
	// `?url_prefix=` runs before `?search=` so the post-filter `X-Total-Count`
	// reflects the intersection. Both Slack receivers above match the prefix;
	// the search needle `B02` then narrows to wh-slack-2.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedURLPrefixWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?url_prefix=https://hooks.slack.com/&search=B02")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (intersection)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 1 || hooks[0].ID != "wh-slack-2" {
		t.Fatalf("expected only wh-slack-2, got %+v", hooks)
	}
}

func TestListWebhooks_FilterByURLPrefix_TotalCountReflectsFiltered(t *testing.T) {
	// X-Total-Count must reflect the post-filter / pre-pagination population.
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	seedURLPrefixWebhooks(t, fake)

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?url_prefix=https://hooks.slack.com/&per_page=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (post-filter; 2 Slack receivers)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 1 {
		t.Fatalf("expected single page of 1, got %d", len(hooks))
	}
}

func TestListWebhooks_FilterByActive_ComposesWithTag(t *testing.T) {
	ts, _, fake, cleanup := webhookTestServer(t)
	defer cleanup()
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-prod-on", URL: "https://a", Secret: "k", Active: true, Tags: []string{"production"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-prod-off", URL: "https://b", Secret: "k", Active: false, Tags: []string{"production"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fake.PutWebhook(&types.Webhook{ID: "wh-stg-on", URL: "https://c", Secret: "k", Active: true, Tags: []string{"staging"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/webhooks?active=true&tag=production")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var hooks []*types.Webhook
	decodeJSON(t, resp, &hooks)
	if len(hooks) != 1 || hooks[0].ID != "wh-prod-on" {
		t.Fatalf("expected only wh-prod-on, got %+v", hooks)
	}
}
