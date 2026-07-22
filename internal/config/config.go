package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the full vmSmith configuration.
type Config struct {
	Daemon    DaemonConfig    `yaml:"daemon"`
	Libvirt   LibvirtConfig   `yaml:"libvirt"`
	Hosts     []HostConfig    `yaml:"hosts"`
	Storage   StorageConfig   `yaml:"storage"`
	Network   NetworkConfig   `yaml:"network"`
	Defaults  DefaultsConfig  `yaml:"defaults"`
	Quotas    QuotasConfig    `yaml:"quotas"`
	Metrics   MetricsConfig   `yaml:"metrics"`
	Events    EventsConfig    `yaml:"events"`
	Webhooks  WebhooksConfig  `yaml:"webhooks"`
	Schedules SchedulesConfig `yaml:"schedules"`
}

// SchedulesConfig controls the recurring VM-action scheduler.
type SchedulesConfig struct {
	// Enabled controls whether the scheduler subsystem runs (default true).
	// When false, the /api/v1/schedules endpoints return 503.
	Enabled bool `yaml:"enabled"`
	// WorkerPoolSize bounds concurrent schedule fires (default 4).
	WorkerPoolSize int `yaml:"worker_pool_size"`
	// QueueSize bounds the dispatch backlog; overflow records a queue_full
	// skip instead of blocking the cron goroutine (default 64).
	QueueSize int `yaml:"queue_size"`
	// MaxRetries is the number of retries after the first attempt for a
	// transient action error (default 2).
	MaxRetries int `yaml:"max_retries"`
	// ActionTimeoutSeconds bounds a single action attempt (default 300).
	ActionTimeoutSeconds int `yaml:"action_timeout_seconds"`
	// MaxCatchUp caps replayed missed fires per schedule on startup (default 100).
	MaxCatchUp int `yaml:"max_catch_up"`
	// TickIntervalSeconds is how often the catch-up cursor is advanced (default 60).
	TickIntervalSeconds int `yaml:"tick_interval_seconds"`
}

type DaemonConfig struct {
	Listen               string        `yaml:"listen"`
	PIDFile              string        `yaml:"pid_file"`
	LogFile              string        `yaml:"log_file"`
	TLS                  TLSConfig     `yaml:"tls"`
	Auth                 AuthConfig    `yaml:"auth"`
	Console              ConsoleConfig `yaml:"console"`
	MaxRequestBodyBytes  int64         `yaml:"max_request_body_bytes"`
	MaxUploadBodyBytes   int64         `yaml:"max_upload_body_bytes"`
	MaxConcurrentCreates int           `yaml:"max_concurrent_creates"`
	RateLimitPerSecond   float64       `yaml:"rate_limit_per_second"`
	RateLimitBurst       int           `yaml:"rate_limit_burst"`
}

type AuthConfig struct {
	Enabled bool `yaml:"enabled"`
	// APIKeys are legacy shared secrets that keep their historical full
	// (admin) access. Prefer Keys for new deployments.
	APIKeys []string `yaml:"api_keys"`
	// Keys are role-scoped API keys (roadmap 3.1.5): each key carries a
	// role — admin (full access, the default), operator (lifecycle verbs +
	// console tickets + run-now on top of read access), or viewer
	// (read-only).
	Keys []APIKeyConfig `yaml:"keys"`
}

// APIKeyConfig is one role-scoped API key (roadmap 3.1.5).
type APIKeyConfig struct {
	Key string `yaml:"key"`
	// Role is admin (default when empty), operator, or viewer.
	Role string `yaml:"role"`
	// Name is an optional operator-facing alias for the key.
	Name string `yaml:"name"`
}

type TLSConfig struct {
	CertFile         string `yaml:"cert_file"`
	KeyFile          string `yaml:"key_file"`
	AutoCert         string `yaml:"auto_cert"`
	AutoCertCacheDir string `yaml:"auto_cert_cache_dir"`
}

type ConsoleConfig struct {
	MaxConcurrentSessions int    `yaml:"max_concurrent_sessions"`
	MaxSessionSeconds     int    `yaml:"max_session_seconds"`
	IdleTimeoutSeconds    int    `yaml:"idle_timeout_seconds"`
	PasswordKey           string `yaml:"password_key"`
}

func (d DaemonConfig) TLSConfigured() bool {
	return d.TLS.CertFile != "" && d.TLS.KeyFile != ""
}

func (d DaemonConfig) AutoCertConfigured() bool {
	return d.TLS.AutoCert != ""
}

func (d DaemonConfig) TLSEnabled() bool {
	return d.TLSConfigured() || d.AutoCertConfigured()
}

type LibvirtConfig struct {
	URI string `yaml:"uri"`
}

// HostConfig describes one additional libvirt host managed by this daemon
// (roadmap 5.5). The daemon always manages the implicit "local" host via
// libvirt.uri; entries here add remote hosts reached through remote
// libvirt URIs (qemu+ssh://..., qemu+tls://...). v1 assumes shared
// storage: storage.images_dir and storage.base_dir must be mounted at the
// same paths on every host (e.g. via NFS) — see docs/MULTI_HOST.md.
type HostConfig struct {
	// Name is the operator-facing host identifier used by `--host`,
	// `spec.host`, and the hosts dashboard. Must be unique; "local" is
	// reserved for the implicit local host.
	Name string `yaml:"name"`
	// URI is the libvirt connection URI for the host,
	// e.g. "qemu+ssh://root@hv2.example.com/system".
	URI string `yaml:"uri"`
	// Description is free-form operator context.
	Description string `yaml:"description"`
}

// LocalHostName is the reserved name of the implicit local host.
const LocalHostName = "local"

// HostNames returns every configured host name including the implicit
// local host (always first).
func (c *Config) HostNames() []string {
	names := []string{LocalHostName}
	for _, h := range c.Hosts {
		names = append(names, h.Name)
	}
	return names
}

// ValidateHosts checks the hosts section for empty/duplicate/reserved
// names and missing URIs.
func (c *Config) ValidateHosts() error {
	seen := map[string]bool{LocalHostName: true}
	for _, h := range c.Hosts {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			return fmt.Errorf("hosts: every host needs a name")
		}
		if name == LocalHostName {
			return fmt.Errorf("hosts: %q is reserved for the implicit local host", LocalHostName)
		}
		if seen[name] {
			return fmt.Errorf("hosts: duplicate host name %q", name)
		}
		seen[name] = true
		if strings.TrimSpace(h.URI) == "" {
			return fmt.Errorf("hosts: host %q needs a libvirt uri", name)
		}
	}
	return nil
}

type StorageConfig struct {
	ImagesDir string `yaml:"images_dir"`
	BaseDir   string `yaml:"base_dir"`
	DBPath    string `yaml:"db_path"`

	// VirtioWinISO is an optional path to the virtio-win driver ISO. When set
	// (or when the well-known location /usr/share/virtio-win/virtio-win.iso
	// exists) it is attached as an extra cdrom to Windows guests so the
	// in-guest installer can load the paravirtual storage/network/balloon
	// drivers. Empty disables the attachment.
	VirtioWinISO string `yaml:"virtio_win_iso"`
}

// DefaultVirtioWinISOPath is the conventional install location for the
// virtio-win driver ISO on RHEL/Fedora (package: virtio-win). It is probed as
// a fallback when StorageConfig.VirtioWinISO is empty.
const DefaultVirtioWinISOPath = "/usr/share/virtio-win/virtio-win.iso"

type NetworkConfig struct {
	Name      string `yaml:"name"`
	Subnet    string `yaml:"subnet"`
	DHCPStart string `yaml:"dhcp_start"`
	DHCPEnd   string `yaml:"dhcp_end"`
}

type DefaultsConfig struct {
	CPUs    int    `yaml:"cpus"`
	RAMMB   int    `yaml:"ram_mb"`
	DiskGB  int    `yaml:"disk_gb"`
	SSHUser string `yaml:"ssh_user"`
}

type QuotasConfig struct {
	MaxVMs            int `yaml:"max_vms"`
	MaxTotalCPUs      int `yaml:"max_total_cpus"`
	MaxTotalRAMMB     int `yaml:"max_total_ram_mb"`
	MaxTotalDiskGB    int `yaml:"max_total_disk_gb"`
	MaxTotalGPUs      int `yaml:"max_total_gpus"`
	MaxSnapshotsPerVM int `yaml:"max_snapshots_per_vm"`
}

// EventsConfig controls the in-process event bus and persistence.
type EventsConfig struct {
	// MaxRecords caps the number of persisted events.  When the count
	// exceeds this value the retention loop deletes the oldest events
	// until it falls back below the cap.  Default 50000.  Zero disables
	// count-based retention.
	MaxRecords int `yaml:"max_records"`
	// MaxAgeSeconds caps the age of persisted events.  Events whose
	// OccurredAt timestamp is older than (now - max_age) are deleted on
	// each retention sweep.  Default 2592000 (30 days).  Zero disables
	// age-based retention.
	MaxAgeSeconds int `yaml:"max_age_seconds"`
	// RetentionInterval is the number of seconds between retention sweeps.
	// Default 60.  Zero disables the retention loop.
	RetentionInterval int `yaml:"retention_interval"`
}

// WebhooksConfig controls the outbound webhook delivery subsystem.
type WebhooksConfig struct {
	// AllowedHosts is a case-insensitive set of hostnames that bypass the
	// SSRF deny-list (loopback, link-local, 169.254.169.254 metadata,
	// VM NAT range).  Intended for test setups; production deployments
	// should leave this empty.
	AllowedHosts []string `yaml:"allowed_hosts"`
}

// MetricsConfig controls the in-process VM resource metrics sampler.
type MetricsConfig struct {
	// Enabled controls whether the metrics sampler runs (default true).
	Enabled bool `yaml:"enabled"`
	// SampleInterval is the number of seconds between libvirt bulk-stats polls (default 10).
	SampleInterval int `yaml:"sample_interval"`
	// HistorySize is the number of samples to retain per VM in the in-memory ring buffer (default 360, ~1 hour at 10 s).
	HistorySize int `yaml:"history_size"`
	// ScrapeListen is an optional separate listen address for the Prometheus /metrics endpoint.
	// When empty the /metrics endpoint is served on the main daemon port.
	ScrapeListen string `yaml:"scrape_listen"`
}

// DefaultConfig returns a Config with sensible defaults.
// Storage paths are placed under /var/lib/vmsmith/ so that the libvirt-qemu
// system user can access them without requiring home-directory traversal.
// The install scripts create this directory with the correct ownership.
func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := "/var/lib/vmsmith"

	return &Config{
		Daemon: DaemonConfig{
			Listen:  "0.0.0.0:8080",
			PIDFile: "/var/run/vmsmith.pid",
			LogFile: filepath.Join(homeDir, ".vmsmith", "vmsmith.log"),
			TLS: TLSConfig{
				AutoCertCacheDir: filepath.Join(homeDir, ".vmsmith", "autocert"),
			},
			Console: ConsoleConfig{
				MaxConcurrentSessions: 8,
				MaxSessionSeconds:     3600,
				IdleTimeoutSeconds:    600,
			},
			MaxRequestBodyBytes:  50 << 20,
			MaxUploadBodyBytes:   50 << 30,
			MaxConcurrentCreates: 2,
			RateLimitPerSecond:   10,
			RateLimitBurst:       20,
		},
		Libvirt: LibvirtConfig{
			URI: "qemu:///system",
		},
		Storage: StorageConfig{
			ImagesDir: filepath.Join(dataDir, "images"),
			BaseDir:   filepath.Join(dataDir, "vms"),
			// Keep the DB in ~/.vmsmith so it stays with the user without root.
			DBPath: filepath.Join(homeDir, ".vmsmith", "vmsmith.db"),
		},
		Network: NetworkConfig{
			Name:      "vmsmith-net",
			Subnet:    "192.168.100.0/24",
			DHCPStart: "192.168.100.10",
			DHCPEnd:   "192.168.100.254",
		},
		Defaults: DefaultsConfig{
			CPUs:    2,
			RAMMB:   2048,
			DiskGB:  20,
			SSHUser: "ubuntu",
		},
		Quotas: QuotasConfig{},
		Metrics: MetricsConfig{
			Enabled:        true,
			SampleInterval: 10,
			HistorySize:    360,
		},
		Events: EventsConfig{
			MaxRecords:        50000,
			MaxAgeSeconds:     30 * 24 * 60 * 60, // 30 days
			RetentionInterval: 60,
		},
		Schedules: SchedulesConfig{
			Enabled:              true,
			WorkerPoolSize:       4,
			QueueSize:            64,
			MaxRetries:           2,
			ActionTimeoutSeconds: 300,
			MaxCatchUp:           100,
			TickIntervalSeconds:  60,
		},
	}
}

// Load reads a config file and merges it with defaults.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path == "" {
		// Try standard locations
		candidates := []string{
			filepath.Join(mustHomeDir(), ".vmsmith", "config.yaml"),
			"/etc/vmsmith/config.yaml",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				path = c
				break
			}
		}
	}

	if path == "" {
		return cfg, nil // No config file found, use defaults
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	cfg.expandPaths()

	return cfg, nil
}

// expandPaths replaces a leading "~" or "~/" in path-typed config fields with
// the current user's home directory. Config files routinely contain values
// like "~/.vmsmith/vmsmith.db" copied from the example file, and the syscalls
// underneath (bolt.Open, os.OpenFile, etc.) do not perform shell expansion.
func (c *Config) expandPaths() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	expand := func(p *string) {
		if p == nil || *p == "" {
			return
		}
		if *p == "~" {
			*p = home
		} else if strings.HasPrefix(*p, "~/") {
			*p = filepath.Join(home, (*p)[2:])
		}
	}
	expand(&c.Daemon.LogFile)
	expand(&c.Daemon.PIDFile)
	expand(&c.Daemon.TLS.CertFile)
	expand(&c.Daemon.TLS.KeyFile)
	expand(&c.Daemon.TLS.AutoCertCacheDir)
	expand(&c.Storage.ImagesDir)
	expand(&c.Storage.BaseDir)
	expand(&c.Storage.DBPath)
	expand(&c.Storage.VirtioWinISO)
}

// EnsureDirs creates all required directories.
func (c *Config) EnsureDirs() error {
	dirs := []string{c.Storage.ImagesDir, c.Storage.BaseDir}
	if c.Storage.DBPath != "" {
		dirs = append(dirs, filepath.Dir(c.Storage.DBPath))
	}
	if c.Daemon.LogFile != "" {
		dirs = append(dirs, filepath.Dir(c.Daemon.LogFile))
	}
	if c.Daemon.AutoCertConfigured() && c.Daemon.TLS.AutoCertCacheDir != "" {
		dirs = append(dirs, c.Daemon.TLS.AutoCertCacheDir)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}

func mustHomeDir() string {
	h, _ := os.UserHomeDir()
	return h
}
