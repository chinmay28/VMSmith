package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/config"
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

// buildStorageLocations assembles the GET /host/storage-locations payload:
// every configured location (default first) with capacity from usage and a
// count of the VMs whose disk currently lives there (derived from each
// VM's disk path, so legacy VMs are counted correctly too).
func buildStorageLocations(cfg config.StorageConfig, vms []*types.VM, usage func(string) (uint64, uint64, error)) []types.StorageLocation {
	counts := map[string]int{}
	for _, v := range vms {
		if name := cfg.DiskLocationForDiskPath(v.DiskPath); name != "" {
			counts[name]++
		}
	}

	locs := cfg.AllDiskLocations()
	out := make([]types.StorageLocation, 0, len(locs))
	for _, loc := range locs {
		entry := types.StorageLocation{
			Name:        loc.Name,
			Path:        loc.Path,
			Description: loc.Description,
			Default:     loc.Name == config.DefaultDiskLocation,
			VMCount:     counts[loc.Name],
		}
		if total, free, err := usage(loc.Path); err != nil {
			entry.Error = err.Error()
		} else {
			entry.Available = true
			entry.TotalBytes = total
			entry.FreeBytes = free
		}
		out = append(out, entry)
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
	writeJSON(w, http.StatusOK, buildStorageLocations(s.storageConfig, vms, filesystemUsage))
}

// validateDiskLocationName rejects a location name the daemon does not
// know about. Whether its directory currently exists is the manager's
// call (disk_location_unavailable), since that is host state, not input.
func (s *Server) validateDiskLocationName(name string) error {
	if _, ok := s.storageConfig.ResolveDiskLocation(name); ok {
		return nil
	}
	names := make([]string, 0, len(s.storageConfig.DiskLocations)+1)
	for _, loc := range s.storageConfig.AllDiskLocations() {
		names = append(names, loc.Name)
	}
	return types.NewAPIError("invalid_disk_location",
		fmt.Sprintf("disk location %q is not configured (known: %s)", strings.TrimSpace(name), strings.Join(names, ", ")))
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
