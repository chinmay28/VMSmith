package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// CreateVM handles POST /api/v1/vms
func (s *Server) CreateVM(w http.ResponseWriter, r *http.Request) {
	var spec types.VMSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	vm, err := s.vmManager.Create(r.Context(), spec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, vm)
}

// ListVMs handles GET /api/v1/vms
func (s *Server) ListVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vms)
}

// GetVM handles GET /api/v1/vms/{vmID}
func (s *Server) GetVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	vm, err := s.vmManager.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vm)
}

// DeleteVM handles DELETE /api/v1/vms/{vmID}
func (s *Server) DeleteVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Delete(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// StartVM handles POST /api/v1/vms/{vmID}/start
func (s *Server) StartVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Start(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// StopVM handles POST /api/v1/vms/{vmID}/stop
func (s *Server) StopVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Stop(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}
