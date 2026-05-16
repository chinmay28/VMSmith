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

func TestEntryMatchesVMID_EmptyTargetMatchesAll(t *testing.T) {
	if !EntryMatchesVMID(Entry{}, "") {
		t.Fatal("expected empty target to match zero entry")
	}
	if !EntryMatchesVMID(Entry{Fields: map[string]string{"vm_id": "vm-1"}}, "") {
		t.Fatal("expected empty target to match populated entry")
	}
}

func TestEntryMatchesVMID_ExactMatch(t *testing.T) {
	e := Entry{Fields: map[string]string{"vm_id": "vm-1741234567890"}}
	if !EntryMatchesVMID(e, "vm-1741234567890") {
		t.Fatal("expected exact vm_id to match")
	}
	if EntryMatchesVMID(e, "vm-1741234567891") {
		t.Fatal("expected different vm_id to miss")
	}
}

func TestEntryMatchesVMID_PrefixDoesNotMatch(t *testing.T) {
	// The exact-match contract prevents `vm-123` from accidentally swallowing
	// `vm-12345`. This is a deliberate guard against substring confusion when
	// VM IDs share leading digits.
	e := Entry{Fields: map[string]string{"vm_id": "vm-12345"}}
	if EntryMatchesVMID(e, "vm-123") {
		t.Fatal("expected vm_id prefix to NOT match (exact-match contract)")
	}
}

func TestEntryMatchesVMID_CaseSensitive(t *testing.T) {
	// VM IDs are opaque `vm-<unix-nano>` strings — case-sensitive by
	// construction. An operator typo should miss rather than silently match.
	e := Entry{Fields: map[string]string{"vm_id": "vm-ABC123"}}
	if EntryMatchesVMID(e, "vm-abc123") {
		t.Fatal("expected case-sensitive miss")
	}
	if !EntryMatchesVMID(e, "vm-ABC123") {
		t.Fatal("expected exact-case to match")
	}
}

func TestEntryMatchesVMID_NilFieldsMissesNonEmptyTarget(t *testing.T) {
	if EntryMatchesVMID(Entry{Message: "no fields"}, "vm-1") {
		t.Fatal("expected nil fields with non-empty target to miss")
	}
}

func TestEntryMatchesVMID_MissingVMIDFieldMisses(t *testing.T) {
	e := Entry{Fields: map[string]string{"method": "POST"}}
	if EntryMatchesVMID(e, "vm-1") {
		t.Fatal("expected entry without vm_id field to miss")
	}
}

func TestEntryMatchesVMID_OtherFieldsIgnored(t *testing.T) {
	// Only the `vm_id` field counts — a vm-shaped value in some other key
	// must not match. This is what separates this filter from `search`.
	e := Entry{Fields: map[string]string{"resource_id": "vm-1", "vm_id": "vm-2"}}
	if EntryMatchesVMID(e, "vm-1") {
		t.Fatal("expected resource_id to be ignored")
	}
	if !EntryMatchesVMID(e, "vm-2") {
		t.Fatal("expected vm_id to be the authoritative field")
	}
}
