package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func withBulkLocation(t *testing.T) func(*config.Config) {
	t.Helper()
	bulk := t.TempDir()
	return func(cfg *config.Config) {
		cfg.Storage.BaseDir = "/var/lib/vmsmith/vms"
		cfg.Storage.DiskLocations = []config.DiskLocation{{Name: "bulk", Path: bulk, Description: "big HDD"}}
	}
}

func postDiskJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func decodeAPIError(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	return body.Code
}

func TestBuildStorageLocations(t *testing.T) {
	cfg := config.StorageConfig{
		BaseDir: "/var/lib/vmsmith/vms",
		DiskLocations: []config.DiskLocation{
			{Name: "bulk", Path: "/mnt/bulk", Description: "hdd"},
			{Name: "gone", Path: "/mnt/gone"},
		},
	}
	vms := []*types.VM{
		{ID: "vm-1", DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2"},
		{ID: "vm-2", DiskPath: "/mnt/bulk/vm-2/disk.qcow2"},
		{ID: "vm-3", DiskPath: "/mnt/bulk/vm-3/disk.qcow2"},
		{ID: "vm-4", DiskPath: "/elsewhere/vm-4/disk.qcow2"},
	}
	usage := func(path string) (uint64, uint64, error) {
		if path == "/mnt/gone" {
			return 0, 0, errors.New("not mounted")
		}
		return 1000, 400, nil
	}

	got := buildStorageLocations(cfg, vms, usage)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	def, bulk, gone := got[0], got[1], got[2]
	if def.Name != "default" || !def.Default || def.VMCount != 1 || !def.Available || def.FreeBytes != 400 || def.TotalBytes != 1000 {
		t.Errorf("default = %+v", def)
	}
	if bulk.Name != "bulk" || bulk.Default || bulk.VMCount != 2 || bulk.Description != "hdd" {
		t.Errorf("bulk = %+v", bulk)
	}
	if gone.Available || gone.Error != "not mounted" || gone.FreeBytes != 0 {
		t.Errorf("gone = %+v", gone)
	}
}

func TestListStorageLocationsEndpoint(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, withBulkLocation(t))
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2"})

	resp, err := http.Get(ts.URL + "/api/v1/host/storage-locations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var locs []types.StorageLocation
	if err := json.NewDecoder(resp.Body).Decode(&locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 2 || locs[0].Name != "default" || locs[0].VMCount != 1 || locs[1].Name != "bulk" {
		t.Fatalf("locations = %+v", locs)
	}
	if !locs[1].Available || locs[1].TotalBytes == 0 {
		t.Fatalf("bulk (a real temp dir) should be available with capacity: %+v", locs[1])
	}
}

func TestCreateVMWithDiskLocation(t *testing.T) {
	mut := withBulkLocation(t)
	ts, mockMgr, cleanup := testServerWithConfig(t, mut)
	defer cleanup()
	mockMgr.DiskLocations = map[string]string{"bulk": "/mnt/bulk"}

	resp := postDiskJSON(t, ts.URL+"/api/v1/vms", `{"name":"placed","image":"img","disk_location":"bulk"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var created types.VM
	json.NewDecoder(resp.Body).Decode(&created)
	if created.Spec.DiskLocation != "bulk" || !strings.HasPrefix(created.DiskPath, "/mnt/bulk/") {
		t.Fatalf("created = %+v", created)
	}

	bad := postDiskJSON(t, ts.URL+"/api/v1/vms", `{"name":"nowhere","image":"img","disk_location":"nope"}`)
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest || decodeAPIError(t, bad) != "invalid_disk_location" {
		t.Fatalf("unknown location status = %d", bad.StatusCode)
	}
}

func TestMoveVMDisk(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, withBulkLocation(t))
	defer cleanup()
	mockMgr.DiskLocations = map[string]string{"bulk": "/mnt/bulk"}
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", State: types.VMStateStopped, DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2"})

	resp := postDiskJSON(t, ts.URL+"/api/v1/vms/vm-1/disk/move", `{"location":"bulk"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var moved types.VM
	json.NewDecoder(resp.Body).Decode(&moved)
	if moved.DiskPath != "/mnt/bulk/vm-1/disk.qcow2" || moved.Spec.DiskLocation != "bulk" {
		t.Fatalf("moved = %+v", moved)
	}
}

func TestMoveVMDiskErrors(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, withBulkLocation(t))
	defer cleanup()
	mockMgr.DiskLocations = map[string]string{"bulk": "/mnt/bulk"}
	mockMgr.SeedVM(&types.VM{ID: "vm-run", Name: "r", State: types.VMStateRunning, DiskPath: "/var/lib/vmsmith/vms/vm-run/disk.qcow2"})
	mockMgr.SeedVM(&types.VM{ID: "vm-stop", Name: "s", State: types.VMStateStopped, DiskPath: "/var/lib/vmsmith/vms/vm-stop/disk.qcow2"})

	cases := []struct {
		name, vm, body string
		wantStatus     int
		wantCode       string
	}{
		{"missing location", "vm-stop", `{}`, 400, "invalid_disk_location"},
		{"unknown location", "vm-stop", `{"location":"nope"}`, 400, "invalid_disk_location"},
		{"bad body", "vm-stop", `{`, 400, "invalid_request_body"},
		{"running vm", "vm-run", `{"location":"bulk"}`, 409, "vm_running"},
		{"unchanged", "vm-stop", `{"location":"default"}`, 409, "disk_location_unchanged"},
		{"unknown vm", "vm-missing", `{"location":"bulk"}`, 404, "resource_not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := postDiskJSON(t, ts.URL+"/api/v1/vms/"+tc.vm+"/disk/move", tc.body)
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if code := decodeAPIError(t, resp); code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}

	for code, status := range map[string]int{
		"insufficient_storage":      507,
		"disk_location_unavailable": 422,
		"disk_move_conflict":        409,
	} {
		mockMgr.MoveDiskErr = types.NewAPIError(code, "x")
		resp := postDiskJSON(t, ts.URL+"/api/v1/vms/vm-stop/disk/move", `{"location":"bulk"}`)
		resp.Body.Close()
		if resp.StatusCode != status {
			t.Errorf("%s: status = %d, want %d", code, resp.StatusCode, status)
		}
	}
}
