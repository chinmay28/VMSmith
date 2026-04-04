package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the full vmSmith configuration.
type Config struct {
	Daemon   DaemonConfig   `yaml:"daemon"`
	Libvirt  LibvirtConfig  `yaml:"libvirt"`
	Storage  StorageConfig  `yaml:"storage"`
	Network  NetworkConfig  `yaml:"network"`
	Defaults DefaultsConfig `yaml:"defaults"`
	Quotas   QuotasConfig   `yaml:"quotas"`
}

type DaemonConfig struct {
	Listen               string     `yaml:"listen"`
	PIDFile              string     `yaml:"pid_file"`
	LogFile              string     `yaml:"log_file"`
	TLS                  TLSConfig  `yaml:"tls"`
	Auth                 AuthConfig `yaml:"auth"`
	MaxRequestBodyBytes  int64      `yaml:"max_request_body_bytes"`
	MaxUploadBodyBytes   int64      `yaml:"max_upload_body_bytes"`
	MaxConcurrentCreates int        `yaml:"max_concurrent_creates"`
	RateLimitPerSecond   float64    `yaml:"rate_limit_per_second"`
	RateLimitBurst       int        `yaml:"rate_limit_burst"`
}

type AuthConfig struct {
	Enabled bool     `yaml:"enabled"`
	APIKeys []string `yaml:"api_keys"`
}

type TLSConfig struct {
	CertFile         string `yaml:"cert_file"`
	KeyFile          string `yaml:"key_file"`
	AutoCert         string `yaml:"auto_cert"`
	AutoCertCacheDir string `yaml:"auto_cert_cache_dir"`
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

type StorageConfig struct {
	ImagesDir string `yaml:"images_dir"`
	BaseDir   string `yaml:"base_dir"`
	DBPath    string `yaml:"db_path"`
}

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
	MaxVMs         int `yaml:"max_vms"`
	MaxTotalCPUs   int `yaml:"max_total_cpus"`
	MaxTotalRAMMB  int `yaml:"max_total_ram_mb"`
	MaxTotalDiskGB int `yaml:"max_total_disk_gb"`
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
			Listen:               "0.0.0.0:8080",
			PIDFile:              "/var/run/vmsmith.pid",
			LogFile:              filepath.Join(homeDir, ".vmsmith", "vmsmith.log"),
			TLS: TLSConfig{
				AutoCertCacheDir: filepath.Join(homeDir, ".vmsmith", "autocert"),
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

	return cfg, nil
}

// EnsureDirs creates all required directories.
func (c *Config) EnsureDirs() error {
	dirs := []string{c.Storage.ImagesDir, c.Storage.BaseDir}
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
