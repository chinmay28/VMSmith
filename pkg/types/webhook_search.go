package types

import "strings"

// WebhookMatchesSearch reports whether the given webhook matches the
// (already normalised, lowercase) search query. The match is a
// case-insensitive substring scan across the webhook's URL and event-type
// filters — the fields an operator types into a "find this webhook" box.
// An empty query matches everything; callers should short-circuit before
// calling.
//
// Intentionally excluded from the haystack:
//
//   - Secret — never reaches the API anyway (redacted on every read).
//     Including it in the search would also be a small information leak
//     (a needle that hits would confirm a substring of the secret).
//   - ID — opaque `wh-<unix-nano>` string; nobody types those from memory
//     and including them generates noisy numeric false positives.
//   - LastError — error text is operator-noise that changes between
//     retries; "what's broken?" is what `last_status` and the per-row
//     status badge are for, not a free-text search predicate.
//
// Mirrors the contract of VMMatchesSearch (2.2.13),
// TemplateMatchesSearch (5.4.12), and EventMatchesSearch (4.2.20):
// the haystack is lowercased on each call, the needle is the caller's
// responsibility to lowercase + trim once.
func WebhookMatchesSearch(wh *Webhook, query string) bool {
	if wh == nil {
		return false
	}
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(wh.URL), query) {
		return true
	}
	for _, et := range wh.EventTypes {
		if strings.Contains(strings.ToLower(et), query) {
			return true
		}
	}
	return false
}
