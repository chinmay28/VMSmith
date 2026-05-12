package types

import "testing"

func TestImageMatchesSearch_EmptyQueryMatchesAll(t *testing.T) {
	img := &Image{Name: "rocky9"}
	if !ImageMatchesSearch(img, "") {
		t.Fatalf("empty query should match every image")
	}
}

func TestImageMatchesSearch_NilImageNeverMatches(t *testing.T) {
	if ImageMatchesSearch(nil, "anything") {
		t.Fatalf("nil image must not match a non-empty query")
	}
}

func TestImageMatchesSearch_NameSubstring(t *testing.T) {
	img := &Image{Name: "rocky9-base"}
	if !ImageMatchesSearch(img, "rocky") {
		t.Fatalf("expected substring match in name")
	}
	if ImageMatchesSearch(img, "ubuntu") {
		t.Fatalf("did not expect 'ubuntu' to match name %q", img.Name)
	}
}

func TestImageMatchesSearch_DescriptionSubstring(t *testing.T) {
	img := &Image{Name: "rocky9-base", Description: "Hardened CIS-1 build"}
	if !ImageMatchesSearch(img, "hardened") {
		t.Fatalf("expected description substring to match")
	}
}

func TestImageMatchesSearch_TagSubstring(t *testing.T) {
	img := &Image{Name: "rocky9-base", Tags: []string{"team-storage", "prod"}}
	if !ImageMatchesSearch(img, "storage") {
		t.Fatalf("expected tag substring to match")
	}
}

func TestImageMatchesSearch_CallerLowercasesQuery(t *testing.T) {
	// ImageMatchesSearch lowercases the haystack but trusts the caller to
	// have lowercased the needle (see the API/CLI handlers). Locking that
	// contract in here so a future caller doesn't accidentally regress it.
	img := &Image{Name: "Rocky9-Base"}
	if ImageMatchesSearch(img, "ROCKY") {
		t.Fatalf("expected uppercase needle to miss; caller is responsible for lowercasing")
	}
	if !ImageMatchesSearch(img, "rocky") {
		t.Fatalf("expected lowercase needle to hit case-insensitive haystack")
	}
}

func TestImageMatchesSearch_SkipsEmptyDescription(t *testing.T) {
	img := &Image{Name: "rocky9-base", Description: ""}
	if ImageMatchesSearch(img, "rocky9-not-here") {
		t.Fatalf("did not expect needle to match an empty description")
	}
}

func TestImageMatchesSearch_NoMatchReturnsFalse(t *testing.T) {
	img := &Image{Name: "alpha", Description: "some desc", Tags: []string{"prod"}}
	if ImageMatchesSearch(img, "needle-not-present-anywhere") {
		t.Fatalf("expected no-match to return false")
	}
}
