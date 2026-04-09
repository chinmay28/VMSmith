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
			authz := strings.TrimSpace(r.Header.Get("Authorization"))
			if !strings.HasPrefix(authz, "Bearer ") {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing bearer token", Code: "unauthorized", Message: "missing bearer token"})
				return
			}

			token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
			if token == "" || !matchesAnyAPIKey(token, keys) {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid api key", Code: "unauthorized", Message: "invalid api key"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func matchesAnyAPIKey(token string, keys []string) bool {
	for _, key := range keys {
		if subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1 {
			return true
		}
	}
	return false
}
