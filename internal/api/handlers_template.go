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
// Optional query params (applied in order so X-Total-Count reflects the
// post-filter / pre-pagination population):
//   - tag=<value>            case-insensitive filter; only templates carrying
//     this tag are returned.
//   - image=<value>          case-insensitive exact-match against the
//     template's `image` field. Closes the operator query "show me every
//     template built from rocky9.qcow2" that `?search=` matches fuzzily
//     across name/description/tags and that `?tag=` cannot answer without
//     pre-tagging every template by its base image. Mirrors 5.4.22.
//   - default_user=<value>   case-insensitive exact-match against the
//     template's `default_user` field. Closes the operator query "show me
//     every template that provisions the `deploy` SSH user". Mirrors the VM
//     `?default_user=` filter (5.4.23), but without that filter's
//     empty-means-root fallback: a template's empty default_user means "use
//     the image's built-in user", so an empty stored value never matches a
//     non-empty query.
//   - network=<name>         case-insensitive exact-match (any-of) against the
//     name of any of the template's additional network attachments
//     (networks[].name). Closes the operator query "show me every template
//     that attaches `data-net`". Mirrors the VM `?network=` filter (5.4.36):
//     the implicit primary NAT network is not represented in the template's
//     networks list, so this only scopes to explicitly-attached extra
//     networks. Whitespace trimmed; empty disables.
//   - since=<rfc3339>        keep templates with created_at >= since
//     (inclusive). Whitespace trimmed; empty disables. Invalid values
//     return 400 `invalid_since`.
//   - until=<rfc3339>        keep templates with created_at <= until
//     (inclusive). Same shape as since; 400 `invalid_until` on garbage.
//     A template with a zero / unknown created_at is filtered OUT whenever
//     any bound is set — operators querying a time window don't want
//     unbounded entries silently included.
//   - min_cpus=<n>           inclusive lower bound on `cpus`. Whitespace
//     trimmed; empty disables; non-numeric or negative values return 400
//     `invalid_min_cpus`. Mirrors the VM `?min_cpus=` filter (5.4.44);
//     opens the same capacity-audit query against the template cohort —
//     "show me every template that provisions >= 8 vCPU VMs" — that
//     `?search=` / `?tag=` cannot answer.
//   - max_cpus=<n>           inclusive upper bound on `cpus`. Same shape
//     as min_cpus; 400 `invalid_max_cpus` on garbage.
//   - min_ram_mb=<n>         inclusive lower bound on `ram_mb`. Whitespace
//     trimmed; empty disables; non-numeric or negative values return 400
//     `invalid_min_ram_mb`. Mirrors the VM `?min_ram_mb=` filter (5.4.48);
//     opens the same capacity-audit query against the template cohort —
//     "show me every template that provisions >= 8 GB RAM VMs" — that
//     `?search=` / `?tag=` cannot answer.
//   - max_ram_mb=<n>         inclusive upper bound on `ram_mb`. Same shape
//     as min_ram_mb; 400 `invalid_max_ram_mb` on garbage.
//   - min_disk_gb=<n>        inclusive lower bound on `disk_gb`. Whitespace
//     trimmed; empty disables; non-numeric or negative values return 400
//     `invalid_min_disk_gb`. Mirrors the VM `?min_disk_gb=` filter (5.4.50);
//     opens the same capacity-audit query against the template cohort —
//     "show me every template that provisions >= 100 GB disk VMs" — and
//     completes the cpus/ram/disk capacity-audit trio on the template list
//     alongside `?min_cpus=`/`?max_cpus=` (5.4.51) and `?min_ram_mb=` /
//     `?max_ram_mb=` (5.4.52).
//   - max_disk_gb=<n>        inclusive upper bound on `disk_gb`. Same shape
//     as min_disk_gb; 400 `invalid_max_disk_gb` on garbage.
//   - search=<value>         case-insensitive substring filter applied to
//     `name`, `description`, and `tags`. ID, image, default_user, and
//     network attachments are intentionally excluded from the haystack.
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

	q := r.URL.Query()
	sinceTime, sinceSet, apiErr := parseTimeRangeParam(q.Get("since"), "since")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	untilTime, untilSet, apiErr := parseTimeRangeParam(q.Get("until"), "until")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	minCPUs, minCPUsSet, apiErr := parseCountRangeParam(q.Get("min_cpus"), "min_cpus")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	maxCPUs, maxCPUsSet, apiErr := parseCountRangeParam(q.Get("max_cpus"), "max_cpus")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	minRAM, minRAMSet, apiErr := parseCountRangeParam(q.Get("min_ram_mb"), "min_ram_mb")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	maxRAM, maxRAMSet, apiErr := parseCountRangeParam(q.Get("max_ram_mb"), "max_ram_mb")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	minDisk, minDiskSet, apiErr := parseCountRangeParam(q.Get("min_disk_gb"), "min_disk_gb")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	maxDisk, maxDiskSet, apiErr := parseCountRangeParam(q.Get("max_disk_gb"), "max_disk_gb")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}

	templates, err := s.storageMgr.ListTemplates()
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	if tagFilter := strings.TrimSpace(q.Get("tag")); tagFilter != "" {
		templates = filterTemplatesByTag(templates, tagFilter)
	}

	imageFilter := strings.TrimSpace(strings.ToLower(q.Get("image")))
	if imageFilter != "" {
		filtered := templates[:0]
		for _, tpl := range templates {
			if strings.EqualFold(tpl.Image, imageFilter) {
				filtered = append(filtered, tpl)
			}
		}
		templates = filtered
	}

	defaultUserFilter := strings.TrimSpace(strings.ToLower(q.Get("default_user")))
	if defaultUserFilter != "" {
		filtered := templates[:0]
		for _, tpl := range templates {
			if strings.EqualFold(tpl.DefaultUser, defaultUserFilter) {
				filtered = append(filtered, tpl)
			}
		}
		templates = filtered
	}

	networkFilter := strings.TrimSpace(strings.ToLower(q.Get("network")))
	if networkFilter != "" {
		filtered := templates[:0]
		for _, tpl := range templates {
			if types.TemplateMatchesNetwork(tpl, networkFilter) {
				filtered = append(filtered, tpl)
			}
		}
		templates = filtered
	}

	if sinceSet || untilSet {
		filtered := templates[:0]
		for _, tpl := range templates {
			if !snapshotInTimeRange(tpl.CreatedAt, sinceTime, sinceSet, untilTime, untilSet) {
				continue
			}
			filtered = append(filtered, tpl)
		}
		templates = filtered
	}

	if minCPUsSet || maxCPUsSet {
		filtered := templates[:0]
		for _, tpl := range templates {
			if !countInRange(tpl.CPUs, minCPUs, minCPUsSet, maxCPUs, maxCPUsSet) {
				continue
			}
			filtered = append(filtered, tpl)
		}
		templates = filtered
	}

	if minRAMSet || maxRAMSet {
		filtered := templates[:0]
		for _, tpl := range templates {
			if !countInRange(tpl.RAMMB, minRAM, minRAMSet, maxRAM, maxRAMSet) {
				continue
			}
			filtered = append(filtered, tpl)
		}
		templates = filtered
	}

	if minDiskSet || maxDiskSet {
		filtered := templates[:0]
		for _, tpl := range templates {
			if !countInRange(tpl.DiskGB, minDisk, minDiskSet, maxDisk, maxDiskSet) {
				continue
			}
			filtered = append(filtered, tpl)
		}
		templates = filtered
	}

	searchFilter := strings.ToLower(strings.TrimSpace(q.Get("search")))
	if searchFilter != "" {
		filtered := templates[:0]
		for _, tpl := range templates {
			if types.TemplateMatchesSearch(tpl, searchFilter) {
				filtered = append(filtered, tpl)
			}
		}
		templates = filtered
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

// bulkDeleteTemplatesRequest selects templates to delete in a single batch.
//
// Exactly one of IDs or Tag must be set. When Tag is set, every template whose
// (case-insensitive) tag list contains that tag is targeted — the cheap way
// to retire a cohort ("legacy-rocky8") without enumerating IDs. Mirrors the
// image bulk-delete shape (2.3.6) so the two surfaces share semantics.
type bulkDeleteTemplatesRequest struct {
	IDs []string `json:"ids,omitempty"`
	Tag string   `json:"tag,omitempty"`
}

type bulkDeleteTemplateResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type bulkDeleteTemplatesResponse struct {
	Results []bulkDeleteTemplateResult `json:"results"`
}

// BulkDeleteTemplates handles POST /api/v1/templates/bulk_delete.
//
// Accepts either an explicit list of template IDs ("ids") or a tag selector
// ("tag"). Returns a per-target result list so partial failures (one template
// missing, the rest succeeded) surface in a single response — mirroring the
// image / snapshot / port-forward bulk-delete shapes.
func (s *Server) BulkDeleteTemplates(w http.ResponseWriter, r *http.Request) {
	var req bulkDeleteTemplatesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}

	tag := strings.TrimSpace(req.Tag)
	cleanedIDs := make([]string, 0, len(req.IDs))
	for _, id := range req.IDs {
		if t := strings.TrimSpace(id); t != "" {
			cleanedIDs = append(cleanedIDs, t)
		}
	}

	if tag == "" && len(cleanedIDs) == 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"exactly one of ids or tag must be provided"))
		return
	}
	if tag != "" && len(cleanedIDs) > 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"ids and tag are mutually exclusive"))
		return
	}

	targets := cleanedIDs
	if tag != "" {
		tpls, err := s.storageMgr.ListTemplates()
		if err != nil {
			apiErr := sanitizeManagerError(err)
			writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
			return
		}
		for _, tpl := range filterTemplatesByTag(tpls, tag) {
			targets = append(targets, tpl.ID)
		}
	}

	results := make([]bulkDeleteTemplateResult, 0, len(targets))
	for _, id := range targets {
		if err := s.storageMgr.DeleteTemplate(id); err != nil {
			err = sanitizeManagerError(err)
			result := bulkDeleteTemplateResult{ID: id, Success: false}
			if apiErr, ok := err.(*types.APIError); ok {
				result.Code = apiErr.Code
				result.Message = apiErr.Message
			} else {
				result.Message = err.Error()
			}
			results = append(results, result)
			continue
		}
		results = append(results, bulkDeleteTemplateResult{ID: id, Success: true})
		s.publishAppEvent("template.deleted", "", "template "+id+" deleted", map[string]string{
			"template_id": id,
			"bulk":        "true",
		})
	}

	writeJSON(w, http.StatusOK, bulkDeleteTemplatesResponse{Results: results})
}
