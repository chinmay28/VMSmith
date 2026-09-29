package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
)

func TestResolveInstallISO(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = "/var/lib/vmsmith/images"
	m := &LibvirtManager{cfg: cfg}

	cases := map[string]string{
		"":                          "",
		"  ":                        "",
		"umbrelos.iso":              "/var/lib/vmsmith/images/umbrelos.iso",
		" umbrelos.iso ":            "/var/lib/vmsmith/images/umbrelos.iso",
		"isos/debian.iso":           "/var/lib/vmsmith/images/isos/debian.iso",
		"/srv/isos/debian.iso":      "/srv/isos/debian.iso",
		"/srv/isos/../isos/deb.iso": "/srv/isos/deb.iso",
	}
	for in, want := range cases {
		if got := m.resolveInstallISO(in); got != want {
			t.Errorf("resolveInstallISO(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDropMissingInstallISO(t *testing.T) {
	present := filepath.Join(t.TempDir(), "present.iso")
	if err := os.WriteFile(present, []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}

	params := DomainParams{InstallISO: present, InstallISOTarget: "sdd"}
	dropMissingInstallISO(&params, "vm")
	if params.InstallISO != present {
		t.Errorf("existing ISO was dropped: %+v", params)
	}

	params = DomainParams{InstallISO: filepath.Join(t.TempDir(), "gone.iso"), InstallISOTarget: "sdd"}
	dropMissingInstallISO(&params, "vm")
	if params.InstallISO != "" {
		t.Errorf("missing ISO should be dropped, got %q", params.InstallISO)
	}
	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	// Without the ISO the domain falls back to the plain os-level hd boot.
	if !strings.Contains(xml, "<boot dev='hd'/>") || strings.Contains(xml, "<boot order=") {
		t.Errorf("domain without install ISO should use <boot dev='hd'/> only:\n%s", xml)
	}
}
