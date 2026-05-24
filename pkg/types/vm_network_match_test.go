package types

import "testing"

func vmWithNetworks(names ...string) *VM {
	attachments := make([]NetworkAttachment, 0, len(names))
	for _, n := range names {
		attachments = append(attachments, NetworkAttachment{Name: n})
	}
	return &VM{Spec: VMSpec{Networks: attachments}}
}

func TestVMMatchesNetwork_NilVM(t *testing.T) {
	if VMMatchesNetwork(nil, "data-net") {
		t.Fatalf("nil VM must never match")
	}
}

func TestVMMatchesNetwork_EmptyQueryMatchesEverything(t *testing.T) {
	if !VMMatchesNetwork(vmWithNetworks(), "") {
		t.Fatalf("empty query should match a VM with no networks")
	}
	if !VMMatchesNetwork(vmWithNetworks("data-net"), "") {
		t.Fatalf("empty query should match a VM with networks")
	}
}

func TestVMMatchesNetwork_ExactMatch(t *testing.T) {
	vm := vmWithNetworks("data-net", "storage-net")
	if !VMMatchesNetwork(vm, "storage-net") {
		t.Fatalf("expected match against second attachment")
	}
}

func TestVMMatchesNetwork_CaseInsensitive(t *testing.T) {
	vm := vmWithNetworks("Data-Net")
	if !VMMatchesNetwork(vm, "data-net") {
		t.Fatalf("expected case-insensitive match (caller passes lowercase query)")
	}
}

func TestVMMatchesNetwork_NoMatch(t *testing.T) {
	vm := vmWithNetworks("data-net")
	if VMMatchesNetwork(vm, "storage-net") {
		t.Fatalf("did not expect a match for an unattached network")
	}
}

func TestVMMatchesNetwork_NoNetworks(t *testing.T) {
	if VMMatchesNetwork(vmWithNetworks(), "data-net") {
		t.Fatalf("a VM with no attachments must not match a named query")
	}
}

func TestVMMatchesNetwork_PartialIsNotAMatch(t *testing.T) {
	// Exact-match contract: a substring of an attachment name must not match.
	vm := vmWithNetworks("storage-net")
	if VMMatchesNetwork(vm, "storage") {
		t.Fatalf("partial query should not match an exact-match filter")
	}
}
