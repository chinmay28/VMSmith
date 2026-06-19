package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// bulkVMActionSpec captures the per-action details needed to dispatch a bulk
// VM operation: the manager call to invoke, the event type to publish on
// success, and the human-readable verb used in the audit message.
type bulkVMActionSpec struct {
	apply     func(*Server, *http.Request, string) error
	eventType string
	verb      string
}

// supportedBulkVMActions lists every action accepted by POST /api/v1/vms/bulk.
// The map key is the wire value of `action` (lowercase). Adding an entry here
// is the only change required to extend the bulk endpoint with a new
// lifecycle verb that already exists on `vm.Manager`.
var supportedBulkVMActions = map[string]bulkVMActionSpec{
	"start": {
		apply:     func(s *Server, r *http.Request, id string) error { return s.vmManager.Start(r.Context(), id) },
		eventType: "vm.start_requested",
		verb:      "start",
	},
	"stop": {
		apply:     func(s *Server, r *http.Request, id string) error { return s.vmManager.Stop(r.Context(), id) },
		eventType: "vm.stop_requested",
		verb:      "stop",
	},
	"delete": {
		apply:     func(s *Server, r *http.Request, id string) error { return s.vmManager.Delete(r.Context(), id) },
		eventType: "vm.deleted",
		verb:      "delete",
	},
	"restart": {
		apply:     func(s *Server, r *http.Request, id string) error { return s.vmManager.Restart(r.Context(), id) },
		eventType: "vm.restart_requested",
		verb:      "restart",
	},
	"force-stop": {
		apply:     func(s *Server, r *http.Request, id string) error { return s.vmManager.ForceStop(r.Context(), id) },
		eventType: "vm.force_stop_requested",
		verb:      "force-stop",
	},
	"reboot": {
		apply:     func(s *Server, r *http.Request, id string) error { return s.vmManager.Reboot(r.Context(), id) },
		eventType: "vm.reboot_requested",
		verb:      "reboot",
	},
	"suspend": {
		apply:     func(s *Server, r *http.Request, id string) error { return s.vmManager.Suspend(r.Context(), id) },
		eventType: "vm.suspend_requested",
		verb:      "suspend",
	},
	"resume": {
		apply:     func(s *Server, r *http.Request, id string) error { return s.vmManager.Resume(r.Context(), id) },
		eventType: "vm.resume_requested",
		verb:      "resume",
	},
}

// supportedBulkVMActionsList returns the action keys in a stable order so
// error messages and OpenAPI docs are deterministic.
var supportedBulkVMActionsList = []string{"start", "stop", "delete", "restart", "force-stop", "reboot", "suspend", "resume"}

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
		err = logAndSanitizeManagerError("create vm", err)
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
	if strings.TrimSpace(string(merged.OSType)) == "" {
		merged.OSType = tpl.OSType
	}
	if strings.TrimSpace(merged.OSVariant) == "" {
		merged.OSVariant = tpl.OSVariant
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
		err = logAndSanitizeManagerError("update vm", err)
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
	sortField, order, err := parseVMSort(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	q := r.URL.Query()
	autoStartFilter, autoStartSet, apiErr := parseTristateBoolParam(q.Get("auto_start"), "auto_start")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	lockedFilter, lockedSet, apiErr := parseTristateBoolParam(q.Get("locked"), "locked")
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
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

	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	tagFilter := strings.TrimSpace(strings.ToLower(q.Get("tag")))
	statusFilter := strings.TrimSpace(strings.ToLower(q.Get("status")))
	searchFilter := strings.TrimSpace(strings.ToLower(q.Get("search")))
	imageFilter := strings.TrimSpace(strings.ToLower(q.Get("image")))
	defaultUserFilter := strings.TrimSpace(strings.ToLower(q.Get("default_user")))
	networkFilter := strings.TrimSpace(strings.ToLower(q.Get("network")))
	// Case-sensitive `HasPrefix(vm.Name, prefix)` mirrors the 5.4.75 snapshot
	// prefix selector and the case-sensitive `vmsmith` VM-name alphabet
	// ([A-Za-z0-9-]) — operators distinguishing `web-prod-1` from `Web-prod-1`
	// expect strict-match semantics. Whitespace-trimmed; empty disables.
	prefixFilter := strings.TrimSpace(q.Get("prefix"))
	natStaticIPFilter, natStaticIPSet := parseNATStaticIPFilter(q.Get("nat_static_ip"))
	natGatewayFilter, natGatewaySet := parseNATGatewayFilter(q.Get("nat_gateway"))
	ipFilter, ipSet := parseIPFilter(q.Get("ip"))
	osTypeFilter, osTypeSet, apiErr := parseOSTypeFilter(q.Get("os_type"))
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	osVariantFilter, osVariantSet, apiErr := parseOSVariantFilter(q.Get("os_variant"))
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	firmwareFilter, firmwareSet, apiErr := parseFirmwareFilter(q.Get("firmware"))
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	diskBusFilter, diskBusSet, apiErr := parseDiskBusFilter(q.Get("disk_bus"))
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	nicModelFilter, nicModelSet, apiErr := parseNICModelFilter(q.Get("nic_model"))
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	machineFilter, machineSet, apiErr := parseMachineFilter(q.Get("machine"))
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	clockOffsetFilter, clockOffsetSet, apiErr := parseClockOffsetFilter(q.Get("clock_offset"))
	if apiErr != nil {
		writeAPIError(w, http.StatusBadRequest, apiErr)
		return
	}
	if tagFilter != "" || statusFilter != "" || searchFilter != "" || imageFilter != "" || defaultUserFilter != "" || networkFilter != "" || prefixFilter != "" || natStaticIPSet || natGatewaySet || ipSet || osTypeSet || osVariantSet || firmwareSet || diskBusSet || nicModelSet || machineSet || clockOffsetSet || autoStartSet || lockedSet || sinceSet || untilSet || minCPUsSet || maxCPUsSet || minRAMSet || maxRAMSet || minDiskSet || maxDiskSet {
		filtered := make([]*types.VM, 0, len(vms))
		for _, vm := range vms {
			if statusFilter != "" && !strings.EqualFold(string(vm.State), statusFilter) {
				continue
			}
			if !countInRange(vm.Spec.CPUs, minCPUs, minCPUsSet, maxCPUs, maxCPUsSet) {
				continue
			}
			if !countInRange(vm.Spec.RAMMB, minRAM, minRAMSet, maxRAM, maxRAMSet) {
				continue
			}
			if !countInRange(vm.Spec.DiskGB, minDisk, minDiskSet, maxDisk, maxDiskSet) {
				continue
			}
			if imageFilter != "" && !strings.EqualFold(vm.Spec.Image, imageFilter) {
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
			if defaultUserFilter != "" {
				effectiveUser := vm.Spec.DefaultUser
				if effectiveUser == "" {
					effectiveUser = "root"
				}
				if !strings.EqualFold(effectiveUser, defaultUserFilter) {
					continue
				}
			}
			if osTypeSet && vm.Spec.ResolvedOSType() != osTypeFilter {
				continue
			}
			if osVariantSet && !strings.EqualFold(vm.Spec.OSVariant, osVariantFilter) {
				continue
			}
			if firmwareSet && !vmMatchesFirmwareFilter(vm.Spec, firmwareFilter) {
				continue
			}
			if diskBusSet && !vmMatchesDiskBusFilter(vm.Spec, diskBusFilter) {
				continue
			}
			if nicModelSet && !vmMatchesNICModelFilter(vm.Spec, nicModelFilter) {
				continue
			}
			if machineSet && !vmMatchesMachineFilter(vm.Spec, machineFilter) {
				continue
			}
			if clockOffsetSet && !vmMatchesClockOffsetFilter(vm.Spec, clockOffsetFilter) {
				continue
			}
			if autoStartSet && vm.Spec.AutoStart != autoStartFilter {
				continue
			}
			if lockedSet && vm.Spec.Locked != lockedFilter {
				continue
			}
			if networkFilter != "" && !types.VMMatchesNetwork(vm, networkFilter) {
				continue
			}
			// Prefix filter applied between network and time-range so it
			// composes additively with every other VM filter; X-Total-Count
			// reflects the post-filter population. Case-sensitive HasPrefix
			// mirrors `strings.HasPrefix` semantics and the 5.4.75 snapshot
			// prefix contract.
			if prefixFilter != "" && !strings.HasPrefix(vm.Name, prefixFilter) {
				continue
			}
			// NAT static IP filter (5.4.79): exact-match on the VM's CIDR
			// `spec.nat_static_ip` (or the IP portion of the CIDR), so
			// operators can answer *"which VM lives at 192.168.100.50?"*
			// without round-tripping `?search=`. Empty stored values
			// (DHCP-assigned VMs) drop out when the filter is set.
			if natStaticIPSet && !vmMatchesNATStaticIPFilter(vm, natStaticIPFilter) {
				continue
			}
			// NAT gateway filter (5.4.80): case-insensitive exact-match
			// on `spec.nat_gateway` (a plain IP — no CIDR dual-form).
			// VMs with an empty NatGateway drop out when set, mirroring
			// the empty-stored-excludes contract on `?nat_static_ip=`.
			if natGatewaySet && !vmMatchesNATGatewayFilter(vm, natGatewayFilter) {
				continue
			}
			// IP filter (5.4.81): case-insensitive exact-match on the
			// VM's runtime-discovered `vm.IP` (the value shown in the
			// list table, populated by the libvirt DHCP lease lookup
			// with fallback to the IP portion of `spec.nat_static_ip`
			// for static-IP VMs). Covers DHCP-assigned VMs that
			// `?nat_static_ip=` (5.4.79) cannot, since DHCP VMs have an
			// empty `spec.nat_static_ip`. VMs with an empty IP
			// (stopped, no lease yet) drop out when the filter is set.
			if ipSet && !vmMatchesIPFilter(vm, ipFilter) {
				continue
			}
			if !snapshotInTimeRange(vm.CreatedAt, sinceTime, sinceSet, untilTime, untilSet) {
				continue
			}
			if searchFilter != "" && !types.VMMatchesSearch(vm, searchFilter) {
				continue
			}
			filtered = append(filtered, vm)
		}
		vms = filtered
	}

	types.SortVMs(vms, sortField, order)

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

	// Stream live qemu-img convert progress for the clone over the dedicated
	// SSE channel (GET /vms/{id}/operations/progress), keyed by the source VM.
	ctx := r.Context()
	if s.operationProgress != nil {
		ctx = vm.WithCloneProgress(ctx, s.operationProgress.progressCallback(id, "clone", req.Name))
		defer s.operationProgress.publish(id, operationProgressMsg{Op: "clone", Name: req.Name, Done: true})
	}

	cloned, err := s.vmManager.Clone(ctx, id, req.Name)
	if err != nil {
		err = logAndSanitizeManagerError("clone vm", err)
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

// ForceStopVM handles POST /api/v1/vms/{vmID}/force-stop.  Skips the ACPI
// shutdown signal that StopVM relies on and immediately destroys the running
// domain — the libvirt equivalent of pulling the power cord.  Used when the
// guest OS is unresponsive or the operator deliberately wants to skip
// graceful shutdown.  Returns 409 vm_already_stopped when the VM is not in a
// running state.
func (s *Server) ForceStopVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.ForceStop(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	s.publishAppEvent("vm.force_stop_requested", id, fmt.Sprintf("VM %q force-stop requested", id), nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "force_stopped"})
}

// RestartVM handles POST /api/v1/vms/{vmID}/restart.  It performs a graceful
// stop followed by a start; if the VM is already stopped it just starts.
func (s *Server) RestartVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Restart(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	s.publishAppEvent("vm.restart_requested", id, fmt.Sprintf("VM %q restart requested", id), nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

// RebootVM handles POST /api/v1/vms/{vmID}/reboot.  Sends an ACPI reboot
// signal to the running guest via libvirt's dom.Reboot().  Unlike Restart
// (stop+start, which power-cycles QEMU), Reboot keeps the domain alive and
// preserves the IP / MAC / DHCP reservation.  Returns 409 `vm_not_running`
// when the VM is not currently running.
func (s *Server) RebootVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Reboot(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	s.publishAppEvent("vm.reboot_requested", id, fmt.Sprintf("VM %q reboot requested", id), nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "rebooted"})
}

// SuspendVM handles POST /api/v1/vms/{vmID}/suspend.  Pauses CPU+memory of a
// running VM so it can be resumed later without rebooting.  Returns 409 with
// `vm_not_running` if the VM is not currently running, and 409 with
// `vm_already_paused` if it is already paused.
func (s *Server) SuspendVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Suspend(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	s.publishAppEvent("vm.suspend_requested", id, fmt.Sprintf("VM %q suspend requested", id), nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "suspended"})
}

// ResumeVM handles POST /api/v1/vms/{vmID}/resume.  Unpauses a suspended VM,
// restoring it to the running state.  Returns 409 with `vm_not_paused` if the
// VM is not currently paused.
func (s *Server) ResumeVM(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	if err := s.vmManager.Resume(r.Context(), id); err != nil {
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	s.publishAppEvent("vm.resume_requested", id, fmt.Sprintf("VM %q resume requested", id), nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
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
	spec, ok := supportedBulkVMActions[req.Action]
	if !ok {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_bulk_action",
			"action must be one of: "+strings.Join(supportedBulkVMActionsList, ", ")))
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

		if err := spec.apply(s, r, id); err != nil {
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
		s.publishAppEvent(spec.eventType, id,
			fmt.Sprintf("VM %q %s requested (bulk)", id, spec.verb),
			map[string]string{"bulk": "true"})
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
