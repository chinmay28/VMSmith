package vm

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestParseSnapshotXML_DescriptionAndCreationTime(t *testing.T) {
	raw := `<domainsnapshot>
  <name>before-patch</name>
  <description>before applying May patch</description>
  <creationTime>1714694400</creationTime>
</domainsnapshot>`

	desc, created, err := parseSnapshotXML(raw)
	if err != nil {
		t.Fatalf("parseSnapshotXML: %v", err)
	}
	if desc != "before applying May patch" {
		t.Errorf("description = %q", desc)
	}
	want := time.Unix(1714694400, 0).UTC()
	if !created.Equal(want) {
		t.Errorf("created = %v, want %v", created, want)
	}
}

func TestParseSnapshotXML_NoDescription(t *testing.T) {
	raw := `<domainsnapshot><name>plain</name></domainsnapshot>`
	desc, created, err := parseSnapshotXML(raw)
	if err != nil {
		t.Fatalf("parseSnapshotXML: %v", err)
	}
	if desc != "" {
		t.Errorf("description = %q, want empty", desc)
	}
	if !created.IsZero() {
		t.Errorf("created = %v, want zero", created)
	}
}

func TestParseSnapshotXML_TrimsDescription(t *testing.T) {
	raw := `<domainsnapshot><name>n</name><description>
		hello
	</description></domainsnapshot>`
	desc, _, err := parseSnapshotXML(raw)
	if err != nil {
		t.Fatalf("parseSnapshotXML: %v", err)
	}
	if desc != "hello" {
		t.Errorf("description = %q, want trimmed 'hello'", desc)
	}
}

func TestParseSnapshotXML_BadXML(t *testing.T) {
	if _, _, err := parseSnapshotXML("<not-xml"); err == nil {
		t.Fatal("expected error for malformed xml")
	}
}

func TestParseSnapshotXML_NonNumericCreationTime(t *testing.T) {
	raw := `<domainsnapshot><name>n</name><creationTime>not-a-number</creationTime></domainsnapshot>`
	_, created, err := parseSnapshotXML(raw)
	if err != nil {
		t.Fatalf("parseSnapshotXML: %v", err)
	}
	if !created.IsZero() {
		t.Errorf("created = %v, want zero for non-numeric creationTime", created)
	}
}

func TestRewriteSnapshotDescription_AddsDescription(t *testing.T) {
	raw := `<domainsnapshot>` +
		`<name>snap-a</name>` +
		`<state>running</state>` +
		`<creationTime>1714694400</creationTime>` +
		`</domainsnapshot>`

	out, err := rewriteSnapshotDescription(raw, "freshly added")
	if err != nil {
		t.Fatalf("rewriteSnapshotDescription: %v", err)
	}
	if !strings.Contains(out, "<description>freshly added</description>") {
		t.Errorf("missing description element: %q", out)
	}
	// state and creationTime must survive verbatim
	if !strings.Contains(out, "<state>running</state>") {
		t.Errorf("state element lost: %q", out)
	}
	if !strings.Contains(out, "<creationTime>1714694400</creationTime>") {
		t.Errorf("creationTime element lost: %q", out)
	}
	desc, created, err := parseSnapshotXML(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if desc != "freshly added" {
		t.Errorf("description = %q", desc)
	}
	if created.IsZero() {
		t.Errorf("created should round-trip, got zero")
	}
}

func TestRewriteSnapshotDescription_ReplacesExistingDescription(t *testing.T) {
	raw := `<domainsnapshot>` +
		`<name>snap-r</name>` +
		`<description>OLD</description>` +
		`<state>running</state>` +
		`<creationTime>1714694400</creationTime>` +
		`</domainsnapshot>`

	out, err := rewriteSnapshotDescription(raw, "NEW")
	if err != nil {
		t.Fatalf("rewriteSnapshotDescription: %v", err)
	}
	if strings.Contains(out, "OLD") {
		t.Errorf("old description not removed: %q", out)
	}
	if !strings.Contains(out, "<description>NEW</description>") {
		t.Errorf("missing replacement description: %q", out)
	}
}

func TestRewriteSnapshotDescription_ClearsDescription(t *testing.T) {
	raw := `<domainsnapshot>` +
		`<name>snap-c</name>` +
		`<description>to be cleared</description>` +
		`<state>running</state>` +
		`</domainsnapshot>`

	out, err := rewriteSnapshotDescription(raw, "")
	if err != nil {
		t.Fatalf("rewriteSnapshotDescription: %v", err)
	}
	if strings.Contains(out, "<description>") {
		t.Errorf("description element should be absent, got: %q", out)
	}
	if !strings.Contains(out, "<state>running</state>") {
		t.Errorf("state element lost when clearing description: %q", out)
	}
}

func TestRewriteSnapshotDescription_PreservesAttributes(t *testing.T) {
	// libvirt's snapshot dumpxml may include attributes on the root element
	// (e.g. xmlns). Make sure attributes survive the round-trip.
	raw := `<domainsnapshot xmlns="http://example/ns" type="external">` +
		`<name>snap-a</name>` +
		`<creationTime>1714694400</creationTime>` +
		`</domainsnapshot>`
	out, err := rewriteSnapshotDescription(raw, "x")
	if err != nil {
		t.Fatalf("rewriteSnapshotDescription: %v", err)
	}
	if !strings.Contains(out, `type="external"`) {
		t.Errorf("type attribute lost: %q", out)
	}
}

func TestRewriteSnapshotDescription_EscapesUnsafeText(t *testing.T) {
	raw := `<domainsnapshot><name>n</name></domainsnapshot>`
	out, err := rewriteSnapshotDescription(raw, "quote: \" & < tag >")
	if err != nil {
		t.Fatalf("rewriteSnapshotDescription: %v", err)
	}
	// Re-parse the rewritten doc — if the description wasn't escaped, the
	// outer XML would be malformed and Unmarshal would fail.
	desc, _, err := parseSnapshotXML(out)
	if err != nil {
		t.Fatalf("re-parse rewritten xml: %v\nxml: %s", err, out)
	}
	if desc != "quote: \" & < tag >" {
		t.Errorf("description = %q", desc)
	}
}

func TestRewriteSnapshotDescription_BadXML(t *testing.T) {
	if _, err := rewriteSnapshotDescription("<not-xml", "x"); err == nil {
		t.Fatal("expected error for malformed xml")
	}
}

func TestSnapshotXMLDoc_RoundTrip(t *testing.T) {
	doc := snapshotXMLDoc{Name: "snap-1", Description: "rolled back to here"}
	buf, err := xml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(buf)
	if !strings.Contains(out, "<name>snap-1</name>") {
		t.Errorf("missing name element: %q", out)
	}
	if !strings.Contains(out, "<description>rolled back to here</description>") {
		t.Errorf("missing description element: %q", out)
	}

	// And parsing it back should yield the same description
	desc, _, err := parseSnapshotXML(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if desc != "rolled back to here" {
		t.Errorf("parsed description = %q", desc)
	}
}
