package types

import (
	"testing"
	"time"
)

func TestWebhookDeliveryStatus_Nil(t *testing.T) {
	if got := WebhookDeliveryStatus(nil); got != WebhookDeliveryNever {
		t.Fatalf("nil receiver: got %q, want %q", got, WebhookDeliveryNever)
	}
}

func TestWebhookDeliveryStatus_Never_ZeroLastDeliveryAt(t *testing.T) {
	wh := &Webhook{ID: "wh-1", URL: "https://a", Secret: "k", Active: true}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryNever {
		t.Fatalf("zero LastDeliveryAt: got %q, want %q", got, WebhookDeliveryNever)
	}
}

func TestWebhookDeliveryStatus_Never_PrecedesLegacyStatus(t *testing.T) {
	// Even if some legacy fixture left LastStatus / LastError populated
	// without a LastDeliveryAt timestamp, the predicate classifies the
	// webhook as "never" so operators see it in the never-fired bucket.
	wh := &Webhook{
		ID: "wh-legacy", URL: "https://a", Secret: "k", Active: true,
		LastStatus: 200, LastError: "",
	}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryNever {
		t.Fatalf("zero LastDeliveryAt overrides legacy status: got %q, want %q", got, WebhookDeliveryNever)
	}
}

func TestWebhookDeliveryStatus_Healthy_200(t *testing.T) {
	wh := &Webhook{
		ID: "wh-h", URL: "https://a", Secret: "k", Active: true,
		LastDeliveryAt: time.Now(), LastStatus: 200,
	}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryHealthy {
		t.Fatalf("200 + no error: got %q, want %q", got, WebhookDeliveryHealthy)
	}
}

func TestWebhookDeliveryStatus_Healthy_204(t *testing.T) {
	wh := &Webhook{
		ID: "wh-h2", URL: "https://a", Secret: "k", Active: true,
		LastDeliveryAt: time.Now(), LastStatus: 204,
	}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryHealthy {
		t.Fatalf("204: got %q, want %q", got, WebhookDeliveryHealthy)
	}
}

func TestWebhookDeliveryStatus_Failing_TransportError(t *testing.T) {
	// LastStatus == 0 with a non-empty LastError is the canonical
	// transport-error / final-retry-exhausted shape.
	wh := &Webhook{
		ID: "wh-tx", URL: "https://a", Secret: "k", Active: true,
		LastDeliveryAt: time.Now(), LastStatus: 0, LastError: "connection refused",
	}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryFailing {
		t.Fatalf("transport error: got %q, want %q", got, WebhookDeliveryFailing)
	}
}

func TestWebhookDeliveryStatus_Failing_5xx(t *testing.T) {
	wh := &Webhook{
		ID: "wh-5", URL: "https://a", Secret: "k", Active: true,
		LastDeliveryAt: time.Now(), LastStatus: 503, LastError: "service unavailable",
	}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryFailing {
		t.Fatalf("5xx: got %q, want %q", got, WebhookDeliveryFailing)
	}
}

func TestWebhookDeliveryStatus_Failing_4xx(t *testing.T) {
	wh := &Webhook{
		ID: "wh-4", URL: "https://a", Secret: "k", Active: true,
		LastDeliveryAt: time.Now(), LastStatus: 401,
	}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryFailing {
		t.Fatalf("4xx: got %q, want %q", got, WebhookDeliveryFailing)
	}
}

func TestWebhookDeliveryStatus_Failing_3xx(t *testing.T) {
	// 3xx is not followed by the delivery client today, so a 3xx is
	// operator-visible as "delivered but bounced elsewhere" — that's a
	// failing state for the purpose of operator triage.
	wh := &Webhook{
		ID: "wh-3", URL: "https://a", Secret: "k", Active: true,
		LastDeliveryAt: time.Now(), LastStatus: 301,
	}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryFailing {
		t.Fatalf("3xx: got %q, want %q", got, WebhookDeliveryFailing)
	}
}

func TestWebhookDeliveryStatus_Failing_200WithError(t *testing.T) {
	// Belt-and-braces: a 2xx status with a non-empty LastError is still
	// failing — the delivery loop should not have left LastError set on
	// success, but the predicate is paranoid about partial fixtures.
	wh := &Webhook{
		ID: "wh-mixed", URL: "https://a", Secret: "k", Active: true,
		LastDeliveryAt: time.Now(), LastStatus: 200, LastError: "stale error",
	}
	if got := WebhookDeliveryStatus(wh); got != WebhookDeliveryFailing {
		t.Fatalf("2xx + LastError: got %q, want %q", got, WebhookDeliveryFailing)
	}
}

func TestIsValidWebhookDeliveryStatus(t *testing.T) {
	for _, ok := range []string{"never", "healthy", "failing"} {
		if !IsValidWebhookDeliveryStatus(ok) {
			t.Fatalf("%q: want valid", ok)
		}
	}
	for _, bad := range []string{"", "HEALTHY", " healthy", "unknown", "alive", "dead"} {
		if IsValidWebhookDeliveryStatus(bad) {
			t.Fatalf("%q: want invalid", bad)
		}
	}
}
