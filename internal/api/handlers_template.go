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
//
// Optional query params:
//   - tag=<value>            case-insensitive filter; only templates carrying
//     this tag are returned. Filtering happens before sort + pagination so
//     the X-Total-Count header reflects the filtered population.
//   - sort=<id|name|created_at>  default id; case-insensitive
//   - order=<asc|desc>       default asc
//   - page / per_page (see parsePagination)
//
// All comparators tiebreak on `id` so paginated requests return the same
// set across two independent fetches.
func (s *Server) ListTemplates(w http.ResponseWriter, r *http.Request) {
	sortField, order, err := parseTemplateSort(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	templates, err := s.storageMgr.ListTemplates()
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	if tagFilter := strings.TrimSpace(r.URL.Query().Get("tag")); tagFilter != "" {
		templates = filterTemplatesByTag(templates, tagFilter)
	}

	types.SortTemplates(templates, sortField, order)

	total := len(templates)
	pagination := parsePagination(r)
	templates = paginateSlice(templates, pagination.Page, pagination.PerPage)
	if templates == nil {
		templates = []*types.VMTemplate{}
	}
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, templates)
}

// UpdateTemplate handles PATCH /api/v1/templates/{templateID}. Description
// and Tags are the only mutable fields — image, resources, name, and
// network attachments are immutable post-create. See
// types.TemplateUpdateSpec for PATCH semantics.
func (s *Server) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "templateID")

	var patch types.TemplateUpdateSpec
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if err := validateTemplateDescription(patch.Description); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if patch.Tags != nil {
		tags, err := normalizeTags(patch.Tags)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		// normalizeTags returns nil for an all-blank input slice; preserve
		// the caller's "explicitly clear" intent so the manager still
		// replaces the current tag set with [].
		if tags == nil {
			tags = []string{}
		}
		patch.Tags = tags
	}

	tpl, changed, err := s.storageMgr.UpdateTemplate(id, patch)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusNotFound), apiErr)
		return
	}

	if changed {
		s.publishAppEvent("template.updated", "", "template "+tpl.Name+" updated", map[string]string{
			"template_id":   tpl.ID,
			"template_name": tpl.Name,
		})
	}

	writeJSON(w, http.StatusOK, tpl)
}

func filterTemplatesByTag(templates []*types.VMTemplate, tag string) []*types.VMTemplate {
	out := make([]*types.VMTemplate, 0, len(templates))
	for _, tpl := range templates {
		for _, t := range tpl.Tags {
			if strings.EqualFold(t, tag) {
				out = append(out, tpl)
				break
			}
		}
	}
	return out
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
