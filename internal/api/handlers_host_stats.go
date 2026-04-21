package api

import "net/http"

// GetHostStats handles GET /api/v1/host/stats.
func (s *Server) GetHostStats(w http.ResponseWriter, r *http.Request) {
	vms, err := s.vmManager.List(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}

	stats, err := collectHostStats(r.Context(), s.hostStatsPath, len(vms))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
