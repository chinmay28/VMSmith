package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func attachGPU(t *testing.T, tsURL, vmID, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(tsURL+"/api/v1/vms/"+vmID+"/gpus", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST gpus: %v", err)
	}
	return resp
}

func detachGPU(t *testing.T, tsURL, vmID, addr string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, tsURL+"/api/v1/vms/"+vmID+"/gpus/"+addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE gpus: %v", err)
	}
	return resp
}

func TestAttachGPU_StoppedVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "g", State: types.VMStateStopped})

	resp := attachGPU(t, ts.URL, "vm-1", `{"address":"01:00.0"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vm types.VM
	json.NewDecoder(resp.Body).Decode(&vm)
	if len(vm.Spec.GPUs) != 1 || vm.Spec.GPUs[0] != "0000:01:00.0" {
		t.Fatalf("GPUs = %v, want normalized [0000:01:00.0]", vm.Spec.GPUs)
	}
}

func TestAttachGPU_RunningVMRequiresForce(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "g", State: types.VMStateRunning})

	resp := attachGPU(t, ts.URL, "vm-1", `{"address":"01:00.0"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "vm_running")

	// force=true is allowed.
	resp2 := attachGPU(t, ts.URL, "vm-1", `{"address":"01:00.0","force":true}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("force status = %d, want 200", resp2.StatusCode)
	}
}

func TestAttachGPU_DuplicateConflict(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{
		ID: "vm-1", Name: "g", State: types.VMStateStopped,
		Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}},
	})

	resp := attachGPU(t, ts.URL, "vm-1", `{"address":"01:00.0"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "gpu_already_attached")
}

func TestAttachGPU_InvalidAddress(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "g", State: types.VMStateStopped})

	resp := attachGPU(t, ts.URL, "vm-1", `{"address":"not-a-pci-addr"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_gpu")
}

func TestAttachGPU_MissingAddress(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "g", State: types.VMStateStopped})

	resp := attachGPU(t, ts.URL, "vm-1", `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAttachGPU_QuotaEnforced(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxTotalGPUs = 1
	})
	defer cleanup()
	mockMgr.SeedVM(&types.VM{
		ID: "vm-1", Name: "holder", State: types.VMStateStopped,
		Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}},
	})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "wants", State: types.VMStateStopped})

	resp := attachGPU(t, ts.URL, "vm-2", `{"address":"02:00.0"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "quota_exceeded")
}

func TestDetachGPU_HappyPath(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{
		ID: "vm-1", Name: "g", State: types.VMStateStopped,
		Spec: types.VMSpec{GPUs: []string{"0000:01:00.0", "0000:02:00.0"}},
	})

	resp := detachGPU(t, ts.URL, "vm-1", "01:00.0")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vm types.VM
	json.NewDecoder(resp.Body).Decode(&vm)
	if len(vm.Spec.GPUs) != 1 || vm.Spec.GPUs[0] != "0000:02:00.0" {
		t.Fatalf("GPUs = %v, want [0000:02:00.0]", vm.Spec.GPUs)
	}
}

func TestDetachGPU_NotAttached(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "g", State: types.VMStateStopped})

	resp := detachGPU(t, ts.URL, "vm-1", "01:00.0")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "gpu_not_attached")
}

func TestAttachGPU_UnknownVM(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp := attachGPU(t, ts.URL, "vm-missing", `{"address":"01:00.0"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
