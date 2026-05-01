package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// WebhookStore is the persistence interface used by the webhook REST handlers.
// Defined here so the api package can depend on the smaller surface without
// pulling the concrete *store.Store everywhere.
type WebhookStore interface {
	PutWebhook(*types.Webhook) error
	GetWebhook(id string) (*types.Webhook, error)
	ListWebhooks() ([]*types.Webhook, error)
	DeleteWebhook(id string) error
}

// WebhookRegistrar lets the API server inform the runtime webhook manager
// about CRUD changes so workers can be started/stopped without a daemon
// restart.
type WebhookRegistrar interface {
	Register(*types.Webhook)
	Unregister(id string)
}

// SetWebhookSubsystem wires the persistence and runtime manager into the
// server.  Either may be nil; with no store the endpoints return 503.
func (s *Server) SetWebhookSubsystem(store WebhookStore, mgr WebhookRegistrar) {
	s.webhookStore = store
	s.webhookManager = mgr
}

func (s *Server) requireWebhookStore(w http.ResponseWriter) bool {
	if s.webhookStore == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "webhooks_disabled", "webhook subsystem not configured")
		return false
	}
	return true
}

// CreateWebhook handles POST /api/v1/webhooks.
func (s *Server) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireWebhookStore(w) {
		return
	}
	var req types.WebhookCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	url := strings.TrimSpace(req.URL)
	secret := strings.TrimSpace(req.Secret)
	if url == "" {
		writeErrorCode(w, http.StatusBadRequest, "invalid_url", "url is required")
		return
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		writeErrorCode(w, http.StatusBadRequest, "invalid_url", "url must use http or https scheme")
		return
	}
	if secret == "" {
		writeErrorCode(w, http.StatusBadRequest, "missing_secret", "secret is required for HMAC signing")
		return
	}

	now := time.Now().UTC()
	wh := &types.Webhook{
		ID:         fmt.Sprintf("wh-%d", now.UnixNano()),
		URL:        url,
		Secret:     secret,
		EventTypes: normalizeEventTypes(req.EventTypes),
		Active:     true,
		CreatedAt:  now,
	}

	if err := s.webhookStore.PutWebhook(wh); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to persist webhook")
		return
	}
	if s.webhookManager != nil {
		s.webhookManager.Register(wh)
	}

	writeJSON(w, http.StatusCreated, redactWebhook(wh))
}

// ListWebhooks handles GET /api/v1/webhooks.
func (s *Server) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireWebhookStore(w) {
		return
	}
	hooks, err := s.webhookStore.ListWebhooks()
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to list webhooks")
		return
	}
	if hooks == nil {
		hooks = []*types.Webhook{}
	}
	sort.Slice(hooks, func(i, j int) bool {
		return hooks[i].CreatedAt.Before(hooks[j].CreatedAt)
	})
	out := make([]*types.Webhook, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, redactWebhook(h))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetWebhook handles GET /api/v1/webhooks/{id}.
func (s *Server) GetWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireWebhookStore(w) {
		return
	}
	id := chi.URLParam(r, "webhookID")
	wh, err := s.webhookStore.GetWebhook(id)
	if err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "webhook not found")
		return
	}
	writeJSON(w, http.StatusOK, redactWebhook(wh))
}

// DeleteWebhook handles DELETE /api/v1/webhooks/{id}.
func (s *Server) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireWebhookStore(w) {
		return
	}
	id := chi.URLParam(r, "webhookID")
	if _, err := s.webhookStore.GetWebhook(id); err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "webhook not found")
		return
	}
	if err := s.webhookStore.DeleteWebhook(id); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to delete webhook")
		return
	}
	if s.webhookManager != nil {
		s.webhookManager.Unregister(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// redactWebhook returns a shallow copy with the Secret cleared.  Secrets are
// only stored server-side; outbound responses must never expose them.
func redactWebhook(wh *types.Webhook) *types.Webhook {
	if wh == nil {
		return nil
	}
	clone := *wh
	clone.Secret = ""
	return &clone
}

func normalizeEventTypes(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
