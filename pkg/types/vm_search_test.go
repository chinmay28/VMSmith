package types

import "testing"

func TestVMMatchesSearch_EmptyQueryMatchesAll(t *testing.T) {
	vm := &VM{Name: "alpha"}
	if !VMMatchesSearch(vm, "") {
		t.Fatalf("empty query should match every VM")
	}
}

func TestVMMatchesSearch_NilVMNeverMatches(t *testing.T) {
	if VMMatchesSearch(nil, "anything") {
		t.Fatalf("nil VM must not match a non-empty query")
	}
}

func TestVMMatchesSearch_NameSubstring(t *testing.T) {
	vm := &VM{Name: "web-prod-01"}
	if !VMMatchesSearch(vm, "prod") {
		t.Fatalf("expected substring match in name")
	}
	if VMMatchesSearch(vm, "stage") {
		t.Fatalf("did not expect 'stage' to match name %q", vm.Name)
	}
}

func TestVMMatchesSearch_DescriptionSubstring(t *testing.T) {
	vm := &VM{Name: "alpha", Description: "Customer A jumpbox"}
	if !VMMatchesSearch(vm, "jumpbox") {
		t.Fatalf("expected description substring to match")
	}
}

func TestVMMatchesSearch_TagSubstring(t *testing.T) {
	vm := &VM{Name: "alpha", Tags: []string{"team-storage", "prod"}}
	if !VMMatchesSearch(vm, "storage") {
		t.Fatalf("expected tag substring to match")
	}
}

func TestVMMatchesSearch_CallerLowercasesQuery(t *testing.T) {
	// VMMatchesSearch lowercases the haystack but trusts the caller to
	// have lowercased the needle (see the API/CLI handlers). Locking that
	// contract in here so a future caller doesn't accidentally regress it.
	vm := &VM{Name: "Web-Prod-01"}
	if VMMatchesSearch(vm, "WEB") {
		t.Fatalf("expected uppercase needle to miss; caller is responsible for lowercasing")
	}
	if !VMMatchesSearch(vm, "web") {
		t.Fatalf("expected lowercase needle to hit case-insensitive haystack")
	}
}

func TestVMMatchesSearch_SkipsEmptyDescription(t *testing.T) {
	vm := &VM{Name: "alpha", Description: ""}
	if VMMatchesSearch(vm, "alpha-not-here") {
		t.Fatalf("did not expect needle to match an empty description")
	}
}
