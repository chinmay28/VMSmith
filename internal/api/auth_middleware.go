package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/vmsmith/vmsmith/internal/config"
)

// Role-based access control (roadmap 3.1.5). Keys carry one of three
// roles:
//
//	viewer   — read-only: GET/HEAD on everything
//	operator — viewer + VM lifecycle verbs (start/stop/restart/force-stop/
//	           reboot/suspend/resume, the bulk endpoint for those verbs),
//	           console tickets, and schedule run-now
//	admin    — everything (create/delete/config mutations included)
//
// Legacy `api_keys` entries keep their historical full access (admin).

type apiRole int

const (
	roleViewer apiRole = iota + 1
	roleOperator
	roleAdmin
)

func (r apiRole) String() string {
	switch r {
	case roleViewer:
		return "viewer"
	case roleOperator:
		return "operator"
	case roleAdmin:
		return "admin"
	}
	return "unknown"
}

func parseRole(s string) (apiRole, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "admin":
		return roleAdmin, true
	case "operator":
		return roleOperator, true
	case "viewer":
		return roleViewer, true
	}
	return 0, false
}

type roleContextKey struct{}

// requestRole returns the authenticated key's role. When auth is disabled
// there is no role in the context and every caller is implicitly admin.
func requestRole(r *http.Request) apiRole {
	if v, ok := r.Context().Value(roleContextKey{}).(apiRole); ok {
		return v
	}
	return roleAdmin
}

type authKey struct {
	key  string
	role apiRole
}

// buildAuthKeys flattens legacy api_keys (admin) and role-scoped keys.
// Unknown roles fail loudly so a typo can't silently grant admin.
func buildAuthKeys(cfg config.AuthConfig) ([]authKey, error) {
	var keys []authKey
	for _, key := range cfg.APIKeys {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			keys = append(keys, authKey{key: trimmed, role: roleAdmin})
		}
	}
	for i, k := range cfg.Keys {
		trimmed := strings.TrimSpace(k.Key)
		if trimmed == "" {
			return nil, fmt.Errorf("daemon.auth.keys[%d]: key must not be empty", i)
		}
		role, ok := parseRole(k.Role)
		if !ok {
			return nil, fmt.Errorf("daemon.auth.keys[%d]: unknown role %q (want admin, operator, or viewer)", i, k.Role)
		}
		keys = append(keys, authKey{key: trimmed, role: role})
	}
	return keys, nil
}

// operatorPOSTSuffixes are the request suffixes an operator key may POST
// to: VM lifecycle verbs, console tickets, and schedule run-now.
var operatorPOSTSuffixes = []string{
	"/start", "/stop", "/force-stop", "/restart", "/reboot",
	"/suspend", "/resume", "/console/ticket", "/run-now",
}

// minimumRoleFor classifies a request by the least-privileged role allowed
// to perform it. The bulk endpoint is operator-classified here; its delete
// action is additionally admin-gated in the handler (the middleware does
// not read request bodies).
func minimumRoleFor(r *http.Request) apiRole {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return roleViewer
	}
	if r.Method == http.MethodPost {
		p := strings.TrimRight(r.URL.Path, "/")
		for _, suffix := range operatorPOSTSuffixes {
			if strings.HasSuffix(p, suffix) {
				return roleOperator
			}
		}
		if strings.HasSuffix(p, "/vms/bulk") {
			return roleOperator
		}
	}
	return roleAdmin
}

func apiKeyAuth(cfg config.AuthConfig) func(http.Handler) http.Handler {
	keys, err := buildAuthKeys(cfg)
	if err != nil {
		// Config was validated at daemon startup; a broken config reaching
		// this point fails closed rather than open.
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "auth misconfigured", Code: "auth_misconfigured", Message: err.Error()})
			})
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
			role, ok := matchAPIKey(token, keys)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid api key", Code: "unauthorized", Message: "invalid api key"})
				return
			}
			if required := minimumRoleFor(r); role < required {
				writeJSON(w, http.StatusForbidden, errorResponse{
					Error:   "forbidden",
					Code:    "forbidden",
					Message: fmt.Sprintf("this endpoint requires the %s role (key has %s)", required, role),
				})
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), roleContextKey{}, role)))
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

// matchAPIKey constant-time-compares the token against every configured
// key, returning the matched key's role.
func matchAPIKey(token string, keys []authKey) (apiRole, bool) {
	var matched apiRole
	found := false
	for _, k := range keys {
		if subtle.ConstantTimeCompare([]byte(token), []byte(k.key)) == 1 && !found {
			matched = k.role
			found = true
		}
	}
	return matched, found
}
