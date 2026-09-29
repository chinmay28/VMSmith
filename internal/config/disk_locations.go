package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultDiskLocation is the reserved name of the implicit disk location
// backed by storage.base_dir. It is always present and cannot be declared
// in storage.disk_locations.
const DefaultDiskLocation = "default"

// DiskLocation is a named directory on the host that VM disk directories
// can live under (e.g. a second physical drive mounted at /mnt/bulk). A VM
// placed in a location keeps all of its per-VM files (system disk,
// provisioning ISO) in <path>/<vm-id>/.
type DiskLocation struct {
	Name        string `yaml:"name"`
	Path        string `yaml:"path"`
	Description string `yaml:"description,omitempty"`
}

// diskLocationNameRe bounds location names to a lowercase slug so they are
// safe in URLs, CLI flags, and log lines.
var diskLocationNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// NormalizeDiskLocationName canonicalises a caller-supplied location name:
// whitespace-trimmed, lowercased, and "" mapped to DefaultDiskLocation.
func NormalizeDiskLocationName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return DefaultDiskLocation
	}
	return name
}

// ValidateDiskLocations checks storage.disk_locations: every entry needs a
// slug name (not the reserved "default"), an absolute path, and both the
// name and the path must be unique — including against storage.base_dir,
// which backs the implicit default location.
func (s StorageConfig) ValidateDiskLocations() error {
	names := map[string]bool{DefaultDiskLocation: true}
	paths := map[string]string{}
	if s.BaseDir != "" {
		paths[filepath.Clean(s.BaseDir)] = DefaultDiskLocation
	}
	for i, loc := range s.DiskLocations {
		name := strings.TrimSpace(loc.Name)
		if !diskLocationNameRe.MatchString(name) {
			return fmt.Errorf("storage.disk_locations[%d]: name %q must match %s", i, loc.Name, diskLocationNameRe.String())
		}
		if names[name] {
			if name == DefaultDiskLocation {
				return fmt.Errorf("storage.disk_locations[%d]: %q is reserved for storage.base_dir", i, name)
			}
			return fmt.Errorf("storage.disk_locations[%d]: duplicate name %q", i, name)
		}
		names[name] = true

		p := strings.TrimSpace(loc.Path)
		if p == "" || !filepath.IsAbs(p) {
			return fmt.Errorf("storage.disk_locations[%d] (%s): path %q must be absolute", i, name, loc.Path)
		}
		p = filepath.Clean(p)
		if other, dup := paths[p]; dup {
			return fmt.Errorf("storage.disk_locations[%d] (%s): path %q is already used by location %q", i, name, p, other)
		}
		paths[p] = name
	}
	return nil
}

// AllDiskLocations returns every location VMs can be placed in, the
// implicit default (storage.base_dir) first, then the configured entries in
// declaration order. Names and paths are returned in canonical form.
func (s StorageConfig) AllDiskLocations() []DiskLocation {
	out := make([]DiskLocation, 0, len(s.DiskLocations)+1)
	out = append(out, DiskLocation{
		Name:        DefaultDiskLocation,
		Path:        filepath.Clean(s.BaseDir),
		Description: "storage.base_dir",
	})
	for _, loc := range s.DiskLocations {
		out = append(out, DiskLocation{
			Name:        strings.TrimSpace(loc.Name),
			Path:        filepath.Clean(strings.TrimSpace(loc.Path)),
			Description: strings.TrimSpace(loc.Description),
		})
	}
	return out
}

// ResolveDiskLocation looks up a location by name (case-insensitive,
// whitespace-trimmed; "" means the default location).
func (s StorageConfig) ResolveDiskLocation(name string) (DiskLocation, bool) {
	want := NormalizeDiskLocationName(name)
	for _, loc := range s.AllDiskLocations() {
		if loc.Name == want {
			return loc, true
		}
	}
	return DiskLocation{}, false
}

// DiskLocationForDiskPath reports which configured location a VM disk file
// lives in, derived from its path (<location>/<vm-id>/disk.qcow2). It is
// the source of truth for legacy VMs created before locations existed.
// Returns "" when the disk lives outside every configured location.
func (s StorageConfig) DiskLocationForDiskPath(diskPath string) string {
	if diskPath == "" {
		return ""
	}
	parent := filepath.Dir(filepath.Dir(filepath.Clean(diskPath)))
	for _, loc := range s.AllDiskLocations() {
		if loc.Path == parent {
			return loc.Name
		}
	}
	return ""
}
