package types

import "testing"

func TestSnapshotMatchesSearch_EmptyQueryMatchesAll(t *testing.T) {
	snap := &Snapshot{Name: "pre-upgrade"}
	if !SnapshotMatchesSearch(snap, "") {
		t.Fatalf("empty query should match every snapshot")
	}
}

func TestSnapshotMatchesSearch_NilSnapshotNeverMatches(t *testing.T) {
	if SnapshotMatchesSearch(nil, "anything") {
		t.Fatalf("nil snapshot must not match a non-empty query")
	}
}

func TestSnapshotMatchesSearch_NameSubstring(t *testing.T) {
	snap := &Snapshot{Name: "pre-upgrade-2026-05"}
	if !SnapshotMatchesSearch(snap, "upgrade") {
		t.Fatalf("expected substring match in name")
	}
	if SnapshotMatchesSearch(snap, "rollback") {
		t.Fatalf("did not expect 'rollback' to match name %q", snap.Name)
	}
}

func TestSnapshotMatchesSearch_DescriptionSubstring(t *testing.T) {
	snap := &Snapshot{Name: "snap-001", Description: "Before applying CIS hardening playbook"}
	if !SnapshotMatchesSearch(snap, "hardening") {
		t.Fatalf("expected description substring to match")
	}
}

func TestSnapshotMatchesSearch_CallerLowercasesQuery(t *testing.T) {
	// SnapshotMatchesSearch lowercases the haystack but trusts the caller
	// to have lowercased the needle (see the API/CLI handlers). Lock the
	// contract in so a future caller can't silently regress it.
	snap := &Snapshot{Name: "Pre-Upgrade"}
	if SnapshotMatchesSearch(snap, "PRE") {
		t.Fatalf("expected uppercase needle to miss; caller is responsible for lowercasing")
	}
	if !SnapshotMatchesSearch(snap, "pre") {
		t.Fatalf("expected lowercase needle to hit case-insensitive haystack")
	}
}

func TestSnapshotMatchesSearch_SkipsEmptyDescription(t *testing.T) {
	// A blank description must not crash the predicate or contribute to the
	// haystack — keeps the "no description set" path quiet.
	snap := &Snapshot{Name: "snap-001"}
	if SnapshotMatchesSearch(snap, "alpha-not-here") {
		t.Fatalf("unrelated query must not match")
	}
	if !SnapshotMatchesSearch(snap, "snap") {
		t.Fatalf("name substring should still match when description is empty")
	}
}

func TestSnapshotMatchesSearch_NoMatch(t *testing.T) {
	snap := &Snapshot{Name: "snap-001", Description: "rollback point"}
	if SnapshotMatchesSearch(snap, "needle-not-present") {
		t.Fatalf("unrelated query should not match")
	}
}

func TestSnapshotMatchesSearch_IDAndVMIDNotInHaystack(t *testing.T) {
	// ID is `<vmID>/<name>` and VMID is the URL scope — both are excluded
	// from the search haystack by design. Lock that in: a needle that only
	// appears in those fields must not match.
	snap := &Snapshot{
		ID:   "vm-1741234567890123/snap-001",
		VMID: "vm-1741234567890123",
		Name: "snap-001",
	}
	if SnapshotMatchesSearch(snap, "1741234") {
		t.Fatalf("VM-id substring must not match — IDs are excluded from haystack")
	}
}

func TestSnapshotMatchesSearch_TagSubstring(t *testing.T) {
	snap := &Snapshot{Name: "snap-001", Tags: []string{"production", "audit"}}
	if !SnapshotMatchesSearch(snap, "audit") {
		t.Fatalf("expected exact tag to match")
	}
	if !SnapshotMatchesSearch(snap, "prod") {
		t.Fatalf("expected tag substring to match")
	}
	if SnapshotMatchesSearch(snap, "staging") {
		t.Fatalf("unrelated tag-shaped query should not match")
	}
}

func TestSnapshotMatchesSearch_TagCaseInsensitive(t *testing.T) {
	// Tags are normalised to lowercase on the server, so the haystack is
	// already lowercased. Caller lowercases the needle. Confirm the
	// case-insensitive end-to-end shape works.
	snap := &Snapshot{Name: "snap-001", Tags: []string{"production"}}
	if !SnapshotMatchesSearch(snap, "prod") {
		t.Fatalf("expected lowercase needle to hit lowercase tag")
	}
}

func TestSnapshotMatchesSearch_EmptyTagsHandled(t *testing.T) {
	snap := &Snapshot{Name: "snap-001", Tags: []string{"", "audit"}}
	if !SnapshotMatchesSearch(snap, "audit") {
		t.Fatalf("expected non-empty tag to still match through an empty sibling")
	}
}
