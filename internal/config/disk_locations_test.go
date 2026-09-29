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
		loc, ok := s.ResolveDiskLocation(name)
		if !ok || loc.Name != DefaultDiskLocation || loc.Path != "/var/lib/vmsmith/vms" {
			t.Fatalf("ResolveDiskLocation(%q) = %+v, %v", name, loc, ok)
		}
	}
	loc, ok := s.ResolveDiskLocation(" Bulk")
	if !ok || loc.Name != "bulk" || loc.Path != "/mnt/bulk" || loc.Description != "big disk" {
		t.Fatalf("ResolveDiskLocation(bulk) = %+v, %v", loc, ok)
	}
	if _, ok := s.ResolveDiskLocation("nope"); ok {
		t.Fatal("unknown location resolved")
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
		"/mnt/other/vm-1/disk.qcow2":           "",
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
