package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type createSnapshotRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// updateSnapshotRequest carries the editable metadata for an existing
// snapshot. Description is a pointer so the caller can distinguish between
// "omit" (leave untouched) and "set to empty string" (clear). Today only
// description is editable; libvirt has no in-place rename for snapshots, so
// adding a Name field would require a copy + delete of the underlying disk
// state and is intentionally out of scope.
type updateSnapshotRequest struct {
	Description *string `json:"description,omitempty"`
}

// bulkDeleteSnapshotsRequest selects snapshots to delete in a single batch.
//
// Exactly one of Names or Prefix must be set.  When Prefix is set, every
// snapshot returned by ListSnapshots whose Name starts with that prefix is
// targeted; this is the cheap way to clean up a series of automated snapshots
// (e.g. all "auto-nightly-*" snapshots) without enumerating them client-side.
type bulkDeleteSnapshotsRequest struct {
	Names  []string `json:"names,omitempty"`
	Prefix string   `json:"prefix,omitempty"`
}

type bulkDeleteSnapshotResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type bulkDeleteSnapshotsResponse struct {
	Results []bulkDeleteSnapshotResult `json:"results"`
}

// CreateSnapshot handles POST /api/v1/vms/{vmID}/snapshots
func (s *Server) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	var req createSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if err := validateCreateSnapshotRequest(req.Name, req.Description); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	spec := types.SnapshotSpec{Name: req.Name, Description: req.Description}
	snap, err := s.vmManager.CreateSnapshot(r.Context(), vmID, spec)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	attrs := map[string]string{"snapshot": req.Name}
	if req.Description != "" {
		attrs["description"] = req.Description
	}
	s.publishAppEvent("snapshot.created", vmID, "snapshot "+req.Name+" created", attrs)

	writeJSON(w, http.StatusCreated, snap)
}

// ListSnapshots handles GET /api/v1/vms/{vmID}/snapshots.
//
// Optional query params:
//   - search=<needle>            case-insensitive substring filter on name and
//     description. Applied before sort + pagination so X-Total-Count reflects
//     the post-search population.
//   - sort=<id|name|created_at>  default id; case-insensitive
//   - order=<asc|desc>           default asc
//   - page / per_page (see parsePagination)
func (s *Server) ListSnapshots(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	sortField, order, err := parseSnapshotSort(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	snaps, err := s.vmManager.ListSnapshots(r.Context(), vmID)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	searchFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	if searchFilter != "" {
		filtered := snaps[:0]
		for _, snap := range snaps {
			if types.SnapshotMatchesSearch(snap, searchFilter) {
				filtered = append(filtered, snap)
			}
		}
		snaps = filtered
	}

	types.SortSnapshots(snaps, sortField, order)

	total := len(snaps)
	pagination := parsePagination(r)
	snaps = paginateSlice(snaps, pagination.Page, pagination.PerPage)
	if snaps == nil {
		snaps = []*types.Snapshot{}
	}
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, snaps)
}

// UpdateSnapshot handles PATCH /api/v1/vms/{vmID}/snapshots/{snapName}.
//
// Currently only the snapshot description is editable; the underlying
// disk/memory state and parent pointer are immutable. A nil Description means
// "leave as-is"; an explicit empty string clears the description.
func (s *Server) UpdateSnapshot(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")
	snapName := chi.URLParam(r, "snapName")

	var req updateSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if err := validateUpdateSnapshotRequest(req.Description); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	patch := types.SnapshotUpdateSpec{Description: req.Description}
	snap, err := s.vmManager.UpdateSnapshot(r.Context(), vmID, snapName, patch)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	attrs := map[string]string{"snapshot": snapName}
	if req.Description != nil {
		attrs["description"] = strings.TrimSpace(*req.Description)
	}
	s.publishAppEvent("snapshot.updated", vmID, "snapshot "+snapName+" updated", attrs)

	writeJSON(w, http.StatusOK, snap)
}

// RestoreSnapshot handles POST /api/v1/vms/{vmID}/snapshots/{snapName}/restore
func (s *Server) RestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")
	snapName := chi.URLParam(r, "snapName")

	if err := s.vmManager.RestoreSnapshot(r.Context(), vmID, snapName); err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	s.publishAppEvent("snapshot.restored", vmID, "snapshot "+snapName+" restored", map[string]string{
		"snapshot": snapName,
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

// DeleteSnapshot handles DELETE /api/v1/vms/{vmID}/snapshots/{snapName}
func (s *Server) DeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")
	snapName := chi.URLParam(r, "snapName")

	if err := s.vmManager.DeleteSnapshot(r.Context(), vmID, snapName); err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	s.publishAppEvent("snapshot.deleted", vmID, "snapshot "+snapName+" deleted", map[string]string{
		"snapshot": snapName,
	})

	w.WriteHeader(http.StatusNoContent)
}

// BulkDeleteSnapshots handles POST /api/v1/vms/{vmID}/snapshots/bulk_delete.
//
// Accepts either an explicit list of snapshot names ("names") or a prefix
// match against the VM's existing snapshots ("prefix").  Returns a per-target
// result list so partial failures (one snapshot missing, the rest succeeded)
// surface in a single response — mirroring the bulk VM action shape.
func (s *Server) BulkDeleteSnapshots(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	var req bulkDeleteSnapshotsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}

	prefix := strings.TrimSpace(req.Prefix)
	cleanedNames := make([]string, 0, len(req.Names))
	for _, n := range req.Names {
		if t := strings.TrimSpace(n); t != "" {
			cleanedNames = append(cleanedNames, t)
		}
	}

	if prefix == "" && len(cleanedNames) == 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"exactly one of names or prefix must be provided"))
		return
	}
	if prefix != "" && len(cleanedNames) > 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"names and prefix are mutually exclusive"))
		return
	}

	targets := cleanedNames
	if prefix != "" {
		snaps, err := s.vmManager.ListSnapshots(r.Context(), vmID)
		if err != nil {
			apiErr := sanitizeManagerError(err)
			writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
			return
		}
		for _, snap := range snaps {
			if strings.HasPrefix(snap.Name, prefix) {
				targets = append(targets, snap.Name)
			}
		}
	}

	results := make([]bulkDeleteSnapshotResult, 0, len(targets))
	for _, name := range targets {
		if err := s.vmManager.DeleteSnapshot(r.Context(), vmID, name); err != nil {
			err = sanitizeManagerError(err)
			result := bulkDeleteSnapshotResult{Name: name, Success: false}
			if apiErr, ok := err.(*types.APIError); ok {
				result.Code = apiErr.Code
				result.Message = apiErr.Message
			} else {
				result.Message = err.Error()
			}
			results = append(results, result)
			continue
		}
		results = append(results, bulkDeleteSnapshotResult{Name: name, Success: true})
		s.publishAppEvent("snapshot.deleted", vmID, "snapshot "+name+" deleted", map[string]string{
			"snapshot": name,
			"bulk":     "true",
		})
	}

	writeJSON(w, http.StatusOK, bulkDeleteSnapshotsResponse{Results: results})
}
