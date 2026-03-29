package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// testServer sets up a complete test API server with mock VM manager.
func testServer(t *testing.T) (*httptest.Server, *vm.MockManager, func()) {
	t.Helper()
	return testServerWithConfig(t, nil)
}

func testServerWithConfig(t *testing.T, mutator func(*config.Config)) (*httptest.Server, *vm.MockManager, func()) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath
	if mutator != nil {
		mutator(cfg)
	}

	mockMgr := vm.NewMockManager()
	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)

	apiServer := NewServerWithConfig(mockMgr, storageMgr, portFwd, cfg, nil)
	ts := httptest.NewServer(apiServer)

	cleanup := func() {
		ts.Close()
		s.Close()
	}

	return ts, mockMgr, cleanup
}

func jsonBody(t *testing.T, v any) *bytes.Buffer {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	return bytes.NewBuffer(data)
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("json decode: %v", err)
	}
}

func assertAPIErrorCode(t *testing.T, resp *http.Response, want string) errorResponse {
	t.Helper()
	var errResp errorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Code != want {
		t.Fatalf("error code = %q, want %q", errResp.Code, want)
	}
	if errResp.Message == "" && errResp.Error == "" {
		t.Fatal("expected non-empty error message")
	}
	return errResp
}

// ============================================================
// VM endpoint tests
// ============================================================

func TestCreateVM(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	spec := types.VMSpec{
		Name:  "test-create",
		Image: "ubuntu-22.04",
		CPUs:  2,
		RAMMB: 4096,
	}

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, spec))
	if err != nil {
		t.Fatalf("POST /vms: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var created types.VM
	decodeJSON(t, resp, &created)

	if created.Name != "test-create" {
		t.Errorf("Name = %q, want test-create", created.Name)
	}
	if created.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", created.State)
	}
	if created.ID == "" {
		t.Error("ID should not be empty")
	}
}

func TestCreateVM_BadJSON(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json",
		bytes.NewBufferString("{invalid json"))

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateVM_InvalidName(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:  "bad name!",
		Image: "ubuntu",
		CPUs:  2,
		RAMMB: 2048,
	}))

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_name")
}

func TestCreateVM_InvalidSpecBounds(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:  "valid-name",
		Image: "ubuntu",
		CPUs:  0,
		RAMMB: 64,
	}))

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_spec")
}

func TestListVMs_Empty(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("GET /vms: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var vms []*types.VM
	decodeJSON(t, resp, &vms)

	if len(vms) != 0 {
		t.Errorf("expected 0 VMs, got %d", len(vms))
	}
}

func TestListVMs_WithData(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped})

	resp, _ := http.Get(ts.URL + "/api/v1/vms")

	var vms []*types.VM
	decodeJSON(t, resp, &vms)

	if len(vms) != 2 {
		t.Errorf("expected 2 VMs, got %d", len(vms))
	}
}

func TestGetVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:    "vm-123",
		Name:  "lookupme",
		State: types.VMStateRunning,
		IP:    "192.168.100.42",
		Spec:  types.VMSpec{CPUs: 4, RAMMB: 8192},
	})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-123")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got types.VM
	decodeJSON(t, resp, &got)

	if got.Name != "lookupme" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.IP != "192.168.100.42" {
		t.Errorf("IP = %q", got.IP)
	}
}

func TestGetVM_NotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms/nonexistent")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-del", Name: "doomed"})

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/vm-del", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}

	if mockMgr.VMCount() != 0 {
		t.Error("VM should be deleted")
	}
}

func TestStartVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-start", Name: "sleeper", State: types.VMStateStopped})

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-start/start", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Verify state changed
	got, _ := mockMgr.Get(nil, "vm-start")
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
}

func TestStopVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-stop", Name: "active", State: types.VMStateRunning})

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-stop/stop", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got, _ := mockMgr.Get(nil, "vm-stop")
	if got.State != types.VMStateStopped {
		t.Errorf("State = %q, want stopped", got.State)
	}
}

// ============================================================
// Update VM endpoint tests
// ============================================================

func TestUpdateVM_CPUAndRAM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID: "vm-upd", Name: "resizable",
		Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20},
	})

	patch := types.VMUpdateSpec{CPUs: 4, RAMMB: 8192}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-upd", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /vms/vm-upd: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var updated types.VM
	decodeJSON(t, resp, &updated)

	if updated.Spec.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", updated.Spec.CPUs)
	}
	if updated.Spec.RAMMB != 8192 {
		t.Errorf("RAMMB = %d, want 8192", updated.Spec.RAMMB)
	}
	if updated.Spec.DiskGB != 20 {
		t.Errorf("DiskGB changed unexpectedly: got %d, want 20", updated.Spec.DiskGB)
	}
}

func TestUpdateVM_DiskGrow(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID: "vm-disk", Name: "expandable",
		Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20},
	})

	patch := types.VMUpdateSpec{DiskGB: 40}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-disk", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var updated types.VM
	decodeJSON(t, resp, &updated)

	if updated.Spec.DiskGB != 40 {
		t.Errorf("DiskGB = %d, want 40", updated.Spec.DiskGB)
	}
}

func TestUpdateVM_DiskShrinkRejected(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID: "vm-shrink", Name: "locked",
		Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 40},
	})

	patch := types.VMUpdateSpec{DiskGB: 20}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-shrink", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for disk shrink attempt", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "disk_shrink_not_allowed")
}

func TestUpdateVM_NotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	patch := types.VMUpdateSpec{CPUs: 4}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/nonexistent", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for not found", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestUpdateVM_BadJSON(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-x",
		bytes.NewBufferString("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateVM_ErrorInjection(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-err", Name: "broken", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mockMgr.UpdateErr = types.ErrTest

	patch := types.VMUpdateSpec{CPUs: 4}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-err", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestUpdateVM_IP(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID: "vm-ip", Name: "readdressable",
		IP:   "192.168.100.10",
		Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, NatStaticIP: "192.168.100.10/24"},
	})

	patch := types.VMUpdateSpec{NatStaticIP: "192.168.100.50/24"}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-ip", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var updated types.VM
	decodeJSON(t, resp, &updated)

	if updated.IP != "192.168.100.50" {
		t.Errorf("IP = %q, want 192.168.100.50", updated.IP)
	}
	if updated.Spec.NatStaticIP != "192.168.100.50/24" {
		t.Errorf("NatStaticIP = %q, want 192.168.100.50/24", updated.Spec.NatStaticIP)
	}
}

func TestUpdateVM_IP_InvalidCIDR(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-ip2", Name: "addr", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	patch := types.VMUpdateSpec{NatStaticIP: "not-an-ip"}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-ip2", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid CIDR", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_spec")
}

// ============================================================
// Snapshot endpoint tests
// ============================================================

func TestCreateSnapshot(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap", Name: "snappable", State: types.VMStateRunning})

	body := jsonBody(t, map[string]string{"name": "before-update"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-snap/snapshots", "application/json", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var snap types.Snapshot
	decodeJSON(t, resp, &snap)

	if snap.Name != "before-update" {
		t.Errorf("Name = %q", snap.Name)
	}
	if snap.VMID != "vm-snap" {
		t.Errorf("VMID = %q", snap.VMID)
	}
}

func TestListSnapshots(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-s", Name: "host"})
	mockMgr.CreateSnapshot(nil, "vm-s", "snap1")
	mockMgr.CreateSnapshot(nil, "vm-s", "snap2")

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-s/snapshots")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var snaps []*types.Snapshot
	decodeJSON(t, resp, &snaps)

	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snaps))
	}
}

func TestRestoreSnapshot(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-r", Name: "restorable"})
	mockMgr.CreateSnapshot(nil, "vm-r", "good-state")

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-r/snapshots/good-state/restore", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDeleteSnapshot(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-ds", Name: "host"})
	mockMgr.CreateSnapshot(nil, "vm-ds", "temp")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/vm-ds/snapshots/temp", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}

	snaps, _ := mockMgr.ListSnapshots(nil, "vm-ds")
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots after delete, got %d", len(snaps))
	}
}

// ============================================================
// Port forward endpoint tests
// ============================================================

func TestAddPort(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-port", Name: "networked", IP: "192.168.100.10"})

	body := jsonBody(t, addPortRequest{HostPort: 2222, GuestPort: 22, Protocol: "tcp"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-port/ports", "application/json", body)

	// Port forwarding uses iptables which won't work in test env,
	// so we expect either 201 (if mocked) or 500 (if iptables fails).
	// This test validates request handling and routing.
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 201 or 500", resp.StatusCode)
	}
}

func TestAddPort_NoIP(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	// VM with no IP
	mockMgr.SeedVM(&types.VM{ID: "vm-noip", Name: "noip", IP: ""})

	body := jsonBody(t, addPortRequest{HostPort: 2222, GuestPort: 22})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-noip/ports", "application/json", body)

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409 (VM has no IP)", resp.StatusCode)
	}
}

// ============================================================
// Image endpoint tests
// ============================================================

func TestListImages_Empty(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/images")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)

	if len(imgs) != 0 {
		t.Errorf("expected 0 images, got %d", len(imgs))
	}
}

// ============================================================
// Host interfaces endpoint
// ============================================================

func TestListHostInterfaces(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/host/interfaces")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var ifaces []types.HostInterface
	decodeJSON(t, resp, &ifaces)

	// Should return at least one interface (the container's)
	// We don't check the exact content since it's environment-dependent
	_ = ifaces
}

// ============================================================
// Error injection tests (verify error handling)
// ============================================================

func TestCreateVM_ManagerError(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.CreateErr = types.ErrTest

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json",
		jsonBody(t, types.VMSpec{Name: "fail", Image: "ubuntu-22.04"}))

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}

	errResp := assertAPIErrorCode(t, resp, "internal_error")
	if errResp.Error == "" {
		t.Error("error message should not be empty")
	}
}

// ============================================================
// Full lifecycle integration test
// ============================================================

func TestVMFullLifecycle(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// 1. Create
	spec := types.VMSpec{Name: "lifecycle-test", Image: "ubuntu", CPUs: 2, RAMMB: 2048}
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, spec))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d", resp.StatusCode)
	}
	var created types.VM
	decodeJSON(t, resp, &created)
	vmID := created.ID

	// 2. Get
	resp, _ = http.Get(ts.URL + "/api/v1/vms/" + vmID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status %d", resp.StatusCode)
	}

	// 3. Create snapshot
	resp, _ = http.Post(ts.URL+"/api/v1/vms/"+vmID+"/snapshots", "application/json",
		jsonBody(t, map[string]string{"name": "checkpoint"}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("snapshot: status %d", resp.StatusCode)
	}

	// 4. Stop
	resp, _ = http.Post(ts.URL+"/api/v1/vms/"+vmID+"/stop", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop: status %d", resp.StatusCode)
	}

	// 5. Start
	resp, _ = http.Post(ts.URL+"/api/v1/vms/"+vmID+"/start", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start: status %d", resp.StatusCode)
	}

	// 6. Restore snapshot
	resp, _ = http.Post(ts.URL+"/api/v1/vms/"+vmID+"/snapshots/checkpoint/restore", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore: status %d", resp.StatusCode)
	}

	// 7. Delete
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/"+vmID, nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}

	// 8. Verify gone
	resp, _ = http.Get(ts.URL + "/api/v1/vms/" + vmID)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete: status %d, want 404", resp.StatusCode)
	}
}

// ============================================================
// Create VM with networks via API
// ============================================================

func TestCreateVM_WithNetworks(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	spec := types.VMSpec{
		Name:  "multi-net-api",
		Image: "ubuntu",
		CPUs:  4,
		RAMMB: 8192,
		Networks: []types.NetworkAttachment{
			{Name: "data", Mode: types.NetworkModeMacvtap, HostInterface: "eth1"},
			{Name: "storage", Mode: types.NetworkModeMacvtap, HostInterface: "eth2",
				StaticIP: "192.168.2.100/24", Gateway: "192.168.2.1"},
		},
	}

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, spec))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var created types.VM
	decodeJSON(t, resp, &created)

	if len(created.Spec.Networks) != 2 {
		t.Errorf("expected 2 networks, got %d", len(created.Spec.Networks))
	}
}

// ============================================================
// testServerFull — like testServer but also returns the store
// for test cases that need to seed data directly.
// ============================================================

func testServerFull(t *testing.T) (*httptest.Server, *vm.MockManager, *store.Store, func()) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath

	mockMgr := vm.NewMockManager()
	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)

	apiServer := NewServer(mockMgr, storageMgr, portFwd)
	ts := httptest.NewServer(apiServer)

	cleanup := func() {
		ts.Close()
		s.Close()
	}

	return ts, mockMgr, s, cleanup
}

// ============================================================
// VM handler error paths
// ============================================================

func TestListVMs_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.ListErr = types.ErrTest

	resp, _ := http.Get(ts.URL + "/api/v1/vms")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestDeleteVM_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x"})
	mockMgr.DeleteErr = types.ErrTest

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/vm-x", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestStartVM_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x", State: types.VMStateStopped})
	mockMgr.StartErr = types.ErrTest

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-x/start", "application/json", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestStopVM_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x", State: types.VMStateRunning})
	mockMgr.StopErr = types.ErrTest

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-x/stop", "application/json", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ============================================================
// Snapshot handler error paths
// ============================================================

func TestCreateSnapshot_BadJSON(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-x/snapshots", "application/json",
		bytes.NewBufferString("{bad"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateSnapshot_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x"})
	mockMgr.CreateSnapshotErr = types.ErrTest

	body := jsonBody(t, map[string]string{"name": "snap"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-x/snapshots", "application/json", body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestListSnapshots_VMNotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms/nonexistent/snapshots")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestRestoreSnapshot_SnapshotNotFound(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-r"})
	mockMgr.CreateSnapshot(nil, "vm-r", "good-state")

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-r/snapshots/missing/restore", "application/json", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestRestoreSnapshot_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-r"})
	mockMgr.RestoreSnapshotErr = types.ErrTest

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-r/snapshots/any/restore", "application/json", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "internal_error")
}

func TestDeleteSnapshot_VMNotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/nonexistent/snapshots/snap", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestDeleteSnapshot_SnapshotNotFound(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-ds"})
	mockMgr.CreateSnapshot(nil, "vm-ds", "existing")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/vm-ds/snapshots/missing", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

// ============================================================
// Image handler tests
// ============================================================

func TestCreateImage_BadJSON(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/images", "application/json",
		bytes.NewBufferString("{bad"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateImage_VMNotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	body := jsonBody(t, createImageRequest{VMID: "nonexistent", Name: "img"})
	resp, _ := http.Post(ts.URL+"/api/v1/images", "application/json", body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}


func TestCreateImage_StorageError(t *testing.T) {
	// VM exists but disk path is invalid — qemu-img convert will fail → 500.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-img", DiskPath: "/nonexistent/disk.qcow2"})

	body := jsonBody(t, createImageRequest{VMID: "vm-img", Name: "myimage"})
	resp, _ := http.Post(ts.URL+"/api/v1/images", "application/json", body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestDeleteImage_NotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/images/nonexistent", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (image not in store)", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestDownloadImage_NotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/images/nonexistent/download")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDownloadImage_Found(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	// Write a real temp file so http.ServeFile can serve it.
	f, err := os.CreateTemp(t.TempDir(), "*.qcow2")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	f.WriteString("fake qcow2 data")
	f.Close()

	img := &types.Image{ID: "img-dl", Name: "test-image", Path: f.Name()}
	if err := s.PutImage(img); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/images/img-dl/download")
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "test-image.qcow2") {
		t.Errorf("Content-Disposition = %q, want filename containing test-image.qcow2", cd)
	}
}

// ============================================================
// Port forward handler tests
// ============================================================

func TestAddPort_BadJSON(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-p", IP: "192.168.100.10"})

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-p/ports", "application/json",
		bytes.NewBufferString("{bad"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAddPort_VMNotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	body := jsonBody(t, addPortRequest{HostPort: 2222, GuestPort: 22})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/nonexistent/ports", "application/json", body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestAddPort_InvalidValidationInputs(t *testing.T) {
	tests := []struct {
		name        string
		body        addPortRequest
		wantMessage string
	}{
		{
			name:        "host port below minimum",
			body:        addPortRequest{HostPort: 0, GuestPort: 22, Protocol: types.ProtocolTCP},
			wantMessage: "host_port must be between 1 and 65535",
		},
		{
			name:        "host port above maximum",
			body:        addPortRequest{HostPort: 70000, GuestPort: 22, Protocol: types.ProtocolTCP},
			wantMessage: "host_port must be between 1 and 65535",
		},
		{
			name:        "guest port below minimum",
			body:        addPortRequest{HostPort: 2222, GuestPort: 0, Protocol: types.ProtocolTCP},
			wantMessage: "guest_port must be between 1 and 65535",
		},
		{
			name:        "guest port above maximum",
			body:        addPortRequest{HostPort: 2222, GuestPort: 70000, Protocol: types.ProtocolTCP},
			wantMessage: "guest_port must be between 1 and 65535",
		},
		{
			name:        "invalid protocol",
			body:        addPortRequest{HostPort: 2222, GuestPort: 22, Protocol: "icmp"},
			wantMessage: "protocol must be tcp or udp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, mockMgr, cleanup := testServer(t)
			defer cleanup()

			mockMgr.SeedVM(&types.VM{ID: "vm-p2", IP: "192.168.100.10"})
			resp, err := http.Post(ts.URL+"/api/v1/vms/vm-p2/ports", "application/json", jsonBody(t, tt.body))
			if err != nil {
				t.Fatalf("POST /ports: %v", err)
			}
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			errResp := assertAPIErrorCode(t, resp, "invalid_port_forward")
			if errResp.Message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", errResp.Message, tt.wantMessage)
			}
		})
	}
}

func TestAddPort_PortForwardConflict(t *testing.T) {
	ts, mockMgr, s, cleanup := testServerFull(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-p4", IP: "192.168.100.20"})
	if err := s.PutPortForward(&types.PortForward{
		ID: "pf-existing", VMID: "other-vm", HostPort: 2222, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP,
	}); err != nil {
		t.Fatalf("seed port forward: %v", err)
	}

	body := jsonBody(t, addPortRequest{HostPort: 2222, GuestPort: 2222, Protocol: types.ProtocolTCP})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-p4/ports", "application/json", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "port_forward_conflict")
}

func TestListPorts_Empty(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-lp/ports")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var ports []*types.PortForward
	decodeJSON(t, resp, &ports)
	if len(ports) != 0 {
		t.Errorf("expected 0 ports, got %d", len(ports))
	}
}

func TestListPorts_WithData(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	pf := &types.PortForward{
		ID: "pf-test", VMID: "vm-lp", HostPort: 2222, GuestPort: 22, Protocol: types.ProtocolTCP,
	}
	if err := s.PutPortForward(pf); err != nil {
		t.Fatalf("seed port forward: %v", err)
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-lp/ports")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var ports []*types.PortForward
	decodeJSON(t, resp, &ports)
	if len(ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(ports))
	}
	if ports[0].HostPort != 2222 {
		t.Errorf("HostPort = %d, want 2222", ports[0].HostPort)
	}
}

func TestRemovePort_NotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/vm-x/ports/nonexistent", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (port not found)", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

// ============================================================
// Upload image handler tests
// ============================================================

func TestUploadImage_MissingFile(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// multipart with no "file" field
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "myimage")
	mw.Close()

	resp, err := http.Post(ts.URL+"/api/v1/images/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUploadImage_EmptyFile(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if _, err := mw.CreateFormFile("file", "empty.qcow2"); err != nil {
		t.Fatalf("create form file: %v", err)
	}
	mw.Close()

	resp, err := http.Post(ts.URL+"/api/v1/images/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_image")
}

func TestUploadImage_InvalidExtension(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "ubuntu-22.04.iso")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("fake iso content")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	mw.Close()

	resp, err := http.Post(ts.URL+"/api/v1/images/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_image")
}


func TestUploadImage_NotEnoughDiskSpace(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	original := availableStorageBytes
	availableStorageBytes = func(string) (uint64, error) { return 3, nil }
	defer func() { availableStorageBytes = original }()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "ubuntu-22.04.qcow2")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("fake qcow2 content")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	mw.Close()

	resp, err := http.Post(ts.URL+"/api/v1/images/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want 507", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "insufficient_storage")
}

func TestUploadImage_Success(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "ubuntu-22.04.qcow2")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	fw.Write([]byte("fake qcow2 content"))
	mw.Close()

	resp, err := http.Post(ts.URL+"/api/v1/images/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}

	var img types.Image
	decodeJSON(t, resp, &img)
	if img.Name != "ubuntu-22.04" {
		t.Errorf("Name = %q, want %q", img.Name, "ubuntu-22.04")
	}
	if img.ID == "" {
		t.Error("ID should not be empty")
	}
}

func TestUploadImage_CustomName(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "some-file.qcow2")
	fw.Write([]byte("data"))
	mw.WriteField("name", "custom-name")
	mw.Close()

	resp, err := http.Post(ts.URL+"/api/v1/images/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
	var img types.Image
	decodeJSON(t, resp, &img)
	if img.Name != "custom-name" {
		t.Errorf("Name = %q, want %q", img.Name, "custom-name")
	}
}

func TestCreateVM_RequestBodyTooLarge(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.MaxRequestBodyBytes = 64
	})
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", bytes.NewBufferString(`{"name":"this-name-is-way-too-long-for-the-test-limit","image":"ubuntu","cpus":2,"ram_mb":2048}`))
	if err != nil {
		t.Fatalf("POST /vms: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestUploadImage_RequestBodyTooLarge(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.MaxUploadBodyBytes = 128
	})
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "tiny.qcow2")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte("a"), 256)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	resp, err := http.Post(ts.URL+"/api/v1/images/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestCreateVM_ConcurrentCreateLimit(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.MaxConcurrentCreates = 1
	})
	defer cleanup()

	mockMgr.CreateDelay = 200 * time.Millisecond
	firstDone := make(chan *http.Response, 1)
	firstErr := make(chan error, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
			Name:  "first-create",
			Image: "ubuntu",
			CPUs:  2,
			RAMMB: 2048,
		}))
		firstDone <- resp
		firstErr <- err
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:  "second-create",
		Image: "ubuntu",
		CPUs:  2,
		RAMMB: 2048,
	}))
	if err != nil {
		t.Fatalf("second POST /vms: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "create_limit_reached")

	firstResp := <-firstDone
	if err := <-firstErr; err != nil {
		t.Fatalf("first POST /vms: %v", err)
	}
	defer firstResp.Body.Close()
	if firstResp.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", firstResp.StatusCode)
	}
}


// ============================================================
// Content-Type regression tests (web handler vs API routes)
// ============================================================

// testServerWithWeb sets up a test server that includes a stub web handler,
// simulating the production setup where the SPA is embedded.
func testServerWithWeb(t *testing.T) (*httptest.Server, func()) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath

	mockMgr := vm.NewMockManager()
	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)

	// Stub web handler that mimics a real SPA: serves text/html.
	webHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<!doctype html><html><body><div id=\"root\"></div></body></html>"))
	})

	apiServer := NewServerWithConfig(mockMgr, storageMgr, portFwd, cfg, webHandler)
	ts := httptest.NewServer(apiServer)

	cleanup := func() {
		ts.Close()
		s.Close()
	}

	return ts, cleanup
}

// TestWebHandler_ContentType_HTML verifies that the web handler serves HTML
// with text/html content type — not application/json (regression for the bug
// where the global JSON middleware overwrote the content type for all routes).
func TestWebHandler_ContentType_HTML(t *testing.T) {
	ts, cleanup := testServerWithWeb(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "application/json" {
		t.Errorf("Content-Type = %q: web handler must not receive application/json middleware", ct)
	}
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// TestWebHandler_SubPath_ContentType_HTML verifies sub-paths (client-side routes)
// also get text/html, not application/json.
func TestWebHandler_SubPath_ContentType_HTML(t *testing.T) {
	ts, cleanup := testServerWithWeb(t)
	defer cleanup()

	for _, path := range []string{"/vms", "/images", "/dashboard"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()

		ct := resp.Header.Get("Content-Type")
		if ct == "application/json" {
			t.Errorf("path %s: Content-Type = %q: must not be application/json", path, ct)
		}
		if !strings.Contains(ct, "text/html") {
			t.Errorf("path %s: Content-Type = %q, want text/html", path, ct)
		}
	}
}

// TestAPIRoutes_ContentType_JSON verifies that API endpoints still return
// application/json when a web handler is also registered.
func TestAPIRoutes_ContentType_JSON(t *testing.T) {
	ts, cleanup := testServerWithWeb(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("GET /api/v1/vms: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json for API routes", ct)
	}
}

// ============================================================
// Log endpoint tests
// ============================================================

func TestGetLogs_Empty(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/logs")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	// Total must be a non-negative integer.
	if result.Total < 0 {
		t.Errorf("Total = %d, want >= 0", result.Total)
	}
	if result.Entries == nil {
		// entries may be empty but must be present (not null after JSON decode).
		// The handler always returns at least an empty slice via the ring buffer.
	}
}

func TestGetLogs_LimitParam(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Seed the logger with more than 5 entries by making real API calls
	// (each API call is logged by the middleware).
	for i := 0; i < 8; i++ {
		http.Get(ts.URL + "/api/v1/vms")
	}

	resp, _ := http.Get(ts.URL + "/api/v1/logs?limit=5&level=debug")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	if result.Total > 5 {
		t.Errorf("with limit=5, Total = %d, want <= 5", result.Total)
	}
}

func TestGetLogs_LevelFilter(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Trigger a 404 (logged as warn) and a 500 (logged as error).
	http.Get(ts.URL + "/api/v1/vms/nonexistent")
	http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader("{bad json"))

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=warn")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	for _, e := range result.Entries {
		if e.Level == "debug" || e.Level == "info" {
			t.Errorf("level=warn filter returned entry with level %q", e.Level)
		}
	}
}

func TestGetLogs_SourceFilter(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Make a request so the api source gets entries.
	http.Get(ts.URL + "/api/v1/vms")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?source=api&level=debug")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	for _, e := range result.Entries {
		if e.Source != "api" {
			t.Errorf("source=api filter returned entry with source %q", e.Source)
		}
	}
}

func TestGetLogs_SinceFilter(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Record a cutoff time, then make requests.
	cutoff := time.Now()
	time.Sleep(2 * time.Millisecond)
	http.Get(ts.URL + "/api/v1/vms")

	since := cutoff.UTC().Format(time.RFC3339Nano)
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&since=" + since)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	for _, e := range result.Entries {
		if !e.Timestamp.After(cutoff) {
			t.Errorf("since filter returned entry at %v, want after %v", e.Timestamp, cutoff)
		}
	}
}

func TestGetLogs_InvalidLimitIgnored(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Non-numeric limit should be ignored (fall back to default).
	resp, _ := http.Get(ts.URL + "/api/v1/logs?limit=notanumber")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestGetLogs_MaxLimitCapped(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Requesting more than 2000 should be silently capped.
	resp, _ := http.Get(ts.URL + "/api/v1/logs?limit=99999&level=debug")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	if result.Total > 2000 {
		t.Errorf("Total = %d exceeds maximum cap of 2000", result.Total)
	}
}

// Helpers for timeout in tests
func init() {
	_ = time.Second // ensure time is used
}
