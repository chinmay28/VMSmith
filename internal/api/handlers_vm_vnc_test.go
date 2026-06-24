package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestCreateVM_VNCPasswordRedacted(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	spec := types.VMSpec{
		Name:        "vnc-protected",
		Image:       "ubuntu-22.04",
		CPUs:        2,
		RAMMB:       2048,
		VNCPassword: "hunter2",
	}

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, spec))
	if err != nil {
		t.Fatalf("POST /vms: %v", err)
	}
	if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 or 422", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "vnc_password_key_missing") {
			t.Fatalf("422 body = %s, want vnc_password_key_missing", body)
		}
		return
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, needle := range []string{"hunter2", "vnc_password_hash", "vnc_password_enc"} {
		if strings.Contains(string(raw), needle) {
			t.Errorf("create response leaks %q: %s", needle, raw)
		}
	}

	createdID := extractVMID(t, raw)

	getResp, err := http.Get(ts.URL + "/api/v1/vms/" + createdID)
	if err != nil {
		t.Fatalf("GET vm: %v", err)
	}
	rawGet, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	for _, needle := range []string{"hunter2", "vnc_password_hash", "vnc_password_enc"} {
		if strings.Contains(string(rawGet), needle) {
			t.Errorf("GET response leaks %q: %s", needle, rawGet)
		}
	}

	listResp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("GET vms: %v", err)
	}
	rawList, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()
	for _, needle := range []string{"hunter2", "vnc_password_hash", "vnc_password_enc"} {
		if strings.Contains(string(rawList), needle) {
			t.Errorf("list response leaks %q: %s", needle, rawList)
		}
	}
}

func extractVMID(t *testing.T, raw []byte) string {
	t.Helper()
	const marker = `"id":"`
	idx := strings.Index(string(raw), marker)
	if idx < 0 {
		t.Fatalf("no id in response: %s", raw)
	}
	rest := string(raw)[idx+len(marker):]
	end := strings.Index(rest, `"`)
	return rest[:end]
}

func TestUpdateVM_VNCPasswordRunningRejected(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-on", Name: "on", State: types.VMStateRunning})

	pw := "newpass"
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-on", types.VMUpdateSpec{VNCPassword: &pw})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409; body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "vm_running") {
		t.Errorf("body = %s, want vm_running code", body)
	}
}

func TestUpdateVM_VNCPasswordStopped(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-off", Name: "off", State: types.VMStateStopped})

	pw := "rotate-me"
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-off", types.VMUpdateSpec{VNCPassword: &pw})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "rotate-me") || strings.Contains(string(body), "vnc_password_hash") {
		t.Errorf("PATCH response leaks secrets: %s", body)
	}

	clear := ""
	resp2 := patchJSON(t, ts.URL+"/api/v1/vms/vm-off", types.VMUpdateSpec{VNCPassword: &clear})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body2, _ := io.ReadAll(resp2.Body)
		t.Fatalf("clear status = %d, want 200; body=%s", resp2.StatusCode, body2)
	}
}

func TestCreateVM_VNCPasswordTooLong(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	spec := types.VMSpec{
		Name:        "vnc-too-long",
		Image:       "ubuntu-22.04",
		VNCPassword: strings.Repeat("x", 65),
	}
	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, spec))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "invalid_vnc_password") {
		t.Errorf("body = %s, want invalid_vnc_password", body)
	}
}
