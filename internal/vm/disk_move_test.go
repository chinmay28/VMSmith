package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestRewriteDiskDirInXML(t *testing.T) {
	doc := `<domainsnapshot><domain><devices>
<disk><source file='/var/lib/vmsmith/vms/vm-1/disk.qcow2'/></disk>
<disk><source file='/var/lib/vmsmith/vms/vm-1/cidata.iso'/></disk>
<disk><source file='/var/lib/vmsmith/vms/vm-10/disk.qcow2'/></disk>
<disk><source file='/var/lib/vmsmith/images/rocky9.qcow2'/></disk>
</devices></domain></domainsnapshot>`
	got := rewriteDiskDirInXML(doc, "/var/lib/vmsmith/vms/vm-1", "/mnt/bulk/vm-1/")
	for _, want := range []string{
		"/mnt/bulk/vm-1/disk.qcow2",
		"/mnt/bulk/vm-1/cidata.iso",
		"/var/lib/vmsmith/vms/vm-10/disk.qcow2", // prefix sibling untouched
		"/var/lib/vmsmith/images/rocky9.qcow2",  // backing image untouched
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten xml missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/var/lib/vmsmith/vms/vm-1/") {
		t.Errorf("old dir still referenced:\n%s", got)
	}
}

func TestRewriteDiskDirInXMLEscaped(t *testing.T) {
	doc := `<disk><source file='/data/a&amp;b/vm-1/disk.qcow2'/></disk>`
	got := rewriteDiskDirInXML(doc, "/data/a&b/vm-1", "/mnt/c&d/vm-1")
	if want := `/mnt/c&amp;d/vm-1/disk.qcow2`; !strings.Contains(got, want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestRewriteDiskDirInXMLNoMatchIsIdentity(t *testing.T) {
	doc := `<domainsnapshot><name>s1</name></domainsnapshot>`
	if got := rewriteDiskDirInXML(doc, "/a/vm-1", "/b/vm-1"); got != doc {
		t.Fatalf("got %s", got)
	}
}

func TestStoredDiskLocationName(t *testing.T) {
	cases := map[string]string{"": "", "default": "", " Default ": "", "Bulk": "bulk", " nvme ": "nvme", " /Mnt/USB/ ": "/Mnt/USB"}
	for in, want := range cases {
		if got := storedDiskLocationName(in); got != want {
			t.Errorf("storedDiskLocationName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDiskLocationTypedErrors(t *testing.T) {
	root := t.TempDir()
	cfg := config.StorageConfig{
		BaseDir: root,
		DiskLocations: []config.DiskLocation{
			{Name: "present", Path: root},
			{Name: "unmounted", Path: filepath.Join(root, "missing")},
		},
	}
	if loc, err := ResolveDiskLocation(cfg, ""); err != nil || loc.Name != "default" {
		t.Fatalf("default: %+v %v", loc, err)
	}
	if loc, err := ResolveDiskLocation(cfg, "Present"); err != nil || loc.Path != root {
		t.Fatalf("present: %+v %v", loc, err)
	}

	_, err := ResolveDiskLocation(cfg, "nope")
	if apiErr, ok := err.(*types.APIError); !ok || apiErr.Code != "invalid_disk_location" || !strings.Contains(apiErr.Message, "present") {
		t.Fatalf("unknown location err = %v", err)
	}
	_, err = ResolveDiskLocation(cfg, "unmounted")
	if apiErr, ok := err.(*types.APIError); !ok || apiErr.Code != "disk_location_unavailable" {
		t.Fatalf("missing dir err = %v", err)
	}

	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.DiskLocations = append(cfg.DiskLocations, config.DiskLocation{Name: "file", Path: file})
	if _, err := ResolveDiskLocation(cfg, "file"); err == nil {
		t.Fatal("a regular file resolved as a location")
	}
}

func TestResolveDiskLocationByPath(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{filepath.Join(allowed, "usb"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.StorageConfig{BaseDir: filepath.Join(root, "vms"), DiskLocationRoots: []string{allowed}}

	loc, err := ResolveDiskLocation(cfg, filepath.Join(allowed, "usb")+"/")
	if err != nil || loc.Name != filepath.Join(allowed, "usb") || loc.Path != loc.Name {
		t.Fatalf("path location = %+v, %v", loc, err)
	}
	if storedDiskLocationName(loc.Name) != loc.Name {
		t.Fatalf("stored name of a path location must be the path, got %q", storedDiskLocationName(loc.Name))
	}

	_, err = ResolveDiskLocation(cfg, filepath.Join(allowed, "missing"))
	if apiErr, ok := err.(*types.APIError); !ok || apiErr.Code != "disk_location_unavailable" {
		t.Fatalf("missing dir err = %v", err)
	}
	_, err = ResolveDiskLocation(cfg, outside)
	if apiErr, ok := err.(*types.APIError); !ok || apiErr.Code != "invalid_disk_location" {
		t.Fatalf("outside roots err = %v", err)
	}

	// A symlink under an allowed root must not smuggle disks elsewhere...
	escape := filepath.Join(allowed, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveDiskLocation(cfg, escape)
	if apiErr, ok := err.(*types.APIError); !ok || apiErr.Code != "invalid_disk_location" || !strings.Contains(apiErr.Message, "resolves to") {
		t.Fatalf("symlink escape err = %v", err)
	}
	// ...while one that stays inside resolves to (and is stored as) its target.
	inside := filepath.Join(allowed, "link")
	if err := os.Symlink(filepath.Join(allowed, "usb"), inside); err != nil {
		t.Fatal(err)
	}
	if loc, err := ResolveDiskLocation(cfg, inside); err != nil || loc.Path != filepath.Join(allowed, "usb") {
		t.Fatalf("inside symlink = %+v, %v", loc, err)
	}
}
