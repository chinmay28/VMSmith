package types

import (
	"testing"
	"time"
)

func whIDs(hooks []*Webhook) []string {
	out := make([]string, len(hooks))
	for i, h := range hooks {
		out[i] = h.ID
	}
	return out
}

func TestSortWebhooks_ByID_Asc(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-3"},
		{ID: "wh-1"},
		{ID: "wh-2"},
	}
	SortWebhooks(hooks, WebhookSortID, SortOrderAsc)
	want := []string{"wh-1", "wh-2", "wh-3"}
	got := whIDs(hooks)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortWebhooks_ByURL_CaseInsensitive(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-3", URL: "https://alpha.example.com/hook"},
		{ID: "wh-1", URL: "https://Beta.example.com/hook"},
		{ID: "wh-2", URL: "https://gamma.example.com/hook"},
	}
	SortWebhooks(hooks, WebhookSortURL, SortOrderAsc)
	// case-insensitive: "alpha..." < "beta..." < "gamma..."
	want := []string{"wh-3", "wh-1", "wh-2"}
	got := whIDs(hooks)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortWebhooks_ByURL_TiebreaksOnID(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-3", URL: "https://x.example.com/h"},
		{ID: "wh-1", URL: "https://x.example.com/h"},
		{ID: "wh-2", URL: "https://x.example.com/h"},
	}
	SortWebhooks(hooks, WebhookSortURL, SortOrderAsc)
	want := []string{"wh-1", "wh-2", "wh-3"}
	got := whIDs(hooks)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortWebhooks_ByCreatedAt_Desc(t *testing.T) {
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	hooks := []*Webhook{
		{ID: "wh-1", CreatedAt: base.Add(1 * time.Hour)},
		{ID: "wh-3", CreatedAt: base.Add(3 * time.Hour)},
		{ID: "wh-2", CreatedAt: base.Add(2 * time.Hour)},
	}
	SortWebhooks(hooks, WebhookSortCreatedAt, SortOrderDesc)
	want := []string{"wh-3", "wh-2", "wh-1"}
	got := whIDs(hooks)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortWebhooks_ByLastDelivery_ZerosSortLastAscFirstDesc(t *testing.T) {
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	hooks := []*Webhook{
		{ID: "wh-3", LastDeliveryAt: base.Add(3 * time.Hour)},
		{ID: "wh-2", LastDeliveryAt: time.Time{}}, // never delivered
		{ID: "wh-1", LastDeliveryAt: base.Add(1 * time.Hour)},
	}
	SortWebhooks(hooks, WebhookSortLastDelivery, SortOrderAsc)
	// ascending: oldest delivery first, never-delivered tail
	wantAsc := []string{"wh-1", "wh-3", "wh-2"}
	if got := whIDs(hooks); !equalStrings(got, wantAsc) {
		t.Errorf("asc: got %v, want %v", got, wantAsc)
	}

	hooks = []*Webhook{
		{ID: "wh-3", LastDeliveryAt: base.Add(3 * time.Hour)},
		{ID: "wh-2", LastDeliveryAt: time.Time{}},
		{ID: "wh-1", LastDeliveryAt: base.Add(1 * time.Hour)},
	}
	SortWebhooks(hooks, WebhookSortLastDelivery, SortOrderDesc)
	// descending: never-delivered head, then newest-first
	wantDesc := []string{"wh-2", "wh-3", "wh-1"}
	if got := whIDs(hooks); !equalStrings(got, wantDesc) {
		t.Errorf("desc: got %v, want %v", got, wantDesc)
	}
}

func TestSortWebhooks_UnknownFieldFallsBackToID(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-3"},
		{ID: "wh-1"},
		{ID: "wh-2"},
	}
	SortWebhooks(hooks, "secret", SortOrderAsc)
	want := []string{"wh-1", "wh-2", "wh-3"}
	got := whIDs(hooks)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortWebhooks_StableEqualKeys(t *testing.T) {
	// Two independent sorts on equal-key data must produce the same order so
	// repeated requests return deterministic results.
	build := func() []*Webhook {
		return []*Webhook{
			{ID: "wh-3", URL: "https://shared.example.com/h"},
			{ID: "wh-1", URL: "https://shared.example.com/h"},
			{ID: "wh-4", URL: "https://shared.example.com/h"},
			{ID: "wh-2", URL: "https://shared.example.com/h"},
		}
	}
	a, b := build(), build()
	SortWebhooks(a, WebhookSortURL, SortOrderAsc)
	SortWebhooks(b, WebhookSortURL, SortOrderAsc)
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("idx %d: a=%q b=%q — equal-URL tie not deterministic", i, a[i].ID, b[i].ID)
		}
	}
}

// 5.4.98 — delivery_status sort axis. Alphabetical: failing < healthy < never.
// Tiebreak on `id`.

// buildDeliveryStatusWebhooks returns three webhooks — one of each
// classification (never / healthy / failing) — laid out in non-sorted order
// so the test asserts the comparator's effect, not the input order.
func buildDeliveryStatusWebhooks() []*Webhook {
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	return []*Webhook{
		// never — LastDeliveryAt zero takes precedence even if LastStatus is set
		{ID: "wh-never", LastDeliveryAt: time.Time{}, LastStatus: 0, LastError: ""},
		// healthy — last attempt 2xx with empty LastError
		{ID: "wh-healthy", LastDeliveryAt: base.Add(1 * time.Hour), LastStatus: 200, LastError: ""},
		// failing — last attempt non-2xx
		{ID: "wh-failing", LastDeliveryAt: base.Add(2 * time.Hour), LastStatus: 500, LastError: ""},
	}
}

func TestSortWebhooks_ByDeliveryStatus_AscAlphabetical(t *testing.T) {
	hooks := buildDeliveryStatusWebhooks()
	SortWebhooks(hooks, WebhookSortDeliveryStatus, SortOrderAsc)
	// alphabetical: failing < healthy < never
	want := []string{"wh-failing", "wh-healthy", "wh-never"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Errorf("asc: got %v, want %v", got, want)
	}
}

func TestSortWebhooks_ByDeliveryStatus_DescAlphabetical(t *testing.T) {
	hooks := buildDeliveryStatusWebhooks()
	SortWebhooks(hooks, WebhookSortDeliveryStatus, SortOrderDesc)
	// reverse alphabetical: never > healthy > failing
	want := []string{"wh-never", "wh-healthy", "wh-failing"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Errorf("desc: got %v, want %v", got, want)
	}
}

func TestSortWebhooks_ByDeliveryStatus_TiebreaksOnID(t *testing.T) {
	// Three failing webhooks with the same classification — must order by id.
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	hooks := []*Webhook{
		{ID: "wh-3", LastDeliveryAt: base, LastStatus: 500},
		{ID: "wh-1", LastDeliveryAt: base, LastStatus: 500},
		{ID: "wh-2", LastDeliveryAt: base, LastStatus: 500},
	}
	SortWebhooks(hooks, WebhookSortDeliveryStatus, SortOrderAsc)
	want := []string{"wh-1", "wh-2", "wh-3"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Errorf("tiebreak asc: got %v, want %v", got, want)
	}
	// Desc flips the id tiebreaker too — repeated requests stay deterministic.
	hooks = []*Webhook{
		{ID: "wh-3", LastDeliveryAt: base, LastStatus: 500},
		{ID: "wh-1", LastDeliveryAt: base, LastStatus: 500},
		{ID: "wh-2", LastDeliveryAt: base, LastStatus: 500},
	}
	SortWebhooks(hooks, WebhookSortDeliveryStatus, SortOrderDesc)
	wantDesc := []string{"wh-3", "wh-2", "wh-1"}
	if got := whIDs(hooks); !equalStrings(got, wantDesc) {
		t.Errorf("tiebreak desc: got %v, want %v", got, wantDesc)
	}
}

func TestSortWebhooks_ByDeliveryStatus_TransportFailureClassifiesAsFailing(t *testing.T) {
	// A delivery attempt that errored at the transport layer leaves
	// LastDeliveryAt non-zero, LastStatus == 0, and LastError populated.
	// WebhookDeliveryStatus classifies this as "failing"; the sort must
	// order it alongside non-2xx failures, not "healthy".
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	hooks := []*Webhook{
		{ID: "wh-h", LastDeliveryAt: base, LastStatus: 200},
		{ID: "wh-t", LastDeliveryAt: base.Add(time.Hour), LastStatus: 0, LastError: "dial tcp: timeout"},
	}
	SortWebhooks(hooks, WebhookSortDeliveryStatus, SortOrderAsc)
	want := []string{"wh-t", "wh-h"} // failing before healthy
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Errorf("transport failure: got %v, want %v", got, want)
	}
}

func TestIsValidWebhookSort_AcceptsAllAxes(t *testing.T) {
	for _, s := range []string{
		WebhookSortID,
		WebhookSortURL,
		WebhookSortCreatedAt,
		WebhookSortLastDelivery,
		WebhookSortDeliveryStatus,
	} {
		if !IsValidWebhookSort(s) {
			t.Errorf("IsValidWebhookSort(%q) = false, want true", s)
		}
	}
}

func TestIsValidWebhookSort_RejectsUnknown(t *testing.T) {
	for _, s := range []string{"", "secret", "URL", "Delivery_Status", "active"} {
		if IsValidWebhookSort(s) {
			t.Errorf("IsValidWebhookSort(%q) = true, want false", s)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
