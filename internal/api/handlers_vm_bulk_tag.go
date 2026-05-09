package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

type bulkVMTagRequest struct {
	Action string   `json:"action"`
	IDs    []string `json:"ids"`
	Tags   []string `json:"tags"`
}

type bulkVMTagResult struct {
	ID      string   `json:"id"`
	Success bool     `json:"success"`
	Tags    []string `json:"tags,omitempty"`
	Code    string   `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
}

type bulkVMTagResponse struct {
	Action  string            `json:"action"`
	Results []bulkVMTagResult `json:"results"`
}

// BulkVMTag handles POST /api/v1/vms/bulk_tag.
//
// Body: {"action": "add"|"remove"|"set", "ids": [...], "tags": [...]}.
//
//   - add    — append `tags` to each VM's tag set (case-insensitive de-dup).
//     No-op when every requested tag is already present.
//   - remove — drop matching tags from each VM. No-op when none of the
//     requested tags are present.
//   - set    — replace each VM's tag set with `tags` (empty `tags` clears
//     all tags).
//
// Per-VM results follow the same shape as POST /vms/bulk and POST
// /vms/{id}/snapshots/bulk_delete: {id, success, code?, message?}, with
// tags added on success so callers can confirm the post-update state.
func (s *Server) BulkVMTag(w http.ResponseWriter, r *http.Request) {
	var req bulkVMTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}

	action := types.BulkTagAction(strings.TrimSpace(strings.ToLower(req.Action)))
	if !action.IsValid() {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_action", "action must be one of: add, remove, set"))
		return
	}

	if len(req.IDs) == 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request", "ids must contain at least one VM ID"))
		return
	}

	normalizedTags, err := normalizeTags(req.Tags)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	// Distinguish "explicit empty" (set→clear) from "no tags after normalization"
	// for add / remove (where empty is meaningless).
	if action != types.BulkTagActionSet && len(normalizedTags) == 0 {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request", "tags must contain at least one tag for add or remove actions"))
		return
	}

	results := make([]bulkVMTagResult, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			results = append(results, bulkVMTagResult{
				ID:      rawID,
				Success: false,
				Code:    "invalid_vm_id",
				Message: "vm id cannot be empty",
			})
			continue
		}

		current, err := s.vmManager.Get(r.Context(), id)
		if err != nil {
			err = sanitizeManagerError(err)
			result := bulkVMTagResult{ID: id, Success: false}
			if apiErr, ok := err.(*types.APIError); ok {
				result.Code = apiErr.Code
				result.Message = apiErr.Message
			} else {
				result.Message = err.Error()
			}
			results = append(results, result)
			continue
		}

		nextTags := types.MergeTags(action, current.Tags, normalizedTags)
		if types.TagsEqualSet(current.Tags, nextTags) {
			results = append(results, bulkVMTagResult{
				ID:      id,
				Success: true,
				Tags:    append([]string(nil), current.Tags...),
			})
			continue
		}

		// Use a non-nil slice (even when empty) so Update treats this as a
		// tag replacement instead of "field absent" and applies the clear.
		patch := types.VMUpdateSpec{Tags: append([]string{}, nextTags...)}
		updated, err := s.vmManager.Update(r.Context(), id, patch)
		if err != nil {
			err = sanitizeManagerError(err)
			result := bulkVMTagResult{ID: id, Success: false}
			if apiErr, ok := err.(*types.APIError); ok {
				result.Code = apiErr.Code
				result.Message = apiErr.Message
			} else {
				result.Message = err.Error()
			}
			results = append(results, result)
			continue
		}

		s.publishAppEvent("vm.tags_updated", updated.ID, fmt.Sprintf("VM %q tags updated via bulk_tag", updated.Name), map[string]string{
			"name":   updated.Name,
			"action": string(action),
		})
		results = append(results, bulkVMTagResult{
			ID:      id,
			Success: true,
			Tags:    append([]string(nil), updated.Tags...),
		})
	}

	writeJSON(w, http.StatusOK, bulkVMTagResponse{Action: string(action), Results: results})
}
