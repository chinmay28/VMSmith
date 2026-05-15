package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/webhooks"
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

// WebhookTester runs a single synchronous delivery attempt against a registered
// webhook to back the "send test event" UI affordance.
type WebhookTester interface {
	TestDeliver(ctx context.Context, webhookID string) (*types.WebhookTestResult, error)
}

// SetWebhookSubsystem wires the persistence and runtime manager into the
// server.  Either may be nil; with no store the endpoints return 503.
//
// The runtime manager is accepted as a `WebhookRegistrar`.  When it also
// satisfies `WebhookTester` the POST /webhooks/{id}/test endpoint becomes
// available; otherwise it returns 503 webhook_test_unavailable.
func (s *Server) SetWebhookSubsystem(store WebhookStore, mgr WebhookRegistrar) {
	s.webhookStore = store
	s.webhookManager = mgr
	if tester, ok := mgr.(WebhookTester); ok {
		s.webhookTester = tester
	} else {
		s.webhookTester = nil
	}
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
	description := strings.TrimSpace(req.Description)
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
	if err := validateWebhookDescription(description); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	now := time.Now().UTC()
	wh := &types.Webhook{
		ID:          fmt.Sprintf("wh-%d", now.UnixNano()),
		URL:         url,
		Secret:      secret,
		EventTypes:  normalizeEventTypes(req.EventTypes),
		Description: description,
		Active:      true,
		CreatedAt:   now,
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
//
// Optional query params:
//   - search=<value>  case-insensitive substring filter applied to `url`,
//     `description`, and `event_types`. Trimmed + lowercased once before
//     delegating to the shared predicate. Mirrors the symmetric search
//     surface across VMs (2.2.13), images (5.4.9), snapshots (5.4.10),
//     port forwards (5.4.11), templates (5.4.12), events (4.2.20), and
//     logs (5.4.13). Secret, ID, and last_error are intentionally excluded
//     from the haystack — see pkg/types/webhook_search.go.
//   - sort=<field>   whitelisted to id|url|created_at|last_delivery_at.
//     Default `id`. Unknown values return 400 `invalid_sort`.
//   - order=<asc|desc>  default `asc`. Unknown values return 400 `invalid_order`.
//
// All comparators tiebreak on `id` so repeated requests return a deterministic
// order. `url` matches case-insensitively. `last_delivery_at` sorts
// never-delivered webhooks (zero timestamp) at the tail of the ascending list
// and the head of the descending list.
func (s *Server) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireWebhookStore(w) {
		return
	}
	sortField, order, err := parseWebhookSort(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
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

	searchFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))

	out := make([]*types.Webhook, 0, len(hooks))
	for _, h := range hooks {
		if searchFilter != "" && !types.WebhookMatchesSearch(h, searchFilter) {
			continue
		}
		out = append(out, redactWebhook(h))
	}
	types.SortWebhooks(out, sortField, order)
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

// TestWebhook handles POST /api/v1/webhooks/{id}/test.  It synthesises a
// `system.webhook_test` event, delivers it once (no retries), and returns the
// outcome so the UI can surface a quick success/failure verdict.
func (s *Server) TestWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireWebhookStore(w) {
		return
	}
	if s.webhookTester == nil {
		writeErrorCode(w, http.StatusServiceUnavailable, "webhook_test_unavailable",
			"webhook test deliveries are not enabled (no runtime manager configured)")
		return
	}
	id := chi.URLParam(r, "webhookID")
	result, err := s.webhookTester.TestDeliver(r.Context(), id)
	if err != nil {
		if errors.Is(err, webhooks.ErrWebhookNotFound) {
			writeErrorCode(w, http.StatusNotFound, "resource_not_found", "webhook not found")
			return
		}
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to deliver test event")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// UpdateWebhook handles PATCH /api/v1/webhooks/{id}.
//
// Pointer-typed fields on WebhookUpdateSpec express "no change" via JSON-key
// absence:
//   - url / secret: trimmed; empty rejected
//   - event_types: nil = no change; [] = clear filter list
//   - active: nil = no change
//   - description: nil = no change; "" = clear; trimmed; capped at 1024 chars
//
// Any successful change unregisters the in-memory worker and re-registers it
// with the new config so live deliveries pick up the change without a daemon
// restart.  Active=false leaves the worker stopped.
func (s *Server) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireWebhookStore(w) {
		return
	}
	id := chi.URLParam(r, "webhookID")
	current, err := s.webhookStore.GetWebhook(id)
	if err != nil {
		writeErrorCode(w, http.StatusNotFound, "resource_not_found", "webhook not found")
		return
	}

	var req types.WebhookUpdateSpec
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if req.URL == nil && req.Secret == nil && req.EventTypes == nil && req.Active == nil && req.Description == nil {
		writeErrorCode(w, http.StatusBadRequest, "noop_update", "no fields to update")
		return
	}

	updated := *current
	changed := false

	if req.URL != nil {
		nextURL := strings.TrimSpace(*req.URL)
		if nextURL == "" {
			writeErrorCode(w, http.StatusBadRequest, "invalid_url", "url is required")
			return
		}
		if !strings.HasPrefix(nextURL, "http://") && !strings.HasPrefix(nextURL, "https://") {
			writeErrorCode(w, http.StatusBadRequest, "invalid_url", "url must use http or https scheme")
			return
		}
		if nextURL != updated.URL {
			updated.URL = nextURL
			changed = true
		}
	}

	if req.Secret != nil {
		nextSecret := strings.TrimSpace(*req.Secret)
		if nextSecret == "" {
			writeErrorCode(w, http.StatusBadRequest, "missing_secret", "secret cannot be empty")
			return
		}
		if nextSecret != updated.Secret {
			updated.Secret = nextSecret
			changed = true
		}
	}

	if req.EventTypes != nil {
		next := normalizeEventTypes(*req.EventTypes)
		if !eventTypeSetsEqual(next, updated.EventTypes) {
			updated.EventTypes = next
			changed = true
		}
	}

	if req.Active != nil && *req.Active != updated.Active {
		updated.Active = *req.Active
		changed = true
	}

	if req.Description != nil {
		nextDesc := strings.TrimSpace(*req.Description)
		if err := validateWebhookDescription(nextDesc); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if nextDesc != updated.Description {
			updated.Description = nextDesc
			changed = true
		}
	}

	if !changed {
		writeJSON(w, http.StatusOK, redactWebhook(&updated))
		return
	}

	if err := s.webhookStore.PutWebhook(&updated); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to persist webhook")
		return
	}
	if s.webhookManager != nil {
		// Tear down the in-memory worker (idempotent) and re-register so the
		// next delivery uses the new URL/secret/filters/Active state.  When
		// Active=false the register call is a no-op, leaving no worker — which
		// is what we want.
		s.webhookManager.Unregister(updated.ID)
		s.webhookManager.Register(&updated)
	}

	writeJSON(w, http.StatusOK, redactWebhook(&updated))
}

// eventTypeSetsEqual is the no-op detector for the EventTypes patch path.
// Filter lists are semantically sets — `["a","b"]` and `["b","a"]` glob-match
// the same events — so a reorder-only PATCH should not bounce the worker.
// Both inputs are already deduplicated by `normalizeEventTypes`, so a simple
// counting compare suffices.
func eventTypeSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		if seen[s] == 0 {
			return false
		}
		seen[s]--
	}
	return true
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

// bulkDeleteWebhooksRequest selects webhooks to delete in a single batch.
//
// Exactly one of IDs or EventType must be set. When EventType is set, every
// webhook whose `event_types` filter list contains that exact event type is
// targeted — the cheap way to retire a cohort of subscribers after deprecating
// an event ("retire every webhook still listening to vm.deleted"). Catch-all
// webhooks (empty `event_types`) are NOT swept by the categorical selector:
// they fire on every event, including the one being retired, but the operator
// intent for the bulk call is explicit-membership cleanup, not blanket
// removal. Use `ids` to delete catch-alls. Mirrors the image / template
// bulk-delete shape so the API surface is predictable.
type bulkDeleteWebhooksRequest struct {
	IDs       []string `json:"ids,omitempty"`
	EventType string   `json:"event_type,omitempty"`
}

type bulkDeleteWebhookResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type bulkDeleteWebhooksResponse struct {
	Results []bulkDeleteWebhookResult `json:"results"`
}

// BulkDeleteWebhooks handles POST /api/v1/webhooks/bulk_delete.
//
// Accepts either an explicit list of webhook IDs ("ids") or an event-type
// selector ("event_type"). Returns a per-target result list so partial
// failures (one webhook missing, the rest succeeded) surface in a single
// response — mirroring the image / template bulk-delete shapes.
func (s *Server) BulkDeleteWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireWebhookStore(w) {
		return
	}
	var req bulkDeleteWebhooksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}

	eventType := strings.TrimSpace(req.EventType)
	cleanedIDs := make([]string, 0, len(req.IDs))
	for _, id := range req.IDs {
		if t := strings.TrimSpace(id); t != "" {
			cleanedIDs = append(cleanedIDs, t)
		}
	}

	if eventType == "" && len(cleanedIDs) == 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"exactly one of ids or event_type must be provided"))
		return
	}
	if eventType != "" && len(cleanedIDs) > 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"ids and event_type are mutually exclusive"))
		return
	}

	targets := cleanedIDs
	if eventType != "" {
		hooks, err := s.webhookStore.ListWebhooks()
		if err != nil {
			writeErrorCode(w, http.StatusInternalServerError, "internal_error", "failed to list webhooks")
			return
		}
		for _, wh := range hooks {
			if webhookExplicitlySubscribed(wh, eventType) {
				targets = append(targets, wh.ID)
			}
		}
	}

	results := make([]bulkDeleteWebhookResult, 0, len(targets))
	for _, id := range targets {
		if _, err := s.webhookStore.GetWebhook(id); err != nil {
			results = append(results, bulkDeleteWebhookResult{
				ID:      id,
				Success: false,
				Code:    "resource_not_found",
				Message: "webhook not found",
			})
			continue
		}
		if err := s.webhookStore.DeleteWebhook(id); err != nil {
			results = append(results, bulkDeleteWebhookResult{
				ID:      id,
				Success: false,
				Code:    "internal_error",
				Message: "failed to delete webhook",
			})
			continue
		}
		if s.webhookManager != nil {
			s.webhookManager.Unregister(id)
		}
		results = append(results, bulkDeleteWebhookResult{ID: id, Success: true})
	}

	writeJSON(w, http.StatusOK, bulkDeleteWebhooksResponse{Results: results})
}

// webhookExplicitlySubscribed reports whether wh's event_types filter list
// contains the given exact event type. A catch-all webhook (empty event_types)
// returns false — see bulkDeleteWebhooksRequest comment for the rationale.
func webhookExplicitlySubscribed(wh *types.Webhook, eventType string) bool {
	if wh == nil {
		return false
	}
	for _, t := range wh.EventTypes {
		if strings.TrimSpace(t) == eventType {
			return true
		}
	}
	return false
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
