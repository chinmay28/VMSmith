package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func rbacServer(t *testing.T) (string, func(), *typesSeeder) {
	t.Helper()
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.Auth.Enabled = true
		cfg.Daemon.Auth.APIKeys = []string{"legacy-key"}
		cfg.Daemon.Auth.Keys = []config.APIKeyConfig{
			{Key: "admin-key", Role: "admin", Name: "root-user"},
			{Key: "operator-key", Role: "operator", Name: "ops"},
			{Key: "viewer-key", Role: "viewer", Name: "auditor"},
		}
	})
	return ts.URL, cleanup, &typesSeeder{mock: mockMgr}
}

// typesSeeder gives the tests a compact seeding handle.
type typesSeeder struct{ mock seedableManager }

type seedableManager interface{ SeedVM(vm *types.VM) }

func doAuthed(t *testing.T, method, url, key, body string) *http.Response {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func TestRBAC_ViewerReadOnly(t *testing.T) {
	base, cleanup, seed := rbacServer(t)
	defer cleanup()
	seed.mock.SeedVM(&types.VM{ID: "vm-1", Name: "a", State: types.VMStateRunning})

	// Reads are allowed.
	resp := doAuthed(t, http.MethodGet, base+"/api/v1/vms", "viewer-key", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer GET /vms = %d, want 200", resp.StatusCode)
	}

	// Lifecycle verbs are not.
	resp = doAuthed(t, http.MethodPost, base+"/api/v1/vms/vm-1/stop", "viewer-key", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer POST /stop = %d, want 403", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "forbidden")
}

func TestRBAC_OperatorLifecycleAllowedMutationsForbidden(t *testing.T) {
	base, cleanup, seed := rbacServer(t)
	defer cleanup()
	seed.mock.SeedVM(&types.VM{ID: "vm-1", Name: "a", State: types.VMStateRunning})

	// Lifecycle verb allowed.
	resp := doAuthed(t, http.MethodPost, base+"/api/v1/vms/vm-1/stop", "operator-key", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("operator POST /stop = %d, want 2xx", resp.StatusCode)
	}

	// Create is admin-only.
	resp = doAuthed(t, http.MethodPost, base+"/api/v1/vms", "operator-key", `{"name":"x","image":"img"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator POST /vms = %d, want 403", resp.StatusCode)
	}

	// Delete is admin-only.
	resp = doAuthed(t, http.MethodDelete, base+"/api/v1/vms/vm-1", "operator-key", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator DELETE /vms/vm-1 = %d, want 403", resp.StatusCode)
	}
}

func TestRBAC_OperatorBulkLifecycleAllowedBulkDeleteForbidden(t *testing.T) {
	base, cleanup, seed := rbacServer(t)
	defer cleanup()
	seed.mock.SeedVM(&types.VM{ID: "vm-1", Name: "a", State: types.VMStateRunning})

	resp := doAuthed(t, http.MethodPost, base+"/api/v1/vms/bulk", "operator-key", `{"action":"stop","ids":["vm-1"]}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("operator bulk stop = %d, want 200", resp.StatusCode)
	}

	resp = doAuthed(t, http.MethodPost, base+"/api/v1/vms/bulk", "operator-key", `{"action":"delete","ids":["vm-1"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator bulk delete = %d, want 403", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "forbidden")
}

func TestRBAC_AdminAndLegacyKeysHaveFullAccess(t *testing.T) {
	base, cleanup, seed := rbacServer(t)
	defer cleanup()
	seed.mock.SeedVM(&types.VM{ID: "vm-1", Name: "a", State: types.VMStateStopped})

	for _, key := range []string{"admin-key", "legacy-key"} {
		resp := doAuthed(t, http.MethodPost, base+"/api/v1/vms", key,
			`{"name":"made-by-`+strings.Split(key, "-")[0]+`","image":"img"}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s POST /vms = %d, want 201", key, resp.StatusCode)
		}
	}
}

func TestRBAC_UnauthenticatedStill401(t *testing.T) {
	base, cleanup, _ := rbacServer(t)
	defer cleanup()

	resp := doAuthed(t, http.MethodGet, base+"/api/v1/vms", "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no key = %d, want 401", resp.StatusCode)
	}
	resp = doAuthed(t, http.MethodGet, base+"/api/v1/vms", "wrong-key", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad key = %d, want 401", resp.StatusCode)
	}
}

func TestRBAC_OperatorConsoleTicketAllowed(t *testing.T) {
	base, cleanup, seed := rbacServer(t)
	defer cleanup()
	seed.mock.SeedVM(&types.VM{ID: "vm-1", Name: "a", State: types.VMStateRunning})

	// The console store is not installed on the plain test server, so a
	// permitted request reaches the handler and gets its 503 — the point
	// here is that RBAC does NOT 403 the operator.
	resp := doAuthed(t, http.MethodPost, base+"/api/v1/vms/vm-1/console/ticket", "operator-key", "")
	resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("operator console ticket = 403, want non-403")
	}

	resp = doAuthed(t, http.MethodPost, base+"/api/v1/vms/vm-1/console/ticket", "viewer-key", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer console ticket = %d, want 403", resp.StatusCode)
	}
}

func TestBuildAuthKeys_UnknownRoleRejected(t *testing.T) {
	_, err := buildAuthKeys(config.AuthConfig{
		Enabled: true,
		Keys:    []config.APIKeyConfig{{Key: "k", Role: "superuser"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("err = %v, want unknown-role rejection", err)
	}
}

func TestMinimumRoleFor_Classification(t *testing.T) {
	cases := []struct {
		method, path string
		want         apiRole
	}{
		{"GET", "/api/v1/vms", roleViewer},
		{"GET", "/api/v1/events", roleViewer},
		{"POST", "/api/v1/vms/vm-1/start", roleOperator},
		{"POST", "/api/v1/vms/vm-1/force-stop", roleOperator},
		{"POST", "/api/v1/vms/vm-1/console/ticket", roleOperator},
		{"POST", "/api/v1/schedules/sched-1/run-now", roleOperator},
		{"POST", "/api/v1/vms/bulk", roleOperator},
		{"POST", "/api/v1/vms", roleAdmin},
		{"DELETE", "/api/v1/vms/vm-1", roleAdmin},
		{"PATCH", "/api/v1/vms/vm-1", roleAdmin},
		{"POST", "/api/v1/images/upload", roleAdmin},
		{"POST", "/api/v1/webhooks", roleAdmin},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, "http://x"+tc.path, nil)
		if got := minimumRoleFor(req); got != tc.want {
			t.Errorf("%s %s = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
