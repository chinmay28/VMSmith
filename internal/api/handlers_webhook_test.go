package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
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
	mu      sync.Mutex
	called  []string
	result  *types.WebhookTestResult
	err     error
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
