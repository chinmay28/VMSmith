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
