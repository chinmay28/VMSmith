package types

// Webhook delivery-status classification values exposed via the
// `?delivery_status=` filter on `GET /api/v1/webhooks` (5.4.35).
//
// The three values form a complete partition of every persisted webhook:
//
//   - WebhookDeliveryNever — the webhook has never had a delivery attempted
//     (LastDeliveryAt is the zero time).  Operator query: "show me webhooks
//     I registered but that haven't fired yet".
//   - WebhookDeliveryHealthy — the most recent delivery attempt succeeded
//     (LastStatus is in the 2xx range AND LastError is empty).  Operator
//     query: "show me webhooks I can trust".
//   - WebhookDeliveryFailing — the webhook has been attempted at least once
//     and the most recent attempt did NOT meet the healthy contract.  This
//     covers transport errors (LastStatus == 0 with non-empty LastError),
//     non-2xx responses (LastStatus < 200 || LastStatus >= 300), and any
//     other classification that operators would call "broken".  Operator
//     query: "show me webhooks I need to investigate".
const (
	WebhookDeliveryNever   = "never"
	WebhookDeliveryHealthy = "healthy"
	WebhookDeliveryFailing = "failing"
)

// WebhookDeliveryStatus classifies the webhook into exactly one of the
// constants above.  The classification is derived from the webhook's
// LastDeliveryAt / LastStatus / LastError fields — no other state is
// consulted, so the predicate is pure (matches today's in-memory snapshot
// of the webhook) and cheap (no I/O, suitable for a per-request filter
// loop alongside the existing tag / event_type / search predicates).
//
// A nil receiver returns WebhookDeliveryNever — defensive, mirrors the
// existing WebhookMatchesSearch nil-handling contract.
//
// Boundaries:
//
//   - LastDeliveryAt.IsZero() takes precedence — never-fired webhooks are
//     "never" even if some legacy fixture left LastStatus / LastError set.
//   - LastStatus == 200..299 + LastError == "" → "healthy" (the strict
//     contract).  Any deviation falls into "failing".  We deliberately
//     do NOT consider LastStatus in the 1xx or 3xx ranges as healthy:
//     1xx are informational (the delivery loop should not record them
//     as a terminal status), and 3xx redirects are not followed by the
//     delivery client today, so a 3xx is operator-visible as "delivered
//     but the receiver bounced you elsewhere" — that's a failing state
//     for the purpose of operator triage.
func WebhookDeliveryStatus(wh *Webhook) string {
	if wh == nil {
		return WebhookDeliveryNever
	}
	if wh.LastDeliveryAt.IsZero() {
		return WebhookDeliveryNever
	}
	if wh.LastError == "" && wh.LastStatus >= 200 && wh.LastStatus < 300 {
		return WebhookDeliveryHealthy
	}
	return WebhookDeliveryFailing
}

// IsValidWebhookDeliveryStatus reports whether the given string is one of
// the three classification values.  Used by API and CLI input validation
// to reject `?delivery_status=` values outside the enum.  Case-sensitive
// at the contract surface — callers should TrimSpace + ToLower before
// invoking, the same shape used for sort/order param validation across
// the codebase.
func IsValidWebhookDeliveryStatus(s string) bool {
	switch s {
	case WebhookDeliveryNever, WebhookDeliveryHealthy, WebhookDeliveryFailing:
		return true
	}
	return false
}
