package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// Post-create GPU passthrough lifecycle endpoints (roadmap 5.7.10).

// attachGPURequest is the body for POST /vms/{vmID}/gpus.
type attachGPURequest struct {
	// Address is the host GPU PCI address, long or short form.
	Address string `json:"address"`
	// Force live-attaches when the VM is running (risky; the guest
	// typically needs a reboot to initialise the device). Mirrors the CLI
	// --force-attach gate.
	Force bool `json:"force,omitempty"`
}

// gpuLifecycleStatus maps the typed manager errors onto HTTP statuses.
func gpuLifecycleStatus(err error) int {
	if apiErr, ok := err.(*types.APIError); ok {
		switch apiErr.Code {
		case "invalid_gpu":
			return http.StatusBadRequest
		case "gpu_already_attached", "vm_running":
			return http.StatusConflict
		case "gpu_not_attached":
			return http.StatusNotFound
		case "quota_exceeded":
			return http.StatusForbidden
		}
	}
	return 0
}

// AttachGPU handles POST /api/v1/vms/{vmID}/gpus.
func (s *Server) AttachGPU(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")

	var req attachGPURequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Address) == "" {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_gpu", "address is required"))
		return
	}

	vm, err := s.vmManager.AttachGPU(r.Context(), id, req.Address, req.Force)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		status := gpuLifecycleStatus(apiErr)
		if status == 0 {
			status = statusForAPIError(apiErr, http.StatusInternalServerError)
		}
		writeAPIError(w, status, apiErr)
		return
	}

	s.publishAppEvent("vm.gpu_attached", vm.ID, fmt.Sprintf("GPU %s attached to VM %q", types.NormalizePCIAddress(req.Address), vm.Name), map[string]string{
		"gpu":   types.NormalizePCIAddress(req.Address),
		"force": fmt.Sprintf("%t", req.Force),
	})
	writeJSON(w, http.StatusOK, vm.RedactConsoleSecrets())
}

// DetachGPU handles DELETE /api/v1/vms/{vmID}/gpus/{gpuAddr}.
func (s *Server) DetachGPU(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	addr := chi.URLParam(r, "gpuAddr")

	vm, err := s.vmManager.DetachGPU(r.Context(), id, addr)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		status := gpuLifecycleStatus(apiErr)
		if status == 0 {
			status = statusForAPIError(apiErr, http.StatusInternalServerError)
		}
		writeAPIError(w, status, apiErr)
		return
	}

	s.publishAppEvent("vm.gpu_detached", vm.ID, fmt.Sprintf("GPU %s detached from VM %q", types.NormalizePCIAddress(addr), vm.Name), map[string]string{
		"gpu": types.NormalizePCIAddress(addr),
	})
	writeJSON(w, http.StatusOK, vm.RedactConsoleSecrets())
}
