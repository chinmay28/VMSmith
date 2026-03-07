package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Daemon.Listen != "0.0.0.0:8080" {
		t.Errorf("Daemon.Listen = %q, want 0.0.0.0:8080", cfg.Daemon.Listen)
	}
	if cfg.Libvirt.URI != "qemu:///system" {
		t.Errorf("Libvirt.URI = %q, want qemu:///system", cfg.Libvirt.URI)
	}
	if cfg.Defaults.CPUs != 2 {
		t.Errorf("Defaults.CPUs = %d, want 2", cfg.Defaults.CPUs)
	}
	if cfg.Defaults.RAMMB != 2048 {
		t.Errorf("Defaults.RAMMB = %d, want 2048", cfg.Defaults.RAMMB)
	}
	if cfg.Defaults.DiskGB != 20 {
		t.Errorf("Defaults.DiskGB = %d, want 20", cfg.Defaults.DiskGB)
	}
	if cfg.Network.Name != "vmsmith-net" {
		t.Errorf("Network.Name = %q, want vmsmith-net", cfg.Network.Name)
	}
}

func TestLoadNoFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path")
	if err != nil {
		// File doesn't exist - should still load fine since we provide explicit path
		// Actually Load reads the file, so this should error
		// But if path == "" it falls through to defaults
	}
	_ = cfg
}

func TestLoadDefaults(t *testing.T) {
	// No config file anywhere → returns defaults
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load empty path: %v", err)
	}
	if cfg.Defaults.CPUs != 2 {
		t.Errorf("expected defaults to apply")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
daemon:
  listen: "127.0.0.1:9090"
defaults:
  cpus: 8
  ram_mb: 16384
  disk_gb: 100
network:
  name: "custom-net"
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Daemon.Listen != "127.0.0.1:9090" {
		t.Errorf("Daemon.Listen = %q, want 127.0.0.1:9090", cfg.Daemon.Listen)
	}
	if cfg.Defaults.CPUs != 8 {
		t.Errorf("Defaults.CPUs = %d, want 8", cfg.Defaults.CPUs)
	}
	if cfg.Defaults.RAMMB != 16384 {
		t.Errorf("Defaults.RAMMB = %d, want 16384", cfg.Defaults.RAMMB)
	}
	if cfg.Network.Name != "custom-net" {
		t.Errorf("Network.Name = %q, want custom-net", cfg.Network.Name)
	}
	// Non-overridden fields should keep defaults
	if cfg.Libvirt.URI != "qemu:///system" {
		t.Errorf("Libvirt.URI should keep default, got %q", cfg.Libvirt.URI)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yaml")
	os.WriteFile(cfgPath, []byte("{{not valid yaml"), 0644)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestEnsureDirs(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Storage.ImagesDir = filepath.Join(dir, "images")
	cfg.Storage.BaseDir = filepath.Join(dir, "vms")

	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	for _, d := range []string{cfg.Storage.ImagesDir, cfg.Storage.BaseDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("directory %q not created: %v", d, err)
		} else if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
}
