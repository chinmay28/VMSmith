package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the full vmSmith configuration.
type Config struct {
	Daemon  DaemonConfig  `yaml:"daemon"`
	Libvirt LibvirtConfig `yaml:"libvirt"`
	Storage StorageConfig `yaml:"storage"`
	Network NetworkConfig `yaml:"network"`
	Defaults DefaultsConfig `yaml:"defaults"`
}

type DaemonConfig struct {
	Listen  string `yaml:"listen"`
	PIDFile string `yaml:"pid_file"`
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
	CPUs   int `yaml:"cpus"`
	RAMMB  int `yaml:"ram_mb"`
	DiskGB int `yaml:"disk_gb"`
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
			CPUs:   2,
			RAMMB:  2048,
			DiskGB: 20,
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
