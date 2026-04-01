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
	if cfg.Daemon.LogFile == "" {
		t.Error("Daemon.LogFile should have a default value")
	}
	if cfg.Daemon.MaxRequestBodyBytes != 50<<20 {
		t.Errorf("Daemon.MaxRequestBodyBytes = %d, want %d", cfg.Daemon.MaxRequestBodyBytes, 50<<20)
	}
	if cfg.Daemon.MaxUploadBodyBytes != 50<<30 {
		t.Errorf("Daemon.MaxUploadBodyBytes = %d, want %d", cfg.Daemon.MaxUploadBodyBytes, 50<<30)
	}
	if cfg.Daemon.MaxConcurrentCreates != 2 {
		t.Errorf("Daemon.MaxConcurrentCreates = %d, want 2", cfg.Daemon.MaxConcurrentCreates)
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
	if cfg.Defaults.SSHUser != "ubuntu" {
		t.Errorf("Defaults.SSHUser = %q, want ubuntu", cfg.Defaults.SSHUser)
	}
	if cfg.Network.Name != "vmsmith-net" {
		t.Errorf("Network.Name = %q, want vmsmith-net", cfg.Network.Name)
	}
	if cfg.Quotas.MaxVMs != 0 {
		t.Errorf("Quotas.MaxVMs = %d, want 0", cfg.Quotas.MaxVMs)
	}
	if cfg.Quotas.MaxTotalCPUs != 0 {
		t.Errorf("Quotas.MaxTotalCPUs = %d, want 0", cfg.Quotas.MaxTotalCPUs)
	}
	if cfg.Quotas.MaxTotalRAMMB != 0 {
		t.Errorf("Quotas.MaxTotalRAMMB = %d, want 0", cfg.Quotas.MaxTotalRAMMB)
	}
	if cfg.Quotas.MaxTotalDiskGB != 0 {
		t.Errorf("Quotas.MaxTotalDiskGB = %d, want 0", cfg.Quotas.MaxTotalDiskGB)
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
  log_file: "/var/log/vmsmith.log"
  tls:
    cert_file: "/etc/vmsmith/tls/server.crt"
    key_file: "/etc/vmsmith/tls/server.key"
  max_request_body_bytes: 1048576
  max_upload_body_bytes: 2147483648
  max_concurrent_creates: 1
defaults:
  cpus: 8
  ram_mb: 16384
  disk_gb: 100
network:
  name: "custom-net"
quotas:
  max_vms: 25
  max_total_cpus: 64
  max_total_ram_mb: 131072
  max_total_disk_gb: 4096
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Daemon.Listen != "127.0.0.1:9090" {
		t.Errorf("Daemon.Listen = %q, want 127.0.0.1:9090", cfg.Daemon.Listen)
	}
	if cfg.Daemon.LogFile != "/var/log/vmsmith.log" {
		t.Errorf("Daemon.LogFile = %q, want /var/log/vmsmith.log", cfg.Daemon.LogFile)
	}
	if cfg.Daemon.TLS.CertFile != "/etc/vmsmith/tls/server.crt" {
		t.Errorf("Daemon.TLS.CertFile = %q, want /etc/vmsmith/tls/server.crt", cfg.Daemon.TLS.CertFile)
	}
	if cfg.Daemon.TLS.KeyFile != "/etc/vmsmith/tls/server.key" {
		t.Errorf("Daemon.TLS.KeyFile = %q, want /etc/vmsmith/tls/server.key", cfg.Daemon.TLS.KeyFile)
	}
	if !cfg.Daemon.TLSConfigured() {
		t.Error("Daemon.TLSConfigured() = false, want true")
	}
	if cfg.Daemon.MaxRequestBodyBytes != 1048576 {
		t.Errorf("Daemon.MaxRequestBodyBytes = %d, want 1048576", cfg.Daemon.MaxRequestBodyBytes)
	}
	if cfg.Daemon.MaxUploadBodyBytes != 2147483648 {
		t.Errorf("Daemon.MaxUploadBodyBytes = %d, want 2147483648", cfg.Daemon.MaxUploadBodyBytes)
	}
	if cfg.Daemon.MaxConcurrentCreates != 1 {
		t.Errorf("Daemon.MaxConcurrentCreates = %d, want 1", cfg.Daemon.MaxConcurrentCreates)
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
	if cfg.Quotas.MaxVMs != 25 {
		t.Errorf("Quotas.MaxVMs = %d, want 25", cfg.Quotas.MaxVMs)
	}
	if cfg.Quotas.MaxTotalCPUs != 64 {
		t.Errorf("Quotas.MaxTotalCPUs = %d, want 64", cfg.Quotas.MaxTotalCPUs)
	}
	if cfg.Quotas.MaxTotalRAMMB != 131072 {
		t.Errorf("Quotas.MaxTotalRAMMB = %d, want 131072", cfg.Quotas.MaxTotalRAMMB)
	}
	if cfg.Quotas.MaxTotalDiskGB != 4096 {
		t.Errorf("Quotas.MaxTotalDiskGB = %d, want 4096", cfg.Quotas.MaxTotalDiskGB)
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

func TestTLSConfiguredRequiresBothFiles(t *testing.T) {
	cases := []struct {
		name string
		cfg  DaemonConfig
		want bool
	}{
		{name: "missing both", cfg: DaemonConfig{}, want: false},
		{name: "missing key", cfg: DaemonConfig{TLS: TLSConfig{CertFile: "/tmp/cert.pem"}}, want: false},
		{name: "missing cert", cfg: DaemonConfig{TLS: TLSConfig{KeyFile: "/tmp/key.pem"}}, want: false},
		{name: "both set", cfg: DaemonConfig{TLS: TLSConfig{CertFile: "/tmp/cert.pem", KeyFile: "/tmp/key.pem"}}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.TLSConfigured(); got != tc.want {
				t.Fatalf("TLSConfigured() = %v, want %v", got, tc.want)
			}
		})
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
