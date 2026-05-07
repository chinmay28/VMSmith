package api

import (
	"encoding/json"
	"net/http"

	"github.com/vmsmith/vmsmith/pkg/version"
)

// GetVersion returns the running daemon's build information.
//
// The endpoint lives at /api/version (outside the authenticated /api/v1 tree)
// because version info is benign and routinely consumed by load-balancer
// health checks, monitoring agents, and the embedded GUI's footer before any
// API key has been entered.
func (s *Server) GetVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(version.Info())
}
