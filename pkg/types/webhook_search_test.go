package types

import "testing"

func TestWebhookMatchesSearch_EmptyQueryMatchesAll(t *testing.T) {
	wh := &Webhook{URL: "https://hooks.example.com/audit"}
	if !WebhookMatchesSearch(wh, "") {
		t.Fatalf("empty query should match every webhook")
	}
}

func TestWebhookMatchesSearch_NilWebhookNeverMatches(t *testing.T) {
	if WebhookMatchesSearch(nil, "anything") {
		t.Fatalf("nil webhook must not match a non-empty query")
	}
}

func TestWebhookMatchesSearch_URLSubstring(t *testing.T) {
	wh := &Webhook{URL: "https://hooks.example.com/audit"}
	if !WebhookMatchesSearch(wh, "audit") {
		t.Fatalf("expected substring match in url")
	}
	if WebhookMatchesSearch(wh, "metrics") {
		t.Fatalf("did not expect 'metrics' to match url %q", wh.URL)
	}
}

func TestWebhookMatchesSearch_EventTypeSubstring(t *testing.T) {
	wh := &Webhook{
		URL:        "https://hooks.example.com/audit",
		EventTypes: []string{"vm.started", "vm.stopped"},
	}
	if !WebhookMatchesSearch(wh, "started") {
		t.Fatalf("expected event-type substring to match")
	}
}

func TestWebhookMatchesSearch_CallerLowercasesQuery(t *testing.T) {
	// WebhookMatchesSearch lowercases the haystack but trusts the caller to
	// have lowercased the needle (see the API/CLI handlers). Locking that
	// contract in here so a future caller doesn't accidentally regress it.
	wh := &Webhook{URL: "https://Hooks.EXAMPLE.com/Audit"}
	if WebhookMatchesSearch(wh, "AUDIT") {
		t.Fatalf("expected uppercase needle to miss; caller is responsible for lowercasing")
	}
	if !WebhookMatchesSearch(wh, "audit") {
		t.Fatalf("expected lowercase needle to hit case-insensitive haystack")
	}
}

func TestWebhookMatchesSearch_NoMatchReturnsFalse(t *testing.T) {
	wh := &Webhook{
		URL:        "https://hooks.example.com/audit",
		EventTypes: []string{"vm.started"},
	}
	if WebhookMatchesSearch(wh, "needle-not-anywhere") {
		t.Fatalf("expected no-match to return false")
	}
}

func TestWebhookMatchesSearch_SecretNotInHaystack(t *testing.T) {
	// The secret is redacted on every API read, but lock the contract in
	// at the predicate layer too — even if a future code path accidentally
	// hands a populated Secret to the matcher, the search filter must not
	// leak substrings of it.
	wh := &Webhook{
		URL:    "https://hooks.example.com/audit",
		Secret: "super-secret-token",
	}
	if WebhookMatchesSearch(wh, "super-secret-token") {
		t.Fatalf("did not expect secret to match")
	}
	if WebhookMatchesSearch(wh, "secret") {
		t.Fatalf("did not expect secret substring to match")
	}
}

func TestWebhookMatchesSearch_IDNotInHaystack(t *testing.T) {
	// IDs are opaque `wh-<unix-nano>` strings; matching them produces
	// noisy numeric false positives. Lock the contract in.
	wh := &Webhook{ID: "wh-1741234567890123", URL: "https://example.com/x"}
	if WebhookMatchesSearch(wh, "1741234567890123") {
		t.Fatalf("did not expect ID digits to match")
	}
	if WebhookMatchesSearch(wh, "wh-") {
		t.Fatalf("did not expect 'wh-' prefix to match")
	}
}

func TestWebhookMatchesSearch_LastErrorNotInHaystack(t *testing.T) {
	// LastError is operator noise that changes between retries; the
	// status badge / `last_status` is the right place to surface it,
	// not the search index. Lock the contract in.
	wh := &Webhook{
		URL:       "https://example.com/x",
		LastError: "dial tcp 198.51.100.7: connection refused",
	}
	if WebhookMatchesSearch(wh, "connection refused") {
		t.Fatalf("did not expect last_error to match")
	}
	if WebhookMatchesSearch(wh, "198.51.100.7") {
		t.Fatalf("did not expect last_error IP to match")
	}
}

func TestWebhookMatchesSearch_EmptyEventTypesHandled(t *testing.T) {
	wh := &Webhook{URL: "https://example.com/x", EventTypes: nil}
	if WebhookMatchesSearch(wh, "needle") {
		t.Fatalf("expected no match against empty event types")
	}
}

func TestWebhookMatchesSearch_DescriptionSubstring(t *testing.T) {
	wh := &Webhook{
		URL:         "https://example.com/x",
		Description: "Slack notifier for production VM crashes",
	}
	if !WebhookMatchesSearch(wh, "slack") {
		t.Fatalf("expected description substring 'slack' to hit")
	}
	if !WebhookMatchesSearch(wh, "production") {
		t.Fatalf("expected description substring 'production' to hit")
	}
}

func TestWebhookMatchesSearch_DescriptionCaseInsensitive(t *testing.T) {
	wh := &Webhook{
		URL:         "https://example.com/x",
		Description: "PagerDuty Escalation",
	}
	if !WebhookMatchesSearch(wh, "pagerduty") {
		t.Fatalf("expected lowercase needle to match mixed-case description")
	}
}

func TestWebhookMatchesSearch_EmptyDescriptionDoesNotMatchEmptyQuery(t *testing.T) {
	// Empty description is the common case; the haystack scan must not
	// short-circuit to true just because the description happens to be "".
	// A non-empty needle against an empty description should miss unless
	// the URL or event_types carry the needle.
	wh := &Webhook{URL: "https://example.com/x", Description: ""}
	if WebhookMatchesSearch(wh, "anything") {
		t.Fatalf("expected empty description not to match arbitrary needle")
	}
}
