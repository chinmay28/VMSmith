package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// CreateVM handles POST /api/v1/vms
func (s *Server) CreateVM(w http.ResponseWriter, r *http.Request) {
	var spec types.VMSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		if isRequestTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if err := validateVMSpec(spec); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	release, ok := s.acquireCreateSlot()
	if !ok {
		writeAPIError(w, http.StatusTooManyRequests, types.NewAPIError("create_limit_reached", "too many VM create operations in progress; retry once an existing create finishes"))
		return
	}
	if release != nil {
		defer release()
	}

	vm, err := s.vmManager.Create(r.Context(), spec)
	if err != nil {
		err = sanitizeManagerError(err)
		status := http.StatusInternalServerError
		if apiErr, ok := err.(*types.APIError); ok {
			switch apiErr.Code {
			case "resource_not_found":
				status = http.StatusNotFound
			case "invalid_name", "invalid_image", "invalid_spec", "disk_shrink_not_allowed":
				status = http.StatusBadRequest
			}
		}
		writeAPIError(w, status, err)
		return
	}

	writeJSON(w, http.StatusCreated, vm)
}

// UpdateVM handles PATCH /api/v1/vms/{vmID}
// It stops the VM if running, applies CPU/RAM/disk changes, then restarts it.
func (s *Server) UpdateVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	var patch types.VMUpdateSpec
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		if isRequestTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := validateVMUpdateSpec(patch); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	vm, err := s.vmManager.Update(r.Context(), id, patch)
	if err != nil {
		err = sanitizeManagerError(err)
		status := http.StatusInternalServerError
		if apiErr, ok := err.(*types.APIError); ok {
			switch apiErr.Code {
			case "resource_not_found":
				status = http.StatusNotFound
			case "invalid_spec", "disk_shrink_not_allowed":
				status = http.StatusBadRequest
			}
		}
		writeAPIError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, vm)
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
		writeAPIError(w, http.StatusNotFound, sanitizeManagerError(err))
		return
	}
	writeJSON(w, http.StatusOK, vm)
}

// DeleteVM handles DELETE /api/v1/vms/{vmID}
func (s *Server) DeleteVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Delete(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		status := http.StatusInternalServerError
		if apiErr, ok := err.(*types.APIError); ok && apiErr.Code == "resource_not_found" {
			status = http.StatusNotFound
		}
		writeAPIError(w, status, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// StartVM handles POST /api/v1/vms/{vmID}/start
func (s *Server) StartVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Start(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		status := http.StatusInternalServerError
		if apiErr, ok := err.(*types.APIError); ok && apiErr.Code == "resource_not_found" {
			status = http.StatusNotFound
		}
		writeAPIError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// StopVM handles POST /api/v1/vms/{vmID}/stop
func (s *Server) StopVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Stop(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		status := http.StatusInternalServerError
		if apiErr, ok := err.(*types.APIError); ok && apiErr.Code == "resource_not_found" {
			status = http.StatusNotFound
		}
		writeAPIError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) acquireCreateSlot() (func(), bool) {
	if s.createTokens == nil {
		return nil, true
	}

	select {
	case <-s.createTokens:
		return func() {
			s.createTokens <- struct{}{}
		}, true
	default:
		logger.Warn("api", "rejecting VM create due to concurrent create limit", "limit", itoa(s.maxConcurrentCreates))
		return nil, false
	}
}
