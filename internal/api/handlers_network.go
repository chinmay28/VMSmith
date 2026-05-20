package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type addPortRequest struct {
	HostPort    int            `json:"host_port"`
	GuestPort   int            `json:"guest_port"`
	Protocol    types.Protocol `json:"protocol,omitempty"`
	Description string         `json:"description,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
}

// updatePortRequest carries the editable metadata for an existing port-forward
// rule. Description and Tags are pointers so callers can distinguish between
// "omit" (leave untouched) and explicit values. For Tags, a JSON `null` (or
// an omitted key) leaves the tag set unchanged; an explicit empty array
// clears it. The 5-tuple (host_port/guest_port/guest_ip/protocol) that drives
// the underlying iptables rule is intentionally immutable here — changing any
// of those is a delete-and-re-add operation.
type updatePortRequest struct {
	Description *string   `json:"description,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
}

// bulkDeletePortsRequest selects port forwards to delete in a single batch.
//
// Exactly one of Ids or Protocol must be set. Protocol is scoped to the VM in
// the URL (it can never accidentally delete another VM's rules).
type bulkDeletePortsRequest struct {
	IDs      []string       `json:"ids,omitempty"`
	Protocol types.Protocol `json:"protocol,omitempty"`
}

type bulkDeletePortResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type bulkDeletePortsResponse struct {
	Results []bulkDeletePortResult `json:"results"`
}

// AddPort handles POST /api/v1/vms/{vmID}/ports
func (s *Server) AddPort(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	var req addPortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if req.Protocol == "" {
		req.Protocol = types.ProtocolTCP
	}
	if err := validatePortForward(req.HostPort, req.GuestPort, req.Protocol); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	description := strings.TrimSpace(req.Description)
	if err := validatePortForwardDescription(description); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	tags, err := normalizePortForwardTags(req.Tags)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	// Get VM to find its IP
	vm, err := s.vmManager.Get(r.Context(), vmID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, sanitizeManagerError(err))
		return
	}

	if vm.IP == "" {
		writeErrorCode(w, http.StatusConflict, "vm_ip_unavailable", "VM does not have an IP address yet; is it running?")
		return
	}

	pf, err := s.portFwd.Add(vmID, req.HostPort, req.GuestPort, vm.IP, req.Protocol, network.AddOptions{Description: description, Tags: tags})
	if err != nil {
		if apiErr, ok := err.(*types.APIError); ok && apiErr.Code == "port_forward_conflict" {
			writeAPIError(w, http.StatusConflict, apiErr)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, sanitizeManagerError(err))
		return
	}

	s.publishAppEvent("port_forward.added", vmID,
		"port forward added", map[string]string{
			"host_port":  fmt.Sprintf("%d", req.HostPort),
			"guest_port": fmt.Sprintf("%d", req.GuestPort),
			"protocol":   string(req.Protocol),
		})

	writeJSON(w, http.StatusCreated, pf)
}

// ListPorts handles GET /api/v1/vms/{vmID}/ports
//
// Optional query params:
//   - tag=<tag>                  case-insensitive exact-match filter over the
//     rule's tag list. Applied before protocol + search + sort.
//   - protocol=<tcp|udp>         case-insensitive exact-match filter on the
//     rule's transport protocol. Empty disables; anything other than
//     tcp/udp returns 400 `invalid_protocol`. Mirrors the bulk_delete
//     `protocol` selector so the filter and bulk-action surfaces agree.
//   - search=<needle>            case-insensitive substring filter across
//     description, protocol, host_port, guest_port, guest_ip, and tags.
//     Applied before sort.
//   - sort=<id|host_port|guest_port|protocol|description>   default id
//   - order=<asc|desc>           default asc
//   - page / per_page (see parsePagination) — applied after filter + sort so
//     the X-Total-Count header reflects the post-filter / pre-pagination
//     population. `limit` is accepted as a synonym for `per_page`. Mirrors
//     the pagination surface that VMs (5.4.2), images (5.4.5), templates,
//     snapshots, events (4.2.17), logs (5.4.13), and webhooks (5.4.19)
//     already ship.
//
// Unknown sort/order values return 400 `invalid_sort` / `invalid_order`.
// Comparators tiebreak on `id` so repeated requests return a deterministic
// order.
func (s *Server) ListPorts(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	sortField, order, sortErr := parsePortForwardSort(r)
	if sortErr != nil {
		apiErr := sortErr.(*types.APIError)
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}

	protocolFilter := types.Protocol(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol"))))
	if protocolFilter != "" && protocolFilter != types.ProtocolTCP && protocolFilter != types.ProtocolUDP {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_protocol",
			"protocol must be 'tcp' or 'udp'"))
		return
	}

	ports, err := s.portFwd.List(vmID)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	tagFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tag")))
	if tagFilter != "" {
		filtered := ports[:0]
		for _, pf := range ports {
			for _, tag := range pf.Tags {
				if tag == tagFilter {
					filtered = append(filtered, pf)
					break
				}
			}
		}
		ports = filtered
	}

	if protocolFilter != "" {
		filtered := ports[:0]
		for _, pf := range ports {
			if pf.Protocol == protocolFilter {
				filtered = append(filtered, pf)
			}
		}
		ports = filtered
	}

	searchFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	if searchFilter != "" {
		filtered := ports[:0]
		for _, pf := range ports {
			if types.PortForwardMatchesSearch(pf, searchFilter) {
				filtered = append(filtered, pf)
			}
		}
		ports = filtered
	}

	types.SortPortForwards(ports, sortField, order)

	total := len(ports)
	pagination := parsePagination(r)
	ports = paginateSlice(ports, pagination.Page, pagination.PerPage)
	if ports == nil {
		ports = []*types.PortForward{}
	}
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, ports)
}

// UpdatePort handles PATCH /api/v1/vms/{vmID}/ports/{portID}.
//
// Only the description is editable; the underlying iptables 5-tuple
// (host_port/guest_port/guest_ip/protocol) is immutable. A nil Description
// means "leave as-is"; an explicit empty string clears the description.
//
// As with the bulk-delete handler, the URL VM is the authoritative scope: if
// the targeted port-forward exists but belongs to a different VM, the response
// is 404 `resource_not_found` (not a 403). This keeps the safety property
// symmetric across update/delete and prevents one VM's API surface from
// mutating another's rules.
func (s *Server) UpdatePort(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")
	portID := chi.URLParam(r, "portID")

	var req updatePortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}

	if req.Description != nil {
		if err := validatePortForwardDescription(strings.TrimSpace(*req.Description)); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
	}

	var tagsPtr *[]string
	if req.Tags != nil {
		normalized, err := normalizePortForwardTags(*req.Tags)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		// Preserve "clear" semantics: an explicit empty array clears tags;
		// keep normalized as nil in that case so the persistence layer
		// drops the slice.
		if normalized == nil {
			empty := []string{}
			tagsPtr = &empty
		} else {
			tagsPtr = &normalized
		}
	}

	// Foreign-VM safety: verify the rule belongs to the URL VM before mutating
	// it. We list the URL VM's forwards and require the target id to be in the
	// set; any other case (rule missing, rule on a different VM) surfaces as
	// 404 `resource_not_found`.
	ports, err := s.portFwd.List(vmID)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}
	known := false
	for _, p := range ports {
		if p.ID == portID {
			known = true
			break
		}
	}
	if !known {
		writeAPIError(w, http.StatusNotFound,
			types.NewAPIError("resource_not_found", "port forward "+portID+" not found"))
		return
	}

	updated, err := s.portFwd.Update(portID, network.UpdateOptions{Description: req.Description, Tags: tagsPtr})
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	attrs := map[string]string{"port_id": portID}
	if req.Description != nil {
		attrs["description"] = strings.TrimSpace(*req.Description)
	}
	if tagsPtr != nil {
		attrs["tags"] = strings.Join(*tagsPtr, ",")
	}
	s.publishAppEvent("port_forward.updated", vmID,
		"port forward "+portID+" updated", attrs)

	writeJSON(w, http.StatusOK, updated)
}

// RemovePort handles DELETE /api/v1/vms/{vmID}/ports/{portID}
func (s *Server) RemovePort(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")
	portID := chi.URLParam(r, "portID")

	if err := s.portFwd.Remove(portID); err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	s.publishAppEvent("port_forward.removed", vmID,
		"port forward "+portID+" removed", map[string]string{
			"port_id": portID,
		})

	w.WriteHeader(http.StatusNoContent)
}

// BulkDeletePorts handles POST /api/v1/vms/{vmID}/ports/bulk_delete.
//
// Accepts either an explicit list of port-forward IDs ("ids") or a protocol
// selector ("protocol": "tcp"|"udp") that resolves to every forward on the VM
// matching that protocol. Returns a per-target result list so partial failures
// (one ID missing, the rest succeeded) surface in a single response — mirroring
// the snapshot and image bulk_delete shape.
func (s *Server) BulkDeletePorts(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	var req bulkDeletePortsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}

	cleanedIDs := make([]string, 0, len(req.IDs))
	for _, id := range req.IDs {
		if t := strings.TrimSpace(id); t != "" {
			cleanedIDs = append(cleanedIDs, t)
		}
	}
	proto := types.Protocol(strings.TrimSpace(strings.ToLower(string(req.Protocol))))

	if len(cleanedIDs) == 0 && proto == "" {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"exactly one of ids or protocol must be provided"))
		return
	}
	if len(cleanedIDs) > 0 && proto != "" {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"ids and protocol are mutually exclusive"))
		return
	}
	if proto != "" && proto != types.ProtocolTCP && proto != types.ProtocolUDP {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_request",
			"protocol must be 'tcp' or 'udp'"))
		return
	}

	// Resolve targets. For the protocol selector we list this VM's forwards
	// and match. For the ids path we accept the ids verbatim — missing ones
	// will surface as resource_not_found per-result.
	var targets []string
	if proto != "" {
		ports, err := s.portFwd.List(vmID)
		if err != nil {
			apiErr := sanitizeManagerError(err)
			writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
			return
		}
		for _, p := range ports {
			if p.Protocol == proto {
				targets = append(targets, p.ID)
			}
		}
	} else {
		targets = cleanedIDs
	}

	// Pre-load the VM's known port-forwards so we can detect "id belongs to a
	// different VM" without scanning all VMs' rules. The snapshot bulk_delete
	// handler does this implicitly via DeleteSnapshot's per-VM scope; for
	// port forwards we have to do it explicitly because the underlying
	// PortForwarder.Remove looks up globally by ID.
	known := make(map[string]bool)
	if len(cleanedIDs) > 0 {
		ports, err := s.portFwd.List(vmID)
		if err != nil {
			apiErr := sanitizeManagerError(err)
			writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
			return
		}
		for _, p := range ports {
			known[p.ID] = true
		}
	}

	results := make([]bulkDeletePortResult, 0, len(targets))
	for _, id := range targets {
		// For the explicit-ids path, a port-forward that doesn't belong to
		// this VM is a per-target resource_not_found rather than a 404 for
		// the whole request — mirrors how missing snapshot names are
		// handled in BulkDeleteSnapshots.
		if len(cleanedIDs) > 0 && !known[id] {
			results = append(results, bulkDeletePortResult{
				ID: id, Success: false,
				Code: "resource_not_found", Message: "port forward not found",
			})
			continue
		}
		if err := s.portFwd.Remove(id); err != nil {
			err = sanitizeManagerError(err)
			result := bulkDeletePortResult{ID: id, Success: false}
			if apiErr, ok := err.(*types.APIError); ok {
				result.Code = apiErr.Code
				result.Message = apiErr.Message
			} else {
				result.Code = "resource_not_found"
				result.Message = err.Error()
			}
			results = append(results, result)
			continue
		}
		results = append(results, bulkDeletePortResult{ID: id, Success: true})
		s.publishAppEvent("port_forward.removed", vmID,
			"port forward "+id+" removed", map[string]string{
				"port_id": id,
				"bulk":    "true",
			})
	}

	writeJSON(w, http.StatusOK, bulkDeletePortsResponse{Results: results})
}

// ListHostInterfaces handles GET /api/v1/host/interfaces
func (s *Server) ListHostInterfaces(w http.ResponseWriter, r *http.Request) {
	ifaces, err := network.DiscoverInterfaces()
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}
	writeJSON(w, http.StatusOK, ifaces)
}
