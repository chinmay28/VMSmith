package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type createSnapshotRequest struct {
	Name string `json:"name"`
}

// CreateSnapshot handles POST /api/v1/vms/{vmID}/snapshots
func (s *Server) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	var req createSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateCreateSnapshotRequest(req.Name); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	snap, err := s.vmManager.CreateSnapshot(r.Context(), vmID, req.Name)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

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

	w.WriteHeader(http.StatusNoContent)
}
