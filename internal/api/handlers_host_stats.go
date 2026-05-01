package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// GetHostStats handles GET /api/v1/host/stats.
func (s *Server) GetHostStats(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	stats, err := collectHostStats(r.Context(), s.hostStatsPath, len(vms), s.ActiveSSEConnections())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeAPIError(w, http.StatusRequestTimeout, types.NewAPIError("request_timeout", "request timed out"))
			return
		}
		writeAPIError(w, http.StatusInternalServerError, sanitizeManagerError(err))
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
