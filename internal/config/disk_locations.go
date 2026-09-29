package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
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

// DefaultDiskLocationRoots are the host directories under which any
// existing directory can be used as a VM disk location by absolute path,
// without declaring it in storage.disk_locations first. They cover the
// usual homes of extra drives and bulk data (/mnt, /media, /srv, /data)
// and the conventional service-data trees (/var, /opt, /home).
var DefaultDiskLocationRoots = []string{"/mnt", "/media", "/var", "/srv", "/opt", "/data", "/home"}

// deniedDiskLocationPrefixes are system trees VM disks may never be placed
// in, whatever storage.disk_location_roots says: virtual filesystems,
// runtime state, boot files, configuration, and the OS itself.
var deniedDiskLocationPrefixes = []string{
	"/proc", "/sys", "/dev", "/run", "/boot", "/etc",
	"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32",
	"/var/run", "/var/lock",
}

// IsDiskLocationPath reports whether a caller-supplied location refers to
// a directory by absolute path rather than to a configured name.
func IsDiskLocationPath(location string) bool {
	return strings.HasPrefix(strings.TrimSpace(location), "/")
}

// NormalizeDiskLocationName canonicalises a caller-supplied location:
// whitespace-trimmed; names lowercased with "" mapped to
// DefaultDiskLocation; absolute paths cleaned but kept case-sensitive.
func NormalizeDiskLocationName(name string) string {
	name = strings.TrimSpace(name)
	if IsDiskLocationPath(name) {
		return filepath.Clean(name)
	}
	name = strings.ToLower(name)
	if name == "" {
		return DefaultDiskLocation
	}
	return name
}

// pathWithin reports whether p is dir or lies beneath it. Both must be
// clean absolute paths.
func pathWithin(p, dir string) bool {
	if dir == "/" {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// AllowedDiskLocationRoots returns the cleaned storage.disk_location_roots.
func (s StorageConfig) AllowedDiskLocationRoots() []string {
	out := make([]string, 0, len(s.DiskLocationRoots))
	for _, r := range s.DiskLocationRoots {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, filepath.Clean(r))
		}
	}
	return out
}

// CheckDiskLocationPath decides, lexically, whether the absolute directory
// p may hold VM disk directories as an ad-hoc location. It must lie under
// one of storage.disk_location_roots, outside the denied system trees and
// storage.images_dir, and must not be nested inside base_dir or a named
// location (pick that location instead). Callers that touch the disk
// should re-check the symlink-resolved path.
func (s StorageConfig) CheckDiskLocationPath(p string) error {
	p = strings.TrimSpace(p)
	if !filepath.IsAbs(p) {
		return fmt.Errorf("disk location path %q must be absolute", p)
	}
	p = filepath.Clean(p)
	if p == "/" {
		return fmt.Errorf("disk location path must not be the filesystem root")
	}
	for _, denied := range deniedDiskLocationPrefixes {
		if pathWithin(p, denied) {
			return fmt.Errorf("disk location path %s is inside the system directory %s", p, denied)
		}
	}
	if s.ImagesDir != "" && pathWithin(p, filepath.Clean(s.ImagesDir)) {
		return fmt.Errorf("disk location path %s is inside storage.images_dir", p)
	}
	for _, loc := range s.AllDiskLocations() {
		if loc.Path != p && pathWithin(p, loc.Path) {
			return fmt.Errorf("disk location path %s is inside location %q (%s); use that location instead", p, loc.Name, loc.Path)
		}
	}
	roots := s.AllowedDiskLocationRoots()
	for _, root := range roots {
		if pathWithin(p, root) {
			return nil
		}
	}
	if len(roots) == 0 {
		return fmt.Errorf("disk location path %s is not allowed: storage.disk_location_roots is empty", p)
	}
	return fmt.Errorf("disk location path %s is not under an allowed root (%s)", p, strings.Join(roots, ", "))
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
	for i, root := range s.DiskLocationRoots {
		if r := strings.TrimSpace(root); r == "" || !filepath.IsAbs(r) {
			return fmt.Errorf("storage.disk_location_roots[%d]: path %q must be absolute", i, root)
		}
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
// whitespace-trimmed; "" means the default location) or by absolute path.
// A path equal to a named location's directory resolves to that location;
// any other path is an ad-hoc location named by its own path, accepted when
// CheckDiskLocationPath allows it. Existence on disk is not checked here.
func (s StorageConfig) ResolveDiskLocation(location string) (DiskLocation, error) {
	want := NormalizeDiskLocationName(location)
	path := IsDiskLocationPath(want)
	for _, loc := range s.AllDiskLocations() {
		if (!path && loc.Name == want) || (path && loc.Path == want) {
			return loc, nil
		}
	}
	if !path {
		names := make([]string, 0, len(s.DiskLocations)+1)
		for _, loc := range s.AllDiskLocations() {
			names = append(names, loc.Name)
		}
		return DiskLocation{}, fmt.Errorf("disk location %q is not configured (known: %s; or pass an absolute directory path)",
			strings.TrimSpace(location), strings.Join(names, ", "))
	}
	if err := s.CheckDiskLocationPath(want); err != nil {
		return DiskLocation{}, err
	}
	return DiskLocation{Name: want, Path: want}, nil
}

// DiskLocationForDiskPath reports which location a VM disk file lives in,
// derived from its path (<location>/<vm-id>/disk.qcow2): the configured
// name when the parent directory is a named location, otherwise that
// directory's path (an ad-hoc location). It is the source of truth for
// legacy VMs created before locations existed. Returns "" for "".
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
	return parent
}

// DiscoverDiskLocations proposes ad-hoc locations for pickers: every
// allowed root plus every candidate directory (typically mount points)
// that CheckDiskLocationPath accepts, minus the named locations'
// directories. Deduplicated and sorted; existence is not checked.
func (s StorageConfig) DiscoverDiskLocations(candidates []string) []DiskLocation {
	named := map[string]bool{}
	for _, loc := range s.AllDiskLocations() {
		named[loc.Path] = true
	}
	seen := map[string]bool{}
	var out []DiskLocation
	add := func(p, desc string) {
		if !filepath.IsAbs(p) {
			return
		}
		p = filepath.Clean(p)
		if seen[p] || named[p] || s.CheckDiskLocationPath(p) != nil {
			return
		}
		seen[p] = true
		out = append(out, DiskLocation{Name: p, Path: p, Description: desc})
	}
	for _, c := range candidates {
		add(c, "mount point")
	}
	for _, r := range s.AllowedDiskLocationRoots() {
		add(r, "allowed root")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
