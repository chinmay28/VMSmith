package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/internal/config"
)

func apiKeyAuth(cfg config.AuthConfig) func(http.Handler) http.Handler {
	keys := make([]string, 0, len(cfg.APIKeys))
	for _, key := range cfg.APIKeys {
		trimmed := strings.TrimSpace(key)
		if trimmed != "" {
			keys = append(keys, trimmed)
		}
	}

	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractAPIKey(r)
			if token == "" {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing bearer token", Code: "unauthorized", Message: "missing bearer token"})
				return
			}
			if !matchesAnyAPIKey(token, keys) {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid api key", Code: "unauthorized", Message: "invalid api key"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractAPIKey pulls the API key from either the Authorization header
// ("Bearer <token>") or the api_key query parameter.  The query-param
// fallback exists for browser clients that cannot set custom headers
// (e.g. EventSource for the /events/stream SSE endpoint).
func extractAPIKey(r *http.Request) string {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(authz, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	}
	return strings.TrimSpace(r.URL.Query().Get("api_key"))
}

func matchesAnyAPIKey(token string, keys []string) bool {
	for _, key := range keys {
		if subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1 {
			return true
		}
	}
	return false
}
