package types

import "time"

// Webhook represents an HTTP delivery target subscribed to event-bus traffic.
//
// Outbound payloads are signed with HMAC-SHA256 over the raw request body
// using Secret.  See docs/ARCHITECTURE.md "Event System" -> "Webhook contract"
// for the full wire shape (headers, signature scheme, retry schedule, SSRF
// rules).
type Webhook struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	Secret     string    `json:"secret,omitempty"`
	EventTypes []string  `json:"event_types,omitempty"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`

	// LastDeliveryAt is the time of the most recent attempted delivery
	// (success or final failure).  Zero if never attempted.
	LastDeliveryAt time.Time `json:"last_delivery_at,omitempty"`
	// LastStatus is the HTTP status of the most recent successful delivery,
	// or 0 if the most recent attempt failed all retries (in which case
	// LastError describes the failure).
	LastStatus int `json:"last_status,omitempty"`
	// LastError is the error message from the most recent delivery failure,
	// cleared on the next successful delivery.
	LastError string `json:"last_error,omitempty"`
}

// WebhookCreateRequest is the JSON body accepted by POST /api/v1/webhooks.
type WebhookCreateRequest struct {
	URL        string   `json:"url"`
	Secret     string   `json:"secret"`
	EventTypes []string `json:"event_types,omitempty"`
}

// WebhookTestResult is the response from POST /api/v1/webhooks/{id}/test.
// The endpoint synthesises a `system.webhook_test` event, delivers it once
// (no retries — the UI wants a quick answer), and reports the outcome so the
// operator can see whether the receiver is reachable, signature-validating,
// and returning a 2xx.
type WebhookTestResult struct {
	Success     bool      `json:"success"`
	StatusCode  int       `json:"status_code,omitempty"`
	Error       string    `json:"error,omitempty"`
	DurationMs  int64     `json:"duration_ms"`
	AttemptedAt time.Time `json:"attempted_at"`
	EventID     string    `json:"event_id,omitempty"`
}
