package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type addPortRequest struct {
	HostPort  int            `json:"host_port"`
	GuestPort int            `json:"guest_port"`
	Protocol  types.Protocol `json:"protocol,omitempty"`
}

// AddPort handles POST /api/v1/vms/{vmID}/ports
func (s *Server) AddPort(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	var req addPortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Get VM to find its IP
	vm, err := s.vmManager.Get(r.Context(), vmID)
	if err != nil {
		writeError(w, http.StatusNotFound, "VM not found")
		return
	}

	if vm.IP == "" {
		writeError(w, http.StatusConflict, "VM does not have an IP address yet; is it running?")
		return
	}

	pf, err := s.portFwd.Add(vmID, req.HostPort, req.GuestPort, vm.IP, req.Protocol)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, pf)
}

// ListPorts handles GET /api/v1/vms/{vmID}/ports
func (s *Server) ListPorts(w http.ResponseWriter, r *http.Request) {
	vmID := chi.URLParam(r, "vmID")

	ports, err := s.portFwd.List(vmID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ports)
}

// RemovePort handles DELETE /api/v1/vms/{vmID}/ports/{portID}
func (s *Server) RemovePort(w http.ResponseWriter, r *http.Request) {
	portID := chi.URLParam(r, "portID")

	if err := s.portFwd.Remove(portID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListHostInterfaces handles GET /api/v1/host/interfaces
func (s *Server) ListHostInterfaces(w http.ResponseWriter, r *http.Request) {
	ifaces, err := network.DiscoverInterfaces()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ifaces)
}
