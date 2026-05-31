package types

import "testing"

func templateWithNetworks(names ...string) *VMTemplate {
	attachments := make([]NetworkAttachment, 0, len(names))
	for _, n := range names {
		attachments = append(attachments, NetworkAttachment{Name: n})
	}
	return &VMTemplate{Networks: attachments}
}

func TestTemplateMatchesNetwork_NilTemplate(t *testing.T) {
	if TemplateMatchesNetwork(nil, "data-net") {
		t.Fatalf("nil template must never match")
	}
}

func TestTemplateMatchesNetwork_EmptyQueryMatchesEverything(t *testing.T) {
	if !TemplateMatchesNetwork(templateWithNetworks(), "") {
		t.Fatalf("empty query should match a template with no networks")
	}
	if !TemplateMatchesNetwork(templateWithNetworks("data-net"), "") {
		t.Fatalf("empty query should match a template with networks")
	}
}

func TestTemplateMatchesNetwork_ExactMatch(t *testing.T) {
	tpl := templateWithNetworks("data-net", "storage-net")
	if !TemplateMatchesNetwork(tpl, "storage-net") {
		t.Fatalf("expected match against second attachment")
	}
}

func TestTemplateMatchesNetwork_CaseInsensitive(t *testing.T) {
	tpl := templateWithNetworks("Data-Net")
	if !TemplateMatchesNetwork(tpl, "data-net") {
		t.Fatalf("expected case-insensitive match (caller passes lowercase query)")
	}
}

func TestTemplateMatchesNetwork_NoMatch(t *testing.T) {
	tpl := templateWithNetworks("data-net")
	if TemplateMatchesNetwork(tpl, "storage-net") {
		t.Fatalf("did not expect a match for an unattached network")
	}
}

func TestTemplateMatchesNetwork_NoNetworks(t *testing.T) {
	if TemplateMatchesNetwork(templateWithNetworks(), "data-net") {
		t.Fatalf("a template with no attachments must not match a named query")
	}
}

func TestTemplateMatchesNetwork_PartialIsNotAMatch(t *testing.T) {
	// Exact-match contract: a substring of an attachment name must not match.
	tpl := templateWithNetworks("storage-net")
	if TemplateMatchesNetwork(tpl, "storage") {
		t.Fatalf("partial query should not match an exact-match filter")
	}
}
