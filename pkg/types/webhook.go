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
