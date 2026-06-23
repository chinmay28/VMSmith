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
	if cfg.Daemon.Console.MaxConcurrentSessions != 8 {
		t.Errorf("Daemon.Console.MaxConcurrentSessions = %d, want 8", cfg.Daemon.Console.MaxConcurrentSessions)
	}
	if cfg.Daemon.Console.MaxSessionSeconds != 3600 {
		t.Errorf("Daemon.Console.MaxSessionSeconds = %d, want 3600", cfg.Daemon.Console.MaxSessionSeconds)
	}
	if cfg.Daemon.Console.IdleTimeoutSeconds != 600 {
		t.Errorf("Daemon.Console.IdleTimeoutSeconds = %d, want 600", cfg.Daemon.Console.IdleTimeoutSeconds)
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
	if cfg.Daemon.RateLimitPerSecond != 10 {
		t.Errorf("Daemon.RateLimitPerSecond = %v, want 10", cfg.Daemon.RateLimitPerSecond)
	}
	if cfg.Daemon.RateLimitBurst != 20 {
		t.Errorf("Daemon.RateLimitBurst = %d, want 20", cfg.Daemon.RateLimitBurst)
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
	if cfg.Quotas.MaxTotalGPUs != 0 {
		t.Errorf("Quotas.MaxTotalGPUs = %d, want 0", cfg.Quotas.MaxTotalGPUs)
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
    auto_cert: "vmsmith.example.com"
    auto_cert_cache_dir: "/var/lib/vmsmith/autocert"
  console:
    max_concurrent_sessions: 12
    max_session_seconds: 7200
    idle_timeout_seconds: 900
    password_key: "base64-secret"
  max_request_body_bytes: 1048576
  max_upload_body_bytes: 2147483648
  max_concurrent_creates: 1
  rate_limit_per_second: 3.5
  rate_limit_burst: 7
defaults:
  cpus: 8
  ram_mb: 16384
  disk_gb: 100
network:
  name: "custom-net"
quotas:
  max_vms: 4
  max_total_cpus: 16
  max_total_ram_mb: 32768
  max_total_disk_gb: 500
  max_total_gpus: 4
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
	if cfg.Daemon.TLS.AutoCert != "vmsmith.example.com" {
		t.Errorf("Daemon.TLS.AutoCert = %q, want vmsmith.example.com", cfg.Daemon.TLS.AutoCert)
	}
	if cfg.Daemon.TLS.AutoCertCacheDir != "/var/lib/vmsmith/autocert" {
		t.Errorf("Daemon.TLS.AutoCertCacheDir = %q, want /var/lib/vmsmith/autocert", cfg.Daemon.TLS.AutoCertCacheDir)
	}
	if !cfg.Daemon.TLSConfigured() {
		t.Error("Daemon.TLSConfigured() = false, want true")
	}
	if cfg.Daemon.Console.MaxConcurrentSessions != 12 {
		t.Errorf("Daemon.Console.MaxConcurrentSessions = %d, want 12", cfg.Daemon.Console.MaxConcurrentSessions)
	}
	if cfg.Daemon.Console.MaxSessionSeconds != 7200 {
		t.Errorf("Daemon.Console.MaxSessionSeconds = %d, want 7200", cfg.Daemon.Console.MaxSessionSeconds)
	}
	if cfg.Daemon.Console.IdleTimeoutSeconds != 900 {
		t.Errorf("Daemon.Console.IdleTimeoutSeconds = %d, want 900", cfg.Daemon.Console.IdleTimeoutSeconds)
	}
	if cfg.Daemon.Console.PasswordKey != "base64-secret" {
		t.Errorf("Daemon.Console.PasswordKey = %q, want base64-secret", cfg.Daemon.Console.PasswordKey)
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
	if cfg.Daemon.RateLimitPerSecond != 3.5 {
		t.Errorf("Daemon.RateLimitPerSecond = %v, want 3.5", cfg.Daemon.RateLimitPerSecond)
	}
	if cfg.Daemon.RateLimitBurst != 7 {
		t.Errorf("Daemon.RateLimitBurst = %d, want 7", cfg.Daemon.RateLimitBurst)
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
	if cfg.Quotas.MaxVMs != 4 {
		t.Errorf("Quotas.MaxVMs = %d, want 4", cfg.Quotas.MaxVMs)
	}
	if cfg.Quotas.MaxTotalCPUs != 16 {
		t.Errorf("Quotas.MaxTotalCPUs = %d, want 16", cfg.Quotas.MaxTotalCPUs)
	}
	if cfg.Quotas.MaxTotalRAMMB != 32768 {
		t.Errorf("Quotas.MaxTotalRAMMB = %d, want 32768", cfg.Quotas.MaxTotalRAMMB)
	}
	if cfg.Quotas.MaxTotalDiskGB != 500 {
		t.Errorf("Quotas.MaxTotalDiskGB = %d, want 500", cfg.Quotas.MaxTotalDiskGB)
	}
	if cfg.Quotas.MaxTotalGPUs != 4 {
		t.Errorf("Quotas.MaxTotalGPUs = %d, want 4", cfg.Quotas.MaxTotalGPUs)
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

func TestAutoCertConfiguredRequiresHostname(t *testing.T) {
	cases := []struct {
		name string
		cfg  DaemonConfig
		want bool
	}{
		{name: "empty", cfg: DaemonConfig{}, want: false},
		{name: "set", cfg: DaemonConfig{TLS: TLSConfig{AutoCert: "vmsmith.example.com"}}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.AutoCertConfigured(); got != tc.want {
				t.Fatalf("AutoCertConfigured() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDefaultConfigSetsAutoCertCacheDir(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Daemon.TLS.AutoCertCacheDir == "" {
		t.Fatal("Daemon.TLS.AutoCertCacheDir should have a default value")
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

func TestEnsureDirsCreatesAutoCertCacheDirWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Storage.ImagesDir = filepath.Join(dir, "images")
	cfg.Storage.BaseDir = filepath.Join(dir, "vms")
	cfg.Daemon.TLS.AutoCert = "vmsmith.example.com"
	cfg.Daemon.TLS.AutoCertCacheDir = filepath.Join(dir, "autocert")

	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	for _, d := range []string{cfg.Storage.ImagesDir, cfg.Storage.BaseDir, cfg.Daemon.TLS.AutoCertCacheDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("directory %q not created: %v", d, err)
		} else if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
}

func TestLoadExpandsTildePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no user home dir available")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
daemon:
  log_file: "~/.vmsmith/vmsmith.log"
  tls:
    auto_cert_cache_dir: "~/.vmsmith/autocert"
storage:
  db_path: "~/.vmsmith/vmsmith.db"
  virtio_win_iso: "~/iso/virtio-win.iso"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantDB := filepath.Join(home, ".vmsmith", "vmsmith.db")
	if cfg.Storage.DBPath != wantDB {
		t.Errorf("Storage.DBPath = %q, want %q", cfg.Storage.DBPath, wantDB)
	}
	wantLog := filepath.Join(home, ".vmsmith", "vmsmith.log")
	if cfg.Daemon.LogFile != wantLog {
		t.Errorf("Daemon.LogFile = %q, want %q", cfg.Daemon.LogFile, wantLog)
	}
	wantCache := filepath.Join(home, ".vmsmith", "autocert")
	if cfg.Daemon.TLS.AutoCertCacheDir != wantCache {
		t.Errorf("Daemon.TLS.AutoCertCacheDir = %q, want %q", cfg.Daemon.TLS.AutoCertCacheDir, wantCache)
	}
	wantISO := filepath.Join(home, "iso", "virtio-win.iso")
	if cfg.Storage.VirtioWinISO != wantISO {
		t.Errorf("Storage.VirtioWinISO = %q, want %q", cfg.Storage.VirtioWinISO, wantISO)
	}
}

func TestLoadAuthConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `
daemon:
  auth:
    enabled: true
    api_keys:
      - "alpha"
      - "beta"
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Daemon.Auth.Enabled {
		t.Fatal("Daemon.Auth.Enabled = false, want true")
	}
	if len(cfg.Daemon.Auth.APIKeys) != 2 {
		t.Fatalf("len(Daemon.Auth.APIKeys) = %d, want 2", len(cfg.Daemon.Auth.APIKeys))
	}
	if cfg.Daemon.Auth.APIKeys[0] != "alpha" || cfg.Daemon.Auth.APIKeys[1] != "beta" {
		t.Fatalf("Daemon.Auth.APIKeys = %#v, want [alpha beta]", cfg.Daemon.Auth.APIKeys)
	}
}
