package logger

import "testing"

func TestEntryMatchesSearch_EmptyQueryMatchesAll(t *testing.T) {
	if !EntryMatchesSearch(Entry{Message: "anything"}, "") {
		t.Fatal("expected empty query to match")
	}
	if !EntryMatchesSearch(Entry{}, "") {
		t.Fatal("expected empty query to match zero entry")
	}
}

func TestEntryMatchesSearch_MessageSubstring(t *testing.T) {
	e := Entry{Message: "VM created with id vm-12345"}
	if !EntryMatchesSearch(e, "created") {
		t.Fatal("expected message substring to match")
	}
	if !EntryMatchesSearch(e, "vm-12345") {
		t.Fatal("expected token in message to match")
	}
	if EntryMatchesSearch(e, "destroyed") {
		t.Fatal("expected non-substring to miss")
	}
}

func TestEntryMatchesSearch_SourceSubstring(t *testing.T) {
	e := Entry{Message: "ok", Source: "api"}
	if !EntryMatchesSearch(e, "api") {
		t.Fatal("expected source substring to match")
	}
	if EntryMatchesSearch(e, "daemon") {
		t.Fatal("expected unrelated source to miss")
	}
}

func TestEntryMatchesSearch_LevelSubstring(t *testing.T) {
	e := Entry{Message: "ok", Level: "warn"}
	if !EntryMatchesSearch(e, "warn") {
		t.Fatal("expected level substring to match")
	}
	if EntryMatchesSearch(e, "debug") {
		t.Fatal("expected unrelated level to miss")
	}
}

func TestEntryMatchesSearch_FieldValueSubstring(t *testing.T) {
	e := Entry{
		Message: "request handled",
		Fields:  map[string]string{"method": "POST", "path": "/api/v1/vms"},
	}
	if !EntryMatchesSearch(e, "post") {
		t.Fatal("expected case-insensitive match against field value")
	}
	if !EntryMatchesSearch(e, "/api/v1/vms") {
		t.Fatal("expected exact field value to match")
	}
	if EntryMatchesSearch(e, "delete") {
		t.Fatal("expected non-matching needle to miss")
	}
}

func TestEntryMatchesSearch_FieldKeysExcluded(t *testing.T) {
	// "method" is a field key — searching for it should NOT match unless a
	// value also contains it. Keys are a small, repeating vocabulary that
	// would generate noisy matches if included in the haystack.
	e := Entry{
		Message: "request handled",
		Fields:  map[string]string{"method": "POST"},
	}
	if EntryMatchesSearch(e, "method") {
		t.Fatal("expected field key 'method' to be excluded from haystack")
	}
}

func TestEntryMatchesSearch_CallerLowercases(t *testing.T) {
	// Callers (API handler) are required to lower-case + trim the needle
	// before invoking. An uppercase query therefore never matches; this
	// pins the contract.
	e := Entry{Message: "VM started"}
	if EntryMatchesSearch(e, "VM") {
		t.Fatal("predicate must not lowercase the needle; caller's job")
	}
	if !EntryMatchesSearch(e, "vm") {
		t.Fatal("lowercased needle should match haystack")
	}
}

func TestEntryMatchesSearch_NoMatch(t *testing.T) {
	e := Entry{
		Message: "VM started",
		Source:  "daemon",
		Level:   "info",
		Fields:  map[string]string{"vm_id": "vm-1"},
	}
	if EntryMatchesSearch(e, "needle-not-present") {
		t.Fatal("expected no match for unrelated needle")
	}
}

func TestEntryMatchesSearch_EmptyFieldsHandledGracefully(t *testing.T) {
	e := Entry{Message: "VM started"}
	if EntryMatchesSearch(e, "anything") {
		t.Fatal("expected unrelated needle to miss when no fields/source/level present")
	}
}
