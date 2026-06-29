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
		WebhookSortActive,
		WebhookSortDescription,
	} {
		if !IsValidWebhookSort(s) {
			t.Errorf("IsValidWebhookSort(%q) = false, want true", s)
		}
	}
}

func TestIsValidWebhookSort_RejectsUnknown(t *testing.T) {
	for _, s := range []string{"", "secret", "URL", "Delivery_Status", "Active"} {
		if IsValidWebhookSort(s) {
			t.Errorf("IsValidWebhookSort(%q) = true, want false", s)
		}
	}
}

// 5.4.114 — boolean `active` sort axis with closed-and-total classification.
// Mirrors the VM auto_start (5.4.108) / locked (5.4.109) and schedule
// enabled (5.4.113) boolean axes one resource over: every webhook belongs
// to exactly one of the two buckets (true or false) so there is no
// nil-trailing branch — the comparator just orders false before true in
// asc and tiebreaks on id within each cohort.

// TestIsValidWebhookSort_AcceptsActive covers the 5.4.114 active sort
// axis — the symmetric sort counterpart to the tristate `?active=true|
// false` exact-match filter (5.4.37) on the same column.
func TestIsValidWebhookSort_AcceptsActive(t *testing.T) {
	if !IsValidWebhookSort(WebhookSortActive) {
		t.Fatal("active must be an accepted sort key")
	}
	if !IsValidWebhookSort("active") {
		t.Fatal("literal 'active' must be accepted")
	}
}

// TestSortWebhooks_ByActive_AscPutsFalseFirst asserts asc collation
// false < true: the inactive cohort heads the list, the active cohort
// (the webhooks that actually deliver) sinks to the tail. Within each
// cohort the id tiebreak preserves a deterministic order.
func TestSortWebhooks_ByActive_AscPutsFalseFirst(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-1", Active: true},
		{ID: "wh-2", Active: false},
		{ID: "wh-3"}, // zero-value Active == false
		{ID: "wh-4", Active: true},
	}
	SortWebhooks(hooks, WebhookSortActive, SortOrderAsc)
	want := []string{"wh-2", "wh-3", "wh-1", "wh-4"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Fatalf("active asc: got %v, want %v", got, want)
	}
}

// TestSortWebhooks_ByActive_DescPutsTrueFirst flips the asc ordering —
// the active cohort (live webhooks) heads the list, the inactive cohort
// sinks to the tail. Desc reverses the entire compare result including
// the id tiebreak so within each cohort the higher id comes first.
func TestSortWebhooks_ByActive_DescPutsTrueFirst(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-1", Active: true},
		{ID: "wh-2", Active: false},
		{ID: "wh-3", Active: true},
		{ID: "wh-4", Active: false},
	}
	SortWebhooks(hooks, WebhookSortActive, SortOrderDesc)
	want := []string{"wh-3", "wh-1", "wh-4", "wh-2"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Fatalf("active desc: got %v, want %v", got, want)
	}
}

// TestSortWebhooks_ByActive_TiebreaksOnID covers webhooks sharing the
// same active state tiebreak deterministically on id (common case: many
// active production webhooks).
func TestSortWebhooks_ByActive_TiebreaksOnID(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-z", Active: true},
		{ID: "wh-a", Active: true},
		{ID: "wh-m", Active: true},
	}
	SortWebhooks(hooks, WebhookSortActive, SortOrderAsc)
	want := []string{"wh-a", "wh-m", "wh-z"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Fatalf("active id-tiebreak: got %v, want %v", got, want)
	}
}

// TestSortWebhooks_ByActive_AllFalse_TiebreaksOnID covers the zero-value
// cohort tiebreak — every webhook has the default false Active (e.g. a
// wire payload that omitted the field), so all of them collapse to the
// inactive bucket and the id tiebreak takes over. Mirrors the schedule
// enabled AllFalse_TiebreaksOnID test (5.4.113).
func TestSortWebhooks_ByActive_AllFalse_TiebreaksOnID(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-z"},
		{ID: "wh-a"},
		{ID: "wh-m"},
	}
	SortWebhooks(hooks, WebhookSortActive, SortOrderAsc)
	want := []string{"wh-a", "wh-m", "wh-z"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Fatalf("active all-false id-tiebreak: got %v, want %v", got, want)
	}
}

// ============================================================
// `description` sort axis (5.4.122)
// ============================================================
//
// Case-insensitive compare on Webhook.Description with empty-trailing
// nil-handling. Mirrors the VM (5.4.120) / template (5.4.119) / image
// (5.4.118) / snapshot (5.4.121) description axes one resource over.

func TestSortWebhooks_ByDescription_AscCaseInsensitive(t *testing.T) {
	// Mixed-case descriptions sort case-insensitively so `Slack #ops` and
	// `slack #ops` collate as identical. Mirrors the case-insensitive
	// haystack in the `?search=` filter on the same column.
	hooks := []*Webhook{
		{ID: "wh-1", Description: "Slack #ops"},
		{ID: "wh-2", Description: "alpha"},
		{ID: "wh-3", Description: "slack #ops"},
	}
	SortWebhooks(hooks, WebhookSortDescription, SortOrderAsc)
	// asc: `alpha` < `slack #ops` (case-folded). The two `slack #ops`
	// entries tiebreak on id ascending (wh-1 before wh-3).
	want := []string{"wh-2", "wh-1", "wh-3"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortWebhooks_ByDescription_EmptyTrailsInAsc(t *testing.T) {
	// Webhooks with no description sink to the tail in ascending order —
	// operators looking for "which webhooks have a description" want them
	// at the head of asc, not buried among the unset majority. Mirrors
	// the VM (5.4.120) / template (5.4.119) / image (5.4.118) /
	// snapshot (5.4.121) description axes one resource over.
	hooks := []*Webhook{
		{ID: "wh-1", Description: ""},
		{ID: "wh-2", Description: "z"},
		{ID: "wh-3", Description: "a"},
	}
	SortWebhooks(hooks, WebhookSortDescription, SortOrderAsc)
	want := []string{"wh-3", "wh-2", "wh-1"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortWebhooks_ByDescription_EmptyHeadsInDesc(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-1", Description: "a"},
		{ID: "wh-2", Description: ""},
		{ID: "wh-3", Description: ""},
	}
	SortWebhooks(hooks, WebhookSortDescription, SortOrderDesc)
	// Empty heads in desc. The two empty-description entries tiebreak on
	// id — and because the outer desc-wrapper inverts the tiebreak,
	// wh-3 heads wh-2 (higher id first), then wh-1 (the only concrete
	// description) trails. Matches the VM `_DescEmptyHeads` contract.
	want := []string{"wh-3", "wh-2", "wh-1"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortWebhooks_ByDescription_TiebreaksOnID(t *testing.T) {
	hooks := []*Webhook{
		{ID: "wh-3", Description: "same"},
		{ID: "wh-1", Description: "same"},
		{ID: "wh-2", Description: "same"},
	}
	SortWebhooks(hooks, WebhookSortDescription, SortOrderAsc)
	want := []string{"wh-1", "wh-2", "wh-3"}
	if got := whIDs(hooks); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestIsValidWebhookSort_AcceptsDescription(t *testing.T) {
	if !IsValidWebhookSort(WebhookSortDescription) {
		t.Fatalf("IsValidWebhookSort(%q) = false, want true", WebhookSortDescription)
	}
	for _, axis := range []string{"DESCRIPTION", "Description", " description "} {
		if IsValidWebhookSort(axis) {
			t.Errorf("IsValidWebhookSort(%q) = true, want false (parser must normalise before lookup)", axis)
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
