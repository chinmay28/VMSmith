package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDiskLocations(t *testing.T) {
	base := StorageConfig{BaseDir: "/var/lib/vmsmith/vms"}
	cases := []struct {
		name    string
		locs    []DiskLocation
		wantErr string
	}{
		{name: "none", locs: nil},
		{name: "valid", locs: []DiskLocation{{Name: "bulk", Path: "/mnt/bulk"}, {Name: "nvme-1", Path: "/mnt/nvme/"}}},
		{name: "reserved default", locs: []DiskLocation{{Name: "default", Path: "/mnt/x"}}, wantErr: "reserved"},
		{name: "bad name", locs: []DiskLocation{{Name: "Bulk Disk", Path: "/mnt/x"}}, wantErr: "must match"},
		{name: "empty name", locs: []DiskLocation{{Name: "", Path: "/mnt/x"}}, wantErr: "must match"},
		{name: "duplicate name", locs: []DiskLocation{{Name: "a", Path: "/mnt/a"}, {Name: "a", Path: "/mnt/b"}}, wantErr: "duplicate name"},
		{name: "relative path", locs: []DiskLocation{{Name: "a", Path: "mnt/a"}}, wantErr: "absolute"},
		{name: "empty path", locs: []DiskLocation{{Name: "a"}}, wantErr: "absolute"},
		{name: "duplicate path", locs: []DiskLocation{{Name: "a", Path: "/mnt/a"}, {Name: "b", Path: "/mnt/a/"}}, wantErr: "already used"},
		{name: "path equals base dir", locs: []DiskLocation{{Name: "a", Path: "/var/lib/vmsmith/vms"}}, wantErr: "already used by location \"default\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			s.DiskLocations = tc.locs
			err := s.ValidateDiskLocations()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolveDiskLocation(t *testing.T) {
	s := StorageConfig{
		BaseDir:       "/var/lib/vmsmith/vms",
		DiskLocations: []DiskLocation{{Name: "bulk", Path: "/mnt/bulk/", Description: " big disk "}},
	}
	for _, name := range []string{"", "default", " DEFAULT "} {
		loc, err := s.ResolveDiskLocation(name)
		if err != nil || loc.Name != DefaultDiskLocation || loc.Path != "/var/lib/vmsmith/vms" {
			t.Fatalf("ResolveDiskLocation(%q) = %+v, %v", name, loc, err)
		}
	}
	loc, err := s.ResolveDiskLocation(" Bulk")
	if err != nil || loc.Name != "bulk" || loc.Path != "/mnt/bulk" || loc.Description != "big disk" {
		t.Fatalf("ResolveDiskLocation(bulk) = %+v, %v", loc, err)
	}
	if _, err := s.ResolveDiskLocation("nope"); err == nil || !strings.Contains(err.Error(), "bulk") {
		t.Fatalf("unknown location err = %v", err)
	}
}

func TestResolveDiskLocationByPath(t *testing.T) {
	s := StorageConfig{
		BaseDir:           "/var/lib/vmsmith/vms",
		ImagesDir:         "/var/lib/vmsmith/images",
		DiskLocations:     []DiskLocation{{Name: "bulk", Path: "/mnt/bulk"}},
		DiskLocationRoots: DefaultDiskLocationRoots,
	}
	cases := []struct {
		in, wantName, wantErr string
	}{
		{in: "/mnt/bulk/", wantName: "bulk"},
		{in: "/var/lib/vmsmith/vms", wantName: "default"},
		{in: " /media/usb/VMs ", wantName: "/media/usb/VMs"},
		{in: "/mnt/other/../nvme", wantName: "/mnt/nvme"},
		{in: "/var/lib/vmsmith", wantName: "/var/lib/vmsmith"},
		{in: "/srv", wantName: "/srv"},
		{in: "/", wantErr: "filesystem root"},
		{in: "/etc/vms", wantErr: "system directory /etc"},
		{in: "/var/run/x", wantErr: "system directory /var/run"},
		{in: "/mnt/../usr/lib", wantErr: "system directory /usr"},
		{in: "/var/lib/vmsmith/images/x", wantErr: "images_dir"},
		{in: "/var/lib/vmsmith/vms/vm-1", wantErr: `inside location "default"`},
		{in: "/mnt/bulk/sub", wantErr: `inside location "bulk"`},
		{in: "/tmp/vms", wantErr: "not under an allowed root"},
	}
	for _, tc := range cases {
		loc, err := s.ResolveDiskLocation(tc.in)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ResolveDiskLocation(%q) err = %v, want containing %q", tc.in, err, tc.wantErr)
			}
			continue
		}
		if err != nil || loc.Name != tc.wantName {
			t.Errorf("ResolveDiskLocation(%q) = %+v, %v; want name %q", tc.in, loc, err, tc.wantName)
		}
	}

	s.DiskLocationRoots = nil
	if _, err := s.ResolveDiskLocation("/mnt/x"); err == nil || !strings.Contains(err.Error(), "disk_location_roots is empty") {
		t.Fatalf("no roots err = %v", err)
	}
	s.DiskLocationRoots = []string{"/"}
	if _, err := s.ResolveDiskLocation("/tmp/vms"); err != nil {
		t.Fatalf("root / should allow /tmp/vms: %v", err)
	}
	if _, err := s.ResolveDiskLocation("/proc/1"); err == nil {
		t.Fatal("denied trees must stay denied under root /")
	}
}

func TestNormalizeDiskLocationName(t *testing.T) {
	cases := map[string]string{"": "default", " Bulk ": "bulk", "/Mnt/USB/": "/Mnt/USB", " /a//b ": "/a/b"}
	for in, want := range cases {
		if got := NormalizeDiskLocationName(in); got != want {
			t.Errorf("NormalizeDiskLocationName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDiscoverDiskLocations(t *testing.T) {
	s := StorageConfig{
		BaseDir:           "/var/lib/vmsmith/vms",
		DiskLocations:     []DiskLocation{{Name: "bulk", Path: "/mnt/bulk"}},
		DiskLocationRoots: []string{"/mnt", "/media"},
	}
	got := s.DiscoverDiskLocations([]string{"/media/alice/USB", "/mnt/bulk", "/", "/boot/efi", "/mnt/nvme", "/mnt/nvme/", "relative"})
	var paths []string
	for _, l := range got {
		if l.Name != l.Path {
			t.Errorf("discovered location %+v should be named by its path", l)
		}
		paths = append(paths, l.Path)
	}
	want := "/media /media/alice/USB /mnt /mnt/nvme"
	if strings.Join(paths, " ") != want {
		t.Fatalf("discovered = %v, want %s", paths, want)
	}
}

func TestLoadDiskLocationRoots(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cfg, err := Load(write("storage:\n  base_dir: /var/lib/vmsmith/vms\n"))
	if err != nil || strings.Join(cfg.Storage.DiskLocationRoots, ",") != strings.Join(DefaultDiskLocationRoots, ",") {
		t.Fatalf("default roots = %v, %v", cfg.Storage.DiskLocationRoots, err)
	}
	cfg, err = Load(write("storage:\n  disk_location_roots: []\n"))
	if err != nil || len(cfg.Storage.DiskLocationRoots) != 0 {
		t.Fatalf("explicit empty roots = %v, %v", cfg.Storage.DiskLocationRoots, err)
	}
	if _, err := Load(write("storage:\n  disk_location_roots: [mnt]\n")); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative root err = %v", err)
	}
}

func TestAllDiskLocationsDefaultFirst(t *testing.T) {
	s := StorageConfig{
		BaseDir:       "/data/vms",
		DiskLocations: []DiskLocation{{Name: "b", Path: "/b"}, {Name: "a", Path: "/a"}},
	}
	got := s.AllDiskLocations()
	if len(got) != 3 || got[0].Name != "default" || got[1].Name != "b" || got[2].Name != "a" {
		t.Fatalf("AllDiskLocations = %+v", got)
	}
}

func TestDiskLocationForDiskPath(t *testing.T) {
	s := StorageConfig{
		BaseDir:       "/var/lib/vmsmith/vms",
		DiskLocations: []DiskLocation{{Name: "bulk", Path: "/mnt/bulk"}},
	}
	cases := map[string]string{
		"/var/lib/vmsmith/vms/vm-1/disk.qcow2": "default",
		"/mnt/bulk/vm-1/disk.qcow2":            "bulk",
		"/mnt/other/vm-1/disk.qcow2":           "/mnt/other",
		"":                                     "",
	}
	for path, want := range cases {
		if got := s.DiskLocationForDiskPath(path); got != want {
			t.Errorf("DiskLocationForDiskPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestLoadRejectsInvalidDiskLocations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "storage:\n  disk_locations:\n    - name: default\n      path: /mnt/x\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Load error = %v, want reserved-name error", err)
	}
}

func TestLoadExpandsDiskLocationPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "storage:\n  disk_locations:\n    - name: bulk\n      path: ~/bulk\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Storage.DiskLocations[0].Path, filepath.Join(home, "bulk"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
