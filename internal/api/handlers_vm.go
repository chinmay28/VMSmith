package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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

type cloneVMRequest struct {
	Name string `json:"name"`
}

// CreateVM handles POST /api/v1/vms
func (s *Server) CreateVM(w http.ResponseWriter, r *http.Request) {
	var spec types.VMSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}

	if spec.TemplateID != "" {
		templateID := strings.TrimSpace(spec.TemplateID)
		tpl, err := s.storageMgr.GetTemplate(templateID)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_template_id", fmt.Sprintf("template_id %q was not found", templateID)))
			return
		}
		spec = mergeVMSpecWithTemplate(spec, tpl)
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

	s.publishAppEvent("vm.created", vm.ID, fmt.Sprintf("VM %q created", vm.Name), map[string]string{
		"name":  vm.Name,
		"image": spec.Image,
	})

	writeJSON(w, http.StatusCreated, vm)
}

func mergeVMSpecWithTemplate(spec types.VMSpec, tpl *types.VMTemplate) types.VMSpec {
	merged := spec
	merged.TemplateID = strings.TrimSpace(spec.TemplateID)
	if merged.Image == "" {
		merged.Image = tpl.Image
	}
	if merged.CPUs == 0 {
		merged.CPUs = tpl.CPUs
	}
	if merged.RAMMB == 0 {
		merged.RAMMB = tpl.RAMMB
	}
	if merged.DiskGB == 0 {
		merged.DiskGB = tpl.DiskGB
	}
	if strings.TrimSpace(merged.Description) == "" {
		merged.Description = tpl.Description
	}
	if strings.TrimSpace(merged.DefaultUser) == "" {
		merged.DefaultUser = tpl.DefaultUser
	}
	if len(merged.Tags) == 0 && len(tpl.Tags) > 0 {
		merged.Tags = append([]string(nil), tpl.Tags...)
	}
	if len(merged.Networks) == 0 && len(tpl.Networks) > 0 {
		merged.Networks = append([]types.NetworkAttachment(nil), tpl.Networks...)
	}
	return merged
}

// UpdateVM handles PATCH /api/v1/vms/{vmID}
// It stops the VM if running, applies CPU/RAM/disk changes, then restarts it.
func (s *Server) UpdateVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	var patch types.VMUpdateSpec
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
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
	s.publishAppEvent("vm.updated", vm.ID, fmt.Sprintf("VM %q updated", vm.Name), map[string]string{
		"name": vm.Name,
	})
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

	// Sort by ID so pagination is deterministic across backends.
	// LibvirtManager already returns VMs in bbolt key order (which is by ID),
	// but MockManager iterates a Go map, so without an explicit sort the order
	// is non-deterministic and pagination tests flake.
	sort.Slice(vms, func(i, j int) bool { return vms[i].ID < vms[j].ID })

	total := len(vms)
	pagination := parsePagination(r)
	vms = paginateSlice(vms, pagination.Page, pagination.PerPage)
	if vms == nil {
		vms = []*types.VM{}
	}
	setTotalCountHeader(w, total)

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

// CloneVM handles POST /api/v1/vms/{vmID}/clone
func (s *Server) CloneVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")

	var req cloneVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if err := validateCloneVMRequest(req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	existingVMs, err := s.vmManager.List(r.Context())
	if err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	if err := validateUniqueVMName(req.Name, existingVMs); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	cloned, err := s.vmManager.Clone(r.Context(), id, req.Name)
	if err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}

	s.publishAppEvent("vm.cloned", cloned.ID, fmt.Sprintf("VM %q cloned to %q", id, cloned.Name), map[string]string{
		"name":      cloned.Name,
		"source_id": id,
	})

	writeJSON(w, http.StatusCreated, cloned)
}

// DeleteVM handles DELETE /api/v1/vms/{vmID}
func (s *Server) DeleteVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Delete(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	s.publishAppEvent("vm.deleted", id, fmt.Sprintf("VM %q deleted", id), nil)
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
	s.publishAppEvent("vm.start_requested", id, fmt.Sprintf("VM %q start requested", id), nil)
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
	s.publishAppEvent("vm.stop_requested", id, fmt.Sprintf("VM %q stop requested", id), nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// BulkVMAction handles POST /api/v1/vms/bulk.
func (s *Server) BulkVMAction(w http.ResponseWriter, r *http.Request) {
	var req bulkVMActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
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
