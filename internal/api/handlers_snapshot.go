package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type createSnapshotRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
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

// ListSnapshots handles GET /api/v1/vms/{vmID}/snapshots
func (s *Server) ListSnapshots(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	snaps, err := s.vmManager.ListSnapshots(r.Context(), vmID)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	total := len(snaps)
	pagination := parsePagination(r)
	snaps = paginateSlice(snaps, pagination.Page, pagination.PerPage)
	if snaps == nil {
		snaps = []*types.Snapshot{}
	}
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, snaps)
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
