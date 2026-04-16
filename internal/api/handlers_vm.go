package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

var supportedBulkVMActions = map[string]func(*Server, *http.Request, string) error{
	"start": func(s *Server, r *http.Request, id string) error {
		return s.vmManager.Start(r.Context(), id)
	},
	"stop": func(s *Server, r *http.Request, id string) error {
		return s.vmManager.Stop(r.Context(), id)
	},
	"delete": func(s *Server, r *http.Request, id string) error {
		return s.vmManager.Delete(r.Context(), id)
	},
}

type bulkVMActionRequest struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids"`
}

type bulkVMActionResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type bulkVMActionResponse struct {
	Action  string               `json:"action"`
	Results []bulkVMActionResult `json:"results"`
}

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
	if tags, err := normalizeTags(spec.Tags); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	} else {
		spec.Tags = tags
		spec.Description = strings.TrimSpace(spec.Description)
		spec.Name = strings.TrimSpace(spec.Name)
	}

	existingVMs, err := s.vmManager.List(r.Context())
	if err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	if err := validateUniqueVMName(spec.Name, existingVMs); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	if err := s.enforceCreateQuotas(r.Context(), spec); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
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
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
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
	if tags, err := normalizeTags(patch.Tags); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	} else if patch.Tags != nil {
		patch.Tags = tags
	}
	patch.Description = strings.TrimSpace(patch.Description)

	current, err := s.vmManager.Get(r.Context(), id)
	if err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusNotFound), err)
		return
	}
	if err := s.enforceUpdateQuotas(r.Context(), current, patch); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}

	vm, err := s.vmManager.Update(r.Context(), id, patch)
	if err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, vm)
}

// ListVMs handles GET /api/v1/vms
func (s *Server) ListVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	tagFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("tag")))
	statusFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	if tagFilter != "" || statusFilter != "" {
		filtered := make([]*types.VM, 0, len(vms))
		for _, vm := range vms {
			if statusFilter != "" && !strings.EqualFold(string(vm.State), statusFilter) {
				continue
			}
			if tagFilter != "" {
				matchedTag := false
				for _, tag := range vm.Tags {
					if strings.EqualFold(tag, tagFilter) {
						matchedTag = true
						break
					}
				}
				if !matchedTag {
					continue
				}
			}
			filtered = append(filtered, vm)
		}
		vms = filtered
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
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// StartVM handles POST /api/v1/vms/{vmID}/start
func (s *Server) StartVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Start(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// StopVM handles POST /api/v1/vms/{vmID}/stop
func (s *Server) StopVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Stop(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// BulkVMAction handles POST /api/v1/vms/bulk.
func (s *Server) BulkVMAction(w http.ResponseWriter, r *http.Request) {
	var req bulkVMActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	req.Action = strings.TrimSpace(strings.ToLower(req.Action))
	actionFn, ok := supportedBulkVMActions[req.Action]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_action", "action must be one of: start, stop, delete"))
		return
	}
	if len(req.IDs) == 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request", "ids must contain at least one VM ID"))
		return
	}

	results := make([]bulkVMActionResult, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			results = append(results, bulkVMActionResult{
				ID:      rawID,
				Success: false,
				Code:    "invalid_vm_id",
				Message: "vm id cannot be empty",
			})
			continue
		}

		if err := actionFn(s, r, id); err != nil {
			err = sanitizeManagerError(err)
			result := bulkVMActionResult{ID: id, Success: false}
			if apiErr, ok := err.(*types.APIError); ok {
				result.Code = apiErr.Code
				result.Message = apiErr.Message
			} else {
				result.Message = err.Error()
			}
			results = append(results, result)
			continue
		}

		results = append(results, bulkVMActionResult{ID: id, Success: true})
	}

	writeJSON(w, http.StatusOK, bulkVMActionResponse{Action: req.Action, Results: results})
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

// GetQuotaUsage handles GET /api/v1/quotas/usage
func (s *Server) GetQuotaUsage(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, sanitizeManagerError(err))
		return
	}
	writeJSON(w, http.StatusOK, vm.CalculateQuotaUsage(vms, s.quotas))
}
