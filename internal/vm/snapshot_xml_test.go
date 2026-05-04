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

func TestParseUnixSeconds(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"0", 0, false},
		{"1", 1, false},
		{"1714694400", 1714694400, false},
		{" 1714694400 ", 1714694400, false},
		{"abc", 0, true},
		{"12a3", 0, true},
	}
	for _, c := range cases {
		got, err := parseUnixSeconds(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseUnixSeconds(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseUnixSeconds(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseUnixSeconds(%q) = %d, want %d", c.in, got, c.want)
		}
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
