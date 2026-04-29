package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type createTemplateRequest struct {
	Name        string                    `json:"name"`
	Image       string                    `json:"image"`
	CPUs        int                       `json:"cpus,omitempty"`
	RAMMB       int                       `json:"ram_mb,omitempty"`
	DiskGB      int                       `json:"disk_gb,omitempty"`
	Description string                    `json:"description,omitempty"`
	Tags        []string                  `json:"tags,omitempty"`
	DefaultUser string                    `json:"default_user,omitempty"`
	Networks    []types.NetworkAttachment `json:"networks,omitempty"`
}

func (req createTemplateRequest) toTemplate() *types.VMTemplate {
	name := strings.TrimSpace(req.Name)
	return &types.VMTemplate{
		Name:        name,
		Image:       strings.TrimSpace(req.Image),
		CPUs:        req.CPUs,
		RAMMB:       req.RAMMB,
		DiskGB:      req.DiskGB,
		Description: strings.TrimSpace(req.Description),
		Tags:        req.Tags,
		DefaultUser: strings.TrimSpace(req.DefaultUser),
		Networks:    req.Networks,
	}
}

// CreateTemplate handles POST /api/v1/templates.
func (s *Server) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req createTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if err := validateTemplateRequest(req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if tags, err := normalizeTags(req.Tags); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	} else {
		req.Tags = tags
	}

	existing, err := s.storageMgr.ListTemplates()
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}
	if err := validateUniqueTemplateName(req.Name, existing); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	tpl, err := s.storageMgr.CreateTemplate(req.toTemplate())
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	writeJSON(w, http.StatusCreated, tpl)
}

// ListTemplates handles GET /api/v1/templates.
func (s *Server) ListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.storageMgr.ListTemplates()
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	total := len(templates)
	pagination := parsePagination(r)
	templates = paginateSlice(templates, pagination.Page, pagination.PerPage)
	if templates == nil {
		templates = []*types.VMTemplate{}
	}
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, templates)
}

// DeleteTemplate handles DELETE /api/v1/templates/{templateID}.
func (s *Server) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "templateID")
	if err := s.storageMgr.DeleteTemplate(id); err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusNotFound), apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateTemplate handles PUT /api/v1/templates/{templateID}.
func (s *Server) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "templateID")
	if strings.TrimSpace(id) == "" {
		writeErrorCode(w, http.StatusBadRequest, "invalid_request", "template id is required")
		return
	}

	existing, err := s.storageMgr.GetTemplate(id)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusNotFound), apiErr)
		return
	}

	var req createTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if err := validateTemplateRequest(req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if tags, err := normalizeTags(req.Tags); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	} else {
		req.Tags = tags
	}

	all, err := s.storageMgr.ListTemplates()
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}
	others := make([]*types.VMTemplate, 0, len(all))
	for _, t := range all {
		if t == nil || t.ID == existing.ID {
			continue
		}
		others = append(others, t)
	}
	if err := validateUniqueTemplateName(req.Name, others); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	updated := req.toTemplate()
	updated.ID = existing.ID

	tpl, err := s.storageMgr.UpdateTemplate(updated)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	writeJSON(w, http.StatusOK, tpl)
}
