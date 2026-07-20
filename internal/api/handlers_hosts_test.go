package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestListHosts_SingleHostMode(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID: "vm-1", Name: "a", State: types.VMStateRunning,
		Spec: types.VMSpec{CPUs: 4, RAMMB: 4096, DiskGB: 40, GPUs: []string{"0000:01:00.0"}},
	})
	mockMgr.SeedVM(&types.VM{
		ID: "vm-2", Name: "b", State: types.VMStateStopped,
		Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20},
	})

	resp, err := http.Get(ts.URL + "/api/v1/hosts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var hosts []types.HostStatus
	json.NewDecoder(resp.Body).Decode(&hosts)
	if len(hosts) != 1 {
		t.Fatalf("hosts = %+v, want 1 row (implicit local)", hosts)
	}
	h := hosts[0]
	if h.Name != "local" || !h.Default {
		t.Errorf("row = %+v, want default local", h)
	}
	if h.VMCount != 2 || h.CPUs != 6 || h.RAMMB != 6144 || h.DiskGB != 60 || h.GPUs != 1 {
		t.Errorf("aggregates = %+v, want 2 VMs / 6 cpus / 6144 ram / 60 disk / 1 gpu", h)
	}
	if h.Reachable != nil {
		t.Errorf("single-host mode should omit reachable, got %v", *h.Reachable)
	}
}

func TestListHosts_MultiHostConfigAndPlacement(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Hosts = []config.HostConfig{
			{Name: "hv2", URI: "qemu+ssh://root@hv2/system", Description: "rack 2"},
		}
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID: "vm-1", Name: "a", State: types.VMStateRunning,
		Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20},
	})
	mockMgr.SeedVM(&types.VM{
		ID: "vm-2", Name: "b", State: types.VMStateRunning,
		Spec: types.VMSpec{CPUs: 8, RAMMB: 8192, DiskGB: 80, Host: "hv2"},
	})

	resp, err := http.Get(ts.URL + "/api/v1/hosts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var hosts []types.HostStatus
	json.NewDecoder(resp.Body).Decode(&hosts)
	if len(hosts) != 2 {
		t.Fatalf("hosts = %+v, want local + hv2", hosts)
	}
	if hosts[0].Name != "local" || hosts[0].VMCount != 1 || hosts[0].CPUs != 2 {
		t.Errorf("local row = %+v", hosts[0])
	}
	if hosts[1].Name != "hv2" || hosts[1].VMCount != 1 || hosts[1].CPUs != 8 || hosts[1].Description != "rack 2" {
		t.Errorf("hv2 row = %+v", hosts[1])
	}
	if hosts[1].Default {
		t.Error("hv2 must not be the default host")
	}
}

func TestCreateVM_UnknownHostRejected(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json",
		strings.NewReader(`{"name":"misplaced","image":"img","host":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_host")
}

func TestCreateVM_ConfiguredHostAccepted(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Hosts = []config.HostConfig{{Name: "hv2", URI: "qemu+ssh://root@hv2/system"}}
	})
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json",
		strings.NewReader(`{"name":"placed","image":"img","host":"hv2"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var vm types.VM
	json.NewDecoder(resp.Body).Decode(&vm)
	if vm.Spec.Host != "hv2" {
		t.Errorf("host = %q, want hv2", vm.Spec.Host)
	}
}

func TestCreateVM_LocalHostAlwaysAccepted(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json",
		strings.NewReader(`{"name":"local-vm","image":"img","host":"local"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
}
