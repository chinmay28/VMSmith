package api

import (
	"encoding/json"
	"net/http"
	"runtime"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
	"github.com/vmsmith/vmsmith/pkg/version"
)

func TestGetVersion_ReturnsBuildInfo(t *testing.T) {
	prevVersion, prevCommit, prevDate := version.Version, version.Commit, version.BuildDate
	defer func() { version.Version, version.Commit, version.BuildDate = prevVersion, prevCommit, prevDate }()
	version.Version = "v9.9.9-test"
	version.Commit = "abc1234"
	version.BuildDate = "2026-05-06T00:00:00Z"

	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/version")
	if err != nil {
		t.Fatalf("GET /api/version: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}

	var info types.BuildInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if info.Version != "v9.9.9-test" || info.Commit != "abc1234" || info.BuildDate != "2026-05-06T00:00:00Z" {
		t.Errorf("BuildInfo = %+v, want overrides applied", info)
	}
	if info.GoVersion != runtime.Version() || info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Errorf("BuildInfo runtime fields = %+v, want runtime values", info)
	}
}

// /api/version is intentionally unauthenticated.  Hitting it without an API
// key must succeed even when auth is enabled on /api/v1.
func TestGetVersion_DoesNotRequireAuth(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.Auth.Enabled = true
		cfg.Daemon.Auth.APIKeys = []string{"secret-key"}
	})
	defer cleanup()

	// Sanity check: an authenticated route returns 401 without the key.
	authResp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("GET /api/v1/vms: %v", err)
	}
	authResp.Body.Close()
	if authResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/v1/vms status = %d, want 401 to confirm auth is wired", authResp.StatusCode)
	}

	// /api/version must still return 200 without a key.
	resp, err := http.Get(ts.URL + "/api/version")
	if err != nil {
		t.Fatalf("GET /api/version: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (unauthenticated)", resp.StatusCode)
	}
}
