package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/console"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// SetConsoleStore installs a console-ticket store on the API server. When
// nil, the console-ticket endpoint returns 503 service_unavailable.
func (s *Server) SetConsoleStore(store *console.Store) {
	s.consoleStore = store
}

// IssueConsoleTicket handles POST /api/v1/vms/{vmID}/console/ticket.
//
// Validates the VM exists and is in the `running` state — a stopped VM is a
// configuration error from the caller's perspective, so we surface 409
// vm_not_running. On success we return a single-use ticket and the
// websocket URL the client should dial. The ticket carries the caller's
// API key so the websocket handler (5.1.4) can forward it.
func (s *Server) IssueConsoleTicket(w http.ResponseWriter, r *http.Request) {
	if s.consoleStore == nil {
		writeAPIError(w, http.StatusServiceUnavailable, types.NewAPIError("service_unavailable", "console subsystem is not enabled on this daemon"))
		return
	}

	id := chi.URLParam(r, "vmID")
	v, err := s.vmManager.Get(r.Context(), id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, sanitizeManagerError(err))
		return
	}
	if v.State != types.VMStateRunning {
		writeAPIError(w, http.StatusConflict, types.NewAPIError("vm_not_running", "vm must be running to open a console session"))
		return
	}

	apiKey := extractAPIKey(r)
	token, expires, err := s.consoleStore.IssueTicket(v.ID, apiKey)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, types.NewAPIError("internal_error", "failed to issue console ticket"))
		return
	}

	writeJSON(w, http.StatusOK, types.ConsoleTicket{
		Ticket:       token,
		ExpiresAt:    expires,
		WebsocketURL: "/api/v1/vms/" + v.ID + "/console?ticket=" + token,
	})
}
