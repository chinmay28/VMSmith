package types

import "testing"

func TestTemplateMatchesSearch_EmptyQueryMatchesAll(t *testing.T) {
	tpl := &VMTemplate{Name: "rocky9-base"}
	if !TemplateMatchesSearch(tpl, "") {
		t.Fatalf("empty query should match every template")
	}
}

func TestTemplateMatchesSearch_NilTemplateNeverMatches(t *testing.T) {
	if TemplateMatchesSearch(nil, "anything") {
		t.Fatalf("nil template must not match a non-empty query")
	}
}

func TestTemplateMatchesSearch_NameSubstring(t *testing.T) {
	tpl := &VMTemplate{Name: "rocky9-base"}
	if !TemplateMatchesSearch(tpl, "rocky") {
		t.Fatalf("expected substring match in name")
	}
	if TemplateMatchesSearch(tpl, "ubuntu") {
		t.Fatalf("did not expect 'ubuntu' to match name %q", tpl.Name)
	}
}

func TestTemplateMatchesSearch_DescriptionSubstring(t *testing.T) {
	tpl := &VMTemplate{Name: "small", Description: "Hardened CIS-1 build"}
	if !TemplateMatchesSearch(tpl, "hardened") {
		t.Fatalf("expected description substring to match")
	}
}

func TestTemplateMatchesSearch_TagSubstring(t *testing.T) {
	tpl := &VMTemplate{Name: "small", Tags: []string{"team-storage", "prod"}}
	if !TemplateMatchesSearch(tpl, "storage") {
		t.Fatalf("expected tag substring to match")
	}
}

func TestTemplateMatchesSearch_CallerLowercasesQuery(t *testing.T) {
	// TemplateMatchesSearch lowercases the haystack but trusts the caller to
	// have lowercased the needle (see the API/CLI handlers). Locking that
	// contract in here so a future caller doesn't accidentally regress it.
	tpl := &VMTemplate{Name: "Rocky9-Base"}
	if TemplateMatchesSearch(tpl, "ROCKY") {
		t.Fatalf("expected uppercase needle to miss; caller is responsible for lowercasing")
	}
	if !TemplateMatchesSearch(tpl, "rocky") {
		t.Fatalf("expected lowercase needle to hit case-insensitive haystack")
	}
}

func TestTemplateMatchesSearch_SkipsEmptyDescription(t *testing.T) {
	tpl := &VMTemplate{Name: "small", Description: ""}
	if TemplateMatchesSearch(tpl, "needle-not-anywhere") {
		t.Fatalf("did not expect needle to match an empty description")
	}
}

func TestTemplateMatchesSearch_NoMatchReturnsFalse(t *testing.T) {
	tpl := &VMTemplate{Name: "alpha", Description: "some desc", Tags: []string{"prod"}}
	if TemplateMatchesSearch(tpl, "needle-not-present-anywhere") {
		t.Fatalf("expected no-match to return false")
	}
}

func TestTemplateMatchesSearch_ImageNotInHaystack(t *testing.T) {
	// Image / default_user / networks are intentionally excluded from the
	// haystack — they describe what the template produces, not the template
	// itself. Matching them produces noisy false positives. Lock the
	// contract in.
	tpl := &VMTemplate{
		Name:        "small",
		Image:       "rocky9.qcow2",
		DefaultUser: "ec2-user",
	}
	if TemplateMatchesSearch(tpl, "rocky9.qcow2") {
		t.Fatalf("did not expect image path to match")
	}
	if TemplateMatchesSearch(tpl, "ec2-user") {
		t.Fatalf("did not expect default_user to match")
	}
}

func TestTemplateMatchesSearch_IDNotInHaystack(t *testing.T) {
	// IDs are opaque `tmpl-<unix-nano>` strings; matching them produces
	// noisy numeric false positives. Lock the contract in.
	tpl := &VMTemplate{ID: "tmpl-1741234567890123", Name: "alpha"}
	if TemplateMatchesSearch(tpl, "1741234567890123") {
		t.Fatalf("did not expect ID digits to match")
	}
	if TemplateMatchesSearch(tpl, "tmpl-") {
		t.Fatalf("did not expect 'tmpl-' prefix to match")
	}
}
