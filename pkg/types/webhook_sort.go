package types

import (
	"sort"
	"strings"
)

// Webhook list sort fields. Whitelisted at the API/CLI surface so callers
// can't silently fall through to a different ordering.
const (
	WebhookSortID             = "id"
	WebhookSortURL            = "url"
	WebhookSortCreatedAt      = "created_at"
	WebhookSortLastDelivery   = "last_delivery_at"
	WebhookSortDeliveryStatus = "delivery_status"
)

// IsValidWebhookSort reports whether the given string is one of the
// whitelisted webhook sort axes. Single source of truth shared by the API
// and CLI parsers so the two surfaces never drift. Case-sensitive at the
// contract surface — callers should TrimSpace + ToLower before invoking.
func IsValidWebhookSort(s string) bool {
	switch s {
	case WebhookSortID,
		WebhookSortURL,
		WebhookSortCreatedAt,
		WebhookSortLastDelivery,
		WebhookSortDeliveryStatus:
		return true
	}
	return false
}

// SortWebhooks sorts the given webhooks in place by the requested field and
// order. All comparators tiebreak on `id` so pagination over repeated requests
// is deterministic.
//
// `url` matches case-insensitively. `last_delivery_at` puts webhooks that
// have never been delivered to (`LastDeliveryAt == time.Time{}`) at the tail
// of the ascending list (and the head of the descending list) — the same
// "zero values sort last in asc, first in desc" convention used by the
// existing image / template sorts for `created_at`.
//
// `delivery_status` (5.4.98) orders by the webhook's derived health
// classification via `WebhookDeliveryStatus(wh)`. The three categorical
// values fall in alphabetical order so ASC produces:
//
//	failing < healthy < never
//
// Operator triage: ASC surfaces broken receivers first (the "show me
// every receiver I need to investigate" cohort heads the list); DESC
// surfaces never-attempted receivers first (operators registered them
// but no event has matched yet). The classification is closed and total —
// every webhook resolves to exactly one of the three values — so no
// nil-trailing handling is required. The sort is the symmetric counterpart
// to the case-insensitive `?delivery_status=` exact-match filter (5.4.35)
// so the same operator query that narrows the list to one classification
// can now order across the whole list by classification.
//
// Unknown sort/order values silently fall back to id-asc; surface validation
// errors at the parsing layer (see `internal/api.parseWebhookSort`).
func SortWebhooks(hooks []*Webhook, sortField, order string) {
	desc := order == SortOrderDesc
	sort.SliceStable(hooks, func(i, j int) bool {
		ai, aj := hooks[i], hooks[j]
		var less bool
		switch sortField {
		case WebhookSortURL:
			ui, uj := strings.ToLower(ai.URL), strings.ToLower(aj.URL)
			if ui != uj {
				less = ui < uj
				break
			}
			less = ai.ID < aj.ID
		case WebhookSortCreatedAt:
			if !ai.CreatedAt.Equal(aj.CreatedAt) {
				less = ai.CreatedAt.Before(aj.CreatedAt)
				break
			}
			less = ai.ID < aj.ID
		case WebhookSortLastDelivery:
			zi, zj := ai.LastDeliveryAt.IsZero(), aj.LastDeliveryAt.IsZero()
			if zi != zj {
				// In ascending order zero (never-delivered) sorts last; the
				// `desc` flip below inverts this to put never-delivered first.
				less = !zi
				break
			}
			if !ai.LastDeliveryAt.Equal(aj.LastDeliveryAt) {
				less = ai.LastDeliveryAt.Before(aj.LastDeliveryAt)
				break
			}
			less = ai.ID < aj.ID
		case WebhookSortDeliveryStatus:
			// Categorical sort over the three classification buckets
			// (failing < healthy < never, alphabetical). The classification
			// is closed and total so no nil-trailing handling is needed —
			// every webhook resolves to exactly one value. Tiebreak on `id`.
			si, sj := WebhookDeliveryStatus(ai), WebhookDeliveryStatus(aj)
			if si != sj {
				less = si < sj
				break
			}
			less = ai.ID < aj.ID
		default: // WebhookSortID
			less = ai.ID < aj.ID
		}
		if desc {
			return !less
		}
		return less
	})
}
