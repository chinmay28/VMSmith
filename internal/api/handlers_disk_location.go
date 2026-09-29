package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/host"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// Disk placement endpoints: list the configured storage locations with
// live capacity, and move a stopped VM's disk between them.

// filesystemUsage reports total and available bytes for the filesystem
// holding path. Package-level so tests can stub it.
var filesystemUsage = func(path string) (total, free uint64, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	if !info.IsDir() {
		return 0, 0, fmt.Errorf("%s is not a directory", path)
	}
	var fs syscall.Statfs_t
	if err := statFS(path, &fs); err != nil {
		return 0, 0, err
	}
	return fs.Blocks * uint64(fs.Bsize), fs.Bavail * uint64(fs.Bsize), nil
}

// mountInfoPath is the kernel mount table read to discover mounted drives
// as ad-hoc disk locations. Package-level so tests can point it at a
// fixture.
var mountInfoPath = "/proc/self/mountinfo"

// discoverMountPoints lists the host's persistent mount points; failures
// only cost the picker its suggestions, so they are logged and ignored.
func discoverMountPoints() []string {
	mounts, err := host.PersistentMountPoints(mountInfoPath)
	if err != nil {
		logger.Warn("api", "cannot read mount table for storage-location discovery", "path", mountInfoPath, "error", err.Error())
		return nil
	}
	return mounts
}

// traversalWarning reports the first directory on the way to path that
// other users cannot enter (no o+x). QEMU runs as an unprivileged user
// (libvirt-qemu / qemu) and must traverse every parent of a disk image —
// the classic trap is /media/<user>, which udisks creates mode 0750.
func traversalWarning(path string) string {
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0o001 == 0 {
			return fmt.Sprintf("%s (mode %04o) is not traversable by other users; QEMU may be unable to open disks here — chmod o+x it", dir, info.Mode().Perm())
		}
		if dir == "/" {
			return ""
		}
	}
}

// buildStorageLocations assembles the GET /host/storage-locations payload:
// the configured locations (default first), then ad-hoc directories VMs
// could be placed in by path — allowed roots, mount points under them, and
// directories VMs already live in — each with capacity from usage and a
// count of the VMs whose disk lives there (derived from each VM's disk
// path, so legacy VMs are counted too). Discovered directories that do
// not exist and hold no VM are left out.
func buildStorageLocations(cfg config.StorageConfig, vms []*types.VM, mounts []string, usage func(string) (uint64, uint64, error)) []types.StorageLocation {
	counts := map[string]int{}
	candidates := append([]string(nil), mounts...)
	for _, v := range vms {
		if name := cfg.DiskLocationForDiskPath(v.DiskPath); name != "" {
			counts[name]++
			if config.IsDiskLocationPath(name) {
				candidates = append(candidates, name)
			}
		}
	}

	locs := cfg.AllDiskLocations()
	discovered := cfg.DiscoverDiskLocations(candidates)
	out := make([]types.StorageLocation, 0, len(locs)+len(discovered))
	add := func(loc config.DiskLocation, kind string) {
		entry := types.StorageLocation{
			Name:        loc.Name,
			Path:        loc.Path,
			Description: loc.Description,
			Kind:        kind,
			Default:     kind == types.StorageLocationDefault,
			VMCount:     counts[loc.Name],
		}
		if total, free, err := usage(loc.Path); err != nil {
			if kind == types.StorageLocationDiscovered && entry.VMCount == 0 {
				return
			}
			entry.Error = err.Error()
		} else {
			entry.Available = true
			entry.TotalBytes = total
			entry.FreeBytes = free
			entry.Warning = traversalWarning(loc.Path)
		}
		out = append(out, entry)
	}
	for _, loc := range locs {
		kind := types.StorageLocationConfigured
		if loc.Name == config.DefaultDiskLocation {
			kind = types.StorageLocationDefault
		}
		add(loc, kind)
	}
	for _, loc := range discovered {
		add(loc, types.StorageLocationDiscovered)
	}
	return out
}

// ListStorageLocations handles GET /api/v1/host/storage-locations.
func (s *Server) ListStorageLocations(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, buildStorageLocations(s.storageConfig, vms, discoverMountPoints(), filesystemUsage))
}

// validateDiskLocationName rejects a location the daemon will not accept:
// an unknown name, or a path outside storage.disk_location_roots / inside
// a system tree. Whether its directory currently exists is the manager's
// call (disk_location_unavailable), since that is host state, not input.
func (s *Server) validateDiskLocationName(name string) error {
	if _, err := s.storageConfig.ResolveDiskLocation(name); err != nil {
		return types.NewAPIError("invalid_disk_location", err.Error())
	}
	return nil
}

// MoveVMDisk handles POST /api/v1/vms/{vmID}/disk/move. The VM must be
// stopped; cross-filesystem copy progress streams over
// GET /vms/{id}/operations/progress with op "move_disk".
func (s *Server) MoveVMDisk(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")

	var req types.MoveDiskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Location) == "" {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_disk_location", "location is required"))
		return
	}
	if err := s.validateDiskLocationName(req.Location); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	target := config.NormalizeDiskLocationName(req.Location)

	before, err := s.vmManager.Get(r.Context(), id)
	if err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	from := s.storageConfig.DiskLocationForDiskPath(before.DiskPath)

	ctx := r.Context()
	if s.operationProgress != nil {
		ctx = vm.WithDiskMoveProgress(ctx, s.operationProgress.progressCallback(id, "move_disk", target))
		defer s.operationProgress.publish(id, operationProgressMsg{Op: "move_disk", Name: target, Done: true})
	}

	moved, err := s.vmManager.MoveDisk(ctx, id, req.Location)
	if err != nil {
		err = logAndSanitizeManagerError("move vm disk", err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}

	s.publishAppEvent("vm.disk_moved", moved.ID, fmt.Sprintf("VM %q disk moved to location %q", moved.Name, target), map[string]string{
		"from":      from,
		"to":        target,
		"disk_path": moved.DiskPath,
	})
	writeJSON(w, http.StatusOK, moved.RedactConsoleSecrets())
}
