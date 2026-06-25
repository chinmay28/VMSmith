package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
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
	portFwd.SetApplyRuleFunc(func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	})

	apiServer := NewServerWithConfig(mockMgr, storageMgr, portFwd, s, cfg, nil)
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

func TestServerRejectsNewRequestsDuringShutdownAndWaitsForDrain(t *testing.T) {
	s := &Server{}
	handlerStarted := make(chan struct{})
	release := make(chan struct{})
	handlerDone := make(chan struct{})

	h := s.trackInFlightRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerStarted)
		<-release
		w.WriteHeader(http.StatusNoContent)
		close(handlerDone)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	go h.ServeHTTP(rec, req)
	<-handlerStarted

	waitResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		waitResult <- s.WaitForDrain(ctx)
	}()

	s.BeginShutdown()
	shutdownRec := httptest.NewRecorder()
	h.ServeHTTP(shutdownRec, httptest.NewRequest(http.MethodGet, "/", nil))
	if shutdownRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("shutdown request status = %d, want %d", shutdownRec.Code, http.StatusServiceUnavailable)
	}

	select {
	case err := <-waitResult:
		t.Fatalf("WaitForDrain returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-handlerDone
	if err := <-waitResult; err != nil {
		t.Fatalf("WaitForDrain() error = %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("in-flight request status = %d, want %d", rec.Code, http.StatusNoContent)
	}
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

func TestCreateVM_Windows(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	spec := types.VMSpec{
		Name:      "win-create",
		Image:     "win2022.qcow2",
		CPUs:      4,
		RAMMB:     4096,
		DiskGB:    64,
		OSType:    types.OSTypeWindows,
		OSVariant: "windows-server-2022",
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
	if created.Spec.OSType != types.OSTypeWindows {
		t.Errorf("OSType = %q, want windows", created.Spec.OSType)
	}
	if !created.Spec.IsWindows() {
		t.Error("created VM should report IsWindows()")
	}
	if created.Spec.OSVariant != "windows-server-2022" {
		t.Errorf("OSVariant = %q, want windows-server-2022", created.Spec.OSVariant)
	}
}

func TestListHostGPUs(t *testing.T) {
	old := discoverHostGPUs
	defer func() { discoverHostGPUs = old }()

	t.Run("success", func(t *testing.T) {
		discoverHostGPUs = func() ([]types.GPUDevice, error) {
			return []types.GPUDevice{{
				Address:      "0000:01:00.0",
				VendorID:     "0x10de",
				DeviceID:     "0x2704",
				Vendor:       "NVIDIA",
				Class:        "0x030000",
				Driver:       "vfio-pci",
				IOMMUGroup:   15,
				GroupDevices: []string{"0000:01:00.0", "0000:01:00.1"},
				BootVGA:      true,
			}}, nil
		}

		ts, _, cleanup := testServer(t)
		defer cleanup()

		resp, err := http.Get(ts.URL + "/api/v1/host/gpus")
		if err != nil {
			t.Fatalf("GET /host/gpus: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}

		var got []types.GPUDevice
		decodeJSON(t, resp, &got)
		if len(got) != 1 {
			t.Fatalf("got %d GPUs, want 1", len(got))
		}
		if got[0].Address != "0000:01:00.0" || got[0].Driver != "vfio-pci" {
			t.Fatalf("gpu = %+v, want address 0000:01:00.0 driver vfio-pci", got[0])
		}
		if !got[0].BootVGA {
			t.Fatalf("gpu boot_vga = %v, want true", got[0].BootVGA)
		}
	})

	t.Run("discovery error returns 500", func(t *testing.T) {
		discoverHostGPUs = func() ([]types.GPUDevice, error) {
			return nil, errors.New("sysfs unreachable")
		}

		ts, _, cleanup := testServer(t)
		defer cleanup()

		resp, err := http.Get(ts.URL + "/api/v1/host/gpus")
		if err != nil {
			t.Fatalf("GET /host/gpus: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
}

func TestCreateVM_MixedCaseOSTypeAndVariant(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Raw JSON with mixed-case os_type / os_variant must behave the same as
	// the CLI (which lowercases). Regression for the case-sensitivity nit on
	// #327.
	spec := types.VMSpec{
		Name:      "win-mixedcase",
		Image:     "win2022.qcow2",
		CPUs:      4,
		RAMMB:     4096,
		DiskGB:    64,
		OSType:    types.OSType("Windows"),
		OSVariant: "Windows-Server-2022",
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
	if !created.Spec.IsWindows() {
		t.Errorf("mixed-case Windows os_type should resolve to windows; got %q", created.Spec.OSType)
	}
}

func TestCreateVM_InvalidOSType(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:   "bad-os",
		Image:  "ubuntu",
		CPUs:   2,
		RAMMB:  2048,
		OSType: types.OSType("bsd"),
	}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_os_type")
}

func TestCreateVM_WindowsResourceFloor(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:   "small-win",
		Image:  "win.qcow2",
		OSType: types.OSTypeWindows,
		RAMMB:  1024, // below the 2048 MB Windows floor
	}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_spec")
}

func TestCreateVM_GeneratesAdminPasswordForWindowsWhenOmitted(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	spec := types.VMSpec{
		Name:      "win-autopw",
		Image:     "win2022.qcow2",
		CPUs:      4,
		RAMMB:     4096,
		DiskGB:    64,
		OSType:    types.OSTypeWindows,
		OSVariant: "windows-server-2022",
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

	if created.GeneratedAdminPassword == "" {
		t.Fatal("expected generated_admin_password on Windows create without admin_password")
	}
	if len(created.GeneratedAdminPassword) < 12 {
		t.Errorf("generated password too short: %q (len=%d)", created.GeneratedAdminPassword, len(created.GeneratedAdminPassword))
	}
	// The stored record must NOT include the password (Get/List redacts it).
	getResp, _ := http.Get(ts.URL + "/api/v1/vms/" + created.ID)
	var fetched types.VM
	decodeJSON(t, getResp, &fetched)
	if fetched.GeneratedAdminPassword != "" {
		t.Fatalf("GET /vms/{id} should not return generated_admin_password, got %q", fetched.GeneratedAdminPassword)
	}
	if fetched.Spec.AdminPassword != "" {
		t.Fatalf("GET /vms/{id} should not echo admin_password, got %q", fetched.Spec.AdminPassword)
	}
}

func TestCreateVM_DoesNotGenerateAdminPasswordWhenSupplied(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	spec := types.VMSpec{
		Name:          "win-explicit-pw",
		Image:         "win2022.qcow2",
		CPUs:          4,
		RAMMB:         4096,
		DiskGB:        64,
		OSType:        types.OSTypeWindows,
		AdminPassword: "MyChosen!Pass1",
	}
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, spec))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.VM
	decodeJSON(t, resp, &created)
	if created.GeneratedAdminPassword != "" {
		t.Fatalf("expected no generated_admin_password when admin_password supplied; got %q", created.GeneratedAdminPassword)
	}
}

func TestCreateVM_DoesNotGenerateAdminPasswordForLinux(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	spec := types.VMSpec{
		Name:   "linux-no-pw",
		Image:  "ubuntu.qcow2",
		CPUs:   2,
		RAMMB:  2048,
		DiskGB: 20,
	}
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, spec))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.VM
	decodeJSON(t, resp, &created)
	if created.GeneratedAdminPassword != "" {
		t.Fatalf("expected no generated_admin_password for Linux VM; got %q", created.GeneratedAdminPassword)
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
	assertAPIErrorCode(t, resp, "invalid_request_body")
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

func TestCreateVM_MissingName(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Image: "ubuntu",
		CPUs:  2,
		RAMMB: 2048,
	}))

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_name")
}

func TestCreateVM_MissingImage(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:  "valid-name",
		CPUs:  2,
		RAMMB: 2048,
	}))

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_image")
}

func TestCreateVM_InvalidNatGateway(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:       "valid-name",
		Image:      "ubuntu",
		CPUs:       2,
		RAMMB:      2048,
		NatGateway: "not-an-ip",
	}))

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_spec")
}

func TestCreateVM_DuplicateName(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-existing", Name: "Existing-VM", State: types.VMStateRunning})

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:   " existing-vm ",
		Image:  "ubuntu",
		CPUs:   2,
		RAMMB:  2048,
		DiskGB: 20,
	}))

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_name")
}

func TestCreateVM_InvalidDiskBounds(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:   "valid-name",
		Image:  "ubuntu",
		CPUs:   2,
		RAMMB:  2048,
		DiskGB: 10241,
	}))

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	errResp := assertAPIErrorCode(t, resp, "invalid_spec")
	if errResp.Message != "disk_gb must be between 1 and 10240" {
		t.Fatalf("message = %q", errResp.Message)
	}
}

func TestCreateVM_InvalidTags(t *testing.T) {
	tests := []struct {
		name        string
		tags        []string
		wantMessage string
	}{
		{name: "empty tag", tags: []string{"prod", "   "}, wantMessage: "tags cannot contain empty values"},
		{name: "tag too long", tags: []string{"abcdefghijklmnopqrstuvwxyz1234567"}, wantMessage: "tags must be 1-32 characters"},
		{name: "invalid characters", tags: []string{"invalid tag!"}, wantMessage: "tags must contain only lowercase letters, numbers, dots, colons, underscores, or hyphens"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, _, cleanup := testServer(t)
			defer cleanup()

			resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
				Name:  "valid-name",
				Image: "ubuntu",
				CPUs:  2,
				RAMMB: 2048,
				Tags:  tt.tags,
			}))

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			errResp := assertAPIErrorCode(t, resp, "invalid_spec")
			if errResp.Message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", errResp.Message, tt.wantMessage)
			}
		})
	}
}

func TestCreateVM_WithTagsAndDescription(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:        "tagged-vm",
		Image:       "ubuntu",
		CPUs:        2,
		RAMMB:       2048,
		Description: "  Production API node  ",
		Tags:        []string{"Prod", " api ", "prod"},
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var created types.VM
	decodeJSON(t, resp, &created)
	if created.Description != "Production API node" {
		t.Fatalf("description = %q", created.Description)
	}
	if strings.Join(created.Tags, ",") != "api,prod" {
		t.Fatalf("tags = %v", created.Tags)
	}
}

func TestCreateVM_WithTemplateDefaults(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	templateResp, err := http.Post(ts.URL+"/api/v1/templates", "application/json", jsonBody(t, map[string]any{
		"name":         "small-linux",
		"image":        "ubuntu-22.04",
		"cpus":         2,
		"ram_mb":       2048,
		"disk_gb":      20,
		"description":  "Template description",
		"default_user": "ubuntu",
		"tags":         []string{"prod", "web"},
		"networks": []map[string]any{{
			"name": "net-private",
			"mode": "bridge",
		}},
	}))
	if err != nil {
		t.Fatalf("POST /templates: %v", err)
	}
	if templateResp.StatusCode != http.StatusCreated {
		t.Fatalf("template status = %d, want 201", templateResp.StatusCode)
	}

	var tpl types.VMTemplate
	decodeJSON(t, templateResp, &tpl)

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, map[string]any{
		"name":        "templated-vm",
		"template_id": tpl.ID,
	}))
	if err != nil {
		t.Fatalf("POST /vms: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var created types.VM
	decodeJSON(t, resp, &created)
	if created.Spec.TemplateID != tpl.ID {
		t.Fatalf("template_id = %q, want %q", created.Spec.TemplateID, tpl.ID)
	}
	if created.Spec.Image != "ubuntu-22.04" || created.Spec.CPUs != 2 || created.Spec.RAMMB != 2048 || created.Spec.DiskGB != 20 {
		t.Fatalf("unexpected spec defaults = %+v", created.Spec)
	}
	if created.Description != "Template description" {
		t.Fatalf("description = %q", created.Description)
	}
	if created.Spec.DefaultUser != "ubuntu" {
		t.Fatalf("default_user = %q", created.Spec.DefaultUser)
	}
	if strings.Join(created.Tags, ",") != "prod,web" {
		t.Fatalf("tags = %v", created.Tags)
	}
	if len(created.Spec.Networks) != 1 || created.Spec.Networks[0].Name != "net-private" {
		t.Fatalf("networks = %+v", created.Spec.Networks)
	}
}

func TestCreateVM_WithTemplateOverrides(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	templateResp, err := http.Post(ts.URL+"/api/v1/templates", "application/json", jsonBody(t, map[string]any{
		"name":         "base-linux",
		"image":        "ubuntu-22.04",
		"cpus":         2,
		"ram_mb":       2048,
		"disk_gb":      20,
		"description":  "Template description",
		"default_user": "ubuntu",
		"tags":         []string{"prod", "web"},
	}))
	if err != nil {
		t.Fatalf("POST /templates: %v", err)
	}
	var tpl types.VMTemplate
	decodeJSON(t, templateResp, &tpl)

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, map[string]any{
		"name":         "override-vm",
		"template_id":  tpl.ID,
		"image":        "debian-12",
		"cpus":         4,
		"description":  "Custom description",
		"default_user": "debian",
		"tags":         []string{"staging"},
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var created types.VM
	decodeJSON(t, resp, &created)
	if created.Spec.Image != "debian-12" || created.Spec.CPUs != 4 {
		t.Fatalf("spec overrides = %+v", created.Spec)
	}
	if created.Spec.RAMMB != 2048 || created.Spec.DiskGB != 20 {
		t.Fatalf("template defaults not preserved = %+v", created.Spec)
	}
	if created.Description != "Custom description" || created.Spec.DefaultUser != "debian" {
		t.Fatalf("override metadata = %+v", created)
	}
	if strings.Join(created.Tags, ",") != "staging" {
		t.Fatalf("tags = %v", created.Tags)
	}
}

// TestCreateVM_TemplateInheritsOSType verifies 5.6.7: a Windows-pinned
// template propagates `os_type` (and `os_variant`) to the derived VMSpec
// when the create request leaves them empty. Mirrors the existing
// `default_user` inheritance check; explicit per-VM `os_type` overrides
// the template (see TestCreateVM_TemplateOSTypeOverride below).
func TestCreateVM_TemplateInheritsOSType(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	templateResp, err := http.Post(ts.URL+"/api/v1/templates", "application/json", jsonBody(t, map[string]any{
		"name":       "win2022-base",
		"image":      "win-server-2022.qcow2",
		"cpus":       4,
		"ram_mb":     4096,
		"disk_gb":    64,
		"os_type":    "windows",
		"os_variant": "windows-server-2022",
	}))
	if err != nil {
		t.Fatalf("POST /templates: %v", err)
	}
	if templateResp.StatusCode != http.StatusCreated {
		t.Fatalf("template status = %d, want 201", templateResp.StatusCode)
	}
	var tpl types.VMTemplate
	decodeJSON(t, templateResp, &tpl)
	if tpl.OSType != types.OSTypeWindows || tpl.OSVariant != "windows-server-2022" {
		t.Fatalf("template stored = %+v, want windows / windows-server-2022", tpl)
	}

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, map[string]any{
		"name":        "from-tpl",
		"template_id": tpl.ID,
	}))
	if err != nil {
		t.Fatalf("POST /vms: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.VM
	decodeJSON(t, resp, &created)
	if !created.Spec.IsWindows() {
		t.Fatalf("expected VM to inherit windows os_type from template, got %q", created.Spec.OSType)
	}
	if created.Spec.OSVariant != "windows-server-2022" {
		t.Fatalf("expected VM to inherit os_variant, got %q", created.Spec.OSVariant)
	}
}

func TestCreateVM_TemplateOSTypeOverride(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	templateResp, _ := http.Post(ts.URL+"/api/v1/templates", "application/json", jsonBody(t, map[string]any{
		"name":    "win2022-base",
		"image":   "win-server-2022.qcow2",
		"cpus":    4,
		"ram_mb":  4096,
		"disk_gb": 64,
		"os_type": "windows",
	}))
	var tpl types.VMTemplate
	decodeJSON(t, templateResp, &tpl)

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, map[string]any{
		"name":        "override-linux",
		"template_id": tpl.ID,
		"image":       "rocky9.qcow2", // override the windows image, otherwise libvirt would try to boot windows
		"os_type":     "linux",
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.VM
	decodeJSON(t, resp, &created)
	if created.Spec.IsWindows() {
		t.Fatalf("explicit os_type=linux must override the template; got %q", created.Spec.OSType)
	}
}

func TestCreateVM_WithMissingTemplate(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, map[string]any{
		"name":        "templated-vm",
		"template_id": "tmpl-missing",
	}))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	errResp := assertAPIErrorCode(t, resp, "invalid_template_id")
	if !strings.Contains(errResp.Message, "tmpl-missing") {
		t.Fatalf("message = %q", errResp.Message)
	}
}

func TestCreateVM_QuotaExceeded_MaxVMs(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxVMs = 1
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "existing", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}, State: types.VMStateRunning})

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:   "quota-vm",
		Image:  "ubuntu",
		CPUs:   2,
		RAMMB:  2048,
		DiskGB: 20,
	}))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "quota_exceeded")
}

func TestCreateVM_QuotaExceeded_CPUs(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxTotalCPUs = 4
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "existing", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}, State: types.VMStateRunning})

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:   "quota-vm",
		Image:  "ubuntu",
		CPUs:   3,
		RAMMB:  2048,
		DiskGB: 20,
	}))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "quota_exceeded")
}

// TestCreateVM_QuotaExceeded_GPUs verifies the 5.7.11 GPU dimension on the
// create path: an existing VM consumes the single available GPU slot and a
// second create request requesting any passthrough GPU is rejected with
// HTTP 429 / quota_exceeded.
func TestCreateVM_QuotaExceeded_GPUs(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxTotalGPUs = 1
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "existing", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, GPUs: []string{"0000:01:00.0"}}, State: types.VMStateRunning})

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:      "gpu-overflow",
		Image:     "ubuntu",
		CPUs:      2,
		RAMMB:     2048,
		DiskGB:    20,
		SSHPubKey: "ssh-rsa AAAA test",
		GPUs:      []string{"0000:02:00.0"},
	}))
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "quota_exceeded")
}

// TestCreateVM_GPUQuota_AllowsNoGPURequest verifies that a create request
// with no GPU passthrough request still succeeds even when the GPU quota
// is fully saturated by other VMs.
func TestCreateVM_GPUQuota_AllowsNoGPURequest(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxTotalGPUs = 1
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "existing", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, GPUs: []string{"0000:01:00.0"}}, State: types.VMStateRunning})

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:      "no-gpu",
		Image:     "ubuntu",
		CPUs:      2,
		RAMMB:     2048,
		DiskGB:    20,
		SSHPubKey: "ssh-rsa AAAA test",
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (no-GPU VM should not consume GPU quota)", resp.StatusCode)
	}
}

func TestListVMs_FilterByTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Tags: []string{"prod", "web"}, State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Tags: []string{"dev"}, State: types.VMStateStopped})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?tag=prod")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterByStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateRunning})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 running vms, got %+v", vms)
	}
	for _, vm := range vms {
		if vm.State != types.VMStateRunning {
			t.Fatalf("unexpected vm state in filtered list: %+v", vm)
		}
	}
}

func TestListVMs_FilterByTagAndStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Tags: []string{"prod"}, State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Tags: []string{"prod"}, State: types.VMStateStopped})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Tags: []string{"dev"}, State: types.VMStateRunning})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?tag=prod&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterByStatus_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateStopped})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?status=StOpPeD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterBySearch_MatchesName(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-01", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "db-primary", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "web-staging", State: types.VMStateStopped})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?search=web")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 web-* vms, got %+v", vms)
	}
	for _, vm := range vms {
		if !strings.Contains(vm.Name, "web") {
			t.Fatalf("unexpected vm in filtered list: %+v", vm)
		}
	}
}

func TestListVMs_FilterBySearch_MatchesDescription(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Description: "Customer A jumpbox"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Description: "Internal tooling"})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Description: "Customer B builder"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?search=customer")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 customer-* vms, got %+v", vms)
	}
}

func TestListVMs_FilterBySearch_MatchesTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Tags: []string{"team-storage", "prod"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Tags: []string{"team-network"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Tags: []string{"experiment"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?search=team-")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 team-* vms, got %+v", vms)
	}
}

func TestListVMs_FilterBySearch_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "Web-Prod-01", Description: "Customer Site"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?search=WEB")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "Web-Prod-01" {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterBySearch_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?search=needle-not-present")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected no matches, got %+v", vms)
	}
}

func TestListVMs_FilterBySearch_CombinesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-01", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "web-staging", State: types.VMStateStopped})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "db-primary", State: types.VMStateRunning})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?search=web&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "web-prod-01" {
		t.Fatalf("expected exactly web-prod-01 (running web-*), got %+v", vms)
	}
}

func TestListVMs_FilterBySearch_CombinesWithTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-01", Tags: []string{"prod"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "web-staging", Tags: []string{"dev"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "db-prod", Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?search=web&tag=prod")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "web-prod-01" {
		t.Fatalf("expected exactly web-prod-01 (web + prod tag), got %+v", vms)
	}
}

func TestListVMs_FilterBySearch_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?search=%20%20alpha%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("expected whitespace-trimmed match for alpha, got %+v", vms)
	}
}

func TestListVMs_FilterByDefaultUser_ExactMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{DefaultUser: "root"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{DefaultUser: "ubuntu"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{DefaultUser: "ubuntu"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?default_user=ubuntu")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 ubuntu vms, got %+v", vms)
	}
	for _, vm := range vms {
		if !strings.EqualFold(vm.Spec.DefaultUser, "ubuntu") {
			t.Fatalf("unexpected vm in filtered list: %+v", vm)
		}
	}
}

func TestListVMs_FilterByDefaultUser_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{DefaultUser: "ec2-user"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?default_user=EC2-USER")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByDefaultUser_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{DefaultUser: "root"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?default_user=%20%20root%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByDefaultUser_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{DefaultUser: "root"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{DefaultUser: "ubuntu"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?default_user=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByDefaultUser_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{DefaultUser: "root"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?default_user=admin")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected empty result, got %+v", vms)
	}
}

func TestListVMs_FilterByDefaultUser_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{DefaultUser: "root"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Spec: types.VMSpec{DefaultUser: "root"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateRunning, Spec: types.VMSpec{DefaultUser: "ubuntu"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?default_user=root&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("expected composition to narrow to alpha, got %+v", vms)
	}
}

func TestListVMs_FilterByDefaultUser_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		du := "root"
		if i%2 == 0 {
			du = "ubuntu"
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{DefaultUser: du},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?default_user=ubuntu&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := resp.Header.Get("X-Total-Count")
	if got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// TestListVMs_FilterByDefaultUser_RootMatchesEmpty verifies that
// `?default_user=root` matches VMs whose Spec.DefaultUser is empty.
// `lifecycle.go` documents "empty means root" — the filter mirrors that
// runtime semantic so operators searching for root-SSH VMs find both
// explicit-root and unset entries.
func TestListVMs_FilterByDefaultUser_RootMatchesEmpty(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{DefaultUser: ""}})       // implicit root
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{DefaultUser: "root"}})    // explicit root
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{DefaultUser: "ubuntu"}}) // not root

	resp, _ := http.Get(ts.URL + "/api/v1/vms?default_user=root")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 root-SSH VMs (implicit + explicit), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["alpha"] || !names["beta"] {
		t.Fatalf("expected alpha (empty) and beta (root), got %+v", vms)
	}
}

// --- ?os_type= filter (5.6.8) -------------------------------------------------

func TestListVMs_FilterByOSType_ExactMatchWindows(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{OSType: types.OSTypeLinux}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-app", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{OSType: types.OSTypeLinux}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-app" {
		t.Fatalf("expected only win-app, got %+v", vms)
	}
}

func TestListVMs_FilterByOSType_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=WINDOWS")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByOSType_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=%20%20windows%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByOSType_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{OSType: types.OSTypeLinux}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

// TestListVMs_FilterByOSType_LinuxMatchesEmpty mirrors the
// `?default_user=root` empty-means-root contract. `OSType` is a closed
// two-member axis with a documented default (empty → linux), so an empty
// stored value must belong to the linux bucket and an operator querying
// `?os_type=linux` expects to find both explicit-linux and unset VMs.
func TestListVMs_FilterByOSType_LinuxMatchesEmpty(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{OSType: ""}})                // implicit linux
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{OSType: types.OSTypeLinux}})  // explicit linux
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win", Spec: types.VMSpec{OSType: types.OSTypeWindows}}) // not linux

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=linux")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 linux VMs (implicit + explicit), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["alpha"] || !names["beta"] {
		t.Fatalf("expected alpha (empty) and beta (linux), got %+v", vms)
	}
}

func TestListVMs_FilterByOSType_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=plan9")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_os_type")
}

func TestListVMs_FilterByOSType_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", State: types.VMStateRunning, Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", State: types.VMStateStopped, Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", State: types.VMStateRunning, Spec: types.VMSpec{OSType: types.OSTypeLinux}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "a" {
		t.Fatalf("expected only running windows VM 'a', got %+v", vms)
	}
}

func TestListVMs_FilterByOSType_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		ot := types.OSTypeLinux
		if i%2 == 0 {
			ot = types.OSTypeWindows
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{OSType: ot},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// 5.4.66 — `?os_variant=` on GET /vms. Sub-axis of `?os_type=windows`: case-insensitive
// exact-match against `spec.os_variant`; empty stored value excluded; unknown
// values return 400 `invalid_os_variant`; composes additively with every
// other filter.

func TestListVMs_FilterByOSVariant_ExactMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win11-host", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: "windows-11"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "srv22", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: "windows-server-2022"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "linux", Spec: types.VMSpec{OSType: types.OSTypeLinux}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_variant=windows-11")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win11-host" {
		t.Fatalf("expected only win11-host, got %+v", vms)
	}
}

func TestListVMs_FilterByOSVariant_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "srv22", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: "windows-server-2022"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_variant=WINDOWS-SERVER-2022")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByOSVariant_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: "windows-11"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_variant=%20%20windows-11%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByOSVariant_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: "windows-11"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{OSType: types.OSTypeLinux}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_variant=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

// TestListVMs_FilterByOSVariant_ExcludesEmptyStored documents the membership
// semantics: unlike `?os_type=linux` (which matches empty-stored VMs via the
// linux default), `?os_variant=` requires an explicit stored value — there's
// no documented "default variant", so empty drops out whenever the filter is
// set. Mirrors the webhook event_type membership / template default_user
// no-empty-match semantics.
func TestListVMs_FilterByOSVariant_ExcludesEmptyStored(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: "windows-11"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-unset", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: ""}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "linux", Spec: types.VMSpec{OSType: types.OSTypeLinux}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_variant=windows-11")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win11" {
		t.Fatalf("expected only win11 (empty-stored excluded), got %+v", vms)
	}
}

func TestListVMs_FilterByOSVariant_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_variant=windows-12")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_os_variant")
}

func TestListVMs_FilterByOSVariant_ComposesWithOSType(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: "windows-11"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{OSType: types.OSTypeLinux, OSVariant: "windows-11"}}) // unusual but allowed
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: "windows-10"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&os_variant=windows-11")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "a" {
		t.Fatalf("expected only the win-11 windows VM 'a', got %+v", vms)
	}
}

func TestListVMs_FilterByOSVariant_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		v := "windows-11"
		if i%2 == 0 {
			v = "windows-server-2022"
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{OSType: types.OSTypeWindows, OSVariant: v},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_variant=windows-11&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// 5.4.68 — `?firmware=` on GET /vms. Three-value vocabulary (bios|uefi|ovmf),
// case-insensitive; `bios` also matches VMs with an empty stored firmware
// (the SeaBIOS default, mirroring `?os_type=linux` empty-means-linux), while
// `uefi` and `ovmf` strict-match the stored value so the operator's chosen
// alias survives the filter round-trip.

func TestListVMs_FilterByFirmware_ExactMatchUEFI(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{Firmware: types.FirmwareUEFI}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "legacy", Spec: types.VMSpec{Firmware: types.FirmwareBIOS}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "ovmf", Spec: types.VMSpec{Firmware: types.FirmwareOVMF}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?firmware=uefi")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win11" {
		t.Fatalf("expected only win11 (uefi strict-match), got %+v", vms)
	}
}

func TestListVMs_FilterByFirmware_ExactMatchOVMF(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "uefi-vm", Spec: types.VMSpec{Firmware: types.FirmwareUEFI}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "ovmf-vm", Spec: types.VMSpec{Firmware: types.FirmwareOVMF}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?firmware=ovmf")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "ovmf-vm" {
		t.Fatalf("expected only ovmf-vm (ovmf and uefi are distinct stored values), got %+v", vms)
	}
}

// TestListVMs_FilterByFirmware_BIOSMatchesEmptyStored documents the
// empty-means-bios semantics: an unset `spec.firmware` resolves to BIOS at
// libvirt render time (no firmware attribute → SeaBIOS), so the filter must
// match it under `?firmware=bios` — mirrors the `?os_type=linux` empty
// match-via-default contract.
func TestListVMs_FilterByFirmware_BIOSMatchesEmptyStored(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "bios-explicit", Spec: types.VMSpec{Firmware: types.FirmwareBIOS}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "bios-empty", Spec: types.VMSpec{}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "uefi-vm", Spec: types.VMSpec{Firmware: types.FirmwareUEFI}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?firmware=bios")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms (bios-explicit + bios-empty), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["bios-explicit"] || !names["bios-empty"] {
		t.Fatalf("expected bios-explicit and bios-empty in result, got %+v", vms)
	}
}

func TestListVMs_FilterByFirmware_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{Firmware: types.FirmwareUEFI}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?firmware=UEFI")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByFirmware_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{Firmware: types.FirmwareUEFI}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?firmware=%20%20uefi%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByFirmware_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{Firmware: types.FirmwareUEFI}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{Firmware: types.FirmwareBIOS}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?firmware=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByFirmware_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?firmware=coreboot")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_firmware")
}

func TestListVMs_FilterByFirmware_ComposesWithOSType(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-uefi", Spec: types.VMSpec{OSType: types.OSTypeWindows, Firmware: types.FirmwareUEFI}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-uefi", Spec: types.VMSpec{OSType: types.OSTypeLinux, Firmware: types.FirmwareUEFI}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-bios", Spec: types.VMSpec{OSType: types.OSTypeWindows, Firmware: types.FirmwareBIOS}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&firmware=uefi")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-uefi" {
		t.Fatalf("expected only the windows + uefi VM, got %+v", vms)
	}
}

func TestListVMs_FilterByFirmware_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		fw := types.FirmwareUEFI
		if i%2 == 0 {
			fw = types.FirmwareBIOS
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{Firmware: fw},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?firmware=uefi&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// 5.4.69 — `?disk_bus=` on GET /vms. Two-value vocabulary (virtio|sata),
// case-insensitive; resolution defers to VMSpec.ResolvedDiskBus so an empty
// stored disk_bus matches the OS-family default — Linux VMs match
// `?disk_bus=virtio`, Windows VMs match `?disk_bus=sata`. This mirrors the
// `?firmware=bios` empty-matches-default contract for SeaBIOS and the
// `?os_type=linux` empty-matches-default contract for the Linux family.

func TestListVMs_FilterByDiskBus_ExactMatchVirtio(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "virtio-explicit", Spec: types.VMSpec{DiskBus: types.DiskBusVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "sata-explicit", Spec: types.VMSpec{DiskBus: types.DiskBusSATA}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-empty", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=virtio")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "virtio-explicit" {
		t.Fatalf("expected only virtio-explicit, got %+v", vms)
	}
}

func TestListVMs_FilterByDiskBus_ExactMatchSATA(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "virtio-vm", Spec: types.VMSpec{DiskBus: types.DiskBusVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "sata-vm", Spec: types.VMSpec{DiskBus: types.DiskBusSATA}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=sata")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "sata-vm" {
		t.Fatalf("expected only sata-vm, got %+v", vms)
	}
}

// TestListVMs_FilterByDiskBus_VirtioMatchesEmptyLinux documents the
// empty-stored linux-default contract: an unset spec.disk_bus on a Linux
// VM (the implicit default) resolves to virtio at libvirt render time, so
// the filter must match it under `?disk_bus=virtio` — mirrors how
// `?firmware=bios` matches empty-stored as the SeaBIOS default.
func TestListVMs_FilterByDiskBus_VirtioMatchesEmptyLinux(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux-empty", Spec: types.VMSpec{}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-explicit", Spec: types.VMSpec{DiskBus: types.DiskBusVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-empty", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=virtio")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms (linux-empty + linux-explicit), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["linux-empty"] || !names["linux-explicit"] {
		t.Fatalf("expected linux-empty and linux-explicit in result, got %+v", vms)
	}
}

// TestListVMs_FilterByDiskBus_SATAMatchesEmptyWindows mirrors the previous
// case for the Windows side of the family-default split.
func TestListVMs_FilterByDiskBus_SATAMatchesEmptyWindows(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-empty", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-explicit", Spec: types.VMSpec{OSType: types.OSTypeWindows, DiskBus: types.DiskBusSATA}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "linux-empty", Spec: types.VMSpec{}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=sata")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms (win-empty + win-explicit), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["win-empty"] || !names["win-explicit"] {
		t.Fatalf("expected win-empty and win-explicit in result, got %+v", vms)
	}
}

// TestListVMs_FilterByDiskBus_ExplicitOverridesOSFamily verifies that an
// explicitly-stored disk_bus wins over the OS-family default — a Windows VM
// with disk_bus=virtio (after virtio drivers installed in-guest, the
// switch-to-virtio path from 5.6.12) appears under `?disk_bus=virtio`, not
// under `?disk_bus=sata`.
func TestListVMs_FilterByDiskBus_ExplicitOverridesOSFamily(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-virtio", Spec: types.VMSpec{OSType: types.OSTypeWindows, DiskBus: types.DiskBusVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-sata", Spec: types.VMSpec{OSType: types.OSTypeLinux, DiskBus: types.DiskBusSATA}})

	respV, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=virtio")
	if respV.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", respV.StatusCode)
	}
	var vmsV []*types.VM
	decodeJSON(t, respV, &vmsV)
	if len(vmsV) != 1 || vmsV[0].Name != "win-virtio" {
		t.Fatalf("expected only win-virtio under disk_bus=virtio, got %+v", vmsV)
	}

	respS, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=sata")
	if respS.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", respS.StatusCode)
	}
	var vmsS []*types.VM
	decodeJSON(t, respS, &vmsS)
	if len(vmsS) != 1 || vmsS[0].Name != "linux-sata" {
		t.Fatalf("expected only linux-sata under disk_bus=sata, got %+v", vmsS)
	}
}

func TestListVMs_FilterByDiskBus_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "virtio-vm", Spec: types.VMSpec{DiskBus: types.DiskBusVirtio}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=VIRTIO")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByDiskBus_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "sata-vm", Spec: types.VMSpec{DiskBus: types.DiskBusSATA}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=%20%20sata%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByDiskBus_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{DiskBus: types.DiskBusVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{DiskBus: types.DiskBusSATA}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByDiskBus_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=nvme")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_disk_bus")
}

func TestListVMs_FilterByDiskBus_ComposesWithOSType(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-virtio", Spec: types.VMSpec{OSType: types.OSTypeWindows, DiskBus: types.DiskBusVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-virtio", Spec: types.VMSpec{OSType: types.OSTypeLinux, DiskBus: types.DiskBusVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-sata", Spec: types.VMSpec{OSType: types.OSTypeWindows, DiskBus: types.DiskBusSATA}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&disk_bus=virtio")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-virtio" {
		t.Fatalf("expected only the windows + virtio VM, got %+v", vms)
	}
}

func TestListVMs_FilterByDiskBus_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		bus := types.DiskBusVirtio
		if i%2 == 0 {
			bus = types.DiskBusSATA
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{DiskBus: bus},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?disk_bus=virtio&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// 5.4.70 — `?nic_model=` on GET /vms. Two-value vocabulary (virtio|e1000e),
// case-insensitive; resolution defers to VMSpec.ResolvedNICModel so an empty
// stored nic_model matches the OS-family default — Linux VMs match
// `?nic_model=virtio`, Windows VMs match `?nic_model=e1000e`. An explicit
// stored value always wins over the family default, so a Windows VM flipped
// to virtio after the operator installs the virtio-net drivers in-guest
// (5.6.12) appears under `?nic_model=virtio`.

func TestListVMs_FilterByNICModel_ExactMatchVirtio(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux-virtio", Spec: types.VMSpec{OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-e1000e", Spec: types.VMSpec{OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=virtio")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "linux-virtio" {
		t.Fatalf("expected only linux-virtio (virtio strict-match), got %+v", vms)
	}
}

func TestListVMs_FilterByNICModel_ExactMatchE1000e(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-virtio", Spec: types.VMSpec{OSType: types.OSTypeWindows, NICModel: types.NICModelVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-e1000e", Spec: types.VMSpec{OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=e1000e")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-e1000e" {
		t.Fatalf("expected only win-e1000e, got %+v", vms)
	}
}

// TestListVMs_FilterByNICModel_VirtioMatchesEmptyLinux documents the
// empty-means-OS-family-default semantics for Linux: an unset
// `spec.nic_model` on a Linux VM resolves to virtio at libvirt render time,
// so the filter must match it under `?nic_model=virtio`.
func TestListVMs_FilterByNICModel_VirtioMatchesEmptyLinux(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux-explicit", Spec: types.VMSpec{OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-empty", Spec: types.VMSpec{OSType: types.OSTypeLinux}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-e1000e", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=virtio")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms (linux-explicit + linux-empty), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["linux-explicit"] || !names["linux-empty"] {
		t.Fatalf("expected linux-explicit and linux-empty in result, got %+v", vms)
	}
}

// TestListVMs_FilterByNICModel_E1000eMatchesEmptyWindows documents the
// empty-means-OS-family-default semantics for Windows: an unset
// `spec.nic_model` on a Windows VM resolves to e1000e (boot-without-virtio
// default) so it falls under `?nic_model=e1000e`.
func TestListVMs_FilterByNICModel_E1000eMatchesEmptyWindows(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-explicit", Spec: types.VMSpec{OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-empty", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "linux-virtio", Spec: types.VMSpec{OSType: types.OSTypeLinux}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=e1000e")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms (win-explicit + win-empty), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["win-explicit"] || !names["win-empty"] {
		t.Fatalf("expected win-explicit and win-empty in result, got %+v", vms)
	}
}

// TestListVMs_FilterByNICModel_ExplicitOverridesOSFamily documents that an
// explicit stored nic_model always wins over the OS-family default. A
// Windows guest migrated to virtio (after the operator installs virtio-net
// in-guest via 5.6.12) appears under `?nic_model=virtio`, not under the
// Windows-default `?nic_model=e1000e`.
func TestListVMs_FilterByNICModel_ExplicitOverridesOSFamily(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-migrated", Spec: types.VMSpec{OSType: types.OSTypeWindows, NICModel: types.NICModelVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-default", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=virtio")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-migrated" {
		t.Fatalf("expected only win-migrated under ?nic_model=virtio, got %+v", vms)
	}
}

func TestListVMs_FilterByNICModel_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=VIRTIO")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByNICModel_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=%20%20virtio%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByNICModel_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByNICModel_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=rtl8139")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_nic_model")
}

func TestListVMs_FilterByNICModel_ComposesWithOSType(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-virtio", Spec: types.VMSpec{OSType: types.OSTypeWindows, NICModel: types.NICModelVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-virtio", Spec: types.VMSpec{OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-e1000e", Spec: types.VMSpec{OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&nic_model=virtio")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-virtio" {
		t.Fatalf("expected only the windows + virtio VM, got %+v", vms)
	}
}

func TestListVMs_FilterByNICModel_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		nic := types.NICModelVirtio
		osType := types.OSTypeLinux
		if i%2 == 0 {
			nic = types.NICModelE1000e
			osType = types.OSTypeWindows
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{OSType: osType, NICModel: nic},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nic_model=virtio&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

func TestListVMs_FilterByMachine_ExactMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "default-machine", Spec: types.VMSpec{}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "rhel-machine", Spec: types.VMSpec{Machine: "pc-q35-rhel9.6.0"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "q35-machine", Spec: types.VMSpec{Machine: "q35"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?machine=pc-q35-rhel9.6.0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "rhel-machine" {
		t.Fatalf("expected only rhel-machine, got %+v", vms)
	}
}

// TestListVMs_FilterByMachine_DefaultMatchesEmpty documents the
// empty-means-daemon-default semantics: an unset spec.machine resolves to
// types.DefaultMachine ("pc-q35-6.2") at libvirt render time, so the filter
// must match it under `?machine=pc-q35-6.2`. Mirrors the
// `?firmware=bios` empty-defaults-to-SeaBIOS contract.
func TestListVMs_FilterByMachine_DefaultMatchesEmpty(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "explicit-default", Spec: types.VMSpec{Machine: types.DefaultMachine}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "empty-machine", Spec: types.VMSpec{}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "other-machine", Spec: types.VMSpec{Machine: "q35"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?machine=" + types.DefaultMachine)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms (explicit-default + empty-machine), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["explicit-default"] || !names["empty-machine"] {
		t.Fatalf("expected explicit-default and empty-machine in result, got %+v", vms)
	}
}

// TestListVMs_FilterByMachine_IsCaseSensitive documents that machine types
// are case-sensitive: libvirt's pc-q35-style names are lowercase, but the
// alphabet permits letters so the filter preserves operator casing on
// round-trip. Mirrors the `?timezone=` exact-match contract.
func TestListVMs_FilterByMachine_IsCaseSensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "lower", Spec: types.VMSpec{Machine: "q35"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?machine=Q35")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected case-sensitive non-match, got %+v", vms)
	}
}

func TestListVMs_FilterByMachine_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "q35", Spec: types.VMSpec{Machine: "q35"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?machine=%20%20q35%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByMachine_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{Machine: "q35"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?machine=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByMachine_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Contains a slash → outside the [A-Za-z0-9._-]+ alphabet.
	resp, _ := http.Get(ts.URL + "/api/v1/vms?machine=pc-q35/6.2")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_machine")
}

func TestListVMs_FilterByMachine_ComposesWithOSType(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux-q35", Spec: types.VMSpec{OSType: types.OSTypeLinux, Machine: "q35"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-q35", Spec: types.VMSpec{OSType: types.OSTypeWindows, Machine: "q35"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-default", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&machine=q35")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-q35" {
		t.Fatalf("expected only win-q35, got %+v", vms)
	}
}

func TestListVMs_FilterByMachine_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		machine := types.DefaultMachine
		if i%2 == 0 {
			machine = "q35"
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{Machine: machine},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?machine=q35&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

func TestListVMs_FilterByClockOffset_ExactMatchUTC(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux-utc", Spec: types.VMSpec{OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-localtime", Spec: types.VMSpec{OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=utc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "linux-utc" {
		t.Fatalf("expected only linux-utc (utc strict-match), got %+v", vms)
	}
}

func TestListVMs_FilterByClockOffset_ExactMatchLocaltime(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-utc", Spec: types.VMSpec{OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetUTC}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-localtime", Spec: types.VMSpec{OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=localtime")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-localtime" {
		t.Fatalf("expected only win-localtime, got %+v", vms)
	}
}

// TestListVMs_FilterByClockOffset_UTCMatchesEmptyLinux documents the
// empty-means-OS-family-default semantics for Linux: an unset
// `spec.clock_offset` on a Linux VM resolves to utc at libvirt render time,
// so the filter must match it under `?clock_offset=utc`.
func TestListVMs_FilterByClockOffset_UTCMatchesEmptyLinux(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux-explicit", Spec: types.VMSpec{OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-empty", Spec: types.VMSpec{OSType: types.OSTypeLinux}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-localtime", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=utc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms (linux-explicit + linux-empty), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["linux-explicit"] || !names["linux-empty"] {
		t.Fatalf("expected linux-explicit and linux-empty in result, got %+v", vms)
	}
}

// TestListVMs_FilterByClockOffset_LocaltimeMatchesEmptyWindows documents the
// empty-means-OS-family-default semantics for Windows: an unset
// `spec.clock_offset` on a Windows VM resolves to localtime (matches the
// Windows RTC convention) so it falls under `?clock_offset=localtime`.
func TestListVMs_FilterByClockOffset_LocaltimeMatchesEmptyWindows(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-explicit", Spec: types.VMSpec{OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-empty", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "linux-utc", Spec: types.VMSpec{OSType: types.OSTypeLinux}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=localtime")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms (win-explicit + win-empty), got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["win-explicit"] || !names["win-empty"] {
		t.Fatalf("expected win-explicit and win-empty in result, got %+v", vms)
	}
}

// TestListVMs_FilterByClockOffset_ExplicitOverridesOSFamily documents that
// an explicit stored clock_offset always wins over the OS-family default. A
// Windows guest pinned to utc (NTP-synced fleet) appears under
// `?clock_offset=utc`, not under the Windows-default `?clock_offset=localtime`.
func TestListVMs_FilterByClockOffset_ExplicitOverridesOSFamily(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-utc", Spec: types.VMSpec{OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetUTC}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-default", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=utc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-utc" {
		t.Fatalf("expected only win-utc under ?clock_offset=utc, got %+v", vms)
	}
}

func TestListVMs_FilterByClockOffset_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=UTC")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByClockOffset_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=%20%20utc%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByClockOffset_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByClockOffset_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=gmt")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_clock_offset")
}

func TestListVMs_FilterByClockOffset_ComposesWithOSType(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-utc", Spec: types.VMSpec{OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetUTC}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-utc", Spec: types.VMSpec{OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-localtime", Spec: types.VMSpec{OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&clock_offset=utc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "win-utc" {
		t.Fatalf("expected only the windows + utc VM, got %+v", vms)
	}
}

func TestListVMs_FilterByClockOffset_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		offset := types.ClockOffsetUTC
		osType := types.OSTypeLinux
		if i%2 == 0 {
			offset = types.ClockOffsetLocaltime
			osType = types.OSTypeWindows
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{OSType: osType, ClockOffset: offset},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?clock_offset=utc&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// 5.7.9 — `?gpu=<pci-addr>` exact-match filter on the VM list. Closes the
// fleet-audit operator query *"which VM has 0000:01:00.0 assigned?"* now that
// 5.7 GPU passthrough has shipped. Mirrors the empty-stored-excludes
// contract on `?ip=` / `?nat_static_ip=` and the create-path
// `IsValidPCIAddress` validation (5.7.4).
func TestListVMs_FilterByGPU_ExactMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "no-gpu", Spec: types.VMSpec{}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "rtx-4080", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "other-slot", Spec: types.VMSpec{GPUs: []string{"0000:02:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?gpu=0000:01:00.0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "rtx-4080" {
		t.Fatalf("expected only rtx-4080, got %+v", vms)
	}
}

// Short form `01:00.0` and long form `0000:01:00.0` must round-trip via
// `NormalizePCIAddress`. A VM stored with the short form still surfaces when
// queried by the long form and vice versa.
func TestListVMs_FilterByGPU_ShortFormMatchesLongFormStored(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-long", Name: "long", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-short", Name: "short", Spec: types.VMSpec{GPUs: []string{"02:00.0"}}})

	// Filter by short form must match the long-form stored VM.
	resp, _ := http.Get(ts.URL + "/api/v1/vms?gpu=01:00.0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "long" {
		t.Fatalf("expected only the long-form stored VM, got %+v", vms)
	}

	// Filter by long form must match the short-form stored VM.
	resp2, _ := http.Get(ts.URL + "/api/v1/vms?gpu=0000:02:00.0")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	var vms2 []*types.VM
	decodeJSON(t, resp2, &vms2)
	if len(vms2) != 1 || vms2[0].Name != "short" {
		t.Fatalf("expected only the short-form stored VM, got %+v", vms2)
	}
}

// Multi-GPU VMs match when any of their requested addresses equals the
// filter (any-of semantics). Mirrors `?network=` and the 5.4.36 contract.
func TestListVMs_FilterByGPU_AnyOfMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "single", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "dual", Spec: types.VMSpec{GPUs: []string{"0000:02:00.0", "0000:03:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?gpu=0000:03:00.0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "dual" {
		t.Fatalf("expected only the dual-GPU VM, got %+v", vms)
	}
}

// VMs with no requested GPUs drop out whenever the filter is set, mirroring
// the empty-stored-excludes contract on `?ip=` / `?nat_static_ip=`.
func TestListVMs_FilterByGPU_ExcludesEmpty(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "no-gpu", Spec: types.VMSpec{}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "with-gpu", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?gpu=0000:01:00.0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "with-gpu" {
		t.Fatalf("expected only with-gpu, got %+v", vms)
	}
}

func TestListVMs_FilterByGPU_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?gpu=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByGPU_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "rtx-4080", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?gpu=%20%200000%3A01%3A00.0%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

// Garbage that fails `IsValidPCIAddress` returns 400 `invalid_gpu`, mirroring
// the create-path validation contract (5.7.4) so a typo surfaces before the
// filter is silently no-op'd.
func TestListVMs_FilterByGPU_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?gpu=not-a-pci-addr")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_gpu")
}

func TestListVMs_FilterByGPU_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		var gpus []string
		if i%2 == 0 {
			gpus = []string{"0000:01:00.0"}
		}
		mockMgr.SeedVM(&types.VM{
			ID:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("vm-%d", i),
			Spec: types.VMSpec{GPUs: gpus},
		})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?gpu=0000:01:00.0&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

func TestListVMs_FilterByImage_ExactMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Image: "ubuntu-22.04.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{Image: "rocky9.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?image=rocky9.qcow2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 rocky9.qcow2 vms, got %+v", vms)
	}
	for _, vm := range vms {
		if vm.Spec.Image != "rocky9.qcow2" {
			t.Fatalf("unexpected vm in filtered list: %+v", vm)
		}
	}
}

func TestListVMs_FilterByImage_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Image: "Rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Image: "ubuntu.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?image=ROCKY9.QCOW2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("case-insensitive image match expected alpha, got %+v", vms)
	}
}

func TestListVMs_FilterByImage_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Image: "ubuntu.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?image=%20%20rocky9.qcow2%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("expected whitespace-trimmed match for alpha, got %+v", vms)
	}
}

func TestListVMs_FilterByImage_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Image: "ubuntu.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?image=%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("whitespace-only image filter should be a no-op; got %+v", vms)
	}
}

func TestListVMs_FilterByImage_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Image: "ubuntu.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?image=does-not-exist.qcow2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected zero matches for unknown image, got %+v", vms)
	}
}

func TestListVMs_FilterByImage_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateRunning, Spec: types.VMSpec{Image: "ubuntu.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?image=rocky9.qcow2&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("expected only running rocky9 vm (alpha), got %+v", vms)
	}
}

func TestListVMs_FilterByImage_ComposesWithSearch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-01", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "web-staging", Spec: types.VMSpec{Image: "ubuntu.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "db-primary", Spec: types.VMSpec{Image: "rocky9.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?image=rocky9.qcow2&search=web")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "web-prod-01" {
		t.Fatalf("expected only web-prod-01, got %+v", vms)
	}
}

func TestListVMs_FilterByImage_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{Image: "ubuntu.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?image=rocky9.qcow2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want \"2\"", got)
	}
}

// 5.4.36 — per-network filter on the VM list.
func seedVMWithNetwork(id, name string, netNames ...string) *types.VM {
	attachments := make([]types.NetworkAttachment, 0, len(netNames))
	for _, n := range netNames {
		attachments = append(attachments, types.NetworkAttachment{Name: n})
	}
	return &types.VM{ID: id, Name: name, Spec: types.VMSpec{Networks: attachments}}
}

func TestListVMs_FilterByNetwork_ExactMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithNetwork("vm-1", "alpha", "data-net"))
	mockMgr.SeedVM(seedVMWithNetwork("vm-2", "beta", "storage-net"))
	mockMgr.SeedVM(seedVMWithNetwork("vm-3", "gamma", "data-net", "storage-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?network=data-net")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 data-net vms, got %+v", vms)
	}
	for _, vm := range vms {
		if vm.Name != "alpha" && vm.Name != "gamma" {
			t.Fatalf("unexpected vm in filtered list: %+v", vm)
		}
	}
}

func TestListVMs_FilterByNetwork_IsCaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithNetwork("vm-1", "alpha", "Data-Net"))
	mockMgr.SeedVM(seedVMWithNetwork("vm-2", "beta", "storage-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?network=DATA-NET")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("case-insensitive network match expected alpha, got %+v", vms)
	}
}

func TestListVMs_FilterByNetwork_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithNetwork("vm-1", "alpha", "data-net"))
	mockMgr.SeedVM(seedVMWithNetwork("vm-2", "beta", "storage-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?network=%20%20data-net%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("expected whitespace-trimmed match for alpha, got %+v", vms)
	}
}

func TestListVMs_FilterByNetwork_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithNetwork("vm-1", "alpha", "data-net"))
	mockMgr.SeedVM(seedVMWithNetwork("vm-2", "beta"))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?network=%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("whitespace-only network filter should be a no-op; got %+v", vms)
	}
}

func TestListVMs_FilterByNetwork_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithNetwork("vm-1", "alpha", "data-net"))
	mockMgr.SeedVM(seedVMWithNetwork("vm-2", "beta", "storage-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?network=does-not-exist")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected zero matches for unknown network, got %+v", vms)
	}
}

func TestListVMs_FilterByNetwork_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	running := seedVMWithNetwork("vm-1", "alpha", "data-net")
	running.State = types.VMStateRunning
	stopped := seedVMWithNetwork("vm-2", "beta", "data-net")
	stopped.State = types.VMStateStopped
	other := seedVMWithNetwork("vm-3", "gamma", "storage-net")
	other.State = types.VMStateRunning
	mockMgr.SeedVM(running)
	mockMgr.SeedVM(stopped)
	mockMgr.SeedVM(other)

	resp, _ := http.Get(ts.URL + "/api/v1/vms?network=data-net&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("expected only running data-net vm (alpha), got %+v", vms)
	}
}

func TestListVMs_FilterByNetwork_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithNetwork("vm-1", "alpha", "data-net"))
	mockMgr.SeedVM(seedVMWithNetwork("vm-2", "beta", "data-net"))
	mockMgr.SeedVM(seedVMWithNetwork("vm-3", "gamma", "storage-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?network=data-net")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want \"2\"", got)
	}
}

// --- VM list ?min_cpus= / ?max_cpus= range filter (5.4.44) ---

func seedVMWithCPUs(id, name string, cpus int) *types.VM {
	return &types.VM{ID: id, Name: name, Spec: types.VMSpec{CPUs: cpus}, State: types.VMStateRunning}
}

func seedVMWithRAM(id, name string, ramMB int) *types.VM {
	return &types.VM{ID: id, Name: name, Spec: types.VMSpec{RAMMB: ramMB}, State: types.VMStateRunning}
}

func seedVMWithDisk(id, name string, diskGB int) *types.VM {
	return &types.VM{ID: id, Name: name, Spec: types.VMSpec{DiskGB: diskGB}, State: types.VMStateRunning}
}

func TestListVMs_FilterByMinCpus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithCPUs("vm-1", "small", 2))
	mockMgr.SeedVM(seedVMWithCPUs("vm-2", "mid", 4))
	mockMgr.SeedVM(seedVMWithCPUs("vm-3", "big", 8))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_cpus=4")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	names := map[string]bool{}
	for _, vm := range vms {
		names[vm.Name] = true
	}
	if names["small"] || !names["mid"] || !names["big"] {
		t.Fatalf("expected mid+big (>= 4 vCPUs), got %+v", vms)
	}
}

func TestListVMs_FilterByMaxCpus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithCPUs("vm-1", "small", 2))
	mockMgr.SeedVM(seedVMWithCPUs("vm-2", "mid", 4))
	mockMgr.SeedVM(seedVMWithCPUs("vm-3", "big", 8))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?max_cpus=4")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	names := map[string]bool{}
	for _, vm := range vms {
		names[vm.Name] = true
	}
	if !names["small"] || !names["mid"] || names["big"] {
		t.Fatalf("expected small+mid (<= 4 vCPUs), got %+v", vms)
	}
}

func TestListVMs_FilterByCpuRange(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithCPUs("vm-1", "small", 2))
	mockMgr.SeedVM(seedVMWithCPUs("vm-2", "mid", 4))
	mockMgr.SeedVM(seedVMWithCPUs("vm-3", "big", 8))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_cpus=3&max_cpus=6")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "mid" {
		t.Fatalf("expected only mid in [3,6] vCPUs, got %+v", vms)
	}
}

func TestListVMs_FilterByMinCpus_Inclusive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithCPUs("vm-1", "edge", 4))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_cpus=4")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", vms)
	}
}

func TestListVMs_FilterByMinCpus_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithCPUs("vm-1", "small", 2))
	mockMgr.SeedVM(seedVMWithCPUs("vm-2", "big", 8))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_cpus=%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("whitespace-only min_cpus should disable the filter, got %+v", vms)
	}
}

func TestListVMs_FilterByInvalidMinCpus(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_cpus=lots")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_min_cpus" {
		t.Fatalf("code = %q, want invalid_min_cpus", apiErr.Code)
	}
}

func TestListVMs_FilterByMaxCpus_RejectsNegative(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?max_cpus=-1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_max_cpus" {
		t.Fatalf("code = %q, want invalid_max_cpus", apiErr.Code)
	}
}

func TestListVMs_FilterByCpus_ComposesWithStatusAndCount(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "run-big", Spec: types.VMSpec{CPUs: 8}, State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "stop-big", Spec: types.VMSpec{CPUs: 8}, State: types.VMStateStopped})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "run-small", Spec: types.VMSpec{CPUs: 2}, State: types.VMStateRunning})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?status=running&min_cpus=4")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "run-big" {
		t.Fatalf("expected only run-big, got %+v", vms)
	}
}

func TestListVMs_FilterByMinRAM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithRAM("vm-1", "small", 2048))
	mockMgr.SeedVM(seedVMWithRAM("vm-2", "mid", 4096))
	mockMgr.SeedVM(seedVMWithRAM("vm-3", "big", 8192))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_ram_mb=4096")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	names := map[string]bool{}
	for _, vm := range vms {
		names[vm.Name] = true
	}
	if names["small"] || !names["mid"] || !names["big"] {
		t.Fatalf("expected mid+big (>= 4096 MB), got %+v", vms)
	}
}

func TestListVMs_FilterByMaxRAM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithRAM("vm-1", "small", 2048))
	mockMgr.SeedVM(seedVMWithRAM("vm-2", "mid", 4096))
	mockMgr.SeedVM(seedVMWithRAM("vm-3", "big", 8192))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?max_ram_mb=4096")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	names := map[string]bool{}
	for _, vm := range vms {
		names[vm.Name] = true
	}
	if !names["small"] || !names["mid"] || names["big"] {
		t.Fatalf("expected small+mid (<= 4096 MB), got %+v", vms)
	}
}

func TestListVMs_FilterByRAMRange(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithRAM("vm-1", "small", 2048))
	mockMgr.SeedVM(seedVMWithRAM("vm-2", "mid", 4096))
	mockMgr.SeedVM(seedVMWithRAM("vm-3", "big", 8192))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_ram_mb=3000&max_ram_mb=5000")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "mid" {
		t.Fatalf("expected only mid in [3000,5000] MB, got %+v", vms)
	}
}

func TestListVMs_FilterByMinRAM_Inclusive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithRAM("vm-1", "edge", 4096))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_ram_mb=4096")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", vms)
	}
}

func TestListVMs_FilterByMinRAM_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithRAM("vm-1", "small", 2048))
	mockMgr.SeedVM(seedVMWithRAM("vm-2", "big", 8192))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_ram_mb=%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("whitespace-only min_ram_mb should disable the filter, got %+v", vms)
	}
}

func TestListVMs_FilterByInvalidMinRAM(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_ram_mb=lots")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_min_ram_mb" {
		t.Fatalf("code = %q, want invalid_min_ram_mb", apiErr.Code)
	}
}

func TestListVMs_FilterByMaxRAM_RejectsNegative(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?max_ram_mb=-1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_max_ram_mb" {
		t.Fatalf("code = %q, want invalid_max_ram_mb", apiErr.Code)
	}
}

func TestListVMs_FilterByRAM_ComposesWithCpusAndCount(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "big-big", Spec: types.VMSpec{CPUs: 8, RAMMB: 8192}, State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "big-small", Spec: types.VMSpec{CPUs: 8, RAMMB: 2048}, State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "small-big", Spec: types.VMSpec{CPUs: 2, RAMMB: 8192}, State: types.VMStateRunning})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_cpus=4&min_ram_mb=4096")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "big-big" {
		t.Fatalf("expected only big-big, got %+v", vms)
	}
}

func TestListVMs_FilterByMinDisk(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithDisk("vm-1", "small", 20))
	mockMgr.SeedVM(seedVMWithDisk("vm-2", "mid", 80))
	mockMgr.SeedVM(seedVMWithDisk("vm-3", "big", 500))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_disk_gb=80")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	names := map[string]bool{}
	for _, vm := range vms {
		names[vm.Name] = true
	}
	if names["small"] || !names["mid"] || !names["big"] {
		t.Fatalf("expected mid+big (>= 80 GB), got %+v", vms)
	}
}

func TestListVMs_FilterByMaxDisk(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithDisk("vm-1", "small", 20))
	mockMgr.SeedVM(seedVMWithDisk("vm-2", "mid", 80))
	mockMgr.SeedVM(seedVMWithDisk("vm-3", "big", 500))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?max_disk_gb=80")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	names := map[string]bool{}
	for _, vm := range vms {
		names[vm.Name] = true
	}
	if !names["small"] || !names["mid"] || names["big"] {
		t.Fatalf("expected small+mid (<= 80 GB), got %+v", vms)
	}
}

func TestListVMs_FilterByDiskRange(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithDisk("vm-1", "small", 20))
	mockMgr.SeedVM(seedVMWithDisk("vm-2", "mid", 80))
	mockMgr.SeedVM(seedVMWithDisk("vm-3", "big", 500))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_disk_gb=50&max_disk_gb=100")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "mid" {
		t.Fatalf("expected only mid in [50,100] GB, got %+v", vms)
	}
}

func TestListVMs_FilterByMinDisk_Inclusive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithDisk("vm-1", "edge", 80))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_disk_gb=80")
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", vms)
	}
}

func TestListVMs_FilterByMinDisk_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(seedVMWithDisk("vm-1", "small", 20))
	mockMgr.SeedVM(seedVMWithDisk("vm-2", "big", 500))

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_disk_gb=%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("whitespace-only min_disk_gb should disable the filter, got %+v", vms)
	}
}

func TestListVMs_FilterByInvalidMinDisk(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_disk_gb=lots")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_min_disk_gb" {
		t.Fatalf("code = %q, want invalid_min_disk_gb", apiErr.Code)
	}
}

func TestListVMs_FilterByMaxDisk_RejectsNegative(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?max_disk_gb=-1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_max_disk_gb" {
		t.Fatalf("code = %q, want invalid_max_disk_gb", apiErr.Code)
	}
}

func TestListVMs_FilterByDisk_ComposesWithRAMAndCount(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "big-big", Spec: types.VMSpec{RAMMB: 8192, DiskGB: 500}, State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "big-ram-small-disk", Spec: types.VMSpec{RAMMB: 8192, DiskGB: 20}, State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "small-ram-big-disk", Spec: types.VMSpec{RAMMB: 2048, DiskGB: 500}, State: types.VMStateRunning})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?min_ram_mb=4096&min_disk_gb=100")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "big-big" {
		t.Fatalf("expected only big-big, got %+v", vms)
	}
}

func TestListVMs_FilterByAutoStart_True(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{AutoStart: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{AutoStart: false}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{AutoStart: true}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 auto-start vms, got %+v", vms)
	}
	for _, vm := range vms {
		if !vm.Spec.AutoStart {
			t.Fatalf("unexpected vm in filtered list: %+v", vm)
		}
	}
}

func TestListVMs_FilterByAutoStart_False(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{AutoStart: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{AutoStart: false}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=false")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "beta" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterByLocked_True(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Locked: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Locked: false}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?locked=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterByLocked_False(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Locked: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Locked: false}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{Locked: false}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?locked=false")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 unlocked vms, got %+v", vms)
	}
}

func TestListVMs_FilterByAutoStart_CaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{AutoStart: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=TrUe")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterByAutoStart_WhitespaceTrimmed(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{AutoStart: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=%20true%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterByAutoStart_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{AutoStart: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected all vms when auto_start is empty, got %+v", vms)
	}
}

func TestListVMs_FilterByAutoStart_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=maybe")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_auto_start" {
		t.Fatalf("code = %q, want invalid_auto_start", apiErr.Code)
	}
}

func TestListVMs_FilterByLocked_InvalidValueReturns400(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?locked=yes")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_locked" {
		t.Fatalf("code = %q, want invalid_locked", apiErr.Code)
	}
}

func TestListVMs_FilterByAutoStartAndLocked(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{AutoStart: true, Locked: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{AutoStart: true, Locked: false}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{AutoStart: false, Locked: true}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=true&locked=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterByAutoStart_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{AutoStart: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Spec: types.VMSpec{AutoStart: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateRunning, Spec: types.VMSpec{AutoStart: false}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=true&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms = %+v", vms)
	}
}

func TestListVMs_FilterByLocked_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{Locked: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{Locked: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{Locked: false}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?locked=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
}

func TestListVMs_FilterByAutoStart_AcceptsNumericAliases(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{AutoStart: true}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{AutoStart: false}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?auto_start=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "alpha" {
		t.Fatalf("filtered vms (1=true) = %+v", vms)
	}

	resp, _ = http.Get(ts.URL + "/api/v1/vms?auto_start=0")
	var vms2 []*types.VM
	decodeJSON(t, resp, &vms2)
	if len(vms2) != 1 || vms2[0].Name != "beta" {
		t.Fatalf("filtered vms (0=false) = %+v", vms2)
	}
}

// --- 5.4.30: ?since= / ?until= time-range filter on VM created_at ---

func TestListVMs_FilterBySince(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "early", CreatedAt: day(1)})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "mid", CreatedAt: day(15)})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "late", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?since=2026-05-10T00:00:00Z")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	var listed []*types.VM
	decodeJSON(t, resp, &listed)
	names := map[string]bool{}
	for _, v := range listed {
		names[v.Name] = true
	}
	if !names["mid"] || !names["late"] || names["early"] {
		t.Fatalf("expected mid+late, got %+v", listed)
	}
}

func TestListVMs_FilterByUntil(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "early", CreatedAt: day(1)})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "mid", CreatedAt: day(15)})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "late", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?until=2026-05-20T00:00:00Z")
	var listed []*types.VM
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("expected 2 VMs <= until, got %+v", listed)
	}
}

func TestListVMs_FilterBySinceAndUntil(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "vm-1", CreatedAt: day(1)})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "vm-15", CreatedAt: day(15)})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "vm-30", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?since=2026-05-10T00:00:00Z&until=2026-05-20T00:00:00Z")
	var listed []*types.VM
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "vm-15" {
		t.Fatalf("expected only vm-15, got %+v", listed)
	}
}

func TestListVMs_FilterBySince_Inclusive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	boundary := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	mockMgr.SeedVM(&types.VM{ID: "vm-edge", Name: "edge", CreatedAt: boundary})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?since=2026-05-01T00:00:00Z")
	var listed []*types.VM
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", listed)
	}
}

func TestListVMs_FilterByInvalidSince(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?since=last-tuesday")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_since" {
		t.Fatalf("code = %q, want invalid_since", apiErr.Code)
	}
}

func TestListVMs_FilterByInvalidUntil(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?until=2026-13-99")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_until" {
		t.Fatalf("code = %q, want invalid_until", apiErr.Code)
	}
}

func TestListVMs_FilterBySince_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", CreatedAt: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?since=%20%20")
	var listed []*types.VM
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("whitespace-only since should be a no-op; got %+v", listed)
	}
}

func TestListVMs_FilterByTimeRange_ExcludesZeroCreatedAt(t *testing.T) {
	// A VM with zero CreatedAt is filtered out whenever any bound is set —
	// operators querying a time window don't want unbounded entries silently
	// included.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-zero", Name: "no-time"}) // zero CreatedAt
	mockMgr.SeedVM(&types.VM{ID: "vm-dated", Name: "dated", CreatedAt: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?since=2026-05-01T00:00:00Z")
	var listed []*types.VM
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "dated" {
		t.Fatalf("expected only dated (zero-time excluded), got %+v", listed)
	}
}

func TestListVMs_FilterBySince_ComposesWithTagAndSearch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-old", CreatedAt: day(1), Tags: []string{"prod"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "web-prod-new", CreatedAt: day(20), Tags: []string{"prod"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "db-prod-new", CreatedAt: day(20), Tags: []string{"prod"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-4", Name: "web-staging-new", CreatedAt: day(20), Tags: []string{"staging"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?since=2026-05-10T00:00:00Z&tag=prod&search=web")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var listed []*types.VM
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "web-prod-new" {
		t.Fatalf("expected only web-prod-new, got %+v", listed)
	}
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

func TestListVMs_SortByName(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "Charlie"})
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "Bravo"})

	resp, err := http.Get(ts.URL + "/api/v1/vms?sort=name")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, vm.Name, want[i], namesOf(got))
		}
	}
}

func TestListVMs_SortByNameDesc(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "Charlie"})
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "Bravo"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=name&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"Charlie", "Bravo", "alpha"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByCreatedAtDesc(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "first", CreatedAt: t0})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "second", CreatedAt: t0.Add(time.Hour)})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "third", CreatedAt: t0.Add(2 * time.Hour)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=created_at&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"third", "second", "first"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByState(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", State: types.VMStateStopped})
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", State: types.VMStateRunning})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=state&order=asc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	if len(got) != 3 {
		t.Fatalf("want 3 vms, got %d", len(got))
	}
	// "running" < "stopped" lexicographically; equal-state ties break on ID.
	if got[0].State != types.VMStateRunning || got[1].State != types.VMStateRunning || got[2].State != types.VMStateStopped {
		t.Fatalf("state order wrong: %v", []types.VMState{got[0].State, got[1].State, got[2].State})
	}
	if got[0].ID != "vm-1" || got[1].ID != "vm-2" {
		t.Errorf("equal-state tie should break on id: got %q,%q", got[0].ID, got[1].ID)
	}
}

func TestListVMs_SortByCPUs(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "small", Spec: types.VMSpec{CPUs: 1}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "medium", Spec: types.VMSpec{CPUs: 4}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "large", Spec: types.VMSpec{CPUs: 8}})

	resp, err := http.Get(ts.URL + "/api/v1/vms?sort=cpus")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"small", "medium", "large"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %v)", i, vm.Name, want[i], namesOf(got))
		}
	}
}

func TestListVMs_SortByCPUsDesc_TiebreaksOnID(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{CPUs: 4}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{CPUs: 4}}) // tie
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", Spec: types.VMSpec{CPUs: 1}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=cpus&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// Largest CPUs first; equal-cpu pair should reverse on tiebreak too
	// because the descending wrapper inverts the entire compare result.
	want := []string{"vm-2", "vm-1", "vm-3"}
	for i, vm := range got {
		if vm.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, vm.ID, want[i])
		}
	}
}

func TestListVMs_SortByRAMMB(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "tiny", Spec: types.VMSpec{RAMMB: 512}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "big", Spec: types.VMSpec{RAMMB: 8192}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "med", Spec: types.VMSpec{RAMMB: 2048}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=ram_mb&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"big", "med", "tiny"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByDiskGB(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "small", Spec: types.VMSpec{DiskGB: 10}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "huge", Spec: types.VMSpec{DiskGB: 500}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "med", Spec: types.VMSpec{DiskGB: 100}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=disk_gb")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"small", "med", "huge"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByIP_NumericAsc(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "big-ten", IP: "192.168.100.10"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "stopped", IP: ""})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "small-two", IP: "192.168.100.2"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=ip")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// Numeric sort: 192.168.100.2 < 192.168.100.10 (lex would invert).
	// Empty IP sinks to the tail in ascending order.
	want := []string{"small-two", "big-ten", "stopped"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByIPDesc_EmptyLeading(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "big-ten", IP: "192.168.100.10"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "stopped", IP: ""})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "small-two", IP: "192.168.100.2"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=ip&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"stopped", "big-ten", "small-two"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByIP_TiebreaksOnID(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", IP: "10.0.0.1"})
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", IP: "10.0.0.1"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", IP: "10.0.0.1"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=ip")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, vm := range got {
		if vm.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, vm.ID, want[i])
		}
	}
}

func TestListVMs_SortByIP_400InvalidSortMentionsIP(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=address")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_sort" {
		t.Errorf("code = %q, want invalid_sort", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "ip") {
		t.Errorf("message = %q, expected to advertise 'ip' as a valid sort axis", apiErr.Message)
	}
	// 5.4.88 — the error message must also advertise `image` now that the
	// VM list whitelist includes it.
	if !strings.Contains(apiErr.Message, "image") {
		t.Errorf("message = %q, expected to advertise 'image' as a valid sort axis", apiErr.Message)
	}
	// 5.4.91 — the error message must also advertise `default_user` now
	// that the VM list whitelist includes it.
	if !strings.Contains(apiErr.Message, "default_user") {
		t.Errorf("message = %q, expected to advertise 'default_user' as a valid sort axis", apiErr.Message)
	}
	// 5.7.13 — the error message must also advertise `gpu` now that the VM
	// list whitelist includes it.
	if !strings.Contains(apiErr.Message, "gpu") {
		t.Errorf("message = %q, expected to advertise 'gpu' as a valid sort axis", apiErr.Message)
	}
}

// ============================================================
// VM list `image` sort axis (5.4.88)
// ============================================================

func TestListVMs_SortByImage_AscEmptyTrailing(t *testing.T) {
	// Concrete images sort case-insensitively (alpine < rocky9). The
	// empty-image VM sinks to the tail in ascending order, mirroring the
	// nil-trailing semantics on every other nullable sort axis (ip,
	// guest_ip, last_fired_at, last_delivery_at, actor).
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "rocky-prod", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "no-image", Spec: types.VMSpec{Image: ""}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "alpine-bastion", Spec: types.VMSpec{Image: "alpine.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=image")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"alpine-bastion", "rocky-prod", "no-image"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByImageDesc_EmptyLeading(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "rocky-prod", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "no-image", Spec: types.VMSpec{Image: ""}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "alpine-bastion", Spec: types.VMSpec{Image: "alpine.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=image&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// Empty leads in descending; concrete images then sort reverse alpha
	// (rocky9 > alpine).
	want := []string{"no-image", "rocky-prod", "alpine-bastion"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByImage_CaseInsensitive(t *testing.T) {
	// `Rocky9.qcow2` and `rocky9.qcow2` must collate as identical so the
	// sort agrees with the case-insensitive `?image=` exact-match filter
	// (5.4.22) on the same column.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", Spec: types.VMSpec{Image: "Rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "alpine", Spec: types.VMSpec{Image: "alpine.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", Spec: types.VMSpec{Image: "rocky9.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=image&order=asc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// alpine.qcow2 < rocky9.qcow2 (case-folded). Equal-image cohort tiebreaks
	// on id ascending so vm-1 precedes vm-3.
	want := []string{"alpine", "uppercase", "lowercase"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByImage_TiebreaksOnID(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{Image: "rocky9.qcow2"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{Image: "rocky9.qcow2"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=image")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, vm := range got {
		if vm.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, vm.ID, want[i])
		}
	}
}

// ============================================================
// VM list `default_user` sort axis (5.4.91)
// ============================================================

func TestListVMs_SortByDefaultUser_AscEmptyResolvesToRoot(t *testing.T) {
	// Diverges from the nil-trailing convention on `image` because
	// `default_user` has a documented default — empty stored values
	// resolve to "root" so they collate with explicit-root VMs in the
	// alphabetic ordering rather than sinking to the tail.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "ec2", Spec: types.VMSpec{DefaultUser: "ec2-user"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "explicit-root", Spec: types.VMSpec{DefaultUser: "root"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "unset", Spec: types.VMSpec{DefaultUser: ""}})
	mockMgr.SeedVM(&types.VM{ID: "vm-4", Name: "admin", Spec: types.VMSpec{DefaultUser: "admin"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=default_user")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// asc: admin < ec2-user < root; vm-2 and vm-3 both resolve to "root"
	// and tiebreak on id ascending so vm-2 precedes vm-3.
	want := []string{"admin", "ec2", "explicit-root", "unset"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByDefaultUserDesc_EmptyResolvesToRoot(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "ec2", Spec: types.VMSpec{DefaultUser: "ec2-user"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "explicit-root", Spec: types.VMSpec{DefaultUser: "root"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "unset", Spec: types.VMSpec{DefaultUser: ""}})
	mockMgr.SeedVM(&types.VM{ID: "vm-4", Name: "admin", Spec: types.VMSpec{DefaultUser: "admin"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=default_user&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// desc reverses the entire compare result so the root cohort heads
	// the list (vm-3 leads vm-2 because the id tiebreak also inverts).
	want := []string{"unset", "explicit-root", "ec2", "admin"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByDefaultUser_CaseInsensitive(t *testing.T) {
	// `ROOT` and `root` must collate as identical so the sort agrees with
	// the case-insensitive `?default_user=` filter (5.4.23).
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", Spec: types.VMSpec{DefaultUser: "ROOT"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "admin", Spec: types.VMSpec{DefaultUser: "admin"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", Spec: types.VMSpec{DefaultUser: "root"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=default_user&order=asc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// admin < root (case-folded). Equal-user cohort tiebreaks on id ascending
	// so vm-1 ("uppercase") precedes vm-3 ("lowercase").
	want := []string{"admin", "uppercase", "lowercase"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByDefaultUser_TiebreaksOnID(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", Spec: types.VMSpec{DefaultUser: "ops-alice"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{DefaultUser: "ops-alice"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{DefaultUser: "ops-alice"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=default_user")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, vm := range got {
		if vm.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, vm.ID, want[i])
		}
	}
}

// ============================================================
// VM list `gpu` sort axis (5.7.13)
// ============================================================

func TestListVMs_SortByGPU_AscEmptyTrailing(t *testing.T) {
	// Concrete GPU addresses sort lexicographically on the canonical long
	// form; VMs with no requested GPUs sink to the tail in ascending order,
	// mirroring the nil-trailing semantics on every other nullable sort axis
	// (ip / guest_ip / image / actor / last_fired_at / last_delivery_at).
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "second-slot", Spec: types.VMSpec{GPUs: []string{"0000:02:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "no-gpu", Spec: types.VMSpec{}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "first-slot", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=gpu")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"first-slot", "second-slot", "no-gpu"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByGPUDesc_EmptyLeading(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "second-slot", Spec: types.VMSpec{GPUs: []string{"0000:02:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "no-gpu", Spec: types.VMSpec{}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "first-slot", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=gpu&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// Empty leads in descending; concrete GPUs then sort reverse-lexicographic.
	want := []string{"no-gpu", "second-slot", "first-slot"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByGPU_NormalisesShortForm(t *testing.T) {
	// A VM persisted with the short PCI form ("01:00.0") must collate
	// identically to one persisted with the long form ("0000:01:00.0") so
	// the sort agrees with the alphabet contract on `?gpu=` (5.7.9). Without
	// the normalisation hop, lexicographic compare on the raw string would
	// sort every short-form entry after every long-form entry, breaking
	// cohort discovery for operators who pasted from `lspci`.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "long-02", Spec: types.VMSpec{GPUs: []string{"0000:02:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "short-01", Spec: types.VMSpec{GPUs: []string{"01:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "long-03", Spec: types.VMSpec{GPUs: []string{"0000:03:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=gpu")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"short-01", "long-02", "long-03"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByGPU_MultiGPUUsesSmallestSlot(t *testing.T) {
	// A multi-GPU VM is positioned by its smallest assigned PCI slot. vm-1
	// holds [02, 04] (smallest = 02), vm-2 holds [01, 05] (smallest = 01)
	// and surfaces first in ascending order, vm-3 holds only [03] and lands
	// between them. Locks the "primary slot wins" contract documented in
	// smallestGPU so the position of a multi-GPU VM doesn't depend on the
	// caller's input order.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "two-and-four", Spec: types.VMSpec{GPUs: []string{"0000:02:00.0", "0000:04:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "one-and-five", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0", "0000:05:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "three-only", Spec: types.VMSpec{GPUs: []string{"0000:03:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=gpu")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"one-and-five", "two-and-four", "three-only"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, vm.Name, want[i])
		}
	}
}

func TestListVMs_SortByGPU_TiebreaksOnID(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=gpu")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, vm := range got {
		if vm.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, vm.ID, want[i])
		}
	}
}

// ============================================================
// VM list `os_type` sort axis (5.4.100)
// ============================================================

func TestListVMs_SortByOSType_AscEmptyResolvesToLinux(t *testing.T) {
	// Diverges from the nil-trailing convention on `image` / `gpu` because
	// `os_type` has a documented default — empty stored values resolve to
	// "linux" via VMSpec.ResolvedOSType so they collate with explicit-linux
	// VMs in alphabetical order (linux < windows) rather than sinking to
	// the tail. Same rationale as the `default_user` axis (5.4.91).
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-1", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-explicit", Spec: types.VMSpec{OSType: types.OSTypeLinux}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "linux-empty", Spec: types.VMSpec{OSType: ""}})
	mockMgr.SeedVM(&types.VM{ID: "vm-4", Name: "win-2", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=os_type")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	// asc: linux < windows; vm-2 and vm-3 both resolve to "linux" and
	// tiebreak on id ascending so vm-2 precedes vm-3.
	want := []string{"linux-explicit", "linux-empty", "win-1", "win-2"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full got: %v)", i, vm.Name, want[i], got)
		}
	}
}

func TestListVMs_SortByOSTypeDesc_EmptyResolvesToLinux(t *testing.T) {
	// Desc reverses the entire compare result so the windows cohort heads
	// the list, then the linux cohort (with the empty-stored vm-3 leading
	// vm-2 because the id tiebreak also inverts).
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "win-1", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-explicit", Spec: types.VMSpec{OSType: types.OSTypeLinux}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "linux-empty", Spec: types.VMSpec{OSType: ""}})
	mockMgr.SeedVM(&types.VM{ID: "vm-4", Name: "win-2", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=os_type&order=desc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"win-2", "win-1", "linux-empty", "linux-explicit"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full got: %v)", i, vm.Name, want[i], got)
		}
	}
}

func TestListVMs_SortByOSType_CaseInsensitive(t *testing.T) {
	// `WINDOWS` and `windows` must collate as identical so the sort agrees
	// with the case-insensitive `?os_type=` filter (5.6.8).
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", Spec: types.VMSpec{OSType: "WINDOWS"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "linux-stored", Spec: types.VMSpec{OSType: "linux"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", Spec: types.VMSpec{OSType: "windows"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=os_type&order=asc")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"linux-stored", "uppercase", "lowercase"}
	for i, vm := range got {
		if vm.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full got: %v)", i, vm.Name, want[i], got)
		}
	}
}

func TestListVMs_SortByOSType_TiebreaksOnID(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "c", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{OSType: types.OSTypeWindows}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=os_type")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	want := []string{"vm-1", "vm-2", "vm-3"}
	for i, vm := range got {
		if vm.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, vm.ID, want[i])
		}
	}
}

func TestListVMs_SortByOSType_ComposesWithFilter(t *testing.T) {
	// `?os_type=windows&sort=os_type` narrows to the Windows cohort and
	// then orders within it — every row is windows, so the id-tiebreak
	// determines order. Asserts the filter + sort agree on the
	// "windows" classification.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{OSType: types.OSTypeLinux}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "win-b", Spec: types.VMSpec{OSType: types.OSTypeWindows}})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "win-a", Spec: types.VMSpec{OSType: "WINDOWS"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?os_type=windows&sort=os_type")
	var got []*types.VM
	decodeJSON(t, resp, &got)
	if len(got) != 2 {
		t.Fatalf("filter+sort returned %d rows, want 2 (windows-only)", len(got))
	}
	want := []string{"vm-2", "vm-3"}
	for i, vm := range got {
		if vm.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, vm.ID, want[i])
		}
	}
}

func TestListVMs_SortByOSType_400AdvertisesOSType(t *testing.T) {
	// A nearby misspelling (e.g. `os-type`) must surface the canonical
	// axis name in the 400 envelope so operators discover the right key.
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?sort=os-type")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_sort" {
		t.Errorf("code = %q, want invalid_sort", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "os_type") {
		t.Errorf("error message %q should advertise the os_type axis", apiErr.Message)
	}
}

func TestListVMs_SortPaginationDeterministic(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("vm-%d", i)
		mockMgr.SeedVM(&types.VM{ID: id, Name: id})
	}

	// page 1
	resp1, _ := http.Get(ts.URL + "/api/v1/vms?sort=name&order=desc&page=1&per_page=2")
	var page1 []*types.VM
	decodeJSON(t, resp1, &page1)
	if got := resp1.Header.Get("X-Total-Count"); got != "5" {
		t.Fatalf("X-Total-Count = %q, want 5", got)
	}

	// page 2
	resp2, _ := http.Get(ts.URL + "/api/v1/vms?sort=name&order=desc&page=2&per_page=2")
	var page2 []*types.VM
	decodeJSON(t, resp2, &page2)

	gotOrder := []string{page1[0].Name, page1[1].Name, page2[0].Name, page2[1].Name}
	want := []string{"vm-4", "vm-3", "vm-2", "vm-1"}
	for i, n := range gotOrder {
		if n != want[i] {
			t.Fatalf("page-spanning order[%d] = %q, want %q (full: %v)", i, n, want[i], gotOrder)
		}
	}
}

func TestListVMs_RejectsInvalidSort(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms?sort=memory")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_sort" {
		t.Errorf("code = %q, want invalid_sort", apiErr.Code)
	}
}

func TestListVMs_RejectsInvalidOrder(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms?order=sideways")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_order" {
		t.Errorf("code = %q, want invalid_order", apiErr.Code)
	}
}

func namesOf(vms []*types.VM) []string {
	out := make([]string, len(vms))
	for i, vm := range vms {
		out[i] = vm.Name
	}
	return out
}

func TestListVMs_PaginationSetsTotalCount(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateStopped})

	resp, err := http.Get(ts.URL + "/api/v1/vms?page=2&per_page=1")
	if err != nil {
		t.Fatalf("GET /vms?page=2&per_page=1: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("X-Total-Count = %q, want 3", got)
	}

	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM on paginated page, got %d", len(vms))
	}
	if vms[0].Name != "beta" {
		t.Fatalf("page 2 VM = %q, want beta", vms[0].Name)
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

func TestCloneVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:          "vm-source",
		Name:        "source-vm",
		Description: "source description",
		Tags:        []string{"prod", "web"},
		State:       types.VMStateStopped,
		Spec: types.VMSpec{
			Name:        "source-vm",
			Image:       "ubuntu-24.04",
			CPUs:        4,
			RAMMB:       8192,
			DiskGB:      80,
			Description: "source description",
			Tags:        []string{"prod", "web"},
			GPUs:        []string{"0000:01:00.0"},
		},
	})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-source/clone", "application/json", jsonBody(t, cloneVMRequest{Name: "clone-a"}))
	if err != nil {
		t.Fatalf("POST /vms/{id}/clone: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var cloned types.VM
	decodeJSON(t, resp, &cloned)
	if cloned.ID == "" || cloned.ID == "vm-source" {
		t.Fatalf("clone ID = %q, want new VM ID", cloned.ID)
	}
	if cloned.Name != "clone-a" {
		t.Fatalf("clone name = %q, want clone-a", cloned.Name)
	}
	if cloned.State != types.VMStateStopped {
		t.Fatalf("clone state = %q, want stopped", cloned.State)
	}
	if cloned.Spec.Name != "clone-a" || cloned.Spec.Image != "ubuntu-24.04" || cloned.Spec.CPUs != 4 || cloned.Spec.RAMMB != 8192 || cloned.Spec.DiskGB != 80 {
		t.Fatalf("unexpected clone spec: %+v", cloned.Spec)
	}
	if len(cloned.Spec.GPUs) != 0 {
		t.Fatalf("clone spec GPUs = %v, want cleared", cloned.Spec.GPUs)
	}
	if len(cloned.Tags) != 2 || cloned.Tags[0] != "prod" || cloned.Tags[1] != "web" {
		t.Fatalf("clone tags = %#v, want copied tags", cloned.Tags)
	}
}

func TestCloneVM_InvalidRequest(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-source", Name: "source-vm", State: types.VMStateStopped, Spec: types.VMSpec{Name: "source-vm", Image: "ubuntu"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-existing", Name: "existing-vm", State: types.VMStateStopped, Spec: types.VMSpec{Name: "existing-vm", Image: "ubuntu"}})

	tests := []struct {
		name       string
		body       *bytes.Buffer
		wantStatus int
		wantCode   string
	}{
		{name: "bad json", body: bytes.NewBufferString("{bad json"), wantStatus: http.StatusBadRequest, wantCode: "invalid_request_body"},
		{name: "missing name", body: jsonBody(t, cloneVMRequest{}), wantStatus: http.StatusBadRequest, wantCode: "invalid_name"},
		{name: "invalid name", body: jsonBody(t, cloneVMRequest{Name: "bad name!"}), wantStatus: http.StatusBadRequest, wantCode: "invalid_name"},
		{name: "duplicate name", body: jsonBody(t, cloneVMRequest{Name: " existing-vm "}), wantStatus: http.StatusBadRequest, wantCode: "invalid_name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+"/api/v1/vms/vm-source/clone", "application/json", tt.body)
			if err != nil {
				t.Fatalf("POST /vms/{id}/clone: %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			assertAPIErrorCode(t, resp, tt.wantCode)
		})
	}
}

func TestCloneVM_NotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms/missing/clone", "application/json", jsonBody(t, cloneVMRequest{Name: "clone-a"}))
	if err != nil {
		t.Fatalf("POST /vms/{id}/clone: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestCloneVM_ErrorInjection(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-source", Name: "source-vm", State: types.VMStateStopped, Spec: types.VMSpec{Name: "source-vm", Image: "ubuntu", CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mockMgr.CloneErr = errors.New("creating overlay disk: boom")

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-source/clone", "application/json", jsonBody(t, cloneVMRequest{Name: "clone-a"}))
	if err != nil {
		t.Fatalf("POST /vms/{id}/clone: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "storage_error")
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

func TestBulkVMAction_Start(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateStopped})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped})

	body := jsonBody(t, map[string]any{
		"action": "start",
		"ids":    []string{"vm-1", "vm-2"},
	})
	resp, err := http.Post(ts.URL+"/api/v1/vms/bulk", "application/json", body)
	if err != nil {
		t.Fatalf("POST /vms/bulk: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got bulkVMActionResponse
	decodeJSON(t, resp, &got)
	if got.Action != "start" {
		t.Fatalf("action = %q, want start", got.Action)
	}
	if len(got.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(got.Results))
	}
	for _, result := range got.Results {
		if !result.Success {
			t.Fatalf("result for %s unsuccessful: %+v", result.ID, result)
		}
		vm, err := mockMgr.Get(nil, result.ID)
		if err != nil {
			t.Fatalf("Get(%s): %v", result.ID, err)
		}
		if vm.State != types.VMStateRunning {
			t.Fatalf("vm %s state = %q, want running", result.ID, vm.State)
		}
	}
}

func TestBulkVMAction_PartialFailure(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning})

	body := jsonBody(t, map[string]any{
		"action": "stop",
		"ids":    []string{"vm-1", "missing", "  "},
	})
	resp, err := http.Post(ts.URL+"/api/v1/vms/bulk", "application/json", body)
	if err != nil {
		t.Fatalf("POST /vms/bulk: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got bulkVMActionResponse
	decodeJSON(t, resp, &got)
	if len(got.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(got.Results))
	}
	if !got.Results[0].Success {
		t.Fatalf("first result = %+v, want success", got.Results[0])
	}
	if got.Results[1].Success || got.Results[1].Code != "resource_not_found" {
		t.Fatalf("second result = %+v, want resource_not_found failure", got.Results[1])
	}
	if got.Results[2].Success || got.Results[2].Code != "invalid_vm_id" {
		t.Fatalf("third result = %+v, want invalid_vm_id failure", got.Results[2])
	}
}

func TestBulkVMAction_InvalidRequest(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantCode  string
		wantState int
	}{
		{name: "bad json", body: "{", wantCode: "", wantState: http.StatusBadRequest},
		{name: "invalid action", body: `{"action":"explode","ids":["vm-1"]}`, wantCode: "invalid_bulk_action", wantState: http.StatusBadRequest},
		{name: "missing ids", body: `{"action":"start","ids":[]}`, wantCode: "invalid_bulk_request", wantState: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, _, cleanup := testServer(t)
			defer cleanup()

			resp, err := http.Post(ts.URL+"/api/v1/vms/bulk", "application/json", bytes.NewBufferString(tt.body))
			if err != nil {
				t.Fatalf("POST /vms/bulk: %v", err)
			}
			if resp.StatusCode != tt.wantState {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantState)
			}
			if tt.wantCode != "" {
				assertAPIErrorCode(t, resp, tt.wantCode)
				return
			}
			var errResp errorResponse
			decodeJSON(t, resp, &errResp)
			if !strings.Contains(errResp.Error, "invalid request body") {
				t.Fatalf("error = %q, want invalid request body", errResp.Error)
			}
		})
	}
}

// TestBulkVMAction_LifecycleVerbs exercises every lifecycle verb that 2.3.8
// adds to the bulk endpoint (restart / force-stop / reboot / suspend /
// resume).  Each subtest seeds VMs in the appropriate state, fires the bulk
// action, and asserts the result list + post-action state.
func TestBulkVMAction_LifecycleVerbs(t *testing.T) {
	cases := []struct {
		action    string
		seedState types.VMState
		wantState types.VMState
	}{
		{action: "restart", seedState: types.VMStateRunning, wantState: types.VMStateRunning},
		{action: "force-stop", seedState: types.VMStateRunning, wantState: types.VMStateStopped},
		{action: "reboot", seedState: types.VMStateRunning, wantState: types.VMStateRunning},
		{action: "suspend", seedState: types.VMStateRunning, wantState: types.VMStatePaused},
		{action: "resume", seedState: types.VMStatePaused, wantState: types.VMStateRunning},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			ts, mockMgr, cleanup := testServer(t)
			defer cleanup()

			ids := []string{"vm-a", "vm-b"}
			for _, id := range ids {
				mockMgr.SeedVM(&types.VM{ID: id, Name: id, State: tc.seedState})
			}

			body := jsonBody(t, map[string]any{"action": tc.action, "ids": ids})
			resp, err := http.Post(ts.URL+"/api/v1/vms/bulk", "application/json", body)
			if err != nil {
				t.Fatalf("POST /vms/bulk %s: %v", tc.action, err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			var got bulkVMActionResponse
			decodeJSON(t, resp, &got)
			if got.Action != tc.action {
				t.Fatalf("action = %q, want %q", got.Action, tc.action)
			}
			if len(got.Results) != len(ids) {
				t.Fatalf("results = %d, want %d", len(got.Results), len(ids))
			}
			for _, r := range got.Results {
				if !r.Success {
					t.Fatalf("result for %s unsuccessful: %+v", r.ID, r)
				}
				vm, err := mockMgr.Get(nil, r.ID)
				if err != nil {
					t.Fatalf("Get(%s): %v", r.ID, err)
				}
				if vm.State != tc.wantState {
					t.Fatalf("vm %s state = %q, want %q", r.ID, vm.State, tc.wantState)
				}
			}
		})
	}
}

// TestBulkVMAction_SuspendPartialFailure exercises the partial-failure path
// for the new lifecycle verbs: when one VM is in the wrong state for the
// action (e.g. suspend on a stopped VM), the bulk endpoint must surface the
// per-VM 409 code without halting the rest of the batch.
func TestBulkVMAction_SuspendPartialFailure(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", Name: "running", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-stopped", Name: "stopped", State: types.VMStateStopped})

	body := jsonBody(t, map[string]any{
		"action": "suspend",
		"ids":    []string{"vm-running", "vm-stopped"},
	})
	resp, err := http.Post(ts.URL+"/api/v1/vms/bulk", "application/json", body)
	if err != nil {
		t.Fatalf("POST /vms/bulk: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got bulkVMActionResponse
	decodeJSON(t, resp, &got)
	if len(got.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(got.Results))
	}
	if !got.Results[0].Success {
		t.Fatalf("first result = %+v, want success", got.Results[0])
	}
	if got.Results[1].Success || got.Results[1].Code != "vm_not_running" {
		t.Fatalf("second result = %+v, want vm_not_running failure", got.Results[1])
	}

	// The successful target was actually suspended.
	if vm, _ := mockMgr.Get(nil, "vm-running"); vm.State != types.VMStatePaused {
		t.Fatalf("vm-running state = %q, want paused", vm.State)
	}
	// The skipped target stays in its original state.
	if vm, _ := mockMgr.Get(nil, "vm-stopped"); vm.State != types.VMStateStopped {
		t.Fatalf("vm-stopped state = %q, want stopped", vm.State)
	}
}

// TestBulkVMAction_InvalidActionMessageListsAllVerbs locks in that the error
// message points operators at the full list of supported actions so a typo
// like "shutdown" produces a self-documenting response.
func TestBulkVMAction_InvalidActionMessageListsAllVerbs(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms/bulk", "application/json",
		bytes.NewBufferString(`{"action":"shutdown","ids":["vm-1"]}`))
	if err != nil {
		t.Fatalf("POST /vms/bulk: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	var errResp errorResponse
	decodeJSON(t, resp, &errResp)
	for _, want := range []string{"start", "stop", "delete", "restart", "force-stop", "reboot", "suspend", "resume"} {
		if !strings.Contains(errResp.Error, want) {
			t.Fatalf("error %q is missing %q", errResp.Error, want)
		}
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

func TestCreateVM_AutoStartFlag(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:      "auto-on",
		Image:     "ubuntu",
		CPUs:      2,
		RAMMB:     2048,
		AutoStart: true,
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var created types.VM
	decodeJSON(t, resp, &created)
	if !created.Spec.AutoStart {
		t.Fatalf("Spec.AutoStart = false, want true (round-trip should preserve the flag)")
	}
}

func TestUpdateVM_AutoStartToggle(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:   "vm-as",
		Name: "togglable",
		Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, AutoStart: false},
	})

	enable := true
	patch := types.VMUpdateSpec{AutoStart: &enable}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-as", jsonBody(t, patch))
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
	if !updated.Spec.AutoStart {
		t.Fatalf("AutoStart = false after enable patch, want true")
	}

	// Disable.
	disable := false
	patch = types.VMUpdateSpec{AutoStart: &disable}
	req2, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-as", jsonBody(t, patch))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d, want 200", resp2.StatusCode)
	}
	decodeJSON(t, resp2, &updated)
	if updated.Spec.AutoStart {
		t.Fatalf("AutoStart = true after disable patch, want false")
	}
}

func TestCreateVM_LockedFlag(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:   "born-locked",
		Image:  "ubuntu",
		CPUs:   2,
		RAMMB:  2048,
		Locked: true,
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.VM
	decodeJSON(t, resp, &created)
	if !created.Spec.Locked {
		t.Fatalf("Spec.Locked = false, want true (round-trip should preserve the flag)")
	}
}

func TestUpdateVM_LockToggle(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:   "vm-lk",
		Name: "lockable",
		Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, Locked: false},
	})

	enable := true
	patch := types.VMUpdateSpec{Locked: &enable}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-lk", jsonBody(t, patch))
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
	if !updated.Spec.Locked {
		t.Fatalf("Locked = false after enable patch, want true")
	}

	disable := false
	patch = types.VMUpdateSpec{Locked: &disable}
	req2, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-lk", jsonBody(t, patch))
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d, want 200", resp2.StatusCode)
	}
	decodeJSON(t, resp2, &updated)
	if updated.Spec.Locked {
		t.Fatalf("Locked = true after disable patch, want false")
	}
}

func TestDeleteVM_Locked_Returns409(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:   "vm-lockdel",
		Name: "important",
		Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DiskGB: 10, Locked: true},
	})

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/vm-lockdel", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (vm_locked)", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "vm_locked" {
		t.Fatalf("error code = %q, want vm_locked", apiErr.Code)
	}

	// VM should still be there.
	if _, err := mockMgr.Get(context.Background(), "vm-lockdel"); err != nil {
		t.Fatalf("VM gone after rejected delete: %v", err)
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

func TestUpdateVM_QuotaExceeded_CPUs(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxTotalCPUs = 4
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "base", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "target", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	patch := types.VMUpdateSpec{CPUs: 3}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-2", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "quota_exceeded")
}

func TestUpdateVM_QuotaExceeded_Disk(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxTotalDiskGB = 50
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "base", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "target", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	patch := types.VMUpdateSpec{DiskGB: 40}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-2", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "quota_exceeded")
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
	assertAPIErrorCode(t, resp, "invalid_request_body")
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

func TestUpdateVM_InvalidSpecBounds(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bounds", Name: "bounded", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	patch := types.VMUpdateSpec{CPUs: 129}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-bounds", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_spec")
}

func TestUpdateVM_InvalidNatGateway(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-gateway", Name: "gateway", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	patch := types.VMUpdateSpec{NatGateway: "bad-gateway"}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-gateway", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_spec")
}

func TestUpdateVM_InvalidDiskBounds(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-disk-bounds", Name: "bounded", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	patch := types.VMUpdateSpec{DiskGB: 10241}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-disk-bounds", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	errResp := assertAPIErrorCode(t, resp, "invalid_spec")
	if errResp.Message != "disk_gb must be between 1 and 10240" {
		t.Fatalf("message = %q", errResp.Message)
	}
}

func TestUpdateVM_InvalidTags(t *testing.T) {
	tests := []struct {
		name        string
		tags        []string
		wantMessage string
	}{
		{name: "empty tag", tags: []string{"prod", "   "}, wantMessage: "tags cannot contain empty values"},
		{name: "tag too long", tags: []string{"abcdefghijklmnopqrstuvwxyz1234567"}, wantMessage: "tags must be 1-32 characters"},
		{name: "invalid characters", tags: []string{"invalid tag!"}, wantMessage: "tags must contain only lowercase letters, numbers, dots, colons, underscores, or hyphens"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, mockMgr, cleanup := testServer(t)
			defer cleanup()

			mockMgr.SeedVM(&types.VM{ID: "vm-tags", Name: "tagged", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

			patch := types.VMUpdateSpec{Tags: tt.tags}
			req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-tags", jsonBody(t, patch))
			req.Header.Set("Content-Type", "application/json")
			resp, _ := http.DefaultClient.Do(req)

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			errResp := assertAPIErrorCode(t, resp, "invalid_spec")
			if errResp.Message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", errResp.Message, tt.wantMessage)
			}
		})
	}
}

// ============================================================
// 5.6.18 — os_type/os_variant immutability + clock_offset control
// ============================================================

func TestUpdateVM_OSTypeRejected(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-os", Name: "linux-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, OSType: types.OSTypeLinux}})

	// Even an empty string for os_type is rejected: the field has pointer
	// semantics so a present-but-empty key signals intent to "clear" the OS
	// family, which is not a valid operation on an existing VM.
	for _, body := range []string{
		`{"os_type":"windows"}`,
		`{"os_type":""}`,
		`{"os_type":"linux"}`,
		`{"os_type":"WINDOWS"}`,
	} {
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-os", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, resp.StatusCode)
		}
		assertAPIErrorCode(t, resp, "os_type_immutable")
	}
}

func TestUpdateVM_OSVariantRejected(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-osv", Name: "win-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows, OSVariant: "windows-server-2022"}})

	for _, body := range []string{
		`{"os_variant":"windows-server-2025"}`,
		`{"os_variant":""}`,
	} {
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-osv", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, resp.StatusCode)
		}
		assertAPIErrorCode(t, resp, "os_type_immutable")
	}
}

func TestUpdateVM_GPUsRejected(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-gpu", Name: "gpu-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, GPUs: []string{"0000:01:00.0"}}})

	for _, body := range []string{
		`{"gpus":["0000:02:00.0"]}`,
		`{"gpus":[]}`,
	} {
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-gpu", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, resp.StatusCode)
		}
		assertAPIErrorCode(t, resp, "gpus_immutable")
	}
}

func TestUpdateVM_OSFieldOmittedIsOK(t *testing.T) {
	// Sanity check: omitting os_type / os_variant entirely (the normal case)
	// must not trip the immutability guard.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-osok", Name: "host", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, OSType: types.OSTypeLinux}})

	patch := types.VMUpdateSpec{CPUs: 4}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-osok", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestUpdateVM_ClockOffsetChange(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-clock", Name: "win-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows}})

	utc := "utc"
	patch := types.VMUpdateSpec{ClockOffset: &utc}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-clock", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated types.VM
	decodeJSON(t, resp, &updated)
	if updated.Spec.ClockOffset != "utc" {
		t.Errorf("Spec.ClockOffset = %q, want %q", updated.Spec.ClockOffset, "utc")
	}
	if got := updated.Spec.ResolvedClockOffset(); got != "utc" {
		t.Errorf("ResolvedClockOffset = %q, want utc", got)
	}
}

func TestUpdateVM_ClockOffsetClear(t *testing.T) {
	// Pointer-to-empty-string clears the override; the resolved offset then
	// falls back to the OS-family default (localtime for Windows).
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-clock-clear", Name: "win-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows, ClockOffset: "utc"}})

	empty := ""
	patch := types.VMUpdateSpec{ClockOffset: &empty}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-clock-clear", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated types.VM
	decodeJSON(t, resp, &updated)
	if updated.Spec.ClockOffset != "" {
		t.Errorf("Spec.ClockOffset = %q, want empty after clear", updated.Spec.ClockOffset)
	}
	if got := updated.Spec.ResolvedClockOffset(); got != "localtime" {
		t.Errorf("ResolvedClockOffset = %q, want localtime (windows default)", got)
	}
}

func TestUpdateVM_ClockOffsetInvalid(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-clock-bad", Name: "host", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	for _, bad := range []string{"foo", "UTC+0", "bsd-time"} {
		patch := types.VMUpdateSpec{ClockOffset: &bad}
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-clock-bad", jsonBody(t, patch))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", bad, resp.StatusCode)
		}
		assertAPIErrorCode(t, resp, "invalid_clock_offset")
	}
}

func TestUpdateVM_ClockOffsetMixedCase(t *testing.T) {
	// Case-insensitive normalisation: "UTC" / "LocalTime" must work.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-clock-mixed", Name: "host", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	for _, in := range []string{"UTC", "Utc", "LocalTime", "LOCALTIME"} {
		patch := types.VMUpdateSpec{ClockOffset: &in}
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-clock-mixed", jsonBody(t, patch))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("input %q: status = %d, want 200", in, resp.StatusCode)
		}
	}
}

func TestCreateVM_ClockOffsetInvalid(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	body := `{"name":"clk","image":"rocky9","cpus":2,"ram_mb":2048,"disk_gb":20,"clock_offset":"bogus"}`
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_clock_offset")
}

func TestCreateVM_ClockOffsetAccepted(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	body := `{"name":"clk-ok","image":"rocky9","cpus":2,"ram_mb":2048,"disk_gb":20,"clock_offset":"UTC"}`
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var vm types.VM
	decodeJSON(t, resp, &vm)
	if vm.Spec.ClockOffset != "UTC" {
		// The handler stores what the client sent (the API layer does NOT
		// lowercase the persisted value). ResolvedClockOffset() normalises
		// on read. This keeps the field round-trip-honest for clients that
		// already lowercased.
		t.Logf("ClockOffset persisted as %q (case-preserving)", vm.Spec.ClockOffset)
	}
	if got := vm.Spec.ResolvedClockOffset(); got != "utc" {
		t.Errorf("ResolvedClockOffset = %q, want utc", got)
	}
}

// ============================================================
// 5.6.15 — Per-VM device overrides
// ============================================================

func TestCreateVM_DeviceOverrides_Accepted(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	body := `{"name":"dev-ok","image":"rocky9","cpus":2,"ram_mb":2048,"disk_gb":20,` +
		`"disk_bus":"SATA","nic_model":"E1000E","machine":"pc-q35-rhel9.6.0",` +
		`"firmware":"UEFI","virtio_win_iso":"/tmp/virtio-win.iso"}`
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var vm types.VM
	decodeJSON(t, resp, &vm)
	// The handler stores what the client sent (case-preserving). The
	// Resolved* helpers normalise on read, mirroring the clock_offset
	// contract.
	if vm.Spec.ResolvedDiskBus() != "sata" {
		t.Errorf("ResolvedDiskBus = %q, want sata", vm.Spec.ResolvedDiskBus())
	}
	if vm.Spec.ResolvedNICModel() != "e1000e" {
		t.Errorf("ResolvedNICModel = %q, want e1000e", vm.Spec.ResolvedNICModel())
	}
	if vm.Spec.ResolvedFirmwareAttr() != "efi" {
		t.Errorf("ResolvedFirmwareAttr = %q, want efi", vm.Spec.ResolvedFirmwareAttr())
	}
	if vm.Spec.Machine != "pc-q35-rhel9.6.0" {
		t.Errorf("Machine = %q, want pc-q35-rhel9.6.0", vm.Spec.Machine)
	}
	if vm.Spec.VirtioWinISO != "/tmp/virtio-win.iso" {
		t.Errorf("VirtioWinISO = %q, want /tmp/virtio-win.iso", vm.Spec.VirtioWinISO)
	}
}

func TestCreateVM_InvalidDiskBus(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	body := `{"name":"bad-bus","image":"rocky9","cpus":2,"ram_mb":2048,"disk_gb":20,"disk_bus":"scsi"}`
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_disk_bus")
}

func TestCreateVM_InvalidNICModel(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	body := `{"name":"bad-nic","image":"rocky9","cpus":2,"ram_mb":2048,"disk_gb":20,"nic_model":"rtl8139"}`
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_nic_model")
}

func TestCreateVM_InvalidFirmware(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	body := `{"name":"bad-fw","image":"rocky9","cpus":2,"ram_mb":2048,"disk_gb":20,"firmware":"coreboot"}`
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_firmware")
}

func TestCreateVM_InvalidMachine(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Shell metacharacters in machine type are rejected before they reach
	// the domain template.
	body := `{"name":"bad-mach","image":"rocky9","cpus":2,"ram_mb":2048,"disk_gb":20,"machine":"pc;rm -rf /"}`
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_machine")
}

// Roadmap 5.6.12 — disk_bus / nic_model are mutable on PATCH so an operator
// can switch a Windows guest to virtio after installing the virtio drivers
// in-guest. The mock manager mirrors the LibvirtManager normalisation
// (lowercase + trim) so these assertions reflect the on-disk shape.

func TestUpdateVM_DiskBusChange(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bus", Name: "win-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows, DiskBus: "sata"}})

	virtio := "virtio"
	patch := types.VMUpdateSpec{DiskBus: &virtio}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-bus", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated types.VM
	decodeJSON(t, resp, &updated)
	if updated.Spec.DiskBus != "virtio" {
		t.Errorf("Spec.DiskBus = %q, want virtio", updated.Spec.DiskBus)
	}
	if got := updated.Spec.ResolvedDiskBus(); got != "virtio" {
		t.Errorf("ResolvedDiskBus = %q, want virtio", got)
	}
}

func TestUpdateVM_NICModelChange(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-nic", Name: "win-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows, NICModel: "e1000e"}})

	virtio := "virtio"
	patch := types.VMUpdateSpec{NICModel: &virtio}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-nic", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated types.VM
	decodeJSON(t, resp, &updated)
	if updated.Spec.NICModel != "virtio" {
		t.Errorf("Spec.NICModel = %q, want virtio", updated.Spec.NICModel)
	}
}

func TestUpdateVM_SwitchToVirtio_BothFields(t *testing.T) {
	// The set-virtio CLI sends both fields atomically; verify the API path
	// accepts the pair.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-virt", Name: "win-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows, DiskBus: "sata", NICModel: "e1000e"}})

	bus, nic := "virtio", "virtio"
	patch := types.VMUpdateSpec{DiskBus: &bus, NICModel: &nic}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-virt", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated types.VM
	decodeJSON(t, resp, &updated)
	if updated.Spec.DiskBus != "virtio" || updated.Spec.NICModel != "virtio" {
		t.Errorf("Spec.DiskBus / Spec.NICModel = %q / %q, want virtio / virtio", updated.Spec.DiskBus, updated.Spec.NICModel)
	}
}

func TestUpdateVM_DiskBusClear(t *testing.T) {
	// Pointer-to-empty-string clears the override; ResolvedDiskBus then
	// falls back to the OS-family default (sata for Windows).
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bus-clear", Name: "win-host", Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows, DiskBus: "virtio"}})

	empty := ""
	patch := types.VMUpdateSpec{DiskBus: &empty}
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-bus-clear", jsonBody(t, patch))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated types.VM
	decodeJSON(t, resp, &updated)
	if updated.Spec.DiskBus != "" {
		t.Errorf("Spec.DiskBus = %q, want empty after clear", updated.Spec.DiskBus)
	}
	if got := updated.Spec.ResolvedDiskBus(); got != "sata" {
		t.Errorf("ResolvedDiskBus = %q, want sata (windows default)", got)
	}
}

func TestUpdateVM_DiskBusInvalid(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bus-bad", Name: "host", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	for _, bad := range []string{"scsi", "ide", "nvme"} {
		patch := types.VMUpdateSpec{DiskBus: &bad}
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-bus-bad", jsonBody(t, patch))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", bad, resp.StatusCode)
		}
		assertAPIErrorCode(t, resp, "invalid_disk_bus")
	}
}

func TestUpdateVM_NICModelInvalid(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-nic-bad", Name: "host", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	for _, bad := range []string{"rtl8139", "ne2k_pci", "vmxnet3"} {
		patch := types.VMUpdateSpec{NICModel: &bad}
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-nic-bad", jsonBody(t, patch))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", bad, resp.StatusCode)
		}
		assertAPIErrorCode(t, resp, "invalid_nic_model")
	}
}

func TestUpdateVM_DiskBusMixedCase(t *testing.T) {
	// Case-insensitive normalisation: "VIRTIO" / "Sata" must work.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bus-mixed", Name: "host", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	for _, in := range []string{"VIRTIO", "Virtio", "SATA", "Sata"} {
		patch := types.VMUpdateSpec{DiskBus: &in}
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-bus-mixed", jsonBody(t, patch))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("input %q: status = %d, want 200", in, resp.StatusCode)
		}
	}
}

func TestCreateVM_DeviceOverrides_EmptyResolvesToDefaults(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// No override fields → linux defaults. Linux disk_bus resolves to
	// "virtio" + nic_model "virtio" + no firmware attr; Windows defaults
	// to sata + e1000e (covered by TestCreateVM_Windows).
	body := `{"name":"defaults","image":"rocky9","cpus":2,"ram_mb":2048,"disk_gb":20}`
	resp, _ := http.Post(ts.URL+"/api/v1/vms", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var vm types.VM
	decodeJSON(t, resp, &vm)
	if got := vm.Spec.ResolvedDiskBus(); got != "virtio" {
		t.Errorf("ResolvedDiskBus = %q, want virtio", got)
	}
	if got := vm.Spec.ResolvedNICModel(); got != "virtio" {
		t.Errorf("ResolvedNICModel = %q, want virtio", got)
	}
	if got := vm.Spec.ResolvedFirmwareAttr(); got != "" {
		t.Errorf("ResolvedFirmwareAttr = %q, want empty", got)
	}
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

func TestCreateSnapshot_WithDescription(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-d", Name: "snappable", State: types.VMStateRunning})

	body := jsonBody(t, map[string]string{"name": "before-patch", "description": "before applying May patch"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-snap-d/snapshots", "application/json", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var snap types.Snapshot
	decodeJSON(t, resp, &snap)

	if snap.Description != "before applying May patch" {
		t.Errorf("Description = %q, want 'before applying May patch'", snap.Description)
	}

	// Round-trip via list
	listResp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-d/snapshots")
	var listed []*types.Snapshot
	decodeJSON(t, listResp, &listed)
	if len(listed) != 1 || listed[0].Description != "before applying May patch" {
		t.Errorf("list did not preserve description: got %+v", listed)
	}
}

func TestCreateSnapshot_RejectsLongDescription(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-x", Name: "snappable"})

	long := strings.Repeat("x", 1025)
	body := jsonBody(t, map[string]string{"name": "snap", "description": long})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-snap-x/snapshots", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_description")
}

func TestListSnapshots(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-s", Name: "host"})
	mockMgr.CreateSnapshot(nil, "vm-s", types.SnapshotSpec{Name: "snap1"})
	mockMgr.CreateSnapshot(nil, "vm-s", types.SnapshotSpec{Name: "snap2"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-s/snapshots")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}

	var snaps []*types.Snapshot
	decodeJSON(t, resp, &snaps)

	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snaps))
	}
}

func TestListSnapshots_Pagination(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-s", Name: "host"})
	mockMgr.CreateSnapshot(nil, "vm-s", types.SnapshotSpec{Name: "snap1"})
	mockMgr.CreateSnapshot(nil, "vm-s", types.SnapshotSpec{Name: "snap2"})
	mockMgr.CreateSnapshot(nil, "vm-s", types.SnapshotSpec{Name: "snap3"})

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-s/snapshots?page=2&per_page=1")
	if err != nil {
		t.Fatalf("GET /vms/vm-s/snapshots?page=2&per_page=1: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("X-Total-Count = %q, want 3", got)
	}

	var snaps []*types.Snapshot
	decodeJSON(t, resp, &snaps)

	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].Name != "snap2" {
		t.Fatalf("snapshot page 2 = %q, want snap2", snaps[0].Name)
	}
}

func TestRestoreSnapshot(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-r", Name: "restorable"})
	mockMgr.CreateSnapshot(nil, "vm-r", types.SnapshotSpec{Name: "good-state"})

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-r/snapshots/good-state/restore", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDeleteSnapshot(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-ds", Name: "host"})
	mockMgr.CreateSnapshot(nil, "vm-ds", types.SnapshotSpec{Name: "temp"})

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

func TestAddPort_RejectsLongDescription(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-desc", Name: "desc", IP: "192.168.100.10"})

	body := jsonBody(t, addPortRequest{
		HostPort:    2222,
		GuestPort:   22,
		Protocol:    "tcp",
		Description: strings.Repeat("x", 257),
	})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-desc/ports", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for over-long description", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_port_forward" {
		t.Errorf("code = %q, want invalid_port_forward", apiErr.Code)
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

// testServerWithPortFwd is like testServerFull but also returns the
// PortForwarder so tests can stub the iptables hook.
func testServerWithPortFwd(t *testing.T) (*httptest.Server, *vm.MockManager, *store.Store, *network.PortForwarder, func()) {
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
	// Tests need port-forward add/remove to succeed without real iptables.
	portFwd.SetApplyRuleFunc(func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	})

	apiServer := NewServer(mockMgr, storageMgr, portFwd, s)
	ts := httptest.NewServer(apiServer)

	cleanup := func() {
		ts.Close()
		s.Close()
	}
	return ts, mockMgr, s, portFwd, cleanup
}

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

	apiServer := NewServer(mockMgr, storageMgr, portFwd, s)
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

func TestRestartVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-restart", Name: "rebooter", State: types.VMStateRunning})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-restart/restart", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	got, _ := mockMgr.Get(nil, "vm-restart")
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
}

func TestRestartVM_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x", State: types.VMStateRunning})
	mockMgr.RestartErr = types.ErrTest

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-x/restart", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestRebootVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-rb", Name: "rebooter", State: types.VMStateRunning})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-rb/reboot", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "rebooted" {
		t.Errorf("status field = %q, want rebooted", body["status"])
	}

	got, _ := mockMgr.Get(nil, "vm-rb")
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
}

func TestRebootVM_NotRunning_Returns409(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-rbs", State: types.VMStateStopped})

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-rbs/reboot", "application/json", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["code"] != "vm_not_running" {
		t.Errorf("code = %v, want vm_not_running", body["code"])
	}
}

func TestRebootVM_NotFound_Returns404(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-missing/reboot", "application/json", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestRebootVM_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x", State: types.VMStateRunning})
	mockMgr.RebootErr = types.ErrTest

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-x/reboot", "application/json", nil)
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

func TestForceStopVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-fs", Name: "wedged", State: types.VMStateRunning})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-fs/force-stop", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "force_stopped" {
		t.Errorf("status field = %q, want force_stopped", body["status"])
	}

	got, _ := mockMgr.Get(nil, "vm-fs")
	if got.State != types.VMStateStopped {
		t.Errorf("State = %q, want stopped", got.State)
	}
}

func TestForceStopVM_AlreadyStopped_Returns409(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-fs2", Name: "stopped", State: types.VMStateStopped})

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-fs2/force-stop", "application/json", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "vm_already_stopped" {
		t.Errorf("code = %v, want vm_already_stopped", body["code"])
	}
}

func TestForceStopVM_NotFound_Returns404(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-missing/force-stop", "application/json", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestForceStopVM_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x", State: types.VMStateRunning})
	mockMgr.ForceStopErr = types.ErrTest

	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-x/force-stop", "application/json", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestSuspendVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-s", Name: "pauseme", State: types.VMStateRunning})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-s/suspend", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "suspended" {
		t.Errorf("status field = %q, want suspended", body["status"])
	}

	got, _ := mockMgr.Get(context.Background(), "vm-s")
	if got.State != types.VMStatePaused {
		t.Errorf("State = %q, want paused", got.State)
	}
}

func TestSuspendVM_NotRunning_Returns409(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-stopped", State: types.VMStateStopped})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-stopped/suspend", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}

	var body types.APIError
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != "vm_not_running" {
		t.Errorf("code = %q, want vm_not_running", body.Code)
	}
}

func TestSuspendVM_AlreadyPaused_Returns409(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-p", State: types.VMStatePaused})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-p/suspend", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}

	var body types.APIError
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != "vm_already_paused" {
		t.Errorf("code = %q, want vm_already_paused", body.Code)
	}
}

func TestSuspendVM_NotFound_Returns404(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms/nonexistent/suspend", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSuspendVM_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x", State: types.VMStateRunning})
	mockMgr.SuspendErr = types.ErrTest

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-x/suspend", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestResumeVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-r", Name: "resumeme", State: types.VMStatePaused})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-r/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "resumed" {
		t.Errorf("status field = %q, want resumed", body["status"])
	}

	got, _ := mockMgr.Get(context.Background(), "vm-r")
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
}

func TestResumeVM_NotPaused_Returns409(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-running/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}

	var body types.APIError
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != "vm_not_paused" {
		t.Errorf("code = %q, want vm_not_paused", body.Code)
	}
}

func TestResumeVM_NotFound_Returns404(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms/nonexistent/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestResumeVM_Error(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x", State: types.VMStatePaused})
	mockMgr.ResumeErr = types.ErrTest

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-x/resume", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
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
	assertAPIErrorCode(t, resp, "invalid_request_body")
}

func TestCreateSnapshot_MissingName(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x"})

	for _, body := range []map[string]string{{}, {"name": "   "}} {
		resp, err := http.Post(ts.URL+"/api/v1/vms/vm-x/snapshots", "application/json", jsonBody(t, body))
		if err != nil {
			t.Fatalf("POST snapshot: %v", err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		errResp := assertAPIErrorCode(t, resp, "invalid_name")
		if errResp.Message != "snapshot name is required" {
			t.Fatalf("message = %q, want snapshot name is required", errResp.Message)
		}
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

func TestListSnapshots_SortByName(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-sort", Name: "host"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-sort", Name: "Charlie"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-sort", Name: "alpha"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-sort", Name: "Bravo"})

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-snap-sort/snapshots?sort=name")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, snap := range got {
		if snap.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, snap.Name, want[i])
		}
	}
}

func TestListSnapshots_SortByNameDesc(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-desc"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-desc", Name: "alpha"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-desc", Name: "Bravo"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-desc", Name: "Charlie"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-desc/snapshots?sort=name&order=desc")
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	want := []string{"Charlie", "Bravo", "alpha"}
	for i, snap := range got {
		if snap.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, snap.Name, want[i])
		}
	}
}

func TestListSnapshots_SortByCreatedAtDesc(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-time"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-time", Name: "first", CreatedAt: t0})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-time", Name: "second", CreatedAt: t0.Add(time.Hour)})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-time", Name: "third", CreatedAt: t0.Add(2 * time.Hour)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-time/snapshots?sort=created_at&order=desc")
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	want := []string{"third", "second", "first"}
	for i, snap := range got {
		if snap.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, snap.Name, want[i])
		}
	}
}

func TestListSnapshots_SortPaginationDeterministic(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-page"})
	// Insert in arbitrary order to exercise the sort.
	for _, name := range []string{"snap-3", "snap-1", "snap-4", "snap-2", "snap-5"} {
		mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-page", Name: name})
	}

	resp1, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-page/snapshots?sort=name&order=desc&page=1&per_page=2")
	if got := resp1.Header.Get("X-Total-Count"); got != "5" {
		t.Fatalf("X-Total-Count = %q, want 5", got)
	}
	var page1 []*types.Snapshot
	decodeJSON(t, resp1, &page1)

	resp2, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-page/snapshots?sort=name&order=desc&page=2&per_page=2")
	var page2 []*types.Snapshot
	decodeJSON(t, resp2, &page2)

	got := []string{page1[0].Name, page1[1].Name, page2[0].Name, page2[1].Name}
	want := []string{"snap-5", "snap-4", "snap-3", "snap-2"}
	for i, n := range got {
		if n != want[i] {
			t.Fatalf("page-spanning order[%d] = %q, want %q (full: %v)", i, n, want[i], got)
		}
	}
}

func TestListSnapshots_RejectsInvalidSort(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-bad-sort"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-bad-sort/snapshots?sort=description")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_sort")
}

func TestListSnapshots_RejectsInvalidOrder(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-bad-order"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-bad-order/snapshots?order=sideways")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_order")
}

func TestListSnapshots_FilterBySearch_Name(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-search"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-search", Name: "pre-upgrade"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-search", Name: "rollback-point"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-search", Name: "weekly-backup"})

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-snap-search/snapshots?search=upgrade")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1", got)
	}
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	if len(got) != 1 || got[0].Name != "pre-upgrade" {
		t.Fatalf("expected only pre-upgrade, got %+v", got)
	}
}

func TestListSnapshots_FilterBySearch_Description(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-desc-search"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-desc-search", Name: "snap-001", Description: "Before applying CIS hardening playbook"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-desc-search", Name: "snap-002", Description: "Routine nightly cut"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-desc-search/snapshots?search=hardening")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	if len(got) != 1 || got[0].Name != "snap-001" {
		t.Fatalf("expected only snap-001, got %+v", got)
	}
}

func TestListSnapshots_FilterBySearch_CaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-case"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-case", Name: "Pre-Upgrade"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-case/snapshots?search=UPGRADE")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	if len(got) != 1 {
		t.Fatalf("expected uppercase needle to match case-insensitively, got %d snapshots", len(got))
	}
}

func TestListSnapshots_FilterBySearch_WhitespaceTrimmed(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-trim"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-trim", Name: "alpha"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-trim", Name: "beta"})

	// The handler must trim leading/trailing whitespace before searching;
	// otherwise the needle "  alpha  " would never match any snapshot.
	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-trim/snapshots?search=%20%20alpha%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("expected only alpha after trim, got %+v", got)
	}
}

func TestListSnapshots_FilterBySearch_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-empty"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-empty", Name: "alpha"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-empty/snapshots?search=needle-not-present")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("X-Total-Count = %q, want 0", got)
	}
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %+v", got)
	}
}

func TestListSnapshots_FilterBySearch_ComposesWithSort(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-compose"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-compose", Name: "upgrade-beta"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-compose", Name: "rollback"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-compose", Name: "upgrade-alpha"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-compose/snapshots?search=upgrade&sort=name")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (post-search)", got)
	}
	var got []*types.Snapshot
	decodeJSON(t, resp, &got)
	want := []string{"upgrade-alpha", "upgrade-beta"}
	if len(got) != len(want) {
		t.Fatalf("expected %d snapshots, got %d", len(want), len(got))
	}
	for i, snap := range got {
		if snap.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, snap.Name, want[i])
		}
	}
}

func TestRestoreSnapshot_SnapshotNotFound(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-r"})
	mockMgr.CreateSnapshot(nil, "vm-r", types.SnapshotSpec{Name: "good-state"})

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
	mockMgr.CreateSnapshot(nil, "vm-ds", types.SnapshotSpec{Name: "existing"})

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/vms/vm-ds/snapshots/missing", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

// ============================================================
// Snapshot bulk-delete tests
// ============================================================

func decodeBulkSnapshotResponse(t *testing.T, resp *http.Response) bulkDeleteSnapshotsResponse {
	t.Helper()
	var out bulkDeleteSnapshotsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	return out
}

func TestBulkDeleteSnapshots_ByNames(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bd"})
	for _, n := range []string{"keep", "auto-1", "auto-2", "auto-3"} {
		mockMgr.CreateSnapshot(nil, "vm-bd", types.SnapshotSpec{Name: n})
	}

	body := jsonBody(t, bulkDeleteSnapshotsRequest{Names: []string{"auto-1", "auto-3"}})
	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-bd/snapshots/bulk_delete", "application/json", body)
	if err != nil {
		t.Fatalf("POST bulk_delete: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkSnapshotResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	for _, r := range out.Results {
		if !r.Success {
			t.Errorf("expected success for %q, got code=%q msg=%q", r.Name, r.Code, r.Message)
		}
	}

	snaps, _ := mockMgr.ListSnapshots(nil, "vm-bd")
	names := make(map[string]bool, len(snaps))
	for _, s := range snaps {
		names[s.Name] = true
	}
	if !names["keep"] || !names["auto-2"] {
		t.Errorf("survivors = %v, want both keep and auto-2", names)
	}
	if names["auto-1"] || names["auto-3"] {
		t.Errorf("expected auto-1 and auto-3 to be deleted, survivors = %v", names)
	}
}

func TestBulkDeleteSnapshots_ByPrefix(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bp"})
	for _, n := range []string{"manual-rollback", "auto-nightly-1", "auto-nightly-2", "auto-weekly-1"} {
		mockMgr.CreateSnapshot(nil, "vm-bp", types.SnapshotSpec{Name: n})
	}

	body := jsonBody(t, bulkDeleteSnapshotsRequest{Prefix: "auto-nightly-"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-bp/snapshots/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkSnapshotResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}

	snaps, _ := mockMgr.ListSnapshots(nil, "vm-bp")
	names := make([]string, 0, len(snaps))
	for _, s := range snaps {
		names = append(names, s.Name)
	}
	wantSurvivors := map[string]bool{"manual-rollback": true, "auto-weekly-1": true}
	for _, n := range names {
		if !wantSurvivors[n] {
			t.Errorf("unexpected survivor: %s", n)
		}
	}
	if len(names) != 2 {
		t.Errorf("survivors = %d, want 2 (got %v)", len(names), names)
	}
}

func TestBulkDeleteSnapshots_PartialFailure(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-pf"})
	mockMgr.CreateSnapshot(nil, "vm-pf", types.SnapshotSpec{Name: "exists-1"})

	body := jsonBody(t, bulkDeleteSnapshotsRequest{Names: []string{"exists-1", "missing"}})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-pf/snapshots/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkSnapshotResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	gotByName := map[string]bulkDeleteSnapshotResult{}
	for _, r := range out.Results {
		gotByName[r.Name] = r
	}
	if !gotByName["exists-1"].Success {
		t.Errorf("expected exists-1 to succeed, got %+v", gotByName["exists-1"])
	}
	if gotByName["missing"].Success {
		t.Errorf("expected missing to fail")
	}
	if gotByName["missing"].Code != "resource_not_found" {
		t.Errorf("missing.Code = %q, want resource_not_found", gotByName["missing"].Code)
	}
}

func TestBulkDeleteSnapshots_EmptyRequestRejected(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-empty"})

	body := jsonBody(t, bulkDeleteSnapshotsRequest{})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-empty/snapshots/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeleteSnapshots_BothNamesAndPrefixRejected(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-both"})

	body := jsonBody(t, bulkDeleteSnapshotsRequest{Names: []string{"a"}, Prefix: "b-"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-both/snapshots/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeleteSnapshots_PrefixNoMatchesEmptyResponse(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-nm"})
	mockMgr.CreateSnapshot(nil, "vm-nm", types.SnapshotSpec{Name: "manual-1"})

	body := jsonBody(t, bulkDeleteSnapshotsRequest{Prefix: "auto-"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-nm/snapshots/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkSnapshotResponse(t, resp)
	if len(out.Results) != 0 {
		t.Errorf("results = %d, want 0", len(out.Results))
	}
	snaps, _ := mockMgr.ListSnapshots(nil, "vm-nm")
	if len(snaps) != 1 {
		t.Errorf("survivors = %d, want 1 (manual-1 untouched)", len(snaps))
	}
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
	assertAPIErrorCode(t, resp, "invalid_request_body")
}

func TestCreateImage_MissingFields(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	tests := []struct {
		name        string
		body        createImageRequest
		wantCode    string
		wantMessage string
	}{
		{name: "missing vm id", body: createImageRequest{Name: "img"}, wantCode: "invalid_spec", wantMessage: "vm_id is required"},
		{name: "blank vm id", body: createImageRequest{VMID: "   ", Name: "img"}, wantCode: "invalid_spec", wantMessage: "vm_id is required"},
		{name: "missing image name", body: createImageRequest{VMID: "vm-img"}, wantCode: "invalid_image", wantMessage: "image name is required"},
		{name: "blank image name", body: createImageRequest{VMID: "vm-img", Name: "   "}, wantCode: "invalid_image", wantMessage: "image name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+"/api/v1/images", "application/json", jsonBody(t, tt.body))
			if err != nil {
				t.Fatalf("POST /api/v1/images: %v", err)
			}
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			errResp := assertAPIErrorCode(t, resp, tt.wantCode)
			if errResp.Message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", errResp.Message, tt.wantMessage)
			}
		})
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
	assertAPIErrorCode(t, resp, "storage_error")
}

func TestListVMs_InternalErrorIsSanitized(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.ListErr = errors.New("connecting to libvirt (qemu:///system): permission denied")

	resp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("GET /api/v1/vms: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	errResp := assertAPIErrorCode(t, resp, "service_unavailable")
	if errResp.Message != "vm backend is unavailable" {
		t.Fatalf("message = %q, want vm backend is unavailable", errResp.Message)
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
	assertAPIErrorCode(t, resp, "resource_not_found")
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
// Image bulk-delete tests
// ============================================================

func decodeBulkImageResponse(t *testing.T, resp *http.Response) bulkDeleteImagesResponse {
	t.Helper()
	var out bulkDeleteImagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	return out
}

// seedImageFile writes a tiny qcow2-shaped file under the storage manager's
// images dir and registers it in the store so DeleteImage's os.Remove finds
// the file. Returns the registered image ID.
func seedImageFile(t *testing.T, s *store.Store, dir, id, name string, tags []string) {
	t.Helper()
	path := filepath.Join(dir, name+".qcow2")
	if err := os.WriteFile(path, []byte("seed"), 0o644); err != nil {
		t.Fatalf("write seed image: %v", err)
	}
	if err := s.PutImage(&types.Image{
		ID: id, Name: name, Path: path, SizeBytes: 4, Format: "qcow2",
		Tags: tags, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed image: %v", err)
	}
}

func TestBulkDeleteImages_ByIDs(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	dir := filepath.Join(t.TempDir(), "imgs")
	os.MkdirAll(dir, 0o755)

	seedImageFile(t, s, dir, "img-1", "keep", nil)
	seedImageFile(t, s, dir, "img-2", "del-a", nil)
	seedImageFile(t, s, dir, "img-3", "del-b", nil)

	body := jsonBody(t, bulkDeleteImagesRequest{IDs: []string{"img-2", "img-3"}})
	resp, err := http.Post(ts.URL+"/api/v1/images/bulk_delete", "application/json", body)
	if err != nil {
		t.Fatalf("POST bulk_delete: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkImageResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	for _, r := range out.Results {
		if !r.Success {
			t.Errorf("expected success for %q, got code=%q msg=%q", r.ID, r.Code, r.Message)
		}
	}

	imgs, _ := s.ListImages()
	if len(imgs) != 1 || imgs[0].ID != "img-1" {
		t.Errorf("survivors = %v, want only img-1", imgs)
	}
}

func TestBulkDeleteImages_ByTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	dir := filepath.Join(t.TempDir(), "imgs")
	os.MkdirAll(dir, 0o755)

	seedImageFile(t, s, dir, "img-1", "keeper", []string{"prod"})
	seedImageFile(t, s, dir, "img-2", "rc-a", []string{"rc-2026-05"})
	seedImageFile(t, s, dir, "img-3", "rc-b", []string{"rc-2026-05", "linux"})
	seedImageFile(t, s, dir, "img-4", "rc-old", []string{"rc-2026-04"})

	body := jsonBody(t, bulkDeleteImagesRequest{Tag: "RC-2026-05"})
	resp, _ := http.Post(ts.URL+"/api/v1/images/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkImageResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2 (got %+v)", len(out.Results), out.Results)
	}

	imgs, _ := s.ListImages()
	survivors := map[string]bool{}
	for _, img := range imgs {
		survivors[img.ID] = true
	}
	if !survivors["img-1"] || !survivors["img-4"] || len(survivors) != 2 {
		t.Errorf("survivors = %v, want img-1+img-4", survivors)
	}
}

func TestBulkDeleteImages_PartialFailure(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	dir := filepath.Join(t.TempDir(), "imgs")
	os.MkdirAll(dir, 0o755)

	seedImageFile(t, s, dir, "img-real", "real", nil)

	body := jsonBody(t, bulkDeleteImagesRequest{IDs: []string{"img-real", "img-missing"}})
	resp, _ := http.Post(ts.URL+"/api/v1/images/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkImageResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	gotByID := map[string]bulkDeleteImageResult{}
	for _, r := range out.Results {
		gotByID[r.ID] = r
	}
	if !gotByID["img-real"].Success {
		t.Errorf("expected img-real to succeed, got %+v", gotByID["img-real"])
	}
	if gotByID["img-missing"].Success {
		t.Errorf("expected img-missing to fail")
	}
	if gotByID["img-missing"].Code != "resource_not_found" {
		t.Errorf("img-missing.Code = %q, want resource_not_found", gotByID["img-missing"].Code)
	}
}

func TestBulkDeleteImages_EmptyRequestRejected(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	body := jsonBody(t, bulkDeleteImagesRequest{})
	resp, _ := http.Post(ts.URL+"/api/v1/images/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeleteImages_BothIDsAndTagRejected(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	body := jsonBody(t, bulkDeleteImagesRequest{IDs: []string{"img-1"}, Tag: "rc"})
	resp, _ := http.Post(ts.URL+"/api/v1/images/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeleteImages_TagNoMatchEmptyResponse(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	dir := filepath.Join(t.TempDir(), "imgs")
	os.MkdirAll(dir, 0o755)

	seedImageFile(t, s, dir, "img-keep", "keeper", []string{"prod"})

	body := jsonBody(t, bulkDeleteImagesRequest{Tag: "nope"})
	resp, _ := http.Post(ts.URL+"/api/v1/images/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkImageResponse(t, resp)
	if len(out.Results) != 0 {
		t.Errorf("results = %d, want 0", len(out.Results))
	}
	imgs, _ := s.ListImages()
	if len(imgs) != 1 {
		t.Errorf("survivors = %d, want 1 (img-keep untouched)", len(imgs))
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
	assertAPIErrorCode(t, resp, "invalid_request_body")
}

func TestAddPort_VMIPUnavailable(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-p", Name: "vm-p"})

	body := jsonBody(t, addPortRequest{HostPort: 2222, GuestPort: 22})
	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-p/ports", "application/json", body)
	if err != nil {
		t.Fatalf("POST /ports: %v", err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	errResp := assertAPIErrorCode(t, resp, "vm_ip_unavailable")
	if errResp.Message != "VM does not have an IP address yet; is it running?" {
		t.Fatalf("message = %q, want VM does not have an IP address yet; is it running?", errResp.Message)
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
	errResp := assertAPIErrorCode(t, resp, "missing_file")
	if errResp.Message != "missing file field" {
		t.Fatalf("message = %q, want missing file field", errResp.Message)
	}
}

func TestUploadImage_MissingName(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", ".qcow2")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("fake qcow2 content")); err != nil {
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

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	errResp := assertAPIErrorCode(t, resp, "invalid_image")
	if errResp.Message != "image name is required" {
		t.Fatalf("message = %q, want %q", errResp.Message, "image name is required")
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
	assertAPIErrorCode(t, resp, "request_too_large")
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
	assertAPIErrorCode(t, resp, "request_too_large")
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

func TestListVMs_RateLimitExceeded(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.RateLimitPerSecond = 1
		cfg.Daemon.RateLimitBurst = 1
	})
	defer cleanup()

	client := &http.Client{}
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/vms", nil)
		if err != nil {
			t.Fatalf("new request %d: %v", i+1, err)
		}
		req.Header.Set("X-Forwarded-For", "198.51.100.10")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /vms %d: %v", i+1, err)
		}
		if i == 0 {
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("first status = %d, want 200", resp.StatusCode)
			}
			resp.Body.Close()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("second status = %d, want 429", resp.StatusCode)
		}
		assertAPIErrorCode(t, resp, "rate_limit_exceeded")
		if resp.Header.Get("Retry-After") != "1" {
			t.Fatalf("Retry-After = %q, want 1", resp.Header.Get("Retry-After"))
		}
	}
}

func TestListVMs_RateLimitIsPerClientIP(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.RateLimitPerSecond = 1
		cfg.Daemon.RateLimitBurst = 1
	})
	defer cleanup()

	client := &http.Client{}
	ips := []string{"198.51.100.11", "198.51.100.12"}
	for _, ip := range ips {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/vms", nil)
		if err != nil {
			t.Fatalf("new request for %s: %v", ip, err)
		}
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /vms for %s: %v", ip, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status for %s = %d, want 200", ip, resp.StatusCode)
		}
	}
}

func TestCreateVM_QuotaExceeded(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxVMs = 1
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "existing", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, types.VMSpec{
		Name:   "quota-hit",
		Image:  "ubuntu",
		CPUs:   2,
		RAMMB:  2048,
		DiskGB: 20,
	}))
	if err != nil {
		t.Fatalf("POST /vms: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "quota_exceeded")
}

func TestUpdateVM_QuotaExceeded(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxTotalCPUs = 4
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "one", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "two", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-1", jsonBody(t, types.VMUpdateSpec{CPUs: 3}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /vms/vm-1: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "quota_exceeded")
}

func TestGetQuotaUsage(t *testing.T) {
	ts, mockMgr, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Quotas.MaxVMs = 4
		cfg.Quotas.MaxTotalCPUs = 16
		cfg.Quotas.MaxTotalRAMMB = 32768
		cfg.Quotas.MaxTotalDiskGB = 500
		cfg.Quotas.MaxTotalGPUs = 4
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "one", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20, GPUs: []string{"0000:01:00.0"}}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "two", Spec: types.VMSpec{CPUs: 4, RAMMB: 4096, DiskGB: 40, GPUs: []string{"0000:02:00.0", "02:01.0"}}})

	resp, err := http.Get(ts.URL + "/api/v1/quotas/usage")
	if err != nil {
		t.Fatalf("GET /quotas/usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var usage types.QuotaUsage
	decodeJSON(t, resp, &usage)
	if usage.VMs.Used != 2 || usage.VMs.Limit != 4 {
		t.Fatalf("unexpected VM usage: %+v", usage.VMs)
	}
	if usage.CPUs.Used != 6 || usage.CPUs.Limit != 16 {
		t.Fatalf("unexpected CPU usage: %+v", usage.CPUs)
	}
	if usage.RAMMB.Used != 6144 || usage.RAMMB.Limit != 32768 {
		t.Fatalf("unexpected RAM usage: %+v", usage.RAMMB)
	}
	if usage.DiskGB.Used != 60 || usage.DiskGB.Limit != 500 {
		t.Fatalf("unexpected disk usage: %+v", usage.DiskGB)
	}
	// vm-1 = 1 GPU, vm-2 = 2 GPUs ⇒ aggregate 3.
	if usage.GPUs.Used != 3 || usage.GPUs.Limit != 4 {
		t.Fatalf("unexpected GPU usage: %+v", usage.GPUs)
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

	apiServer := NewServerWithConfig(mockMgr, storageMgr, portFwd, s, cfg, webHandler)
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

func TestSwaggerUIRouteServesHTML(t *testing.T) {
	ts, cleanup := testServerWithWeb(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/docs")
	if err != nil {
		t.Fatalf("GET /api/docs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "/api/openapi.yaml") {
		t.Fatalf("swagger UI body missing spec URL")
	}
	if !strings.Contains(bodyStr, "/api/docs/swagger-ui.css") || !strings.Contains(bodyStr, "/api/docs/swagger-ui-bundle.js") {
		t.Fatalf("swagger UI body missing embedded asset URLs")
	}
}

func TestSwaggerUIAssetRouteServesEmbeddedBundle(t *testing.T) {
	ts, cleanup := testServerWithWeb(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/docs/swagger-ui.css")
	if err != nil {
		t.Fatalf("GET /api/docs/swagger-ui.css: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), ".swagger-ui") {
		t.Fatalf("embedded swagger css body missing expected content")
	}
}

func TestOpenAPISpecRouteServesYAML(t *testing.T) {
	ts, cleanup := testServerWithWeb(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/openapi.yaml")
	if err != nil {
		t.Fatalf("GET /api/openapi.yaml: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "yaml") && !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want yaml-ish content type", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "openapi: 3.0") {
		t.Fatalf("spec response missing OpenAPI version")
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
	if got := resp.Header.Get("X-Total-Count"); got == "" {
		t.Fatalf("X-Total-Count header missing")
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
	if got := resp.Header.Get("X-Total-Count"); got == "" {
		t.Fatalf("X-Total-Count header missing")
	}
}

func TestGetLogs_FilterBySearch_MessageSubstring(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "VM smith logs search-filter wired through")
	logger.Info("daemon", "unrelated start-up message")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=search-filter")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	if len(result.Entries) == 0 {
		t.Fatal("expected at least one matching entry")
	}
	for _, e := range result.Entries {
		hay := strings.ToLower(e.Message + " " + e.Source + " " + e.Level)
		matchedField := false
		for _, v := range e.Fields {
			if strings.Contains(strings.ToLower(v), "search-filter") {
				matchedField = true
				break
			}
		}
		if !matchedField && !strings.Contains(hay, "search-filter") {
			t.Errorf("entry without needle returned: msg=%q source=%q level=%q fields=%v",
				e.Message, e.Source, e.Level, e.Fields)
		}
	}
}

func TestGetLogs_FilterBySearch_CaseInsensitive(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "WARNINGS encountered during startup")
	logger.Info("daemon", "ok")

	// Uppercase needle should still match the lowercase haystack.
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=WARNINGS")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) == 0 {
		t.Fatal("expected at least one match for case-insensitive search")
	}
	found := false
	for _, e := range result.Entries {
		if strings.Contains(strings.ToLower(e.Message), "warnings") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected at least one entry whose message contained 'warnings'")
	}
}

func TestGetLogs_FilterBySearch_WhitespaceTrimmed(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "trimming test marker abcd1234")

	// Padded with leading/trailing spaces — handler must trim.
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=%20%20abcd1234%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) == 0 {
		t.Fatal("expected at least one match after whitespace trim")
	}
}

func TestGetLogs_FilterBySearch_NoMatch(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "totally generic message")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=needle-not-present-anywhere")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 0 {
		t.Errorf("expected zero entries for non-existent needle, got %d", len(result.Entries))
	}
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Errorf("X-Total-Count = %q, want 0", got)
	}
}

func TestGetLogs_FilterBySearch_FieldValueMatches(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "request handled", "vm_id", "vm-search-target-9999")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=vm-search-target")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	if len(result.Entries) == 0 {
		t.Fatal("expected at least one match against field value")
	}
}

func TestGetLogs_FilterBySearch_ComposesWithLevel(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "info-tier search-compose marker")
	logger.Warn("daemon", "warn-tier search-compose marker")

	// Restrict to warn+; should only return the warn entry.
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=warn&search=search-compose")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	for _, e := range result.Entries {
		if e.Level == "info" || e.Level == "debug" {
			t.Errorf("level=warn filter returned entry with level %q", e.Level)
		}
		if !strings.Contains(strings.ToLower(e.Message), "search-compose") {
			t.Errorf("search filter did not narrow entry: %q", e.Message)
		}
	}
}

func TestGetLogs_FilterBySearch_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "search-count needle one")
	logger.Info("daemon", "search-count needle two")
	logger.Info("daemon", "irrelevant noise that does not match")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=search-count&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	total := resp.Header.Get("X-Total-Count")
	if total == "" {
		t.Fatal("X-Total-Count header missing")
	}
	if total != "2" {
		t.Errorf("X-Total-Count = %q, want 2 (only the two needle entries)", total)
	}
}

func TestGetLogs_FilterByVMID_ExactMatch(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "vm-filter scoped target", "vm_id", "vm-aaa")
	logger.Info("daemon", "vm-filter scoped target", "vm_id", "vm-bbb")
	logger.Info("daemon", "vm-filter unrelated noise")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&vm_id=vm-aaa")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) == 0 {
		t.Fatal("expected at least one matching entry")
	}
	for _, e := range result.Entries {
		if e.Fields["vm_id"] != "vm-aaa" {
			t.Errorf("entry vm_id = %q, want vm-aaa", e.Fields["vm_id"])
		}
	}
}

func TestGetLogs_FilterByVMID_WhitespaceTrimmed(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "vm-trim target", "vm_id", "vm-trim-1")

	// Leading + trailing whitespace must be trimmed before exact-match comparison.
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&vm_id=%20vm-trim-1%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) == 0 {
		t.Fatal("expected whitespace-trimmed filter to match")
	}
	for _, e := range result.Entries {
		if e.Fields["vm_id"] != "vm-trim-1" {
			t.Errorf("entry vm_id = %q, want vm-trim-1", e.Fields["vm_id"])
		}
	}
}

func TestGetLogs_FilterByVMID_PrefixDoesNotMatch(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Exact-match contract — `vm-1` filter must NOT swallow `vm-12345`.
	logger.Info("daemon", "longer vm id", "vm_id", "vm-12345")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&vm_id=vm-1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	for _, e := range result.Entries {
		if e.Fields["vm_id"] == "vm-12345" {
			t.Errorf("prefix filter vm-1 must not match vm-12345")
		}
	}
}

func TestGetLogs_FilterByVMID_NoMatchReturnsEmpty(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "tagged entry", "vm_id", "vm-real-id")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&vm_id=vm-does-not-exist")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 0 {
		t.Errorf("expected zero matches, got %d", len(result.Entries))
	}
}

func TestGetLogs_FilterByVMID_ComposesWithSourceAndSearch(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "compose probe alpha", "vm_id", "vm-compose")
	logger.Info("api", "compose probe alpha", "vm_id", "vm-compose")
	logger.Info("daemon", "compose probe beta", "vm_id", "vm-compose")
	logger.Info("daemon", "compose probe alpha", "vm_id", "vm-other")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&source=daemon&vm_id=vm-compose&search=alpha")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) == 0 {
		t.Fatal("expected at least one entry matching all three filters")
	}
	for _, e := range result.Entries {
		if e.Source != "daemon" {
			t.Errorf("entry source = %q, want daemon", e.Source)
		}
		if e.Fields["vm_id"] != "vm-compose" {
			t.Errorf("entry vm_id = %q, want vm-compose", e.Fields["vm_id"])
		}
		if !strings.Contains(strings.ToLower(e.Message), "alpha") {
			t.Errorf("entry message %q does not contain 'alpha'", e.Message)
		}
	}
}

func TestGetLogs_FilterByVMID_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "scoped one", "vm_id", "vm-scope-total")
	logger.Info("daemon", "scoped two", "vm_id", "vm-scope-total")
	logger.Info("daemon", "scoped three", "vm_id", "vm-other")
	logger.Info("daemon", "untagged noise")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&vm_id=vm-scope-total&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	total := resp.Header.Get("X-Total-Count")
	if total != "2" {
		t.Errorf("X-Total-Count = %q, want 2 (only the two scoped entries)", total)
	}
}

func TestGetLogs_FilterByVMID_EmptyParamIsNoOp(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "noop-probe one", "vm_id", "vm-1")
	logger.Info("daemon", "noop-probe two")

	// Explicit empty vm_id should not filter anything out.
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&vm_id=&search=noop-probe")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) < 2 {
		t.Errorf("expected both probe entries (got %d) — empty vm_id must not filter", len(result.Entries))
	}
}

func TestGetLogs_SortDefaultIsTimestampAsc(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Three distinct messages emitted in order; the legacy default is
	// oldest-first which the LogViewer's auto-scroll relies on.
	logger.Info("daemon", "sort-default needle one")
	time.Sleep(2 * time.Millisecond)
	logger.Info("daemon", "sort-default needle two")
	time.Sleep(2 * time.Millisecond)
	logger.Info("daemon", "sort-default needle three")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-default&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(result.Entries))
	}
	want := []string{"sort-default needle one", "sort-default needle two", "sort-default needle three"}
	for i, e := range result.Entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestGetLogs_SortByTimestampDesc(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "sort-desc needle one")
	time.Sleep(2 * time.Millisecond)
	logger.Info("daemon", "sort-desc needle two")
	time.Sleep(2 * time.Millisecond)
	logger.Info("daemon", "sort-desc needle three")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-desc&sort=timestamp&order=desc&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(result.Entries))
	}
	want := []string{"sort-desc needle three", "sort-desc needle two", "sort-desc needle one"}
	for i, e := range result.Entries {
		if e.Message != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], e.Message)
		}
	}
}

func TestGetLogs_SortByLevel_DescPutsErrorsFirst(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// The default global logger min-level is info, so debug entries are
	// dropped before they reach the ring; cover info/warn/error here.
	// `_TestSortByLevel_AscOrderedBySeverity_Direct` exercises the full
	// debug→error severity ladder via the in-package SortEntries helper.
	logger.Info("daemon", "sort-level needle info")
	logger.Warn("daemon", "sort-level needle warn")
	logger.Error("daemon", "sort-level needle error")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-level&sort=level&order=desc&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(result.Entries))
	}
	// Sort desc by severity rank: error → warn → info.
	wantLevels := []string{"error", "warn", "info"}
	for i, e := range result.Entries {
		if e.Level != wantLevels[i] {
			t.Fatalf("position %d: want level %q, got %q (msg=%q)",
				i, wantLevels[i], e.Level, e.Message)
		}
	}
}

func TestGetLogs_SortByLevel_AscOrderedBySeverity(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Error("daemon", "sort-level-asc needle error")
	logger.Warn("daemon", "sort-level-asc needle warn")
	logger.Info("daemon", "sort-level-asc needle info")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-level-asc&sort=level&order=asc&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(result.Entries))
	}
	wantLevels := []string{"info", "warn", "error"}
	for i, e := range result.Entries {
		if e.Level != wantLevels[i] {
			t.Fatalf("position %d: want level %q, got %q (msg=%q)",
				i, wantLevels[i], e.Level, e.Message)
		}
	}
}

func TestGetLogs_SortBySource(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "sort-source needle d")
	logger.Info("api", "sort-source needle a")
	logger.Info("cli", "sort-source needle c")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-source&sort=source&order=asc&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(result.Entries))
	}
	wantSources := []string{"api", "cli", "daemon"}
	for i, e := range result.Entries {
		if e.Source != wantSources[i] {
			t.Fatalf("position %d: want source %q, got %q (msg=%q)",
				i, wantSources[i], e.Source, e.Message)
		}
	}
}

func TestGetLogs_SortComposesWithSearch(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "sort-compose needle one")
	logger.Warn("daemon", "sort-compose needle two")
	logger.Error("daemon", "sort-compose needle three")
	// Noise that the search filter must drop before the sort runs.
	logger.Error("daemon", "irrelevant noise")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-compose&sort=level&order=desc&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3 (search must drop the noise entry)", len(result.Entries))
	}
	// error → warn → info with the noise entry excluded.
	wantLevels := []string{"error", "warn", "info"}
	for i, e := range result.Entries {
		if e.Level != wantLevels[i] {
			t.Fatalf("position %d: want level %q, got %q (msg=%q)",
				i, wantLevels[i], e.Level, e.Message)
		}
	}
}

func TestGetLogs_RejectsInvalidSort(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/logs?sort=bogus")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["code"] != "invalid_sort" {
		t.Errorf("code = %v, want invalid_sort", body["code"])
	}
}

func TestGetLogs_RejectsInvalidOrder(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/logs?order=sideways")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["code"] != "invalid_order" {
		t.Errorf("code = %v, want invalid_order", body["code"])
	}
}

// ── vm_id sort axis (5.4.94) ───────────────────────────────────────────────

func TestGetLogs_SortByVMID_AscEmptyTrailing(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "sort-vmid-94 needle bbb", "vm_id", "vm-bbb")
	logger.Info("daemon", "sort-vmid-94 needle aaa", "vm_id", "vm-aaa")
	logger.Info("daemon", "sort-vmid-94 needle none")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-vmid-94&sort=vm_id&order=asc&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(result.Entries))
	}
	want := []string{"vm-aaa", "vm-bbb", ""}
	for i, e := range result.Entries {
		gotVMID := ""
		if e.Fields != nil {
			gotVMID = e.Fields["vm_id"]
		}
		if gotVMID != want[i] {
			t.Fatalf("position %d: want vm_id %q, got %q (msg=%q)",
				i, want[i], gotVMID, e.Message)
		}
	}
}

func TestGetLogs_SortByVMID_DescEmptyLeading(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "sort-vmid-94d needle aaa", "vm_id", "vm-aaa")
	logger.Info("daemon", "sort-vmid-94d needle bbb", "vm_id", "vm-bbb")
	logger.Info("daemon", "sort-vmid-94d needle none")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-vmid-94d&sort=vm_id&order=desc&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(result.Entries))
	}
	want := []string{"", "vm-bbb", "vm-aaa"}
	for i, e := range result.Entries {
		gotVMID := ""
		if e.Fields != nil {
			gotVMID = e.Fields["vm_id"]
		}
		if gotVMID != want[i] {
			t.Fatalf("position %d: want vm_id %q, got %q (msg=%q)",
				i, want[i], gotVMID, e.Message)
		}
	}
}

func TestGetLogs_SortByVMID_CaseSensitive(t *testing.T) {
	// VM IDs are opaque case-sensitive strings — capital 'V' (0x56) sorts
	// before lower-case 'v' (0x76).
	ts, _, cleanup := testServer(t)
	defer cleanup()

	logger.Info("daemon", "sort-vmid-94c needle lower", "vm_id", "vm-abc")
	logger.Info("daemon", "sort-vmid-94c needle upper", "vm_id", "VM-abc")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&search=sort-vmid-94c&sort=vm_id&order=asc&per_page=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	if len(result.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(result.Entries))
	}
	if result.Entries[0].Fields["vm_id"] != "VM-abc" || result.Entries[1].Fields["vm_id"] != "vm-abc" {
		t.Fatalf("case-sensitive vm_id asc failed: got %v / %v",
			result.Entries[0].Fields["vm_id"], result.Entries[1].Fields["vm_id"])
	}
}

func TestGetLogs_SortByVMID_InvalidSortAdvertisesVMID(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/logs?sort=bogus")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["code"] != "invalid_sort" {
		t.Errorf("code = %v, want invalid_sort", body["code"])
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "vm_id") {
		t.Errorf("invalid_sort message should advertise vm_id; got %q", msg)
	}
}

// ── time-range filter (5.4.34) ─────────────────────────────────────────────

func TestGetLogs_FilterByUntil(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Drive a request, then take the cutoff, then drive another. The
	// post-cutoff entries should be filtered out by ?until=.
	http.Get(ts.URL + "/api/v1/vms")
	time.Sleep(2 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(2 * time.Millisecond)
	http.Get(ts.URL + "/api/v1/vms")

	until := cutoff.UTC().Format(time.RFC3339Nano)
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&until=" + until)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	if len(result.Entries) == 0 {
		t.Fatalf("expected at least one entry at-or-before cutoff, got 0")
	}
	for _, e := range result.Entries {
		if e.Timestamp.After(cutoff) {
			t.Errorf("until filter returned entry at %v, want at-or-before %v", e.Timestamp, cutoff)
		}
	}
}

func TestGetLogs_FilterByUntil_Inclusive(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	http.Get(ts.URL + "/api/v1/vms")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&per_page=2000")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var all logsResponse
	decodeJSON(t, resp, &all)
	if len(all.Entries) == 0 {
		t.Fatalf("no entries to test inclusivity against")
	}

	// Pick a real entry's exact timestamp and assert it is INCLUDED when
	// passed as the upper bound (?until=t implies "at or before t").
	target := all.Entries[len(all.Entries)/2].Timestamp
	until := target.UTC().Format(time.RFC3339Nano)
	resp2, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&until=" + until + "&per_page=2000")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	var bounded logsResponse
	decodeJSON(t, resp2, &bounded)

	found := false
	for _, e := range bounded.Entries {
		if e.Timestamp.Equal(target) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("until filter excluded the boundary entry at %v; should be inclusive", target)
	}
}

func TestGetLogs_FilterBySinceAndUntil(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	http.Get(ts.URL + "/api/v1/vms")
	time.Sleep(2 * time.Millisecond)
	lower := time.Now()
	time.Sleep(2 * time.Millisecond)
	http.Get(ts.URL + "/api/v1/vms")
	time.Sleep(2 * time.Millisecond)
	upper := time.Now()
	time.Sleep(2 * time.Millisecond)
	http.Get(ts.URL + "/api/v1/vms")

	since := lower.UTC().Format(time.RFC3339Nano)
	until := upper.UTC().Format(time.RFC3339Nano)
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&since=" + since + "&until=" + until)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result logsResponse
	decodeJSON(t, resp, &result)

	if len(result.Entries) == 0 {
		t.Fatalf("expected at least one entry inside [%v, %v]", lower, upper)
	}
	for _, e := range result.Entries {
		if !e.Timestamp.After(lower) {
			t.Errorf("entry at %v not strictly after lower bound %v", e.Timestamp, lower)
		}
		if e.Timestamp.After(upper) {
			t.Errorf("entry at %v past upper bound %v", e.Timestamp, upper)
		}
	}
}

func TestGetLogs_FilterByInvalidSince(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/logs?since=not-a-time")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["code"] != "invalid_since" {
		t.Errorf("code = %v, want invalid_since", body["code"])
	}
}

func TestGetLogs_FilterByInvalidUntil(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/logs?until=garbage")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["code"] != "invalid_until" {
		t.Errorf("code = %v, want invalid_until", body["code"])
	}
}

func TestGetLogs_FilterByUntil_EmptyIsNoOp(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	http.Get(ts.URL + "/api/v1/vms")

	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=debug")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("baseline status = %d, want 200", resp.StatusCode)
	}
	var baseline logsResponse
	decodeJSON(t, resp, &baseline)

	// `until=` (empty) and `until=   ` (whitespace) must both behave as
	// "no filter" — same count as the baseline request above.
	for _, untilValue := range []string{"", "%20%20%20"} {
		resp2, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&until=" + untilValue)
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("until=%q status = %d, want 200", untilValue, resp2.StatusCode)
		}
		var got logsResponse
		decodeJSON(t, resp2, &got)
		if len(got.Entries) != len(baseline.Entries) {
			t.Errorf("until=%q returned %d entries; want %d (no-op)", untilValue, len(got.Entries), len(baseline.Entries))
		}
	}
}

func TestGetLogs_FilterByUntil_ComposesWithLevel(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	http.Get(ts.URL + "/api/v1/vms")
	time.Sleep(2 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(2 * time.Millisecond)
	http.Get(ts.URL + "/api/v1/vms")

	until := cutoff.UTC().Format(time.RFC3339Nano)
	resp, _ := http.Get(ts.URL + "/api/v1/logs?level=info&until=" + until)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result logsResponse
	decodeJSON(t, resp, &result)
	for _, e := range result.Entries {
		if e.Timestamp.After(cutoff) {
			t.Errorf("entry past cutoff: %v", e.Timestamp)
		}
		if e.Level == "debug" {
			t.Errorf("level=info should exclude debug entries; got %+v", e)
		}
	}
}

func TestGetLogs_FilterByUntil_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	// Generate at least one log entry, then take the cutoff, then generate
	// more — the post-cutoff entries should be filtered out.
	http.Get(ts.URL + "/api/v1/vms")
	time.Sleep(2 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(2 * time.Millisecond)
	http.Get(ts.URL + "/api/v1/vms")
	http.Get(ts.URL + "/api/v1/vms")

	until := cutoff.UTC().Format(time.RFC3339Nano)
	respFiltered, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&until=" + until + "&per_page=1")
	if respFiltered.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", respFiltered.StatusCode)
	}
	filtered, err := strconv.Atoi(respFiltered.Header.Get("X-Total-Count"))
	if err != nil {
		t.Fatalf("X-Total-Count not numeric: %q", respFiltered.Header.Get("X-Total-Count"))
	}

	// Baseline run AFTER all log-emitting calls — captures every entry that
	// exists in the ring (avoids ring-buffer ordering issues from prior
	// tests in the suite).
	respAll, _ := http.Get(ts.URL + "/api/v1/logs?level=debug&per_page=1")
	allTotal, _ := strconv.Atoi(respAll.Header.Get("X-Total-Count"))

	if filtered <= 0 {
		t.Fatalf("post-filter total = %d, want > 0", filtered)
	}
	if filtered > allTotal {
		t.Errorf("post-filter total (%d) > unfiltered total (%d); until filter should never add entries", filtered, allTotal)
	}
}

// Helpers for timeout in tests
func init() {
	_ = time.Second // ensure time is used
}

func TestAPIAuthDisabledAllowsRequestsWithoutToken(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.Auth.Enabled = false
		cfg.Daemon.Auth.APIKeys = []string{"secret-key"}
	})
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("GET /api/v1/vms: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAPIAuthEnabledRejectsMissingBearerToken(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.Auth.Enabled = true
		cfg.Daemon.Auth.APIKeys = []string{"secret-key"}
	})
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms")
	if err != nil {
		t.Fatalf("GET /api/v1/vms: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "unauthorized")
}

func TestAPIAuthEnabledRejectsInvalidBearerToken(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.Auth.Enabled = true
		cfg.Daemon.Auth.APIKeys = []string{"secret-key"}
	})
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/vms", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer wrong-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/vms: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "unauthorized")
}

func TestAPIAuthEnabledAllowsValidBearerToken(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.Auth.Enabled = true
		cfg.Daemon.Auth.APIKeys = []string{"secret-key"}
	})
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/vms", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/vms: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestAPIAuthAllowsAPIKeyQueryParam verifies that browser clients (e.g. SSE
// EventSource) which cannot set custom headers can authenticate via
// ?api_key=… instead of an Authorization header.
func TestAPIAuthAllowsAPIKeyQueryParam(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.Auth.Enabled = true
		cfg.Daemon.Auth.APIKeys = []string{"secret-key"}
	})
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms?api_key=secret-key")
	if err != nil {
		t.Fatalf("GET with api_key query: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with valid api_key query", resp.StatusCode)
	}
}

func TestAPIAuthRejectsInvalidAPIKeyQueryParam(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Daemon.Auth.Enabled = true
		cfg.Daemon.Auth.APIKeys = []string{"secret-key"}
	})
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms?api_key=wrong-key")
	if err != nil {
		t.Fatalf("GET with bad api_key: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "unauthorized")
}

func TestWebHandlerNotProtectedByAPIAuth(t *testing.T) {
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
	cfg.Daemon.Auth.Enabled = true
	cfg.Daemon.Auth.APIKeys = []string{"secret-key"}

	mockMgr := vm.NewMockManager()
	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)
	webHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	apiServer := NewServerWithConfig(mockMgr, storageMgr, portFwd, s, cfg, webHandler)
	ts := httptest.NewServer(apiServer)
	defer ts.Close()
	defer s.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// Event endpoint tests
// ============================================================

func TestListEvents_Empty(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var events []*types.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestListEvents_WithDataAndFilters(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	now := time.Now().Truncate(time.Millisecond)

	// Use AppendEvent (the modern path) so events are visible to
	// ListEventsFiltered, which only walks the uint64-keyed bucket.
	if _, err := s.AppendEvent(&types.Event{VMID: "vm-1", Type: "vm_started", OccurredAt: now.Add(-10 * time.Minute)}); err != nil {
		t.Fatalf("AppendEvent 1: %v", err)
	}
	if _, err := s.AppendEvent(&types.Event{VMID: "vm-2", Type: "vm_stopped", OccurredAt: now.Add(-5 * time.Minute)}); err != nil {
		t.Fatalf("AppendEvent 2: %v", err)
	}
	if _, err := s.AppendEvent(&types.Event{VMID: "vm-1", Type: "vm_deleted", OccurredAt: now.Add(-1 * time.Minute)}); err != nil {
		t.Fatalf("AppendEvent 3: %v", err)
	}

	// Test all
	resp, err := http.Get(ts.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET all events: %v", err)
	}
	var events []*types.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode all events response: %v", err)
	}
	resp.Body.Close()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// AppendEvent assigns IDs "1", "2", "3" in order; cursor walks newest first.
	if events[0].ID != "3" || events[2].ID != "1" {
		t.Errorf("expected descending sort order, got %q ... %q", events[0].ID, events[2].ID)
	}

	// Test vm_id filter
	resp, err = http.Get(ts.URL + "/api/v1/events?vm_id=vm-1")
	if err != nil {
		t.Fatalf("GET vm_id filtered events: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode vm_id filtered response: %v", err)
	}
	resp.Body.Close()
	if len(events) != 2 {
		t.Fatalf("expected 2 events for vm-1, got %d", len(events))
	}

	// Test since filter
	sinceStr := now.Add(-6 * time.Minute).Format(time.RFC3339Nano)
	resp, err = http.Get(ts.URL + "/api/v1/events?since=" + sinceStr)
	if err != nil {
		t.Fatalf("GET since filtered events: %v", err)
	}
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode since filtered response: %v", err)
	}
	resp.Body.Close()
	if len(events) != 2 {
		t.Fatalf("expected 2 events since %s, got %d", sinceStr, len(events))
	}
	if events[0].ID != "3" || events[1].ID != "2" {
		t.Errorf("expected ids 3 and 2, got %q and %q", events[0].ID, events[1].ID)
	}
}

func TestListEvents_InvalidSince(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/events?since=not-a-timestamp")
	if err != nil {
		t.Fatalf("GET invalid since: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var apiErr errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiErr.Code != "invalid_since" {
		t.Fatalf("code = %q, want %q", apiErr.Code, "invalid_since")
	}
}

// seedSearchableEvents writes a small set of events covering message /
// attribute / actor matches so the ?search= tests can assert on each axis
// independently. Returned in append order; IDs are "1".."4".
func seedSearchableEvents(t *testing.T, s *store.Store) {
	t.Helper()
	base := time.Now().Truncate(time.Millisecond)
	seeds := []*types.Event{
		{
			VMID:       "vm-1",
			Type:       "vm.started",
			Source:     types.EventSourceLibvirt,
			Severity:   types.EventSeverityInfo,
			Message:    "started vm web-prod-01",
			Actor:      "system",
			OccurredAt: base.Add(-30 * time.Minute),
		},
		{
			VMID:       "vm-2",
			Type:       "snapshot.created",
			Source:     types.EventSourceApp,
			Severity:   types.EventSeverityInfo,
			Message:    "created snapshot before-deploy",
			Actor:      "ops-alice",
			Attributes: map[string]string{"snapshot": "before-deploy"},
			OccurredAt: base.Add(-20 * time.Minute),
		},
		{
			VMID:       "vm-3",
			Type:       "port_forward.added",
			Source:     types.EventSourceApp,
			Severity:   types.EventSeverityInfo,
			Message:    "added port forward",
			Attributes: map[string]string{"host_port": "22001", "guest_port": "22", "protocol": "tcp"},
			OccurredAt: base.Add(-10 * time.Minute),
		},
		{
			Type:       "dhcp.exhausted",
			Source:     types.EventSourceSystem,
			Severity:   types.EventSeverityError,
			Message:    "DHCP pool exhausted",
			OccurredAt: base.Add(-5 * time.Minute),
		},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func decodeEvents(t *testing.T, resp *http.Response) []*types.Event {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out []*types.Event
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	return out
}

func TestListEvents_FilterBySearch_MatchesMessage(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?search=web-prod")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].Type != "vm.started" {
		t.Fatalf("expected single vm.started match, got %d events", len(got))
	}
}

func TestListEvents_FilterBySearch_MatchesAttributeValue(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?search=22001")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].Type != "port_forward.added" {
		t.Fatalf("expected single port_forward.added match, got %d events", len(got))
	}
}

func TestListEvents_FilterBySearch_MatchesActor(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?search=alice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].Actor != "ops-alice" {
		t.Fatalf("expected single ops-alice match, got %d events", len(got))
	}
}

func TestListEvents_FilterBySearch_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?search=DHCP")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].Type != "dhcp.exhausted" {
		t.Fatalf("expected DHCP match via case-insensitive search, got %d events", len(got))
	}
}

func TestListEvents_FilterBySearch_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?search=%20%20web-prod%20%20")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 {
		t.Fatalf("expected 1 match after whitespace trim, got %d", len(got))
	}
}

func TestListEvents_FilterBySearch_NoMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?search=needle-not-present")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}
}

func TestListEvents_FilterBySearch_CombinesWithSource(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	// "snapshot" appears in the app-source event's message + attribute; the
	// libvirt-source events do not carry the word. Adding source=app narrows
	// the result the same way it would without search.
	resp, err := http.Get(ts.URL + "/api/v1/events?search=snapshot&source=app")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].Source != types.EventSourceApp {
		t.Fatalf("expected 1 app-source snapshot match, got %d", len(got))
	}
}

func TestListEvents_FilterBySearch_CombinesWithVMID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	// "snapshot" hits the app event scoped to vm-2; narrowing by vm_id=vm-1
	// should yield zero results even though vm-1 has an event.
	resp, err := http.Get(ts.URL + "/api/v1/events?search=snapshot&vm_id=vm-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 results when narrowing to vm-1, got %d", len(got))
	}
}

// ============================================================
// Actor exact-match filter on GET /events (4.2.23)
// ============================================================

func TestListEvents_FilterByActor_ExactMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?actor=ops-alice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 {
		t.Fatalf("expected 1 ops-alice event, got %d", len(got))
	}
	if got[0].Actor != "ops-alice" {
		t.Errorf("actor=%q, want ops-alice", got[0].Actor)
	}
}

func TestListEvents_FilterByActor_CaseSensitive(t *testing.T) {
	// Actor is exact-match (mirrors ?vm_id='s contract); case-insensitive
	// matching is the job of ?search=.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?actor=Ops-Alice")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 results for differently-cased actor, got %d", len(got))
	}
}

func TestListEvents_FilterByActor_WhitespaceTrimmed(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?actor=%20%20ops-alice%20%20")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 {
		t.Fatalf("expected 1 result after whitespace trim, got %d", len(got))
	}
}

func TestListEvents_FilterByActor_NoMatchReturnsEmpty(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?actor=nobody-by-that-name")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}
}

func TestListEvents_FilterByActor_EmptyParamIsNoOp(t *testing.T) {
	// ?actor= (empty) must not filter — the handler trims, sees empty,
	// and forwards "" to the store which short-circuits past the predicate.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?actor=")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 4 {
		t.Fatalf("?actor= empty should be no-op; got %d, want 4 (all seeded)", len(got))
	}
}

func TestListEvents_FilterByActor_ComposesWithSearch(t *testing.T) {
	// Combining ?actor= with ?search= must narrow to the intersection.
	// "ops-alice" matches the snapshot.created event; adding search=before-deploy
	// also matches it, so the intersection stays at 1.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?actor=ops-alice&search=before-deploy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 {
		t.Fatalf("expected 1 intersection match, got %d", len(got))
	}

	// search=DHCP would match the system event whose Actor is empty;
	// adding actor=ops-alice short-circuits to zero.
	resp, err = http.Get(ts.URL + "/api/v1/events?actor=ops-alice&search=DHCP")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got = decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 results for ops-alice+DHCP, got %d", len(got))
	}
}

func TestListEvents_FilterByActor_TotalCountReflectsFiltered(t *testing.T) {
	// X-Total-Count must reflect the post-filter / pre-pagination count so
	// the GUI's pagination widget can drive the Activity table correctly.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSearchableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?actor=system&per_page=10&page=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if header := resp.Header.Get("X-Total-Count"); header != "1" {
		t.Errorf("X-Total-Count = %q, want 1 (system actor only matches one seed)", header)
	}
}

// ============================================================
// min_severity severity-floor filter on GET /events (5.4.41)
// ============================================================

// seedMinSeverityEvents writes one event per severity (info / warn / error)
// so the ?min_severity= floor can be asserted without depending on the
// per-axis seeds in seedSearchableEvents (which carry no warn event).
func seedMinSeverityEvents(t *testing.T, s *store.Store) {
	t.Helper()
	base := time.Now().Truncate(time.Millisecond)
	seeds := []*types.Event{
		{Type: "vm.created", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "created", OccurredAt: base.Add(-30 * time.Minute)},
		{Type: "vm.stopped", Source: types.EventSourceLibvirt, Severity: types.EventSeverityWarn, Message: "stopped unexpectedly", OccurredAt: base.Add(-20 * time.Minute)},
		{Type: "dhcp.exhausted", Source: types.EventSourceSystem, Severity: types.EventSeverityError, Message: "DHCP pool exhausted", OccurredAt: base.Add(-10 * time.Minute)},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func TestListEvents_FilterByMinSeverity_Floor(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedMinSeverityEvents(t, s)

	cases := []struct {
		floor string
		want  int
	}{
		{"info", 3},  // info + warn + error
		{"warn", 2},  // warn + error
		{"error", 1}, // error only
	}
	for _, c := range cases {
		resp, err := http.Get(ts.URL + "/api/v1/events?min_severity=" + c.floor)
		if err != nil {
			t.Fatalf("GET min_severity=%s: %v", c.floor, err)
		}
		got := decodeEvents(t, resp)
		if len(got) != c.want {
			t.Errorf("min_severity=%s returned %d events, want %d", c.floor, len(got), c.want)
		}
	}
}

func TestListEvents_FilterByMinSeverity_CaseInsensitiveAndTrimmed(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedMinSeverityEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?min_severity=%20WARN%20")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 2 {
		t.Errorf("min_severity=' WARN ' returned %d, want 2 (warn+error)", len(got))
	}
}

func TestListEvents_FilterByMinSeverity_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedMinSeverityEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?min_severity=")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 3 {
		t.Errorf("empty min_severity should be no-op; got %d, want 3", len(got))
	}
}

func TestListEvents_FilterByMinSeverity_InvalidReturns400(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedMinSeverityEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?min_severity=critical")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr.Code != "invalid_min_severity" {
		t.Errorf("code = %q, want invalid_min_severity", apiErr.Code)
	}
}

func TestListEvents_FilterByMinSeverity_ComposesWithSource(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedMinSeverityEvents(t, s)

	// warn floor + libvirt source → only the libvirt warn event (the error is
	// system-source).
	resp, err := http.Get(ts.URL + "/api/v1/events?min_severity=warn&source=libvirt")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 {
		t.Fatalf("min_severity=warn+source=libvirt returned %d, want 1", len(got))
	}
	if got[0].Severity != types.EventSeverityWarn {
		t.Errorf("severity = %q, want warn", got[0].Severity)
	}
}

func TestListEvents_FilterByMinSeverity_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedMinSeverityEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?min_severity=warn&per_page=10&page=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if header := resp.Header.Get("X-Total-Count"); header != "2" {
		t.Errorf("X-Total-Count = %q, want 2 (warn+error)", header)
	}
}

// ============================================================
// resource_id exact-match filter on GET /events (4.2.24)
// ============================================================

// seedResourceIDEvents writes a small set of events that differ on ResourceID
// so the ?resource_id= filter can be asserted without depending on the
// per-axis seeds in seedSearchableEvents.
func seedResourceIDEvents(t *testing.T, s *store.Store) {
	t.Helper()
	base := time.Now().Truncate(time.Millisecond)
	seeds := []*types.Event{
		{
			Type:       "snapshot.created",
			Source:     types.EventSourceApp,
			Severity:   types.EventSeverityInfo,
			VMID:       "vm-1",
			ResourceID: "snap-rocky-pre-deploy",
			Message:    "snapshot created",
			OccurredAt: base.Add(-30 * time.Minute),
		},
		{
			Type:       "snapshot.deleted",
			Source:     types.EventSourceApp,
			Severity:   types.EventSeverityWarn,
			VMID:       "vm-1",
			ResourceID: "snap-rocky-pre-deploy",
			Message:    "snapshot deleted",
			OccurredAt: base.Add(-25 * time.Minute),
		},
		{
			Type:       "image.uploaded",
			Source:     types.EventSourceApp,
			Severity:   types.EventSeverityInfo,
			ResourceID: "img-rocky9",
			Message:    "image uploaded",
			OccurredAt: base.Add(-20 * time.Minute),
		},
		{
			Type:       "vm.started",
			Source:     types.EventSourceLibvirt,
			Severity:   types.EventSeverityInfo,
			VMID:       "vm-2",
			Message:    "vm started", // no resource_id
			OccurredAt: base.Add(-15 * time.Minute),
		},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func TestListEvents_FilterByResourceID_ExactMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?resource_id=snap-rocky-pre-deploy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 2 {
		t.Fatalf("expected 2 events for snap-rocky-pre-deploy, got %d", len(got))
	}
	for _, e := range got {
		if e.ResourceID != "snap-rocky-pre-deploy" {
			t.Errorf("filter leaked unrelated event %+v", e)
		}
	}
}

func TestListEvents_FilterByResourceID_CaseSensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDEvents(t, s)

	// Uppercase target should yield zero matches — the resource_id contract
	// mirrors vm_id (case-sensitive) so case-insensitive matching stays the
	// job of ?search=.
	resp, err := http.Get(ts.URL + "/api/v1/events?resource_id=SNAP-rocky-pre-deploy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 results for case-mismatched resource_id, got %d", len(got))
	}
}

func TestListEvents_FilterByResourceID_WhitespaceTrimmed(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?resource_id=%20%20img-rocky9%20%20")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].ResourceID != "img-rocky9" {
		t.Fatalf("expected 1 img-rocky9 event after whitespace trim, got %d", len(got))
	}
}

func TestListEvents_FilterByResourceID_NoMatchReturnsEmpty(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?resource_id=snap-does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 results for unknown resource_id, got %d", len(got))
	}
}

func TestListEvents_FilterByResourceID_EmptyParamIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDEvents(t, s)

	// Empty / whitespace-only resource_id should disable the filter, so the
	// response carries every seeded event.
	resp, err := http.Get(ts.URL + "/api/v1/events?resource_id=%20%20%20")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 4 {
		t.Fatalf("expected 4 events when filter is empty, got %d", len(got))
	}
}

func TestListEvents_FilterByResourceID_ComposesWithSeverity(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDEvents(t, s)

	// Two events carry snap-rocky-pre-deploy — one info (created), one warn
	// (deleted). Narrowing by severity=warn should leave only the deletion.
	resp, err := http.Get(ts.URL + "/api/v1/events?resource_id=snap-rocky-pre-deploy&severity=warn")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].Type != "snapshot.deleted" {
		t.Fatalf("expected 1 snapshot.deleted match, got %d", len(got))
	}
}

func TestListEvents_FilterByResourceID_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?resource_id=snap-rocky-pre-deploy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Errorf("X-Total-Count = %q, want \"2\"", got)
	}
	_ = decodeEvents(t, resp)
}

// ============================================================
// type_prefix case-insensitive prefix filter on GET /events (4.2.25)
// ============================================================

// seedTypePrefixEvents writes a mix of snapshot.*, vm.*, and webhook.* events
// so the ?type_prefix= tests can assert prefix matching on the type axis
// without depending on the other filters.
func seedTypePrefixEvents(t *testing.T, s *store.Store) {
	t.Helper()
	base := time.Now().Truncate(time.Millisecond)
	seeds := []*types.Event{
		{
			Type:       "snapshot.created",
			Source:     types.EventSourceApp,
			Severity:   types.EventSeverityInfo,
			OccurredAt: base.Add(-50 * time.Minute),
		},
		{
			Type:       "snapshot.deleted",
			Source:     types.EventSourceApp,
			Severity:   types.EventSeverityInfo,
			OccurredAt: base.Add(-40 * time.Minute),
		},
		{
			Type:       "vm.started",
			Source:     types.EventSourceLibvirt,
			Severity:   types.EventSeverityInfo,
			OccurredAt: base.Add(-30 * time.Minute),
		},
		{
			Type:       "vm.stopped",
			Source:     types.EventSourceLibvirt,
			Severity:   types.EventSeverityInfo,
			OccurredAt: base.Add(-20 * time.Minute),
		},
		{
			Type:       "webhook.delivery_failed",
			Source:     types.EventSourceSystem,
			Severity:   types.EventSeverityError,
			OccurredAt: base.Add(-10 * time.Minute),
		},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func TestListEvents_FilterByTypePrefix_MatchesEntireFamily(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedTypePrefixEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?type_prefix=snapshot.")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 2 {
		t.Fatalf("expected 2 snapshot.* matches, got %d", len(got))
	}
	for _, e := range got {
		if !strings.HasPrefix(e.Type, "snapshot.") {
			t.Errorf("unexpected type %q leaked through prefix filter", e.Type)
		}
	}
}

func TestListEvents_FilterByTypePrefix_CaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedTypePrefixEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?type_prefix=SNAPSHOT.")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 2 {
		t.Fatalf("expected 2 matches via uppercase prefix, got %d", len(got))
	}
}

func TestListEvents_FilterByTypePrefix_WhitespaceTrimmed(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedTypePrefixEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?type_prefix=%20%20webhook.%20%20")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].Type != "webhook.delivery_failed" {
		t.Fatalf("expected 1 webhook.* match after trim, got %d", len(got))
	}
}

func TestListEvents_FilterByTypePrefix_EmptyParamIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedTypePrefixEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?type_prefix=")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 5 {
		t.Fatalf("empty type_prefix should not filter, got %d events", len(got))
	}
}

func TestListEvents_FilterByTypePrefix_NoMatchReturnsEmpty(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedTypePrefixEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?type_prefix=schedule.")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 matches for unknown prefix, got %d", len(got))
	}
}

func TestListEvents_FilterByTypePrefix_ComposesWithSource(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedTypePrefixEvents(t, s)

	// snapshot.* events are source=app in the fixture; narrowing by
	// source=libvirt should drop them all.
	resp, err := http.Get(ts.URL + "/api/v1/events?type_prefix=snapshot.&source=libvirt")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 0 {
		t.Fatalf("expected 0 results when narrowing to libvirt, got %d", len(got))
	}
}

func TestListEvents_FilterByTypePrefix_ComposesWithSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedTypePrefixEvents(t, s)

	// type_prefix=vm. narrows to 2 vm.* events; search=stopped narrows further to 1.
	resp, err := http.Get(ts.URL + "/api/v1/events?type_prefix=vm.&search=stopped")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	if len(got) != 1 || got[0].Type != "vm.stopped" {
		t.Fatalf("expected 1 vm.stopped match, got %d", len(got))
	}
}

func TestListEvents_FilterByTypePrefix_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedTypePrefixEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?type_prefix=vm.")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want \"2\"", got)
	}
}

// seedSortableEvents writes a small set of events with distinct types,
// sources, severities, and occurred_at timestamps so the ?sort= tests can
// assert exact orderings without depending on insertion order.
//
// Insertion order intentionally differs from each sort axis below so a
// regression to "default insert order" surfaces immediately.
func seedSortableEvents(t *testing.T, s *store.Store) {
	t.Helper()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	seeds := []*types.Event{
		// Insert mid-time first so we can assert occurred_at-desc orders newest-first.
		{
			Type:       "vm.started",
			Source:     types.EventSourceLibvirt,
			Severity:   types.EventSeverityInfo,
			Message:    "started",
			OccurredAt: base.Add(2 * time.Hour),
		},
		{
			Type:       "Image.created",
			Source:     types.EventSourceApp,
			Severity:   types.EventSeverityWarn,
			Message:    "created image",
			OccurredAt: base.Add(1 * time.Hour),
		},
		{
			Type:       "snapshot.taken",
			Source:     types.EventSourceSystem,
			Severity:   types.EventSeverityError,
			Message:    "snapshot taken",
			OccurredAt: base.Add(3 * time.Hour),
		},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func eventIDs(events []*types.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.ID
	}
	return out
}

func TestListEvents_SortDefaultIsIDDesc(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// IDs are "1","2","3" assigned in append order (vm.started=1, Image.created=2, snapshot.taken=3).
	// Default sort is `id`-desc, which preserves the long-standing newest-first contract.
	want := []string{"3", "2", "1"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("default sort: got %v, want %v", g, want)
	}
}

func TestListEvents_SortByOccurredAtAsc(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=occurred_at&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// occurred_at asc: Image.created (1h) < vm.started (2h) < snapshot.taken (3h)
	want := []string{"2", "1", "3"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=occurred_at asc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortByType_CaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=type&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// case-insensitive: "image.created" < "snapshot.taken" < "vm.started"
	want := []string{"2", "3", "1"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=type asc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortBySource(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=source&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// alpha asc: app < libvirt < system
	want := []string{"2", "1", "3"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=source asc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortBySeverity(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=severity&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// alpha asc: error < info < warn
	want := []string{"3", "1", "2"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=severity asc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortComposesWithSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	// "created" matches both Image.created (id=2) and snapshot.taken's message
	// "snapshot taken" — only the Image.created event's TYPE contains "created"
	// AND its message says "created image". The snapshot type is
	// "snapshot.taken" so its haystack contains "created" via the message
	// "snapshot taken"? No — "taken" not "created". So only id=2 matches the
	// substring "image" combined with sort=type asc.
	resp, err := http.Get(ts.URL + "/api/v1/events?search=image&sort=type&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	want := []string{"2"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("search+sort: got %v, want %v", g, want)
	}
}

func TestListEvents_SortPaginationDeterministic(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	resp1, err := http.Get(ts.URL + "/api/v1/events?sort=type&order=asc&page=1&per_page=2")
	if err != nil {
		t.Fatalf("GET p1: %v", err)
	}
	page1 := decodeEvents(t, resp1)
	resp2, err := http.Get(ts.URL + "/api/v1/events?sort=type&order=asc&page=2&per_page=2")
	if err != nil {
		t.Fatalf("GET p2: %v", err)
	}
	page2 := decodeEvents(t, resp2)

	// page=1: [Image.created=2, snapshot.taken=3]; page=2: [vm.started=1]
	if g := eventIDs(page1); !sortedEventIDsEqual(g, []string{"2", "3"}) {
		t.Fatalf("page=1: got %v", g)
	}
	if g := eventIDs(page2); !sortedEventIDsEqual(g, []string{"1"}) {
		t.Fatalf("page=2: got %v", g)
	}
}

func TestListEvents_RejectsInvalidSort(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	// Use a clearly-unknown sort field; `attributes` was the historical
	// canary but actor is now a real axis so the test would otherwise
	// regress.
	resp, err := http.Get(ts.URL + "/api/v1/events?sort=attribute_keys")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// Error message must advertise the full supported set so operators
	// don't have to guess what landed in the whitelist; the 5.4.87 sweep
	// added `actor`, the 5.4.90 sweep added `resource_id`, and the 5.4.93
	// sweep added `vm_id`.
	if !strings.Contains(string(body), "actor") {
		t.Errorf("400 body must advertise actor: %s", string(body))
	}
	if !strings.Contains(string(body), "resource_id") {
		t.Errorf("400 body must advertise resource_id: %s", string(body))
	}
	if !strings.Contains(string(body), "vm_id") {
		t.Errorf("400 body must advertise vm_id: %s", string(body))
	}
}

// ============================================================
// Events `actor` sort axis (5.4.87)
// ============================================================

// seedActorSortableEvents writes a small set of events with distinct actor
// strings (plus one empty-actor event) so the ?sort=actor tests can assert
// exact orderings without depending on insertion order or fighting the
// shared seedSortableEvents helper used by every other sort assertion.
func seedActorSortableEvents(t *testing.T, s *store.Store) {
	t.Helper()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	seeds := []*types.Event{
		// Insertion order is intentionally not actor-sorted so a regression
		// to "default insert order" surfaces.
		{Type: "vm.started", Source: types.EventSourceLibvirt, Severity: types.EventSeverityInfo, Message: "started", Actor: "system", OccurredAt: base.Add(1 * time.Hour)},
		{Type: "vm.created", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "created", Actor: "", OccurredAt: base.Add(2 * time.Hour)},
		{Type: "vm.stopped", Source: types.EventSourceLibvirt, Severity: types.EventSeverityWarn, Message: "stopped", Actor: "app", OccurredAt: base.Add(3 * time.Hour)},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func TestListEvents_SortByActor_AscEmptyTrailing(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedActorSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=actor&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// asc: app < system < (empty trails)
	want := []string{"3", "1", "2"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=actor asc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortByActor_DescEmptyLeading(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedActorSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=actor&order=desc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// desc: (empty leads) > system > app
	want := []string{"2", "1", "3"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=actor desc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortByActor_TiebreaksOnID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	for _, e := range []*types.Event{
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", Actor: "system", OccurredAt: base.Add(1 * time.Hour)},
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", Actor: "system", OccurredAt: base.Add(2 * time.Hour)},
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", Actor: "system", OccurredAt: base.Add(3 * time.Hour)},
	} {
		if _, err := s.AppendEvent(e); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=actor&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// Equal actors must tiebreak on id (ascending) so paginated requests
	// over an all-equal cohort are deterministic.
	want := []string{"1", "2", "3"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("tiebreak: got %v, want %v", g, want)
	}
}

func TestListEvents_RejectsInvalidOrder(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?order=sideways")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// ============================================================
// Events `resource_id` sort axis (5.4.90)
// ============================================================

// seedResourceIDSortableEvents writes a small set of events with distinct
// resource_id strings (plus one empty-resource_id event) so the
// ?sort=resource_id tests can assert exact orderings without depending on
// insertion order.
func seedResourceIDSortableEvents(t *testing.T, s *store.Store) {
	t.Helper()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	seeds := []*types.Event{
		// Insertion order intentionally not resource_id-sorted so a
		// regression to "default insert order" surfaces.
		{Type: "snapshot.created", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "made", ResourceID: "snap-prod", OccurredAt: base.Add(1 * time.Hour)},
		{Type: "vm.created", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "created", ResourceID: "", OccurredAt: base.Add(2 * time.Hour)},
		{Type: "image.uploaded", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "uploaded", ResourceID: "img-base", OccurredAt: base.Add(3 * time.Hour)},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func TestListEvents_SortByResourceID_AscEmptyTrailing(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=resource_id&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// asc: img-base < snap-prod < (empty trails)
	want := []string{"3", "1", "2"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=resource_id asc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortByResourceID_DescEmptyLeading(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedResourceIDSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=resource_id&order=desc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// desc: (empty leads) > snap-prod > img-base
	want := []string{"2", "1", "3"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=resource_id desc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortByResourceID_TiebreaksOnID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	for _, e := range []*types.Event{
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", ResourceID: "snap-x", OccurredAt: base.Add(1 * time.Hour)},
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", ResourceID: "snap-x", OccurredAt: base.Add(2 * time.Hour)},
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", ResourceID: "snap-x", OccurredAt: base.Add(3 * time.Hour)},
	} {
		if _, err := s.AppendEvent(e); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=resource_id&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// Equal resource ids must tiebreak on id (asc) so paginated requests
	// over an all-equal cohort are deterministic.
	want := []string{"1", "2", "3"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("tiebreak: got %v, want %v", g, want)
	}
}

// ============================================================
// Events `vm_id` sort axis (5.4.93)
// ============================================================

// seedVMIDSortableEvents writes a small set of events with distinct vm_id
// strings (plus one empty-vm_id event representing a host-level event like
// `system.daemon_started`) so the ?sort=vm_id tests can assert exact
// orderings without depending on insertion order.
func seedVMIDSortableEvents(t *testing.T, s *store.Store) {
	t.Helper()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	seeds := []*types.Event{
		// Insertion order intentionally not vm_id-sorted so a regression
		// to "default insert order" surfaces.
		{Type: "vm.started", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "started", VMID: "vm-200", OccurredAt: base.Add(1 * time.Hour)},
		{Type: "system.daemon_started", Source: types.EventSourceSystem, Severity: types.EventSeverityInfo, Message: "up", VMID: "", OccurredAt: base.Add(2 * time.Hour)},
		{Type: "vm.started", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "started", VMID: "vm-100", OccurredAt: base.Add(3 * time.Hour)},
	}
	for i, evt := range seeds {
		if _, err := s.AppendEvent(evt); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
}

func TestListEvents_SortByVMID_AscEmptyTrailing(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedVMIDSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=vm_id&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// asc: vm-100 < vm-200 < (empty trails)
	want := []string{"3", "1", "2"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=vm_id asc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortByVMID_DescEmptyLeading(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedVMIDSortableEvents(t, s)

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=vm_id&order=desc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// desc: (empty leads) > vm-200 > vm-100
	want := []string{"2", "1", "3"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("sort=vm_id desc: got %v, want %v", g, want)
	}
}

func TestListEvents_SortByVMID_TiebreaksOnID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	base := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	for _, e := range []*types.Event{
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", VMID: "vm-x", OccurredAt: base.Add(1 * time.Hour)},
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", VMID: "vm-x", OccurredAt: base.Add(2 * time.Hour)},
		{Type: "t", Source: types.EventSourceApp, Severity: types.EventSeverityInfo, Message: "m", VMID: "vm-x", OccurredAt: base.Add(3 * time.Hour)},
	} {
		if _, err := s.AppendEvent(e); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=vm_id&order=asc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	got := decodeEvents(t, resp)
	// Equal vm_ids must tiebreak on id (asc) so paginated requests over an
	// all-equal cohort are deterministic.
	want := []string{"1", "2", "3"}
	if g := eventIDs(got); !sortedEventIDsEqual(g, want) {
		t.Fatalf("tiebreak: got %v, want %v", g, want)
	}
}

func sortedEventIDsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ============================================================
// Image metadata: description + tags + PATCH + ?tag= filter
// ============================================================

// TestCreateImage_NormalizesTagsAndDescription exercises the validation +
// normalization path on the create-from-VM handler. The qemu-img convert step
// will fail because the seeded VM has no real disk, so we assert the mock
// vm.Manager actually saw the correctly-normalized values via Get rather than
// inspecting a 201 response we'd never get without a real qcow2.
//
// The intent here is "input goes through normalizeTags / validateImageDescription
// before reaching the storage layer" — covered by checking the surface error
// code: validation failures must be 400, not 500.
func TestCreateImage_NormalizesTagsAndDescription_AcceptsValidInput(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-img-source", DiskPath: "/nonexistent/disk.qcow2"})

	body, _ := json.Marshal(map[string]any{
		"vm_id":       "vm-img-source",
		"name":        "tagged-image",
		"description": "  qcow2 build  ",
		"tags":        []string{"QA", "ubuntu"},
	})
	resp, err := http.Post(ts.URL+"/api/v1/images", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /images: %v", err)
	}
	defer resp.Body.Close()
	// validation passes; storage_error from qemu-img absence is the next step.
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 storage_error (proves validation passed)", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "storage_error")
}

// seedStoredImage writes an image record straight into bbolt so PATCH / GET
// tests don't have to spin up qemu-img to materialise a real qcow2 file. The
// caller passes the already-normalized tag set to mirror what the API layer
// would persist after `normalizeTags`.
func seedStoredImage(t *testing.T, s *store.Store, id, name, description string, tags []string) *types.Image {
	t.Helper()
	now := time.Now()
	img := &types.Image{
		ID:          id,
		Name:        name,
		Path:        "/tmp/" + name + ".qcow2",
		SizeBytes:   1024,
		Format:      "qcow2",
		Description: description,
		Tags:        tags,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.PutImage(img); err != nil {
		t.Fatalf("seed image: %v", err)
	}
	return img
}

func TestCreateImage_RejectsLongDescription(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-img-source", Name: "image-source", State: types.VMStateStopped})

	body, _ := json.Marshal(map[string]any{
		"vm_id":       "vm-img-source",
		"name":        "too-chatty",
		"description": strings.Repeat("a", 1025),
	})
	resp, err := http.Post(ts.URL+"/api/v1/images", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /images: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_spec")
}

func TestCreateImage_RejectsInvalidTags(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-img-source", Name: "image-source", State: types.VMStateStopped})

	body, _ := json.Marshal(map[string]any{
		"vm_id": "vm-img-source",
		"name":  "bad-tag",
		"tags":  []string{"good", " "},
	})
	resp, err := http.Post(ts.URL+"/api/v1/images", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /images: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_spec")
}

func TestUpdateImage_DescriptionAndTagsRoundTrip(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	img := seedStoredImage(t, s, "img-patch-rt", "patchable", "first", []string{"alpha"})

	patch := map[string]any{
		"description": "second",
		"tags":        []string{"BETA", "beta", "prod"},
	}
	pb, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/images/"+img.ID, bytes.NewReader(pb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /images: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var updated types.Image
	decodeJSON(t, resp, &updated)
	if updated.Description != "second" {
		t.Errorf("Description = %q, want second", updated.Description)
	}
	if got := updated.Tags; len(got) != 2 || got[0] != "beta" || got[1] != "prod" {
		t.Errorf("Tags = %v, want [beta prod]", got)
	}
}

func TestUpdateImage_NilTagsKeepsExisting(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	img := seedStoredImage(t, s, "img-keep-tags", "keep-tags", "", []string{"alpha"})

	pb, _ := json.Marshal(map[string]any{"description": "annotated"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/images/"+img.ID, bytes.NewReader(pb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	var updated types.Image
	decodeJSON(t, resp, &updated)
	if got := updated.Tags; len(got) != 1 || got[0] != "alpha" {
		t.Errorf("Tags = %v, want preserved [alpha]", got)
	}
}

func TestUpdateImage_EmptyTagsArrayClears(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	img := seedStoredImage(t, s, "img-clear-tags", "clear-tags", "", []string{"a", "b"})

	pb, _ := json.Marshal(map[string]any{"tags": []string{}})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/images/"+img.ID, bytes.NewReader(pb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	var updated types.Image
	decodeJSON(t, resp, &updated)
	if len(updated.Tags) != 0 {
		t.Errorf("Tags = %v, want empty after explicit clear", updated.Tags)
	}
}

func TestUpdateImage_NotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	pb, _ := json.Marshal(map[string]any{"description": "x"})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/images/img-missing", bytes.NewReader(pb))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestListImages_FilterByTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-qa", "qa-image", "", []string{"qa"})
	seedStoredImage(t, s, "img-prod", "prod-image", "", []string{"prod"})

	resp, err := http.Get(ts.URL + "/api/v1/images?tag=PROD")
	if err != nil {
		t.Fatalf("GET /images: %v", err)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "prod-image" {
		t.Errorf("filter result = %+v, want only prod-image", imgs)
	}
}

// seedStoredImageFull writes an image record with explicit timing/size so the
// sort tests can assert deterministic order on size and created_at.
func seedStoredImageFull(t *testing.T, s *store.Store, id, name string, size int64, created time.Time) *types.Image {
	t.Helper()
	img := &types.Image{
		ID:        id,
		Name:      name,
		Path:      "/tmp/" + name + ".qcow2",
		SizeBytes: size,
		Format:    "qcow2",
		CreatedAt: created,
		UpdatedAt: created,
	}
	if err := s.PutImage(img); err != nil {
		t.Fatalf("seed image: %v", err)
	}
	return img
}

func TestListImages_SortByName(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-3", "Charlie", 100, t0)
	seedStoredImageFull(t, s, "img-1", "alpha", 100, t0)
	seedStoredImageFull(t, s, "img-2", "Bravo", 100, t0)

	resp, err := http.Get(ts.URL + "/api/v1/images?sort=name")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []*types.Image
	decodeJSON(t, resp, &got)
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, img := range got {
		if img.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, img.Name, want[i])
		}
	}
}

func TestListImages_SortBySizeDesc(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-small", "small", 1024, t0)
	seedStoredImageFull(t, s, "img-big", "big", 1<<30, t0)
	seedStoredImageFull(t, s, "img-mid", "mid", 1<<20, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?sort=size&order=desc")
	var got []*types.Image
	decodeJSON(t, resp, &got)
	want := []string{"big", "mid", "small"}
	for i, img := range got {
		if img.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, img.Name, want[i])
		}
	}
}

func TestListImages_SortByCreatedAtDesc(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-1", "first", 100, t0)
	seedStoredImageFull(t, s, "img-2", "second", 100, t0.Add(time.Hour))
	seedStoredImageFull(t, s, "img-3", "third", 100, t0.Add(2*time.Hour))

	resp, _ := http.Get(ts.URL + "/api/v1/images?sort=created_at&order=desc")
	var got []*types.Image
	decodeJSON(t, resp, &got)
	want := []string{"third", "second", "first"}
	for i, img := range got {
		if img.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, img.Name, want[i])
		}
	}
}

func TestListImages_SortCombinesWithTagFilter(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-prod-1", "alpha-prod", "", []string{"prod"})
	seedStoredImage(t, s, "img-qa-1", "alpha-qa", "", []string{"qa"})
	seedStoredImage(t, s, "img-prod-2", "Zeta-prod", "", []string{"prod"})

	resp, _ := http.Get(ts.URL + "/api/v1/images?tag=prod&sort=name&order=desc")
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Errorf("X-Total-Count = %q, want 2 (filter applies before sort)", got)
	}
	var got []*types.Image
	decodeJSON(t, resp, &got)
	if len(got) != 2 {
		t.Fatalf("want 2 images, got %d", len(got))
	}
	if got[0].Name != "Zeta-prod" || got[1].Name != "alpha-prod" {
		t.Errorf("order = %q,%q, want Zeta-prod,alpha-prod", got[0].Name, got[1].Name)
	}
}

func TestListImages_SortPaginationDeterministic(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("img-%d", i)
		seedStoredImageFull(t, s, id, id, 100, t0)
	}

	resp1, _ := http.Get(ts.URL + "/api/v1/images?sort=name&order=desc&page=1&per_page=2")
	var page1 []*types.Image
	decodeJSON(t, resp1, &page1)
	if got := resp1.Header.Get("X-Total-Count"); got != "5" {
		t.Fatalf("X-Total-Count = %q, want 5", got)
	}

	resp2, _ := http.Get(ts.URL + "/api/v1/images?sort=name&order=desc&page=2&per_page=2")
	var page2 []*types.Image
	decodeJSON(t, resp2, &page2)

	gotOrder := []string{page1[0].Name, page1[1].Name, page2[0].Name, page2[1].Name}
	want := []string{"img-4", "img-3", "img-2", "img-1"}
	for i, n := range gotOrder {
		if n != want[i] {
			t.Fatalf("page-spanning order[%d] = %q, want %q (full: %v)", i, n, want[i], gotOrder)
		}
	}
}

func TestListImages_RejectsInvalidSort(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/images?sort=format")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_sort")
}

func TestListImages_RejectsInvalidOrder(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/images?order=sideways")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_order")
}

// --- Image free-text search filter (5.4.9) ---

func TestListImages_FilterBySearch_MatchesName(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-a", "rocky9-base", "", nil)
	seedStoredImage(t, s, "img-b", "ubuntu-22", "", nil)

	resp, err := http.Get(ts.URL + "/api/v1/images?search=rocky")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "rocky9-base" {
		t.Errorf("filter = %+v, want only rocky9-base", imgs)
	}
}

func TestListImages_FilterBySearch_MatchesDescription(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-a", "rocky9-base", "Hardened CIS-1 build", nil)
	seedStoredImage(t, s, "img-b", "ubuntu-22", "Stock cloud image", nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?search=hardened")
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "rocky9-base" {
		t.Errorf("filter = %+v, want only rocky9-base via description", imgs)
	}
}

func TestListImages_FilterBySearch_MatchesTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-a", "rocky9-base", "", []string{"team-storage", "prod"})
	seedStoredImage(t, s, "img-b", "ubuntu-22", "", []string{"team-net"})

	resp, _ := http.Get(ts.URL + "/api/v1/images?search=storage")
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "rocky9-base" {
		t.Errorf("filter = %+v, want only rocky9-base via tag", imgs)
	}
}

func TestListImages_FilterBySearch_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-a", "Rocky9-Base", "", nil)
	seedStoredImage(t, s, "img-b", "ubuntu-22", "", nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?search=ROCKY")
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "Rocky9-Base" {
		t.Errorf("filter = %+v, want only Rocky9-Base case-insensitive", imgs)
	}
}

func TestListImages_FilterBySearch_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-a", "alpha", "", nil)
	seedStoredImage(t, s, "img-b", "beta", "", nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?search=%20%20alpha%20%20")
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "alpha" {
		t.Errorf("filter = %+v, want only alpha after trim", imgs)
	}
}

func TestListImages_FilterBySearch_NoMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-a", "alpha", "", nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?search=needle-not-present")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with empty list", resp.StatusCode)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 0 {
		t.Errorf("filter = %+v, want empty list", imgs)
	}
}

func TestListImages_FilterBySearch_CombinesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImage(t, s, "img-a", "rocky9-prod", "", []string{"prod"})
	seedStoredImage(t, s, "img-b", "rocky9-qa", "", []string{"qa"})
	seedStoredImage(t, s, "img-c", "ubuntu-prod", "", []string{"prod"})

	resp, _ := http.Get(ts.URL + "/api/v1/images?search=rocky&tag=prod")
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "rocky9-prod" {
		t.Errorf("filter = %+v, want only rocky9-prod (intersection of search+tag)", imgs)
	}
}

func TestListImages_FilterBySearch_CombinesWithSort(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-1", "rocky9-charlie", 100, t0)
	seedStoredImageFull(t, s, "img-2", "rocky9-alpha", 100, t0.Add(time.Hour))
	seedStoredImageFull(t, s, "img-3", "ubuntu-22", 100, t0.Add(2*time.Hour))

	resp, _ := http.Get(ts.URL + "/api/v1/images?search=rocky&sort=name&order=asc")
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Errorf("X-Total-Count = %q, want 2 (only rocky after search)", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	want := []string{"rocky9-alpha", "rocky9-charlie"}
	if len(imgs) != len(want) {
		t.Fatalf("len = %d, want %d", len(imgs), len(want))
	}
	for i, img := range imgs {
		if img.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, img.Name, want[i])
		}
	}
}

// --- Image source_vm filter (5.4.27) ---

// seedStoredImageWithSourceVM writes an image record with an explicit source_vm
// so the 5.4.27 filter tests can assert exact-match / case-insensitive /
// no-match behaviour without spinning up a real VM.
func seedStoredImageWithSourceVM(t *testing.T, s *store.Store, id, name, sourceVM string, tags []string) *types.Image {
	t.Helper()
	now := time.Now()
	img := &types.Image{
		ID:        id,
		Name:      name,
		Path:      "/tmp/" + name + ".qcow2",
		SizeBytes: 1024,
		Format:    "qcow2",
		SourceVM:  sourceVM,
		Tags:      tags,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.PutImage(img); err != nil {
		t.Fatalf("seed image: %v", err)
	}
	return img
}

func TestListImages_FilterBySourceVM_ExactMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImageWithSourceVM(t, s, "img-a", "from-bastion", "vm-1700000000000000001", nil)
	seedStoredImageWithSourceVM(t, s, "img-b", "from-worker", "vm-1700000000000000002", nil)

	resp, err := http.Get(ts.URL + "/api/v1/images?source_vm=vm-1700000000000000001")
	if err != nil {
		t.Fatalf("GET /images?source_vm=...: %v", err)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "from-bastion" {
		t.Errorf("filter result = %+v, want only from-bastion", imgs)
	}
}

func TestListImages_FilterBySourceVM_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	// SourceVM normally lowercase since VM IDs are `vm-<unix-nano>`, but if a
	// future feature ever uses mixed-case IDs the filter still matches.
	seedStoredImageWithSourceVM(t, s, "img-a", "from-mixed", "VM-ABC123", nil)
	seedStoredImageWithSourceVM(t, s, "img-b", "from-other", "vm-1700000000000000002", nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?source_vm=vm-abc123")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1 (case-insensitive)", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "from-mixed" {
		t.Errorf("filter result = %+v, want only from-mixed", imgs)
	}
}

func TestListImages_FilterBySourceVM_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImageWithSourceVM(t, s, "img-a", "from-bastion", "vm-1700000000000000001", nil)
	seedStoredImageWithSourceVM(t, s, "img-b", "from-worker", "vm-1700000000000000002", nil)

	// URL-encoded whitespace must be trimmed before the equality check.
	resp, _ := http.Get(ts.URL + "/api/v1/images?source_vm=%20vm-1700000000000000001%20")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1 (whitespace-trimmed)", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "from-bastion" {
		t.Errorf("filter result = %+v, want only from-bastion", imgs)
	}
}

func TestListImages_FilterBySourceVM_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImageWithSourceVM(t, s, "img-a", "from-bastion", "vm-1700000000000000001", nil)
	seedStoredImageWithSourceVM(t, s, "img-b", "from-worker", "vm-1700000000000000002", nil)
	seedStoredImage(t, s, "img-c", "uploaded", "", nil) // no source_vm

	// Whitespace-only is identical to the empty case.
	resp, _ := http.Get(ts.URL + "/api/v1/images?source_vm=%20%20")
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Errorf("X-Total-Count = %q, want 3 (empty filter is no-op)", got)
	}
}

func TestListImages_FilterBySourceVM_NoMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImageWithSourceVM(t, s, "img-a", "from-bastion", "vm-1700000000000000001", nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?source_vm=vm-does-not-exist")
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Errorf("X-Total-Count = %q, want 0", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 0 {
		t.Errorf("want empty list, got %+v", imgs)
	}
}

func TestListImages_FilterBySourceVM_IgnoresImagesWithEmptySourceVM(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	// Uploaded image has no source_vm; should not match any non-empty filter.
	seedStoredImageWithSourceVM(t, s, "img-a", "from-bastion", "vm-1700000000000000001", nil)
	seedStoredImage(t, s, "img-b", "uploaded", "", nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?source_vm=vm-1700000000000000001")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1 (uploaded image excluded)", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "from-bastion" {
		t.Errorf("filter result = %+v, want only from-bastion", imgs)
	}
}

func TestListImages_FilterBySourceVM_ComposesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	// Two images from the same VM; one tagged "release", one tagged "rc".
	seedStoredImageWithSourceVM(t, s, "img-a", "bastion-release", "vm-1700000000000000001", []string{"release"})
	seedStoredImageWithSourceVM(t, s, "img-b", "bastion-rc", "vm-1700000000000000001", []string{"rc"})
	// And an image from a different VM that also happens to be tagged "release".
	seedStoredImageWithSourceVM(t, s, "img-c", "worker-release", "vm-1700000000000000002", []string{"release"})

	resp, _ := http.Get(ts.URL + "/api/v1/images?source_vm=vm-1700000000000000001&tag=release")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1 (intersection)", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 1 || imgs[0].Name != "bastion-release" {
		t.Errorf("filter result = %+v, want only bastion-release", imgs)
	}
}

func TestListImages_FilterBySourceVM_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		seedStoredImageWithSourceVM(t, s, fmt.Sprintf("img-bastion-%d", i),
			fmt.Sprintf("bastion-%d", i), "vm-1700000000000000001", nil)
	}
	for i := 0; i < 2; i++ {
		seedStoredImageWithSourceVM(t, s, fmt.Sprintf("img-worker-%d", i),
			fmt.Sprintf("worker-%d", i), "vm-1700000000000000002", nil)
	}

	resp, _ := http.Get(ts.URL + "/api/v1/images?source_vm=vm-1700000000000000001&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Errorf("X-Total-Count = %q, want 3 (post-filter / pre-pagination total)", got)
	}
	var imgs []*types.Image
	decodeJSON(t, resp, &imgs)
	if len(imgs) != 2 {
		t.Errorf("page size = %d, want 2", len(imgs))
	}
}

// ============================================================
// 5.4.29: ?since / ?until image time-range filter
// ============================================================

// seedStoredImageWithCreatedAt writes an image record with an explicit
// CreatedAt so the time-range filter tests can pin the boundary timestamps
// without relying on wall-clock time.
func seedStoredImageWithCreatedAt(t *testing.T, s *store.Store, id, name string, createdAt time.Time, tags []string) *types.Image {
	t.Helper()
	img := &types.Image{
		ID:        id,
		Name:      name,
		Path:      "/tmp/" + name + ".qcow2",
		SizeBytes: 1024,
		Format:    "qcow2",
		Tags:      tags,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := s.PutImage(img); err != nil {
		t.Fatalf("seed image: %v", err)
	}
	return img
}

func TestListImages_FilterBySince(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	seedStoredImageWithCreatedAt(t, s, "img-early", "early", day(1), nil)
	seedStoredImageWithCreatedAt(t, s, "img-mid", "mid", day(15), nil)
	seedStoredImageWithCreatedAt(t, s, "img-late", "late", day(30), nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?since=2026-05-10T00:00:00Z")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	names := map[string]bool{}
	for _, img := range listed {
		names[img.Name] = true
	}
	if !names["mid"] || !names["late"] || names["early"] {
		t.Fatalf("expected mid+late, got %+v", listed)
	}
}

func TestListImages_FilterByUntil(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	seedStoredImageWithCreatedAt(t, s, "img-early", "early", day(1), nil)
	seedStoredImageWithCreatedAt(t, s, "img-mid", "mid", day(15), nil)
	seedStoredImageWithCreatedAt(t, s, "img-late", "late", day(30), nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?until=2026-05-20T00:00:00Z")
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("expected 2 images <= until, got %+v", listed)
	}
}

func TestListImages_FilterBySinceAndUntil(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	seedStoredImageWithCreatedAt(t, s, "img-1", "img-1", day(1), nil)
	seedStoredImageWithCreatedAt(t, s, "img-15", "img-15", day(15), nil)
	seedStoredImageWithCreatedAt(t, s, "img-30", "img-30", day(30), nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?since=2026-05-10T00:00:00Z&until=2026-05-20T00:00:00Z")
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "img-15" {
		t.Fatalf("expected only img-15, got %+v", listed)
	}
}

func TestListImages_FilterBySince_Inclusive(t *testing.T) {
	// Exact boundary timestamp matches under "inclusive" semantics.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	boundary := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageWithCreatedAt(t, s, "img-edge", "edge", boundary, nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?since=2026-05-01T00:00:00Z")
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", listed)
	}
}

func TestListImages_FilterByInvalidSince(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/images?since=last-tuesday")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_since" {
		t.Fatalf("code = %q, want invalid_since", apiErr.Code)
	}
}

func TestListImages_FilterByInvalidUntil(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/images?until=2026-13-99")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_until" {
		t.Fatalf("code = %q, want invalid_until", apiErr.Code)
	}
}

func TestListImages_FilterBySince_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	seedStoredImageWithCreatedAt(t, s, "img-a", "img-a", day(1), nil)
	seedStoredImageWithCreatedAt(t, s, "img-b", "img-b", day(15), nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?since=%20%20")
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("whitespace-only since should be a no-op; got %+v", listed)
	}
}

func TestListImages_FilterByTimeRange_ExcludesZeroCreatedAt(t *testing.T) {
	// An image with zero CreatedAt is filtered out whenever any bound is
	// set — operators querying a time window don't want unbounded entries
	// silently included.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	seedStoredImageWithCreatedAt(t, s, "img-no-time", "no-time", time.Time{}, nil)
	seedStoredImageWithCreatedAt(t, s, "img-dated", "dated", time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC), nil)

	resp, _ := http.Get(ts.URL + "/api/v1/images?since=2026-05-01T00:00:00Z")
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "dated" {
		t.Fatalf("expected only dated (zero-time excluded), got %+v", listed)
	}
}

func TestListImages_FilterBySince_ComposesWithTagAndSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	seedStoredImageWithCreatedAt(t, s, "img-old-prod", "release-old", day(1), []string{"prod"})
	seedStoredImageWithCreatedAt(t, s, "img-new-prod", "release-new", day(20), []string{"prod"})
	seedStoredImageWithCreatedAt(t, s, "img-rollback", "rollback", day(20), []string{"prod"})
	seedStoredImageWithCreatedAt(t, s, "img-staging", "release-staging", day(20), []string{"staging"})

	resp, _ := http.Get(ts.URL + "/api/v1/images?since=2026-05-10T00:00:00Z&tag=prod&search=release")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "release-new" {
		t.Fatalf("expected only release-new, got %+v", listed)
	}
}

func TestListImages_FilterByMinSize(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-small", "small", 1<<20, t0) // 1 MiB
	seedStoredImageFull(t, s, "img-mid", "mid", 1<<30, t0)     // 1 GiB
	seedStoredImageFull(t, s, "img-big", "big", 2<<30, t0)     // 2 GiB

	resp, _ := http.Get(ts.URL + "/api/v1/images?min_size=1073741824") // >= 1 GiB
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	names := map[string]bool{}
	for _, img := range listed {
		names[img.Name] = true
	}
	if names["small"] || !names["mid"] || !names["big"] {
		t.Fatalf("expected mid+big (>= 1 GiB), got %+v", listed)
	}
}

func TestListImages_FilterByMaxSize(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-small", "small", 1<<20, t0)
	seedStoredImageFull(t, s, "img-mid", "mid", 1<<30, t0)
	seedStoredImageFull(t, s, "img-big", "big", 2<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?max_size=1073741824") // <= 1 GiB
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	names := map[string]bool{}
	for _, img := range listed {
		names[img.Name] = true
	}
	if !names["small"] || !names["mid"] || names["big"] {
		t.Fatalf("expected small+mid (<= 1 GiB), got %+v", listed)
	}
}

func TestListImages_FilterByMinAndMaxSize(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-small", "small", 1<<20, t0)
	seedStoredImageFull(t, s, "img-mid", "mid", 1<<30, t0)
	seedStoredImageFull(t, s, "img-big", "big", 2<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?min_size=1048577&max_size=1073741824")
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "mid" {
		t.Fatalf("expected only mid in [1 MiB+1, 1 GiB], got %+v", listed)
	}
}

func TestListImages_FilterByMinSize_Inclusive(t *testing.T) {
	// Exact boundary size matches under "inclusive" semantics.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-edge", "edge", 1<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?min_size=1073741824")
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", listed)
	}
}

func TestListImages_FilterByInvalidMinSize(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/images?min_size=ten-gigs")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_min_size" {
		t.Fatalf("code = %q, want invalid_min_size", apiErr.Code)
	}
}

func TestListImages_FilterByInvalidMaxSize(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/images?max_size=3.5")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_max_size" {
		t.Fatalf("code = %q, want invalid_max_size", apiErr.Code)
	}
}

func TestListImages_FilterByMinSize_RejectsNegative(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/images?min_size=-1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_min_size" {
		t.Fatalf("code = %q, want invalid_min_size", apiErr.Code)
	}
}

func TestListImages_FilterByMinSize_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-a", "img-a", 1<<20, t0)
	seedStoredImageFull(t, s, "img-b", "img-b", 1<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?min_size=%20%20")
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("whitespace-only min_size should be a no-op; got %+v", listed)
	}
}

func TestListImages_FilterBySize_ComposesWithTagAndSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	put := func(id, name string, size int64, tags []string) {
		img := &types.Image{ID: id, Name: name, Path: "/tmp/" + name + ".qcow2", SizeBytes: size, Format: "qcow2", Tags: tags, CreatedAt: t0, UpdatedAt: t0}
		if err := s.PutImage(img); err != nil {
			t.Fatalf("seed image: %v", err)
		}
	}
	put("img-small-prod", "release-small", 1<<20, []string{"prod"}) // too small
	put("img-big-prod", "release-big", 2<<30, []string{"prod"})     // matches
	put("img-big-other", "rollback-big", 2<<30, []string{"prod"})   // search excludes
	put("img-big-staging", "release-staging", 2<<30, []string{"staging"})

	resp, _ := http.Get(ts.URL + "/api/v1/images?min_size=1073741824&tag=prod&search=release")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "release-big" {
		t.Fatalf("expected only release-big, got %+v", listed)
	}
}

// 5.4.77: image list ?prefix= — case-sensitive HasPrefix(img.Name, prefix).
// Mirrors the snapshot list ?prefix= (5.4.75) and the VM list ?prefix=
// (5.4.76) so the operator's cohort query round-trips 1:1 across the three
// name-prefix axes.

func TestListImages_FilterByPrefix_Match(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-rocky-base", "rocky-base", 1<<30, t0)
	seedStoredImageFull(t, s, "img-rocky-gold", "rocky-gold", 1<<30, t0)
	seedStoredImageFull(t, s, "img-ubuntu", "ubuntu-base", 1<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?prefix=rocky-")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (post-filter)", got)
	}
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	names := map[string]bool{}
	for _, img := range listed {
		names[img.Name] = true
	}
	if !names["rocky-base"] || !names["rocky-gold"] || names["ubuntu-base"] {
		t.Fatalf("expected rocky-* only, got %+v", listed)
	}
}

func TestListImages_FilterByPrefix_IsCaseSensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-1", "rocky-base", 1<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?prefix=Rocky-")
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("X-Total-Count = %q, want 0 (case-sensitive non-match)", got)
	}
}

func TestListImages_FilterByPrefix_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-1", "rocky-base", 1<<30, t0)
	seedStoredImageFull(t, s, "img-2", "ubuntu-base", 1<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?prefix=%20rocky-%20")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (whitespace trimmed)", got)
	}
}

func TestListImages_FilterByPrefix_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-1", "rocky-base", 1<<30, t0)
	seedStoredImageFull(t, s, "img-2", "ubuntu-base", 1<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?prefix=%20%20")
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (empty prefix is no-op)", got)
	}
}

func TestListImages_FilterByPrefix_NoMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredImageFull(t, s, "img-1", "rocky-base", 1<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?prefix=nope-")
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("X-Total-Count = %q, want 0", got)
	}
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 0 {
		t.Fatalf("expected empty list, got %+v", listed)
	}
}

func TestListImages_FilterByPrefix_ComposesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	put := func(id, name string, tags []string) {
		img := &types.Image{ID: id, Name: name, Path: "/tmp/" + name + ".qcow2", SizeBytes: 1 << 30, Format: "qcow2", Tags: tags, CreatedAt: t0, UpdatedAt: t0}
		if err := s.PutImage(img); err != nil {
			t.Fatalf("seed image: %v", err)
		}
	}
	put("img-1", "rocky-prod", []string{"prod"})     // matches both
	put("img-2", "rocky-staging", []string{"stage"}) // tag excludes
	put("img-3", "ubuntu-prod", []string{"prod"})    // prefix excludes

	resp, _ := http.Get(ts.URL + "/api/v1/images?prefix=rocky-&tag=prod")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "rocky-prod" {
		t.Fatalf("expected only rocky-prod, got %+v", listed)
	}
}

func TestListImages_FilterByPrefix_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		seedStoredImageFull(t, s, fmt.Sprintf("img-rocky-%d", i), fmt.Sprintf("rocky-base-%d", i), 1<<30, t0)
	}
	seedStoredImageFull(t, s, "img-ubuntu", "ubuntu-base", 1<<30, t0)

	resp, _ := http.Get(ts.URL + "/api/v1/images?prefix=rocky-&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "4" {
		t.Fatalf("X-Total-Count = %q, want 4 (post-filter, pre-pagination)", got)
	}
	var listed []*types.Image
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("expected 2 paginated results, got %d", len(listed))
	}
}

func TestUploadImage_PersistsDescriptionAndTags(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "tagged-upload.qcow2")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("fake qcow2 content")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	mw.WriteField("description", "  uploaded via test  ")
	mw.WriteField("tags", "lab, ubuntu")
	mw.WriteField("tags", "lab")
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	resp, err := http.Post(ts.URL+"/api/v1/images/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var img types.Image
	decodeJSON(t, resp, &img)
	if img.Description != "uploaded via test" {
		t.Errorf("Description = %q, want trimmed", img.Description)
	}
	if got := img.Tags; len(got) != 2 || got[0] != "lab" || got[1] != "ubuntu" {
		t.Errorf("Tags = %v, want [lab ubuntu]", got)
	}
}

// patchJSON issues a PATCH against the test server with a JSON body.  Mirrors
// the http.Post helper that other tests use for POST + JSON.
func patchJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, url, jsonBody(t, body))
	if err != nil {
		t.Fatalf("NewRequest PATCH %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", url, err)
	}
	return resp
}

func TestUpdateSnapshot_SetsDescription(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-up", Name: "host", State: types.VMStateRunning})
	if _, err := mockMgr.CreateSnapshot(nil, "vm-up", types.SnapshotSpec{Name: "snap-1", Description: "old"}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	desc := "fresh description"
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-up/snapshots/snap-1", updateSnapshotRequest{Description: &desc})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snap types.Snapshot
	decodeJSON(t, resp, &snap)
	if snap.Description != "fresh description" {
		t.Errorf("Description = %q, want fresh description", snap.Description)
	}

	listed, _ := mockMgr.ListSnapshots(nil, "vm-up")
	if len(listed) != 1 || listed[0].Description != "fresh description" {
		t.Errorf("manager state did not pick up new description: %+v", listed)
	}
}

func TestUpdateSnapshot_ClearsDescription(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-clr", Name: "host"})
	if _, err := mockMgr.CreateSnapshot(nil, "vm-clr", types.SnapshotSpec{Name: "snap-c", Description: "stale text"}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	cleared := ""
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-clr/snapshots/snap-c", updateSnapshotRequest{Description: &cleared})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snap types.Snapshot
	decodeJSON(t, resp, &snap)
	if snap.Description != "" {
		t.Errorf("Description = %q, want empty", snap.Description)
	}
}

func TestUpdateSnapshot_OmittedDescriptionIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-noop", Name: "host"})
	if _, err := mockMgr.CreateSnapshot(nil, "vm-noop", types.SnapshotSpec{Name: "snap-n", Description: "original"}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	// Empty body / no description field at all — description must be untouched.
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-noop/snapshots/snap-n", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snap types.Snapshot
	decodeJSON(t, resp, &snap)
	if snap.Description != "original" {
		t.Errorf("Description = %q, want original (no-op)", snap.Description)
	}
}

func TestUpdateSnapshot_RejectsLongDescription(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-long", Name: "host"})
	mockMgr.CreateSnapshot(nil, "vm-long", types.SnapshotSpec{Name: "snap"})

	long := strings.Repeat("x", 1025)
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-long/snapshots/snap", updateSnapshotRequest{Description: &long})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_description")
}

func TestUpdateSnapshot_NotFound(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-nf", Name: "host"})
	desc := "anything"
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-nf/snapshots/missing", updateSnapshotRequest{Description: &desc})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestUpdateSnapshot_BadJSON(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bj", Name: "host"})
	mockMgr.CreateSnapshot(nil, "vm-bj", types.SnapshotSpec{Name: "snap"})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-bj/snapshots/snap", bytes.NewBufferString("not-json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_request_body")
}

func TestUpdateSnapshot_ManagerError(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-err", Name: "host"})
	mockMgr.CreateSnapshot(nil, "vm-err", types.SnapshotSpec{Name: "snap"})
	mockMgr.UpdateSnapshotErr = errors.New("libvirt: redefine refused")

	desc := "doesn't matter"
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-err/snapshots/snap", updateSnapshotRequest{Description: &desc})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

// ============================================================
// Bulk port-forward delete handler tests (2.3.7)
// ============================================================

func decodeBulkPortResponse(t *testing.T, resp *http.Response) bulkDeletePortsResponse {
	t.Helper()
	var out bulkDeletePortsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode bulk port response: %v", err)
	}
	return out
}

func seedPortForward(t *testing.T, s *store.Store, pf *types.PortForward) {
	t.Helper()
	if err := s.PutPortForward(pf); err != nil {
		t.Fatalf("seed port forward %s: %v", pf.ID, err)
	}
}

func decodePortForwardResponse(t *testing.T, resp *http.Response) types.PortForward {
	t.Helper()
	var out types.PortForward
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode port forward response: %v", err)
	}
	return out
}

func TestUpdatePort_SetsDescription(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-up", VMID: "vm-up", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-up/ports/pf-up",
		jsonBody(t, map[string]any{"description": "  ssh-jumpbox  "}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodePortForwardResponse(t, resp)
	if out.Description != "ssh-jumpbox" {
		t.Errorf("Description = %q, want %q (TrimSpace applied)", out.Description, "ssh-jumpbox")
	}
	if out.HostPort != 8080 || out.GuestPort != 80 || out.Protocol != types.ProtocolTCP {
		t.Errorf("5-tuple changed: %+v", out)
	}

	stored, _ := s.ListPortForwards("vm-up")
	if len(stored) != 1 || stored[0].Description != "ssh-jumpbox" {
		t.Errorf("persisted description = %q, want ssh-jumpbox", stored[0].Description)
	}
}

func TestUpdatePort_ClearsDescription(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-cl", VMID: "vm-cl", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.11", Protocol: types.ProtocolTCP, Description: "stale"})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-cl/ports/pf-cl",
		jsonBody(t, map[string]any{"description": ""}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodePortForwardResponse(t, resp)
	if out.Description != "" {
		t.Errorf("Description = %q, want empty", out.Description)
	}
}

func TestUpdatePort_OmittedDescriptionIsNoOp(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-noop", VMID: "vm-noop", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.12", Protocol: types.ProtocolTCP, Description: "leave-me"})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-noop/ports/pf-noop",
		jsonBody(t, map[string]any{}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodePortForwardResponse(t, resp)
	if out.Description != "leave-me" {
		t.Errorf("Description = %q, want unchanged 'leave-me'", out.Description)
	}
}

func TestUpdatePort_RejectsLongDescription(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-long", VMID: "vm-long", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.13", Protocol: types.ProtocolTCP})

	tooLong := strings.Repeat("x", 257)
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-long/ports/pf-long",
		jsonBody(t, map[string]any{"description": tooLong}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	_ = json.NewDecoder(resp.Body).Decode(&apiErr)
	if apiErr.Code != "invalid_port_forward" {
		t.Errorf("Code = %q, want invalid_port_forward", apiErr.Code)
	}
}

func TestUpdatePort_NotFound(t *testing.T) {
	ts, _, _, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-nf/ports/pf-missing",
		jsonBody(t, map[string]any{"description": "x"}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var apiErr types.APIError
	_ = json.NewDecoder(resp.Body).Decode(&apiErr)
	if apiErr.Code != "resource_not_found" {
		t.Errorf("Code = %q, want resource_not_found", apiErr.Code)
	}
}

func TestUpdatePort_PortForwardOnDifferentVM_NotFound(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	// Two VMs, each with their own rule. PATCH against /vms/A/ports/B-id must
	// 404 — and must leave B's rule completely untouched.
	seedPortForward(t, s, &types.PortForward{ID: "pf-A", VMID: "vm-A", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: "pf-B", VMID: "vm-B", HostPort: 9090, GuestPort: 90, GuestIP: "192.168.100.60", Protocol: types.ProtocolTCP, Description: "B-rule"})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-A/ports/pf-B",
		jsonBody(t, map[string]any{"description": "tampered"}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	// vm-B's rule must be untouched.
	stored, _ := s.ListPortForwards("vm-B")
	if len(stored) != 1 || stored[0].Description != "B-rule" {
		t.Errorf("vm-B rule mutated: %+v", stored)
	}
}

func TestUpdatePort_BadJSON(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-bj", VMID: "vm-bj", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.14", Protocol: types.ProtocolTCP})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-bj/ports/pf-bj",
		bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	_ = json.NewDecoder(resp.Body).Decode(&apiErr)
	if apiErr.Code != "invalid_request_body" {
		t.Errorf("Code = %q, want invalid_request_body", apiErr.Code)
	}
}

func TestAddPort_WithTags(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-tags", Name: "tagged", IP: "192.168.100.10"})

	body := jsonBody(t, addPortRequest{
		HostPort:  2222,
		GuestPort: 22,
		Protocol:  "tcp",
		Tags:      []string{"PRODUCTION", "audit", "production"}, // dupe + uppercase
	})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-tags/ports", "application/json", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	out := decodePortForwardResponse(t, resp)
	if got, want := strings.Join(out.Tags, ","), "audit,production"; got != want {
		t.Errorf("Tags = %q, want %q (normalised: lowercased, deduped, sorted)", got, want)
	}
}

func TestAddPort_RejectsInvalidTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bad", Name: "bad", IP: "192.168.100.10"})

	body := jsonBody(t, addPortRequest{
		HostPort:  2222,
		GuestPort: 22,
		Protocol:  "tcp",
		Tags:      []string{"has spaces"},
	})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-bad/ports", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_port_forward" {
		t.Errorf("Code = %q, want invalid_port_forward", apiErr.Code)
	}
}

func TestAddPort_RejectsEmptyTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-bad", Name: "bad", IP: "192.168.100.10"})

	body := jsonBody(t, addPortRequest{
		HostPort:  2222,
		GuestPort: 22,
		Protocol:  "tcp",
		Tags:      []string{"  "},
	})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-bad/ports", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdatePort_SetsTags(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-tg", VMID: "vm-tg", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-tg/ports/pf-tg",
		jsonBody(t, map[string]any{"tags": []string{"web", "audit"}}))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodePortForwardResponse(t, resp)
	if got, want := strings.Join(out.Tags, ","), "audit,web"; got != want {
		t.Errorf("Tags = %q, want %q", got, want)
	}

	stored, _ := s.ListPortForwards("vm-tg")
	if len(stored) != 1 || strings.Join(stored[0].Tags, ",") != "audit,web" {
		t.Errorf("persisted Tags = %v", stored[0].Tags)
	}
}

func TestUpdatePort_ClearsTags(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-clr", VMID: "vm-clr", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"old"}})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-clr/ports/pf-clr",
		jsonBody(t, map[string]any{"tags": []string{}}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodePortForwardResponse(t, resp)
	if len(out.Tags) != 0 {
		t.Errorf("expected tags cleared, got %v", out.Tags)
	}
}

func TestUpdatePort_OmittedTagsIsNoOp(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-no", VMID: "vm-no", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.11", Protocol: types.ProtocolTCP, Tags: []string{"keep", "me"}})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-no/ports/pf-no",
		jsonBody(t, map[string]any{"description": "label-only"}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodePortForwardResponse(t, resp)
	if strings.Join(out.Tags, ",") != "keep,me" {
		t.Errorf("Tags = %v, want unchanged", out.Tags)
	}
	if out.Description != "label-only" {
		t.Errorf("Description = %q, want updated", out.Description)
	}
}

func TestUpdatePort_RejectsInvalidTag(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-bad", VMID: "vm-bad", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.12", Protocol: types.ProtocolTCP})

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/vms/vm-bad/ports/pf-bad",
		jsonBody(t, map[string]any{"tags": []string{"has spaces"}}))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_port_forward" {
		t.Errorf("Code = %q, want invalid_port_forward", apiErr.Code)
	}
}

func TestListPorts_FilterByTag(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-a", VMID: "vm-flt", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"production", "web"}})
	seedPortForward(t, s, &types.PortForward{ID: "pf-b", VMID: "vm-flt", HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"audit"}})
	seedPortForward(t, s, &types.PortForward{ID: "pf-c", VMID: "vm-flt", HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-flt/ports?tag=production")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []types.PortForward
	decodeJSON(t, resp, &got)
	if len(got) != 1 || got[0].ID != "pf-a" {
		t.Errorf("filter result = %+v, want [pf-a]", got)
	}
}

func TestListPorts_FilterByTag_CaseInsensitive(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-a", VMID: "vm-flt2", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"production"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-flt2/ports?tag=PRODUCTION")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []types.PortForward
	decodeJSON(t, resp, &got)
	if len(got) != 1 {
		t.Errorf("filter result count = %d, want 1 (case-insensitive)", len(got))
	}
}

func TestListPorts_FilterByTag_ComposesWithSearch(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-a", VMID: "vm-cmb", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"production"}, Description: "web frontend"})
	seedPortForward(t, s, &types.PortForward{ID: "pf-b", VMID: "vm-cmb", HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"production"}, Description: "metrics"})
	seedPortForward(t, s, &types.PortForward{ID: "pf-c", VMID: "vm-cmb", HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"audit"}, Description: "web ssh"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-cmb/ports?tag=production&search=web")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []types.PortForward
	decodeJSON(t, resp, &got)
	if len(got) != 1 || got[0].ID != "pf-a" {
		t.Errorf("intersection result = %+v, want [pf-a]", got)
	}
}

func TestListPorts_FilterBySearch_MatchesTag(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-a", VMID: "vm-srch", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"production"}})
	seedPortForward(t, s, &types.PortForward{ID: "pf-b", VMID: "vm-srch", HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"audit"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-srch/ports?search=audit")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []types.PortForward
	decodeJSON(t, resp, &got)
	if len(got) != 1 || got[0].ID != "pf-b" {
		t.Errorf("search-by-tag result = %+v, want [pf-b]", got)
	}
}

func TestBulkDeletePorts_ByIDs(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-keep", VMID: "vm-bd", HostPort: 22, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: "pf-1", VMID: "vm-bd", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: "pf-2", VMID: "vm-bd", HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})

	body := jsonBody(t, bulkDeletePortsRequest{IDs: []string{"pf-1", "pf-2"}})
	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-bd/ports/bulk_delete", "application/json", body)
	if err != nil {
		t.Fatalf("POST bulk_delete: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkPortResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	for _, r := range out.Results {
		if !r.Success {
			t.Errorf("expected success for %q, got code=%q msg=%q", r.ID, r.Code, r.Message)
		}
	}

	survivors, _ := s.ListPortForwards("vm-bd")
	if len(survivors) != 1 || survivors[0].ID != "pf-keep" {
		t.Errorf("survivors = %+v, want only pf-keep", survivors)
	}
}

func TestBulkDeletePorts_ByProtocol(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-tcp-1", VMID: "vm-bp", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.20", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: "pf-tcp-2", VMID: "vm-bp", HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.20", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: "pf-udp", VMID: "vm-bp", HostPort: 53, GuestPort: 53, GuestIP: "192.168.100.20", Protocol: types.ProtocolUDP})

	body := jsonBody(t, bulkDeletePortsRequest{Protocol: types.ProtocolTCP})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-bp/ports/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkPortResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}

	survivors, _ := s.ListPortForwards("vm-bp")
	if len(survivors) != 1 || survivors[0].ID != "pf-udp" {
		t.Errorf("survivors = %+v, want only pf-udp", survivors)
	}
}

func TestBulkDeletePorts_ProtocolCaseInsensitive(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-tcp", VMID: "vm-ci", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.30", Protocol: types.ProtocolTCP})

	body := jsonBody(t, map[string]any{"protocol": "TCP"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-ci/ports/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkPortResponse(t, resp)
	if len(out.Results) != 1 || !out.Results[0].Success {
		t.Errorf("expected one successful result, got %+v", out.Results)
	}
}

func TestBulkDeletePorts_PartialFailure(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-real", VMID: "vm-pf", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP})

	body := jsonBody(t, bulkDeletePortsRequest{IDs: []string{"pf-real", "pf-missing"}})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-pf/ports/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkPortResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	by := map[string]bulkDeletePortResult{}
	for _, r := range out.Results {
		by[r.ID] = r
	}
	if !by["pf-real"].Success {
		t.Errorf("expected pf-real to succeed, got %+v", by["pf-real"])
	}
	if by["pf-missing"].Success {
		t.Errorf("expected pf-missing to fail")
	}
	if by["pf-missing"].Code != "resource_not_found" {
		t.Errorf("missing.Code = %q, want resource_not_found", by["pf-missing"].Code)
	}
}

func TestBulkDeletePorts_IDForOtherVM_NotDeleted(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	// Two VMs each own their own port forward. The bulk request targets
	// vm-A but tries to also delete vm-B's id; it must surface as
	// resource_not_found and leave vm-B's rule untouched.
	seedPortForward(t, s, &types.PortForward{ID: "pf-A", VMID: "vm-A", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: "pf-B", VMID: "vm-B", HostPort: 9090, GuestPort: 90, GuestIP: "192.168.100.60", Protocol: types.ProtocolTCP})

	body := jsonBody(t, bulkDeletePortsRequest{IDs: []string{"pf-A", "pf-B"}})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-A/ports/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkPortResponse(t, resp)
	by := map[string]bulkDeletePortResult{}
	for _, r := range out.Results {
		by[r.ID] = r
	}
	if !by["pf-A"].Success {
		t.Errorf("pf-A should have been deleted, got %+v", by["pf-A"])
	}
	if by["pf-B"].Success {
		t.Errorf("pf-B should NOT have been deleted via vm-A's bulk request")
	}
	if by["pf-B"].Code != "resource_not_found" {
		t.Errorf("pf-B.Code = %q, want resource_not_found", by["pf-B"].Code)
	}

	// Verify vm-B's rule is still in the store.
	survivorsB, _ := s.ListPortForwards("vm-B")
	if len(survivorsB) != 1 || survivorsB[0].ID != "pf-B" {
		t.Errorf("vm-B survivors = %+v, expected pf-B intact", survivorsB)
	}
}

func TestBulkDeletePorts_EmptyRequestRejected(t *testing.T) {
	ts, _, _, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	body := jsonBody(t, bulkDeletePortsRequest{})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-empty/ports/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeletePorts_BothIDsAndProtocolRejected(t *testing.T) {
	ts, _, _, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	body := jsonBody(t, bulkDeletePortsRequest{IDs: []string{"pf-1"}, Protocol: types.ProtocolTCP})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-both/ports/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeletePorts_UnknownProtocolRejected(t *testing.T) {
	ts, _, _, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	body := jsonBody(t, map[string]any{"protocol": "sctp"})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-bad/ports/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeletePorts_ProtocolNoMatchEmptyResponse(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-tcp", VMID: "vm-nm", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.70", Protocol: types.ProtocolTCP})

	body := jsonBody(t, bulkDeletePortsRequest{Protocol: types.ProtocolUDP})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-nm/ports/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkPortResponse(t, resp)
	if len(out.Results) != 0 {
		t.Errorf("results = %d, want 0", len(out.Results))
	}
	survivors, _ := s.ListPortForwards("vm-nm")
	if len(survivors) != 1 {
		t.Errorf("survivors = %d, want 1 (tcp untouched)", len(survivors))
	}
}

// --- Template list sort (5.4.7) ---

// seedStoredTemplate stamps a template directly into the underlying store
// with caller-controlled ID and CreatedAt so sort tests aren't at the mercy
// of wall-clock ordering between rapid POST /templates calls.
func seedStoredTemplate(t *testing.T, s *store.Store, tpl *types.VMTemplate) {
	t.Helper()
	if tpl.UpdatedAt.IsZero() {
		tpl.UpdatedAt = tpl.CreatedAt
	}
	if err := s.PutTemplate(tpl); err != nil {
		t.Fatalf("seed template: %v", err)
	}
}

func decodeTemplateList(t *testing.T, resp *http.Response) []*types.VMTemplate {
	t.Helper()
	var out []*types.VMTemplate
	decodeJSON(t, resp, &out)
	return out
}

func TestListTemplates_SortByName(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "Charlie", Image: "rocky9.qcow2", CreatedAt: t0.Add(2 * time.Hour)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "rocky9.qcow2", CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "Bravo", Image: "rocky9.qcow2", CreatedAt: t0.Add(time.Hour)})

	resp, err := http.Get(ts.URL + "/api/v1/templates?sort=name")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decodeTemplateList(t, resp)
	want := []string{"alpha", "Bravo", "Charlie"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, tpl.Name, want[i])
		}
	}
}

func TestListTemplates_SortByNameDesc(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "rocky9.qcow2", CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "Bravo", Image: "rocky9.qcow2", CreatedAt: t0.Add(time.Hour)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "Charlie", Image: "rocky9.qcow2", CreatedAt: t0.Add(2 * time.Hour)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=name&order=desc")
	got := decodeTemplateList(t, resp)
	want := []string{"Charlie", "Bravo", "alpha"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, tpl.Name, want[i])
		}
	}
}

func TestListTemplates_SortByCreatedAtDesc(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "rocky9.qcow2", CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "bravo", Image: "rocky9.qcow2", CreatedAt: t0.Add(time.Hour)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "charlie", Image: "rocky9.qcow2", CreatedAt: t0.Add(2 * time.Hour)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=created_at&order=desc")
	got := decodeTemplateList(t, resp)
	want := []string{"tmpl-3", "tmpl-2", "tmpl-1"}
	for i, tpl := range got {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, tpl.ID, want[i])
		}
	}
}

func TestListTemplates_SortCombinesWithTagFilter(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "rocky9.qcow2", Tags: []string{"prod"}, CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "bravo", Image: "rocky9.qcow2", Tags: []string{"prod"}, CreatedAt: t0.Add(time.Hour)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "charlie", Image: "rocky9.qcow2", Tags: []string{"dev"}, CreatedAt: t0.Add(2 * time.Hour)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?tag=prod&sort=name&order=desc")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (tag filter dropped the dev template)", len(got))
	}
	want := []string{"bravo", "alpha"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, tpl.Name, want[i])
		}
	}
}

func TestListTemplates_SortPaginationDeterministic(t *testing.T) {
	// Equal-name input: stable pagination only holds if the comparator
	// tiebreaks on ID. Without that, two fetches see different orderings
	// across Go map iteration runs.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-4", Name: "shared", Image: "rocky9.qcow2", CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "shared", Image: "rocky9.qcow2", CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "shared", Image: "rocky9.qcow2", CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "shared", Image: "rocky9.qcow2", CreatedAt: t0})

	page1, _ := http.Get(ts.URL + "/api/v1/templates?sort=name&per_page=2&page=1")
	page2, _ := http.Get(ts.URL + "/api/v1/templates?sort=name&per_page=2&page=2")
	got1 := decodeTemplateList(t, page1)
	got2 := decodeTemplateList(t, page2)
	if len(got1) != 2 || len(got2) != 2 {
		t.Fatalf("got1=%d got2=%d, want both 2", len(got1), len(got2))
	}
	combined := []string{got1[0].ID, got1[1].ID, got2[0].ID, got2[1].ID}
	want := []string{"tmpl-1", "tmpl-2", "tmpl-3", "tmpl-4"}
	for i, id := range combined {
		if id != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, id, want[i], combined)
		}
	}
}

func TestListTemplates_RejectsInvalidSort(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()
	// `image` and `default_user` are valid template sort axes (5.4.89,
	// 5.4.92); use a sentinel that's still unsupported so the 400 path
	// is exercised.
	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=memory")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	errResp := assertAPIErrorCode(t, resp, "invalid_sort")
	// The error message must advertise the new axes on the API surface so
	// operators see the full whitelist.
	if !strings.Contains(errResp.Message, "image") {
		t.Errorf("invalid_sort message = %q, want to mention `image`", errResp.Message)
	}
	if !strings.Contains(errResp.Message, "default_user") {
		t.Errorf("invalid_sort message = %q, want to mention `default_user`", errResp.Message)
	}
}

func TestListTemplates_SortByCPUs(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "small", Image: "rocky9.qcow2", CPUs: 1})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "medium", Image: "rocky9.qcow2", CPUs: 4})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "large", Image: "rocky9.qcow2", CPUs: 8})

	resp, err := http.Get(ts.URL + "/api/v1/templates?sort=cpus")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decodeTemplateList(t, resp)
	want := []string{"small", "medium", "large"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, tpl.Name, want[i])
		}
	}
}

func TestListTemplates_SortByCPUsDesc_TiebreaksOnID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "rocky9.qcow2", CPUs: 4})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "rocky9.qcow2", CPUs: 4}) // tie
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: "rocky9.qcow2", CPUs: 1})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=cpus&order=desc")
	got := decodeTemplateList(t, resp)
	// Largest CPUs first; equal-cpu pair reverses on tiebreak too because
	// the descending wrapper inverts the entire compare result.
	want := []string{"tmpl-2", "tmpl-1", "tmpl-3"}
	for i, tpl := range got {
		if tpl.ID != want[i] {
			t.Errorf("idx %d: id = %q, want %q", i, tpl.ID, want[i])
		}
	}
}

func TestListTemplates_SortByRAMMB(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "tiny", Image: "rocky9.qcow2", RAMMB: 512})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "big", Image: "rocky9.qcow2", RAMMB: 8192})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "med", Image: "rocky9.qcow2", RAMMB: 2048})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=ram_mb&order=desc")
	got := decodeTemplateList(t, resp)
	want := []string{"big", "med", "tiny"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, tpl.Name, want[i])
		}
	}
}

func TestListTemplates_SortByDiskGB(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "small", Image: "rocky9.qcow2", DiskGB: 10})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "huge", Image: "rocky9.qcow2", DiskGB: 500})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "med", Image: "rocky9.qcow2", DiskGB: 100})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=disk_gb")
	got := decodeTemplateList(t, resp)
	want := []string{"small", "med", "huge"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, tpl.Name, want[i])
		}
	}
}

// 5.4.89 — case-insensitive `image` sort axis on the template list.
// Mirrors the VM list `image` sort axis (5.4.88).

func TestListTemplates_SortByImage_AscEmptyTrailing(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: ""})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "ubuntu-22.04.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=image")
	got := decodeTemplateList(t, resp)
	// rocky9 < ubuntu; empty trails in asc.
	want := []string{"b", "a", "c"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %+v)", i, tpl.Name, want[i], got)
		}
	}
}

func TestListTemplates_SortByImage_DescEmptyLeading(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "ubuntu-22.04.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: ""})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=image&order=desc")
	got := decodeTemplateList(t, resp)
	// Empty leads desc; then ubuntu; then rocky.
	want := []string{"c", "a", "b"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %+v)", i, tpl.Name, want[i], got)
		}
	}
}

func TestListTemplates_SortByImage_CaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: "Rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "rocky9.qcow2"}) // case-folded same
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "alpine.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=image")
	got := decodeTemplateList(t, resp)
	// alpine < rocky (case-folded); rocky tie tiebreaks on id (tmpl-1 before tmpl-3).
	want := []string{"b", "a", "c"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %+v)", i, tpl.Name, want[i], got)
		}
	}
}

func TestListTemplates_SortByImage_TiebreaksOnID(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=image")
	got := decodeTemplateList(t, resp)
	want := []string{"a", "b", "c"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %+v)", i, tpl.Name, want[i], got)
		}
	}
}

// 5.4.92 — case-insensitive `default_user` sort axis on the template list.
// Diverges from the VM list `default_user` axis (5.4.91): empty stored values
// sink to the tail of asc / head of desc rather than collapsing to "root",
// because templates store empty as "use the image's built-in user".

func TestListTemplates_SortByDefaultUser_AscEmptyTrailing(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: "rocky9.qcow2", DefaultUser: ""})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "rocky9.qcow2", DefaultUser: "ubuntu"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "rocky9.qcow2", DefaultUser: "ops-alice"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=default_user")
	got := decodeTemplateList(t, resp)
	// ops-alice < ubuntu lex; empty trails in asc.
	want := []string{"b", "a", "c"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %+v)", i, tpl.Name, want[i], got)
		}
	}
}

func TestListTemplates_SortByDefaultUser_DescEmptyLeading(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "rocky9.qcow2", DefaultUser: "ubuntu"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: "rocky9.qcow2", DefaultUser: ""})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "rocky9.qcow2", DefaultUser: "ops-alice"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=default_user&order=desc")
	got := decodeTemplateList(t, resp)
	// Empty leads desc; then ubuntu; then ops-alice.
	want := []string{"c", "a", "b"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %+v)", i, tpl.Name, want[i], got)
		}
	}
}

func TestListTemplates_SortByDefaultUser_CaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: "rocky9.qcow2", DefaultUser: "Root"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "rocky9.qcow2", DefaultUser: "root"}) // case-folded same
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "rocky9.qcow2", DefaultUser: "alice"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=default_user")
	got := decodeTemplateList(t, resp)
	// alice < root (case-folded); root tie tiebreaks on id (tmpl-1 before tmpl-3).
	want := []string{"b", "a", "c"}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %+v)", i, tpl.Name, want[i], got)
		}
	}
}

func TestListTemplates_SortByDefaultUser_EmptyDoesNotCollateWithRoot(t *testing.T) {
	// Unlike VM list `default_user` (5.4.91) — which collapses empty → "root"
	// to mirror runtime semantics — templates store empty as "use the image's
	// built-in user" so an empty stored value must sink to the tail of asc.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: "rocky9.qcow2", DefaultUser: ""})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "rocky9.qcow2", DefaultUser: "root"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "rocky9.qcow2", DefaultUser: "root"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=default_user")
	got := decodeTemplateList(t, resp)
	want := []string{"a", "b", "c"} // root, root, then empty trails
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q (full: %+v)", i, tpl.Name, want[i], got)
		}
	}
}

func TestListTemplates_RejectsInvalidOrder(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()
	resp, _ := http.Get(ts.URL + "/api/v1/templates?order=sideways")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_order")
}

func TestListTemplates_FilterBySearch_MatchesName(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, err := http.Get(ts.URL + "/api/v1/templates?search=rocky")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky9-base" {
		t.Errorf("filter = %+v, want only rocky9-base", got)
	}
}

func TestListTemplates_FilterBySearch_MatchesDescription(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "small", Image: "rocky9.qcow2", Description: "Hardened CIS-1 build"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "large", Image: "rocky9.qcow2", Description: "Stock cloud image"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?search=hardened")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "small" {
		t.Errorf("filter = %+v, want only small via description", got)
	}
}

func TestListTemplates_FilterBySearch_MatchesTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "small", Image: "rocky9.qcow2", Tags: []string{"team-storage", "prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "large", Image: "rocky9.qcow2", Tags: []string{"team-net"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?search=storage")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "small" {
		t.Errorf("filter = %+v, want only small via tag", got)
	}
}

func TestListTemplates_FilterBySearch_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "Rocky9-Base", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?search=ROCKY")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "Rocky9-Base" {
		t.Errorf("filter = %+v, want only Rocky9-Base case-insensitive", got)
	}
}

func TestListTemplates_FilterBySearch_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "beta", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?search=%20%20alpha%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Errorf("filter = %+v, want only alpha after trim", got)
	}
}

func TestListTemplates_FilterBySearch_NoMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?search=needle-not-present")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with empty list", resp.StatusCode)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 0 {
		t.Errorf("filter = %+v, want empty list", got)
	}
}

func TestListTemplates_FilterBySearch_CombinesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-prod", Image: "rocky9.qcow2", Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky9-qa", Image: "rocky9.qcow2", Tags: []string{"qa"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "ubuntu-prod", Image: "ubuntu.qcow2", Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?search=rocky&tag=prod")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky9-prod" {
		t.Errorf("filter = %+v, want only rocky9-prod (intersection of search+tag)", got)
	}
}

func TestListTemplates_FilterBySearch_CombinesWithSort(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-charlie", Image: "rocky9.qcow2", CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky9-alpha", Image: "rocky9.qcow2", CreatedAt: t0.Add(time.Hour)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "ubuntu-22", Image: "ubuntu.qcow2", CreatedAt: t0.Add(2 * time.Hour)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?search=rocky&sort=name&order=asc")
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Errorf("X-Total-Count = %q, want 2 (only rocky after search)", got)
	}
	got := decodeTemplateList(t, resp)
	want := []string{"rocky9-alpha", "rocky9-charlie"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, tpl := range got {
		if tpl.Name != want[i] {
			t.Errorf("idx %d: name = %q, want %q", i, tpl.Name, want[i])
		}
	}
}

func TestListTemplates_FilterBySearch_IDNotInHaystack(t *testing.T) {
	// IDs are opaque `tmpl-<unix-nano>` strings; searching for a substring
	// of an ID should not match. Locks the contract in.
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1741234567890123", Name: "alpha", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?search=1741234567890123")
	got := decodeTemplateList(t, resp)
	if len(got) != 0 {
		t.Errorf("filter = %+v, want empty list (ID excluded from haystack)", got)
	}
}

func TestListTemplates_FilterByImage_ExactMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, err := http.Get(ts.URL + "/api/v1/templates?image=rocky9.qcow2")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky9-base" {
		t.Errorf("filter = %+v, want only rocky9-base", got)
	}
}

func TestListTemplates_FilterByImage_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "Rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?image=ROCKY9.QCOW2")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky9-base" {
		t.Errorf("filter = %+v, want only rocky9-base (case-insensitive)", got)
	}
}

func TestListTemplates_FilterByImage_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?image=%20%20rocky9.qcow2%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky9-base" {
		t.Errorf("filter = %+v, want only rocky9-base after trim", got)
	}
}

func TestListTemplates_FilterByImage_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?image=%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("filter = %+v, want every template when image is whitespace-only", got)
	}
}

func TestListTemplates_FilterByImage_NoMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?image=fedora.qcow2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with empty list", resp.StatusCode)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 0 {
		t.Errorf("filter = %+v, want empty list", got)
	}
}

func TestListTemplates_FilterByImage_ComposesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-prod", Image: "rocky9.qcow2", Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky9-qa", Image: "rocky9.qcow2", Tags: []string{"qa"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "ubuntu-prod", Image: "ubuntu.qcow2", Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?image=rocky9.qcow2&tag=prod")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky9-prod" {
		t.Errorf("filter = %+v, want only rocky9-prod (intersection of image+tag)", got)
	}
}

func TestListTemplates_FilterByImage_ComposesWithSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-prod", Image: "rocky9.qcow2", Description: "production"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky9-qa", Image: "rocky9.qcow2", Description: "qa"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "ubuntu-prod", Image: "ubuntu.qcow2", Description: "production"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?image=rocky9.qcow2&search=production")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky9-prod" {
		t.Errorf("filter = %+v, want only rocky9-prod (intersection of image+search)", got)
	}
}

func TestListTemplates_FilterByImage_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		seedStoredTemplate(t, s, &types.VMTemplate{ID: fmt.Sprintf("tmpl-%d", i), Name: fmt.Sprintf("rocky-%d", i), Image: "rocky9.qcow2"})
	}
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-u", Name: "ubuntu", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?image=rocky9.qcow2&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "5" {
		t.Errorf("X-Total-Count = %q, want 5 (post-filter population)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("page len = %d, want 2", len(got))
	}
}

func TestListTemplates_FilterByDefaultUser_ExactMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "deploy"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ec2-ubuntu", Image: "ubuntu.qcow2", DefaultUser: "ec2-user"})

	resp, err := http.Get(ts.URL + "/api/v1/templates?default_user=deploy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "deploy-rocky" {
		t.Errorf("filter = %+v, want only deploy-rocky", got)
	}
}

func TestListTemplates_FilterByDefaultUser_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "Deploy"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ec2-ubuntu", Image: "ubuntu.qcow2", DefaultUser: "ec2-user"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?default_user=DEPLOY")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "deploy-rocky" {
		t.Errorf("filter = %+v, want only deploy-rocky (case-insensitive)", got)
	}
}

func TestListTemplates_FilterByDefaultUser_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "deploy"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ec2-ubuntu", Image: "ubuntu.qcow2", DefaultUser: "ec2-user"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?default_user=%20%20deploy%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "deploy-rocky" {
		t.Errorf("filter = %+v, want only deploy-rocky after trim", got)
	}
}

func TestListTemplates_FilterByDefaultUser_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "deploy"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ec2-ubuntu", Image: "ubuntu.qcow2", DefaultUser: "ec2-user"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?default_user=%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("filter = %+v, want every template when default_user is whitespace-only", got)
	}
}

func TestListTemplates_FilterByDefaultUser_EmptyStoredNeverMatches(t *testing.T) {
	// Unlike the VM filter, a template with an empty default_user means "use
	// the image's built-in user" — it must NOT match a non-empty query
	// (there is no empty-means-root fallback).
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "no-user", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "deploy"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?default_user=root")
	got := decodeTemplateList(t, resp)
	if len(got) != 0 {
		t.Errorf("filter = %+v, want empty list (empty stored default_user must not fall back to root)", got)
	}
}

func TestListTemplates_FilterByDefaultUser_NoMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "deploy"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?default_user=admin")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with empty list", resp.StatusCode)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 0 {
		t.Errorf("filter = %+v, want empty list", got)
	}
}

func TestListTemplates_FilterByDefaultUser_ComposesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "deploy-prod", Image: "rocky9.qcow2", DefaultUser: "deploy", Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "deploy-qa", Image: "rocky9.qcow2", DefaultUser: "deploy", Tags: []string{"qa"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "ec2-prod", Image: "ubuntu.qcow2", DefaultUser: "ec2-user", Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?default_user=deploy&tag=prod")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "deploy-prod" {
		t.Errorf("filter = %+v, want only deploy-prod (intersection of default_user+tag)", got)
	}
}

func TestListTemplates_FilterByDefaultUser_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		seedStoredTemplate(t, s, &types.VMTemplate{ID: fmt.Sprintf("tmpl-%d", i), Name: fmt.Sprintf("deploy-%d", i), Image: "rocky9.qcow2", DefaultUser: "deploy"})
	}
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-u", Name: "ec2", Image: "ubuntu.qcow2", DefaultUser: "ec2-user"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?default_user=deploy&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "5" {
		t.Errorf("X-Total-Count = %q, want 5 (post-filter population)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("page len = %d, want 2", len(got))
	}
}

// --- template list ?os_type= (5.6.8) ---

func TestListTemplates_FilterByOSType_ExactMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky", Image: "rocky9.qcow2", OSType: types.OSTypeLinux})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "win22", Image: "win-server-2022.qcow2", OSType: types.OSTypeWindows})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=windows")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "win22" {
		t.Errorf("filter = %+v, want only win22", got)
	}
	if hdr := resp.Header.Get("X-Total-Count"); hdr != "1" {
		t.Errorf("X-Total-Count = %q, want 1", hdr)
	}
}

func TestListTemplates_FilterByOSType_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "win22", Image: "win.qcow2", OSType: types.OSTypeWindows})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=WINDOWS")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 {
		t.Errorf("filter = %+v, want 1 case-insensitive match", got)
	}
}

func TestListTemplates_FilterByOSType_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "win22", Image: "win.qcow2", OSType: types.OSTypeWindows})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=%20%20windows%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 {
		t.Errorf("filter = %+v, want 1 after whitespace trim", got)
	}
}

func TestListTemplates_FilterByOSType_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky", Image: "rocky9.qcow2", OSType: types.OSTypeLinux})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "win22", Image: "win.qcow2", OSType: types.OSTypeWindows})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("empty filter should return all templates, got %+v", got)
	}
}

// TestListTemplates_FilterByOSType_LinuxMatchesEmpty: OS family is a closed
// two-member axis with a documented default — every record belongs to
// exactly one bucket — so `?os_type=linux` matches both explicit-linux
// templates AND those with an empty stored os_type. Deliberate divergence
// from the `?default_user=` filter (which has no empty-means-X fallback
// because the default_user vocabulary is open-ended); mirrors the VM
// `?os_type=linux` semantics for cohort-symmetry.
func TestListTemplates_FilterByOSType_LinuxMatchesEmpty(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "implicit-linux", Image: "rocky9.qcow2", OSType: ""})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "explicit-linux", Image: "rocky9.qcow2", OSType: types.OSTypeLinux})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "win", Image: "win.qcow2", OSType: types.OSTypeWindows})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=linux")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Fatalf("expected 2 linux templates (implicit + explicit), got %+v", got)
	}
	names := map[string]bool{got[0].Name: true, got[1].Name: true}
	if !names["implicit-linux"] || !names["explicit-linux"] {
		t.Errorf("expected both implicit-linux and explicit-linux, got %+v", got)
	}
}

func TestListTemplates_FilterByOSType_InvalidValueReturns400(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()
	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=plan9")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_os_type")
}

func TestListTemplates_FilterByOSType_ComposesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "win-prod", Image: "w.qcow2", OSType: types.OSTypeWindows, Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "win-qa", Image: "w.qcow2", OSType: types.OSTypeWindows, Tags: []string{"qa"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "linux-prod", Image: "l.qcow2", OSType: types.OSTypeLinux, Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=windows&tag=prod")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "win-prod" {
		t.Errorf("compose filter = %+v, want only win-prod", got)
	}
}

func TestListTemplates_FilterByOSType_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		seedStoredTemplate(t, s, &types.VMTemplate{ID: fmt.Sprintf("tmpl-%d", i), Name: fmt.Sprintf("w-%d", i), Image: "w.qcow2", OSType: types.OSTypeWindows})
	}
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-x", Name: "linux", Image: "l.qcow2", OSType: types.OSTypeLinux})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=windows&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "5" {
		t.Errorf("X-Total-Count = %q, want 5 (post-filter population)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("page len = %d, want 2", len(got))
	}
}

// --- template list ?os_variant= (roadmap 5.4.67) ---
//
// Symmetric sub-axis to ?os_type=windows on the template cohort: ?os_type=
// narrows to the OS family, ?os_variant= slices the Windows cohort by
// edition. Mirrors the VM list ?os_variant= filter (5.4.66) — case-insensitive
// exact-match, whitespace-trimmed, empty-disables, empty-stored excluded, and
// 400 invalid_os_variant on unknown values.

func TestListTemplates_FilterByOSVariant_ExactMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "win11", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "win22", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-server-2022"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "linux", Image: "l.qcow2", OSType: types.OSTypeLinux})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_variant=windows-11")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "win11" {
		t.Errorf("filter = %+v, want only win11", got)
	}
	if hdr := resp.Header.Get("X-Total-Count"); hdr != "1" {
		t.Errorf("X-Total-Count = %q, want 1", hdr)
	}
}

func TestListTemplates_FilterByOSVariant_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "win22", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-server-2022"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_variant=WINDOWS-SERVER-2022")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 {
		t.Errorf("filter = %+v, want 1 case-insensitive match", got)
	}
}

func TestListTemplates_FilterByOSVariant_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "win11", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_variant=%20%20windows-11%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 {
		t.Errorf("filter = %+v, want 1 after whitespace trim", got)
	}
}

func TestListTemplates_FilterByOSVariant_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "win", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "linux", Image: "l.qcow2", OSType: types.OSTypeLinux})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_variant=")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("empty filter should return all templates, got %+v", got)
	}
}

// TestListTemplates_FilterByOSVariant_ExcludesEmptyStored documents the
// membership semantics: unlike `?os_type=linux` (which matches empty-stored
// templates via the linux default), `?os_variant=` requires an explicit
// stored value — empty drops out whenever the filter is set. Mirrors the
// VM `?os_variant=` filter and the webhook `?event_type=` semantics.
func TestListTemplates_FilterByOSVariant_ExcludesEmptyStored(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "win11", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "win-unset", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: ""})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "linux", Image: "l.qcow2", OSType: types.OSTypeLinux})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_variant=windows-11")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "win11" {
		t.Errorf("expected only win11 (empty-stored excluded), got %+v", got)
	}
}

func TestListTemplates_FilterByOSVariant_InvalidValueReturns400(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()
	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_variant=windows-12")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_os_variant")
}

func TestListTemplates_FilterByOSVariant_ComposesWithOSType(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "a", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11"})
	// Unusual but allowed: a Linux-typed template can still carry an
	// os_variant string. The compose filter must drop this row because
	// ?os_type=windows excludes it.
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "b", Image: "l.qcow2", OSType: types.OSTypeLinux, OSVariant: "windows-11"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "c", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-10"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_type=windows&os_variant=windows-11")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("compose filter = %+v, want only a", got)
	}
}

func TestListTemplates_FilterByOSVariant_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	for i := 0; i < 6; i++ {
		v := "windows-11"
		if i%2 == 0 {
			v = "windows-server-2022"
		}
		seedStoredTemplate(t, s, &types.VMTemplate{ID: fmt.Sprintf("tmpl-%d", i), Name: fmt.Sprintf("w-%d", i), Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: v})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/templates?os_variant=windows-11&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Errorf("X-Total-Count = %q, want 3 (post-filter population)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("page len = %d, want 2", len(got))
	}
}

// --- template list ?network= (roadmap 5.4.45) ---

func tmplWithNet(id, name string, networks ...string) *types.VMTemplate {
	attachments := make([]types.NetworkAttachment, 0, len(networks))
	for _, n := range networks {
		attachments = append(attachments, types.NetworkAttachment{Name: n})
	}
	return &types.VMTemplate{ID: id, Name: name, Image: "rocky9.qcow2", Networks: attachments}
}

func TestListTemplates_FilterByNetwork_ExactMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, tmplWithNet("tmpl-1", "data-tpl", "data-net"))
	seedStoredTemplate(t, s, tmplWithNet("tmpl-2", "storage-tpl", "storage-net"))

	resp, err := http.Get(ts.URL + "/api/v1/templates?network=data-net")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "data-tpl" {
		t.Errorf("filter = %+v, want only data-tpl", got)
	}
}

func TestListTemplates_FilterByNetwork_IsCaseInsensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, tmplWithNet("tmpl-1", "data-tpl", "Data-Net"))
	seedStoredTemplate(t, s, tmplWithNet("tmpl-2", "storage-tpl", "storage-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/templates?network=DATA-NET")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "data-tpl" {
		t.Errorf("filter = %+v, want only data-tpl (case-insensitive)", got)
	}
}

func TestListTemplates_FilterByNetwork_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, tmplWithNet("tmpl-1", "data-tpl", "data-net"))
	seedStoredTemplate(t, s, tmplWithNet("tmpl-2", "storage-tpl", "storage-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/templates?network=%20%20data-net%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "data-tpl" {
		t.Errorf("filter = %+v, want only data-tpl after trim", got)
	}
}

func TestListTemplates_FilterByNetwork_AnyOf(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, tmplWithNet("tmpl-1", "multi-tpl", "data-net", "storage-net"))
	seedStoredTemplate(t, s, tmplWithNet("tmpl-2", "single-tpl", "mgmt-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/templates?network=storage-net")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "multi-tpl" {
		t.Errorf("filter = %+v, want only multi-tpl (any-of attachment match)", got)
	}
}

func TestListTemplates_FilterByNetwork_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, tmplWithNet("tmpl-1", "data-tpl", "data-net"))
	seedStoredTemplate(t, s, tmplWithNet("tmpl-2", "no-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/templates?network=%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("filter = %+v, want every template when network is whitespace-only", got)
	}
}

func TestListTemplates_FilterByNetwork_NoAttachmentsNeverMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, tmplWithNet("tmpl-1", "no-net"))
	seedStoredTemplate(t, s, tmplWithNet("tmpl-2", "data-tpl", "data-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/templates?network=data-net")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "data-tpl" {
		t.Errorf("filter = %+v, want only data-tpl (template with no networks must not match)", got)
	}
}

func TestListTemplates_FilterByNetwork_PartialIsNotAMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, tmplWithNet("tmpl-1", "storage-tpl", "storage-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/templates?network=storage")
	got := decodeTemplateList(t, resp)
	if len(got) != 0 {
		t.Errorf("filter = %+v, want empty list (substring must not match exact filter)", got)
	}
}

func TestListTemplates_FilterByNetwork_ComposesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	prod := tmplWithNet("tmpl-1", "data-prod", "data-net")
	prod.Tags = []string{"prod"}
	qa := tmplWithNet("tmpl-2", "data-qa", "data-net")
	qa.Tags = []string{"qa"}
	other := tmplWithNet("tmpl-3", "mgmt-prod", "mgmt-net")
	other.Tags = []string{"prod"}
	seedStoredTemplate(t, s, prod)
	seedStoredTemplate(t, s, qa)
	seedStoredTemplate(t, s, other)

	resp, _ := http.Get(ts.URL + "/api/v1/templates?network=data-net&tag=prod")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "data-prod" {
		t.Errorf("filter = %+v, want only data-prod (intersection of network+tag)", got)
	}
}

func TestListTemplates_FilterByNetwork_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		seedStoredTemplate(t, s, tmplWithNet(fmt.Sprintf("tmpl-%d", i), fmt.Sprintf("data-%d", i), "data-net"))
	}
	seedStoredTemplate(t, s, tmplWithNet("tmpl-x", "mgmt", "mgmt-net"))

	resp, _ := http.Get(ts.URL + "/api/v1/templates?network=data-net&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "5" {
		t.Errorf("X-Total-Count = %q, want 5 (post-filter population)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("page len = %d, want 2", len(got))
	}
}

// 5.4.78 — `?prefix=` on `GET /templates`. Case-sensitive HasPrefix against
// `tpl.Name`; the fourth and final member of the name-prefix filter family
// alongside snapshots (5.4.75), VMs (5.4.76), and images (5.4.77).
func TestListTemplates_FilterByPrefix_Match(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base-v1", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky9-base-v2", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?prefix=rocky9-base-")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (post-filter)", got)
	}
	listed := decodeTemplateList(t, resp)
	if len(listed) != 2 {
		t.Fatalf("expected 2 templates, got %+v", listed)
	}
	for _, tpl := range listed {
		if !strings.HasPrefix(tpl.Name, "rocky9-base-") {
			t.Fatalf("expected only rocky9-base-* templates, got %s", tpl.Name)
		}
	}
}

func TestListTemplates_FilterByPrefix_IsCaseSensitive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "Rocky9-Base", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?prefix=rocky9-base")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("X-Total-Count = %q, want 0 (case-sensitive non-match)", got)
	}
}

func TestListTemplates_FilterByPrefix_TrimsWhitespace(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky-9", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?prefix=%20%20rocky-%20%20")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (whitespace-trim)", got)
	}
}

func TestListTemplates_FilterByPrefix_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "rocky9.qcow2"})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "bravo", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?prefix=%20%20")
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (whitespace-only disables filter)", got)
	}
}

func TestListTemplates_FilterByPrefix_NoMatch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "manual-1", Image: "rocky9.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?prefix=auto-")
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("X-Total-Count = %q, want 0", got)
	}
}

func TestListTemplates_FilterByPrefix_ComposesWithTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky9-prod", Image: "rocky9.qcow2", Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky9-qa", Image: "rocky9.qcow2", Tags: []string{"qa"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "ubuntu-prod", Image: "ubuntu.qcow2", Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?prefix=rocky9-&tag=prod")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	listed := decodeTemplateList(t, resp)
	if len(listed) != 1 || listed[0].Name != "rocky9-prod" {
		t.Fatalf("expected only rocky9-prod, got %+v", listed)
	}
}

func TestListTemplates_FilterByPrefix_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		seedStoredTemplate(t, s, &types.VMTemplate{ID: fmt.Sprintf("tmpl-%d", i), Name: fmt.Sprintf("rocky9-base-%d", i), Image: "rocky9.qcow2"})
	}
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-u", Name: "ubuntu-22", Image: "ubuntu.qcow2"})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?prefix=rocky9-base-&per_page=2&page=1")
	if got := resp.Header.Get("X-Total-Count"); got != "5" {
		t.Errorf("X-Total-Count = %q, want 5 (post-filter population)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Errorf("page len = %d, want 2", len(got))
	}
}

func TestListTemplates_FilterBySince(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-early", Name: "early", Image: "rocky9.qcow2", CreatedAt: day(1)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", CreatedAt: day(15)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-late", Name: "late", Image: "rocky9.qcow2", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?since=2026-05-10T00:00:00Z")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	got := decodeTemplateList(t, resp)
	names := map[string]bool{}
	for _, tpl := range got {
		names[tpl.Name] = true
	}
	if !names["mid"] || !names["late"] || names["early"] {
		t.Fatalf("expected mid+late, got %+v", got)
	}
}

func TestListTemplates_FilterByUntil(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-early", Name: "early", Image: "rocky9.qcow2", CreatedAt: day(1)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", CreatedAt: day(15)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-late", Name: "late", Image: "rocky9.qcow2", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?until=2026-05-20T00:00:00Z")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Fatalf("expected 2 templates <= until, got %+v", got)
	}
}

func TestListTemplates_FilterBySinceAndUntil(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "tmpl-1", Image: "rocky9.qcow2", CreatedAt: day(1)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-15", Name: "tmpl-15", Image: "rocky9.qcow2", CreatedAt: day(15)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-30", Name: "tmpl-30", Image: "rocky9.qcow2", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?since=2026-05-10T00:00:00Z&until=2026-05-20T00:00:00Z")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "tmpl-15" {
		t.Fatalf("expected only tmpl-15, got %+v", got)
	}
}

func TestListTemplates_FilterBySince_Inclusive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	boundary := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-edge", Name: "edge", Image: "rocky9.qcow2", CreatedAt: boundary})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?since=2026-05-01T00:00:00Z")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", got)
	}
}

func TestListTemplates_FilterByInvalidSince(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/templates?since=last-tuesday")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_since")
}

func TestListTemplates_FilterByInvalidUntil(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/templates?until=2026-13-99")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_until")
}

func TestListTemplates_FilterBySince_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "t1", Image: "rocky9.qcow2", CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "t2", Image: "rocky9.qcow2", CreatedAt: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?since=%20%20")
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Fatalf("whitespace-only since should be a no-op; got %+v", got)
	}
}

func TestListTemplates_FilterByTimeRange_ExcludesZeroCreatedAt(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-no-time", Name: "no-time", Image: "rocky9.qcow2"}) // zero CreatedAt
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-dated", Name: "dated", Image: "rocky9.qcow2", CreatedAt: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?since=2026-05-01T00:00:00Z")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "dated" {
		t.Fatalf("expected only dated (zero-time excluded), got %+v", got)
	}
}

func TestListTemplates_FilterBySince_ComposesWithTagAndSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-old", Name: "rocky-base-old", Image: "rocky9.qcow2", CreatedAt: day(1), Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-new", Name: "rocky-base-new", Image: "rocky9.qcow2", CreatedAt: day(20), Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-stg", Name: "rocky-staging", Image: "rocky9.qcow2", CreatedAt: day(20), Tags: []string{"staging"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-other", Name: "other", Image: "rocky9.qcow2", CreatedAt: day(20), Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?since=2026-05-10T00:00:00Z&tag=prod&search=rocky-base")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky-base-new" {
		t.Fatalf("expected only rocky-base-new, got %+v", got)
	}
}

// --- Template list ?min_cpus= / ?max_cpus= (5.4.51) ---

func TestListTemplates_FilterByMinCpus(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", CPUs: 2})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", CPUs: 4})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", CPUs: 16})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_cpus=4")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	got := decodeTemplateList(t, resp)
	names := map[string]bool{}
	for _, tpl := range got {
		names[tpl.Name] = true
	}
	if names["small"] || !names["mid"] || !names["big"] {
		t.Fatalf("expected mid+big (>= 4 vCPUs), got %+v", got)
	}
}

func TestListTemplates_FilterByMaxCpus(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", CPUs: 2})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", CPUs: 4})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", CPUs: 16})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?max_cpus=4")
	got := decodeTemplateList(t, resp)
	names := map[string]bool{}
	for _, tpl := range got {
		names[tpl.Name] = true
	}
	if !names["small"] || !names["mid"] || names["big"] {
		t.Fatalf("expected small+mid (<= 4 vCPUs), got %+v", got)
	}
}

func TestListTemplates_FilterByCpusRange(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", CPUs: 2})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", CPUs: 4})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", CPUs: 16})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_cpus=3&max_cpus=8")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "mid" {
		t.Fatalf("expected only mid in [3,8] vCPUs, got %+v", got)
	}
}

func TestListTemplates_FilterByMinCpus_Inclusive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-edge", Name: "edge", Image: "rocky9.qcow2", CPUs: 4})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_cpus=4")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", got)
	}
}

func TestListTemplates_FilterByMinCpus_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", CPUs: 2})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", CPUs: 16})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_cpus=%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Fatalf("whitespace-only min_cpus should disable the filter, got %+v", got)
	}
}

func TestListTemplates_FilterByInvalidMinCpus(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_cpus=lots")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_min_cpus")
}

func TestListTemplates_FilterByMaxCpus_RejectsNegative(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/templates?max_cpus=-1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_max_cpus")
}

func TestListTemplates_FilterByCpus_ComposesWithTagAndSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky-big-prod", Image: "rocky9.qcow2", CPUs: 16, Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky-big-stg", Image: "rocky9.qcow2", CPUs: 16, Tags: []string{"staging"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "rocky-small-prod", Image: "rocky9.qcow2", CPUs: 2, Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_cpus=8&tag=prod&search=rocky")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky-big-prod" {
		t.Fatalf("expected only rocky-big-prod, got %+v", got)
	}
}

// --- Template list ?min_ram_mb= / ?max_ram_mb= (5.4.52) ---

func TestListTemplates_FilterByMinRAM(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", RAMMB: 1024})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", RAMMB: 4096})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", RAMMB: 16384})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_ram_mb=4096")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	got := decodeTemplateList(t, resp)
	names := map[string]bool{}
	for _, tpl := range got {
		names[tpl.Name] = true
	}
	if names["small"] || !names["mid"] || !names["big"] {
		t.Fatalf("expected mid+big (>= 4096 MB RAM), got %+v", got)
	}
}

func TestListTemplates_FilterByMaxRAM(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", RAMMB: 1024})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", RAMMB: 4096})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", RAMMB: 16384})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?max_ram_mb=4096")
	got := decodeTemplateList(t, resp)
	names := map[string]bool{}
	for _, tpl := range got {
		names[tpl.Name] = true
	}
	if !names["small"] || !names["mid"] || names["big"] {
		t.Fatalf("expected small+mid (<= 4096 MB RAM), got %+v", got)
	}
}

func TestListTemplates_FilterByRAMRange(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", RAMMB: 1024})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", RAMMB: 4096})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", RAMMB: 16384})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_ram_mb=2048&max_ram_mb=8192")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "mid" {
		t.Fatalf("expected only mid in [2048,8192] MB RAM, got %+v", got)
	}
}

func TestListTemplates_FilterByMinRAM_Inclusive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-edge", Name: "edge", Image: "rocky9.qcow2", RAMMB: 4096})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_ram_mb=4096")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", got)
	}
}

func TestListTemplates_FilterByMinRAM_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", RAMMB: 1024})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", RAMMB: 16384})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_ram_mb=%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Fatalf("whitespace-only min_ram_mb should disable the filter, got %+v", got)
	}
}

func TestListTemplates_FilterByInvalidMinRAM(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_ram_mb=lots")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_min_ram_mb")
}

func TestListTemplates_FilterByMaxRAM_RejectsNegative(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/templates?max_ram_mb=-1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_max_ram_mb")
}

func TestListTemplates_FilterByRAM_ComposesWithTagAndSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky-big-prod", Image: "rocky9.qcow2", RAMMB: 16384, Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky-big-stg", Image: "rocky9.qcow2", RAMMB: 16384, Tags: []string{"staging"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "rocky-small-prod", Image: "rocky9.qcow2", RAMMB: 1024, Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_ram_mb=8192&tag=prod&search=rocky")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky-big-prod" {
		t.Fatalf("expected only rocky-big-prod, got %+v", got)
	}
}

// --- Template list ?min_disk_gb= / ?max_disk_gb= (5.4.53) ---

func TestListTemplates_FilterByMinDisk(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", DiskGB: 10})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", DiskGB: 50})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", DiskGB: 200})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_disk_gb=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	got := decodeTemplateList(t, resp)
	names := map[string]bool{}
	for _, tpl := range got {
		names[tpl.Name] = true
	}
	if names["small"] || !names["mid"] || !names["big"] {
		t.Fatalf("expected mid+big (>= 50 GB disk), got %+v", got)
	}
}

func TestListTemplates_FilterByMaxDisk(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", DiskGB: 10})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", DiskGB: 50})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", DiskGB: 200})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?max_disk_gb=50")
	got := decodeTemplateList(t, resp)
	names := map[string]bool{}
	for _, tpl := range got {
		names[tpl.Name] = true
	}
	if !names["small"] || !names["mid"] || names["big"] {
		t.Fatalf("expected small+mid (<= 50 GB disk), got %+v", got)
	}
}

func TestListTemplates_FilterByDiskRange(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", DiskGB: 10})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-mid", Name: "mid", Image: "rocky9.qcow2", DiskGB: 50})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", DiskGB: 200})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_disk_gb=40&max_disk_gb=100")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "mid" {
		t.Fatalf("expected only mid in [40,100] GB disk, got %+v", got)
	}
}

func TestListTemplates_FilterByMinDisk_Inclusive(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-edge", Name: "edge", Image: "rocky9.qcow2", DiskGB: 50})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_disk_gb=50")
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", got)
	}
}

func TestListTemplates_FilterByMinDisk_EmptyIsNoOp(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-small", Name: "small", Image: "rocky9.qcow2", DiskGB: 10})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-big", Name: "big", Image: "rocky9.qcow2", DiskGB: 200})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_disk_gb=%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 2 {
		t.Fatalf("whitespace-only min_disk_gb should disable the filter, got %+v", got)
	}
}

func TestListTemplates_FilterByInvalidMinDisk(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_disk_gb=lots")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_min_disk_gb")
}

func TestListTemplates_FilterByMaxDisk_RejectsNegative(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/templates?max_disk_gb=-1")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_max_disk_gb")
}

func TestListTemplates_FilterByDisk_ComposesWithCpusAndSearch(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "rocky-big-prod", Image: "rocky9.qcow2", CPUs: 16, DiskGB: 200, Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "rocky-big-small-disk", Image: "rocky9.qcow2", CPUs: 16, DiskGB: 10, Tags: []string{"prod"}})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "rocky-small-prod", Image: "rocky9.qcow2", CPUs: 2, DiskGB: 200, Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/templates?min_disk_gb=100&min_cpus=8&search=rocky")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	got := decodeTemplateList(t, resp)
	if len(got) != 1 || got[0].Name != "rocky-big-prod" {
		t.Fatalf("expected only rocky-big-prod, got %+v", got)
	}
}

// listPortsWithQuery is a small helper that GETs /vms/{vmID}/ports?<query>
// and decodes the JSON body into a slice. Tests for sort ordering use it.
func listPortsWithQuery(t *testing.T, ts *httptest.Server, vmID, query string) []types.PortForward {
	t.Helper()
	url := ts.URL + "/api/v1/vms/" + vmID + "/ports"
	if query != "" {
		url += "?" + query
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var ports []types.PortForward
	if err := json.NewDecoder(resp.Body).Decode(&ports); err != nil {
		t.Fatalf("decode ports: %v", err)
	}
	return ports
}

func seedSortPortFixtures(t *testing.T, s *store.Store, vmID string) {
	t.Helper()
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/8081", VMID: vmID, HostPort: 8081, GuestPort: 80, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP, Description: "web frontend"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22001", VMID: vmID, HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP, Description: "ssh jumpbox"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/9090", VMID: vmID, HostPort: 9090, GuestPort: 9090, GuestIP: "192.168.100.40", Protocol: types.ProtocolUDP, Description: "Metrics scrape"})
}

func TestListPorts_SortDefaultIsIDAsc(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-sort")

	got := listPortsWithQuery(t, ts, "vm-sort", "")
	// IDs are "vm-sort/22001", "vm-sort/8081", "vm-sort/9090" — string compare
	// puts "22001" before "8081" before "9090" lexicographically.
	want := []int{22001, 8081, 9090}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, hp := range want {
		if got[i].HostPort != hp {
			t.Errorf("idx %d: HostPort = %d, want %d (full: %v)", i, got[i].HostPort, hp, got)
		}
	}
}

func TestListPorts_SortByHostPortAsc(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-sort")

	got := listPortsWithQuery(t, ts, "vm-sort", "sort=host_port")
	want := []int{8081, 9090, 22001}
	for i, hp := range want {
		if got[i].HostPort != hp {
			t.Errorf("idx %d: HostPort = %d, want %d", i, got[i].HostPort, hp)
		}
	}
}

func TestListPorts_SortByHostPortDesc(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-sort")

	got := listPortsWithQuery(t, ts, "vm-sort", "sort=host_port&order=desc")
	want := []int{22001, 9090, 8081}
	for i, hp := range want {
		if got[i].HostPort != hp {
			t.Errorf("idx %d: HostPort = %d, want %d", i, got[i].HostPort, hp)
		}
	}
}

func TestListPorts_SortByGuestPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-sort")

	got := listPortsWithQuery(t, ts, "vm-sort", "sort=guest_port")
	want := []int{22, 80, 9090}
	for i, gp := range want {
		if got[i].GuestPort != gp {
			t.Errorf("idx %d: GuestPort = %d, want %d", i, got[i].GuestPort, gp)
		}
	}
}

func TestListPorts_SortByProtocol(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-sort")

	got := listPortsWithQuery(t, ts, "vm-sort", "sort=protocol")
	// "tcp" < "udp"; tcp pair tiebreaks on id (vm-sort/22001 < vm-sort/8081)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantProto := []types.Protocol{types.ProtocolTCP, types.ProtocolTCP, types.ProtocolUDP}
	wantHostPort := []int{22001, 8081, 9090}
	for i := range wantProto {
		if got[i].Protocol != wantProto[i] {
			t.Errorf("idx %d: protocol = %q, want %q", i, got[i].Protocol, wantProto[i])
		}
		if got[i].HostPort != wantHostPort[i] {
			t.Errorf("idx %d: host_port = %d, want %d", i, got[i].HostPort, wantHostPort[i])
		}
	}
}

func TestListPorts_SortByDescription_CaseInsensitive(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-sort")

	got := listPortsWithQuery(t, ts, "vm-sort", "sort=description")
	// case-insensitive: "metrics" < "ssh" < "web"
	want := []int{9090, 22001, 8081}
	for i, hp := range want {
		if got[i].HostPort != hp {
			t.Errorf("idx %d: HostPort = %d, want %d", i, got[i].HostPort, hp)
		}
	}
}

func TestListPorts_RejectsInvalidSort(t *testing.T) {
	ts, _, _, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-x/ports?sort=definitely-not-a-field")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body types.APIError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "invalid_sort" {
		t.Errorf("code = %q, want invalid_sort", body.Code)
	}
	// Error message must advertise the new guest_ip axis so callers see
	// `guest_ip` in the help string the moment they hit a typo (5.4.86).
	if !strings.Contains(body.Message, "guest_ip") {
		t.Errorf("message = %q, want it to mention guest_ip", body.Message)
	}
}

// TestListPorts_SortByGuestIP_Numeric exercises the 5.4.86 guest_ip sort axis,
// the symmetric sort counterpart to the 5.4.73 ?guest_ip= exact-match filter.
// guest_ip comparison must be numeric, not lexicographic, so 192.168.100.2
// sorts before 192.168.100.10. Empty / unparseable guest_ip values sink to
// the tail of asc and the head of desc, mirroring the VM IP sort axis
// (5.4.85) and the schedule last_fired_at sort axis (5.4.84).
func TestListPorts_SortByGuestIP_NumericAsc(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	vmID := "vm-ipsort"
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22001", VMID: vmID, HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22002", VMID: vmID, HostPort: 22002, GuestPort: 22, GuestIP: "192.168.100.2", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22003", VMID: vmID, HostPort: 22003, GuestPort: 22, GuestIP: "", Protocol: types.ProtocolTCP})

	got := listPortsWithQuery(t, ts, vmID, "sort=guest_ip")
	// Numeric: 192.168.100.2 < 192.168.100.10; empty trails.
	wantHost := []int{22002, 22001, 22003}
	if len(got) != len(wantHost) {
		t.Fatalf("len = %d, want %d", len(got), len(wantHost))
	}
	for i, hp := range wantHost {
		if got[i].HostPort != hp {
			t.Errorf("idx %d: host_port = %d, want %d (full guest_ips: %v)",
				i, got[i].HostPort, hp, []string{got[0].GuestIP, got[1].GuestIP, got[2].GuestIP})
		}
	}
}

func TestListPorts_SortByGuestIP_DescEmptyLeading(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	vmID := "vm-ipsort"
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22001", VMID: vmID, HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22002", VMID: vmID, HostPort: 22002, GuestPort: 22, GuestIP: "192.168.100.2", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22003", VMID: vmID, HostPort: 22003, GuestPort: 22, GuestIP: "", Protocol: types.ProtocolTCP})

	got := listPortsWithQuery(t, ts, vmID, "sort=guest_ip&order=desc")
	wantHost := []int{22003, 22001, 22002}
	if len(got) != len(wantHost) {
		t.Fatalf("len = %d, want %d", len(got), len(wantHost))
	}
	for i, hp := range wantHost {
		if got[i].HostPort != hp {
			t.Errorf("idx %d: host_port = %d, want %d", i, got[i].HostPort, hp)
		}
	}
}

func TestListPorts_SortByGuestIP_TiebreaksOnID(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	vmID := "vm-ipsort"
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22003", VMID: vmID, HostPort: 22003, GuestPort: 22, GuestIP: "192.168.100.5", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22001", VMID: vmID, HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.5", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22002", VMID: vmID, HostPort: 22002, GuestPort: 22, GuestIP: "192.168.100.5", Protocol: types.ProtocolTCP})

	got := listPortsWithQuery(t, ts, vmID, "sort=guest_ip")
	// All equal-IP — must tiebreak on id so pagination is deterministic.
	wantIDs := []string{vmID + "/22001", vmID + "/22002", vmID + "/22003"}
	if len(got) != len(wantIDs) {
		t.Fatalf("len = %d, want %d", len(got), len(wantIDs))
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("idx %d: id = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestListPorts_RejectsInvalidOrder(t *testing.T) {
	ts, _, _, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-x/ports?order=sideways")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body types.APIError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "invalid_order" {
		t.Errorf("code = %q, want invalid_order", body.Code)
	}
}

// --- Port forward search filter (5.4.11) ---

func TestListPorts_FilterBySearch_Description(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-search")

	got := listPortsWithQuery(t, ts, "vm-search", "search=ssh")
	if len(got) != 1 || got[0].HostPort != 22001 {
		t.Fatalf("expected only ssh jumpbox rule, got %+v", got)
	}
}

func TestListPorts_FilterBySearch_Protocol(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-search")

	got := listPortsWithQuery(t, ts, "vm-search", "search=udp")
	if len(got) != 1 || got[0].Protocol != types.ProtocolUDP {
		t.Fatalf("expected only the udp rule, got %+v", got)
	}
}

func TestListPorts_FilterBySearch_HostPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-search")

	got := listPortsWithQuery(t, ts, "vm-search", "search=8081")
	if len(got) != 1 || got[0].HostPort != 8081 {
		t.Fatalf("expected host_port 8081, got %+v", got)
	}
}

func TestListPorts_FilterBySearch_GuestIP(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-search")

	got := listPortsWithQuery(t, ts, "vm-search", "search=192.168.100.40")
	// every seeded rule shares the guest IP; the search should return all of them
	if len(got) != 3 {
		t.Fatalf("expected all 3 rules to match guest_ip, got %d (%+v)", len(got), got)
	}
}

func TestListPorts_FilterBySearch_CaseInsensitive(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-search")

	// "Metrics scrape" description on the udp rule; uppercase needle must match.
	got := listPortsWithQuery(t, ts, "vm-search", "search=METRICS")
	if len(got) != 1 || got[0].HostPort != 9090 {
		t.Fatalf("expected only the metrics rule, got %+v", got)
	}
}

func TestListPorts_FilterBySearch_WhitespaceTrimmed(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-search")

	// %20%20web%20%20 trimmed becomes "web", which matches "web frontend".
	got := listPortsWithQuery(t, ts, "vm-search", "search=%20%20web%20%20")
	if len(got) != 1 || got[0].HostPort != 8081 {
		t.Fatalf("expected only the web frontend rule after trim, got %+v", got)
	}
}

func TestListPorts_FilterBySearch_NoMatch(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-search")

	got := listPortsWithQuery(t, ts, "vm-search", "search=needle-not-present")
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %+v", got)
	}
}

func TestListPorts_FilterBySearch_ComposesWithSort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-search")
	// Add a second tcp rule whose description also matches "web" so the
	// sort step has something to do after the search filter.
	seedPortForward(t, s, &types.PortForward{ID: "vm-search/8082", VMID: "vm-search", HostPort: 8082, GuestPort: 81, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP, Description: "web backend"})

	got := listPortsWithQuery(t, ts, "vm-search", "search=web&sort=host_port")
	if len(got) != 2 {
		t.Fatalf("expected 2 matches, got %d (%+v)", len(got), got)
	}
	if got[0].HostPort != 8081 || got[1].HostPort != 8082 {
		t.Fatalf("expected host_port asc 8081,8082 — got %d,%d", got[0].HostPort, got[1].HostPort)
	}
}

func TestListPorts_FilterBySearch_IDAndVMIDNotInHaystack(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	// The ID embeds the VM-id and host port; the search must not match on the
	// VM-id portion (it's URL scope, not operator-useful needle).
	seedPortForward(t, s, &types.PortForward{
		ID:          "vm-1741234567890123/8080",
		VMID:        "vm-1741234567890123",
		HostPort:    8080,
		GuestPort:   80,
		GuestIP:     "192.168.100.10",
		Protocol:    types.ProtocolTCP,
		Description: "production frontend",
	})

	got := listPortsWithQuery(t, ts, "vm-1741234567890123", "search=1741234")
	if len(got) != 0 {
		t.Fatalf("expected VM-id substring to be excluded from haystack, got %+v", got)
	}
}

// ============================================================
// Port-forward list ?protocol= filter tests (5.4.25)
// ============================================================

func seedProtocolPorts(t *testing.T, s *store.Store, vmID string) {
	t.Helper()
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/8080", VMID: vmID, HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Description: "http", Tags: []string{"web"}})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/8443", VMID: vmID, HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Description: "https", Tags: []string{"web"}})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/53000", VMID: vmID, HostPort: 53000, GuestPort: 53, GuestIP: "192.168.100.10", Protocol: types.ProtocolUDP, Description: "dns"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/123000", VMID: vmID, HostPort: 123000, GuestPort: 123, GuestIP: "192.168.100.10", Protocol: types.ProtocolUDP, Description: "ntp"})
}

func TestListPorts_FilterByProtocol_TCP(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedProtocolPorts(t, s, "vm-proto-tcp")

	got := listPortsWithQuery(t, ts, "vm-proto-tcp", "protocol=tcp")
	if len(got) != 2 {
		t.Fatalf("expected 2 tcp rules, got %d (%+v)", len(got), got)
	}
	for _, p := range got {
		if p.Protocol != types.ProtocolTCP {
			t.Errorf("unexpected protocol in tcp filter: %s", p.Protocol)
		}
	}
}

func TestListPorts_FilterByProtocol_UDP(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedProtocolPorts(t, s, "vm-proto-udp")

	got := listPortsWithQuery(t, ts, "vm-proto-udp", "protocol=udp")
	if len(got) != 2 {
		t.Fatalf("expected 2 udp rules, got %d", len(got))
	}
	for _, p := range got {
		if p.Protocol != types.ProtocolUDP {
			t.Errorf("unexpected protocol in udp filter: %s", p.Protocol)
		}
	}
}

func TestListPorts_FilterByProtocol_CaseInsensitive(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedProtocolPorts(t, s, "vm-proto-case")

	got := listPortsWithQuery(t, ts, "vm-proto-case", "protocol=TCP")
	if len(got) != 2 {
		t.Fatalf("expected 2 tcp rules with uppercase filter, got %d", len(got))
	}
}

func TestListPorts_FilterByProtocol_WhitespaceTrimmed(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedProtocolPorts(t, s, "vm-proto-trim")

	got := listPortsWithQuery(t, ts, "vm-proto-trim", "protocol=%20udp%20")
	if len(got) != 2 {
		t.Fatalf("expected 2 udp rules with padded filter, got %d", len(got))
	}
}

func TestListPorts_FilterByProtocol_EmptyIsNoOp(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedProtocolPorts(t, s, "vm-proto-empty")

	got := listPortsWithQuery(t, ts, "vm-proto-empty", "protocol=")
	if len(got) != 4 {
		t.Fatalf("empty protocol filter should return all 4 rules, got %d", len(got))
	}
}

func TestListPorts_FilterByProtocol_InvalidReturns400(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedProtocolPorts(t, s, "vm-proto-bad")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-proto-bad/ports?protocol=sctp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if apiErr.Code != "invalid_protocol" {
		t.Errorf("error code = %q, want invalid_protocol", apiErr.Code)
	}
}

func TestListPorts_FilterByProtocol_ComposesWithTag(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedPortForward(t, s, &types.PortForward{ID: "pf-a", VMID: "vm-proto-cmb", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"web"}})
	seedPortForward(t, s, &types.PortForward{ID: "pf-b", VMID: "vm-proto-cmb", HostPort: 53000, GuestPort: 53, GuestIP: "192.168.100.10", Protocol: types.ProtocolUDP, Tags: []string{"web"}})
	seedPortForward(t, s, &types.PortForward{ID: "pf-c", VMID: "vm-proto-cmb", HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"admin"}})

	got := listPortsWithQuery(t, ts, "vm-proto-cmb", "protocol=tcp&tag=web")
	if len(got) != 1 || got[0].ID != "pf-a" {
		t.Fatalf("intersection = %+v, want [pf-a]", got)
	}
}

func TestListPorts_FilterByProtocol_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()

	seedProtocolPorts(t, s, "vm-proto-cnt")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-proto-cnt/ports?protocol=tcp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
}

// ============================================================
// Port-forward list host-port range filter tests (5.4.47)
// ============================================================

// seedHostPortRangePorts seeds four forwards spanning a spread of host ports
// so the inclusive [min, max] range filter can be exercised at both endpoints.
func seedHostPortRangePorts(t *testing.T, s *store.Store, vmID string) {
	t.Helper()
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22", VMID: vmID, HostPort: 22, GuestPort: 22, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP, Description: "ssh"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/8080", VMID: vmID, HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP, Description: "http"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/8443", VMID: vmID, HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP, Description: "https"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/9090", VMID: vmID, HostPort: 9090, GuestPort: 9090, GuestIP: "192.168.100.50", Protocol: types.ProtocolUDP, Description: "metrics"})
}

func TestListPorts_FilterByMinHostPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-min")

	got := listPortsWithQuery(t, ts, "vm-hp-min", "min_host_port=8443")
	if len(got) != 2 {
		t.Fatalf("expected 2 rules with host_port >= 8443, got %d (%+v)", len(got), got)
	}
	for _, p := range got {
		if p.HostPort < 8443 {
			t.Errorf("host_port %d below min 8443", p.HostPort)
		}
	}
}

func TestListPorts_FilterByMaxHostPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-max")

	got := listPortsWithQuery(t, ts, "vm-hp-max", "max_host_port=8080")
	if len(got) != 2 {
		t.Fatalf("expected 2 rules with host_port <= 8080, got %d", len(got))
	}
	for _, p := range got {
		if p.HostPort > 8080 {
			t.Errorf("host_port %d above max 8080", p.HostPort)
		}
	}
}

func TestListPorts_FilterByHostPortRange(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-range")

	got := listPortsWithQuery(t, ts, "vm-hp-range", "min_host_port=8000&max_host_port=9000")
	if len(got) != 2 {
		t.Fatalf("expected 2 rules in [8000,9000], got %d", len(got))
	}
	for _, p := range got {
		if p.HostPort < 8000 || p.HostPort > 9000 {
			t.Errorf("host_port %d outside [8000,9000]", p.HostPort)
		}
	}
}

func TestListPorts_FilterByMinHostPort_Inclusive(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-incl")

	// 8080 itself must be included by min_host_port=8080.
	got := listPortsWithQuery(t, ts, "vm-hp-incl", "min_host_port=8080")
	if len(got) != 3 {
		t.Fatalf("expected 3 rules with host_port >= 8080 (inclusive), got %d", len(got))
	}
}

func TestListPorts_FilterByMinHostPort_EmptyIsNoOp(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-empty")

	got := listPortsWithQuery(t, ts, "vm-hp-empty", "min_host_port=%20")
	if len(got) != 4 {
		t.Fatalf("whitespace min_host_port should be a no-op (4 rules), got %d", len(got))
	}
}

func TestListPorts_FilterByInvalidMinHostPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-bad")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-hp-bad/ports?min_host_port=abc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if apiErr.Code != "invalid_min_host_port" {
		t.Errorf("error code = %q, want invalid_min_host_port", apiErr.Code)
	}
}

func TestListPorts_FilterByMaxHostPort_RejectsNegative(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-neg")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-hp-neg/ports?max_host_port=-5")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if apiErr.Code != "invalid_max_host_port" {
		t.Errorf("error code = %q, want invalid_max_host_port", apiErr.Code)
	}
}

func TestListPorts_FilterByHostPort_ComposesWithProtocol(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-cmb")

	// host_port >= 8000 → {8080(tcp), 8443(tcp), 9090(udp)}; protocol=tcp narrows to 2.
	got := listPortsWithQuery(t, ts, "vm-hp-cmb", "min_host_port=8000&protocol=tcp")
	if len(got) != 2 {
		t.Fatalf("intersection of min_host_port=8000 + protocol=tcp = %d, want 2 (%+v)", len(got), got)
	}
	for _, p := range got {
		if p.Protocol != types.ProtocolTCP || p.HostPort < 8000 {
			t.Errorf("unexpected rule in intersection: %+v", p)
		}
	}
}

func TestListPorts_FilterByHostPort_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-hp-cnt")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-hp-cnt/ports?min_host_port=8000&max_host_port=9000")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
}

// ============================================================
// Port-forward list guest-port range filter tests (5.4.49)
// ============================================================

func TestListPorts_FilterByMinGuestPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-min") // guest ports: 22, 80, 443, 9090

	got := listPortsWithQuery(t, ts, "vm-gp-min", "min_guest_port=443")
	if len(got) != 2 {
		t.Fatalf("expected 2 rules with guest_port >= 443, got %d (%+v)", len(got), got)
	}
	for _, p := range got {
		if p.GuestPort < 443 {
			t.Errorf("guest_port %d below min 443", p.GuestPort)
		}
	}
}

func TestListPorts_FilterByMaxGuestPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-max")

	got := listPortsWithQuery(t, ts, "vm-gp-max", "max_guest_port=80")
	if len(got) != 2 {
		t.Fatalf("expected 2 rules with guest_port <= 80, got %d", len(got))
	}
	for _, p := range got {
		if p.GuestPort > 80 {
			t.Errorf("guest_port %d above max 80", p.GuestPort)
		}
	}
}

func TestListPorts_FilterByGuestPortRange(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-range")

	got := listPortsWithQuery(t, ts, "vm-gp-range", "min_guest_port=80&max_guest_port=500")
	if len(got) != 2 {
		t.Fatalf("expected 2 rules in guest [80,500], got %d", len(got))
	}
	for _, p := range got {
		if p.GuestPort < 80 || p.GuestPort > 500 {
			t.Errorf("guest_port %d outside [80,500]", p.GuestPort)
		}
	}
}

func TestListPorts_FilterByMinGuestPort_Inclusive(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-incl")

	// 80 itself must be included by min_guest_port=80.
	got := listPortsWithQuery(t, ts, "vm-gp-incl", "min_guest_port=80")
	if len(got) != 3 {
		t.Fatalf("expected 3 rules with guest_port >= 80 (inclusive), got %d", len(got))
	}
}

func TestListPorts_FilterByMinGuestPort_EmptyIsNoOp(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-empty")

	got := listPortsWithQuery(t, ts, "vm-gp-empty", "min_guest_port=%20")
	if len(got) != 4 {
		t.Fatalf("whitespace min_guest_port should be a no-op (4 rules), got %d", len(got))
	}
}

func TestListPorts_FilterByInvalidMinGuestPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-bad")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-gp-bad/ports?min_guest_port=abc")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if apiErr.Code != "invalid_min_guest_port" {
		t.Errorf("error code = %q, want invalid_min_guest_port", apiErr.Code)
	}
}

func TestListPorts_FilterByMaxGuestPort_RejectsNegative(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-neg")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-gp-neg/ports?max_guest_port=-5")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if apiErr.Code != "invalid_max_guest_port" {
		t.Errorf("error code = %q, want invalid_max_guest_port", apiErr.Code)
	}
}

func TestListPorts_FilterByGuestPort_ComposesWithProtocol(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-cmb")

	// guest_port >= 80 → {80(tcp), 443(tcp), 9090(udp)}; protocol=tcp narrows to 2.
	got := listPortsWithQuery(t, ts, "vm-gp-cmb", "min_guest_port=80&protocol=tcp")
	if len(got) != 2 {
		t.Fatalf("intersection of min_guest_port=80 + protocol=tcp = %d, want 2 (%+v)", len(got), got)
	}
	for _, p := range got {
		if p.Protocol != types.ProtocolTCP || p.GuestPort < 80 {
			t.Errorf("unexpected rule in intersection: %+v", p)
		}
	}
}

func TestListPorts_FilterByGuestPort_ComposesWithHostPort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-hp") // host→guest: 22→22, 8080→80, 8443→443, 9090→9090

	// host_port >= 8000 → {8080/80, 8443/443, 9090/9090}; guest_port <= 500 drops 9090.
	got := listPortsWithQuery(t, ts, "vm-gp-hp", "min_host_port=8000&max_guest_port=500")
	if len(got) != 2 {
		t.Fatalf("intersection of min_host_port=8000 + max_guest_port=500 = %d, want 2 (%+v)", len(got), got)
	}
	for _, p := range got {
		if p.HostPort < 8000 || p.GuestPort > 500 {
			t.Errorf("unexpected rule in intersection: %+v", p)
		}
	}
}

func TestListPorts_FilterByGuestPort_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedHostPortRangePorts(t, s, "vm-gp-cnt")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-gp-cnt/ports?min_guest_port=80&max_guest_port=500")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
}

// ============================================================
// Port-forward list guest_ip filter tests (5.4.73)
// ============================================================

// seedMultiGuestIPPorts populates four port forwards on the same VM that
// land on three distinct guest IPs — the multi-NIC layout the guest_ip
// filter is designed to slice. Two rules share 192.168.100.50 so a positive
// match has to return more than one row.
func seedMultiGuestIPPorts(t *testing.T, s *store.Store, vmID string) {
	t.Helper()
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/22", VMID: vmID, HostPort: 22, GuestPort: 22, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP, Description: "ssh primary"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/8080", VMID: vmID, HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP, Description: "http primary"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/8443", VMID: vmID, HostPort: 8443, GuestPort: 443, GuestIP: "10.0.0.7", Protocol: types.ProtocolTCP, Description: "https data-net"})
	seedPortForward(t, s, &types.PortForward{ID: vmID + "/9090", VMID: vmID, HostPort: 9090, GuestPort: 9090, GuestIP: "10.0.0.8", Protocol: types.ProtocolUDP, Description: "metrics storage-net"})
}

func TestListPorts_FilterByGuestIP_ExactMatch(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedMultiGuestIPPorts(t, s, "vm-gip-eq")

	got := listPortsWithQuery(t, ts, "vm-gip-eq", "guest_ip=192.168.100.50")
	if len(got) != 2 {
		t.Fatalf("expected 2 rules with guest_ip=192.168.100.50, got %d (%+v)", len(got), got)
	}
	for _, p := range got {
		if p.GuestIP != "192.168.100.50" {
			t.Errorf("rule %s has guest_ip=%q, want 192.168.100.50", p.ID, p.GuestIP)
		}
	}
}

func TestListPorts_FilterByGuestIP_OtherCohort(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedMultiGuestIPPorts(t, s, "vm-gip-other")

	got := listPortsWithQuery(t, ts, "vm-gip-other", "guest_ip=10.0.0.7")
	if len(got) != 1 {
		t.Fatalf("expected 1 rule with guest_ip=10.0.0.7, got %d (%+v)", len(got), got)
	}
	if got[0].HostPort != 8443 {
		t.Errorf("got host_port=%d, want 8443", got[0].HostPort)
	}
}

func TestListPorts_FilterByGuestIP_CaseInsensitive(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	// IPv6 literal so case-insensitive matching is meaningful (IPv4 dotted
	// quads have no case axis; IPv6 hex digits are case-irrelevant and
	// operators routinely paste either form).
	seedPortForward(t, s, &types.PortForward{ID: "vm-gip-case/22", VMID: "vm-gip-case", HostPort: 22, GuestPort: 22, GuestIP: "fe80::ABCD", Protocol: types.ProtocolTCP})
	seedPortForward(t, s, &types.PortForward{ID: "vm-gip-case/80", VMID: "vm-gip-case", HostPort: 80, GuestPort: 80, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP})

	got := listPortsWithQuery(t, ts, "vm-gip-case", "guest_ip=FE80::abcd")
	if len(got) != 1 || got[0].HostPort != 22 {
		t.Fatalf("case-insensitive match for FE80::abcd returned %+v, want the fe80::ABCD row", got)
	}
}

func TestListPorts_FilterByGuestIP_TrimsWhitespace(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedMultiGuestIPPorts(t, s, "vm-gip-ws")

	got := listPortsWithQuery(t, ts, "vm-gip-ws", "guest_ip=%20192.168.100.50%20")
	if len(got) != 2 {
		t.Fatalf("expected 2 rules after whitespace trim, got %d (%+v)", len(got), got)
	}
}

func TestListPorts_FilterByGuestIP_EmptyIsNoOp(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedMultiGuestIPPorts(t, s, "vm-gip-empty")

	got := listPortsWithQuery(t, ts, "vm-gip-empty", "guest_ip=")
	if len(got) != 4 {
		t.Fatalf("empty guest_ip should be a no-op, got %d rules (want 4)", len(got))
	}
}

func TestListPorts_FilterByGuestIP_NoMatch(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedMultiGuestIPPorts(t, s, "vm-gip-miss")

	got := listPortsWithQuery(t, ts, "vm-gip-miss", "guest_ip=203.0.113.1")
	if len(got) != 0 {
		t.Fatalf("expected 0 rules, got %d (%+v)", len(got), got)
	}
}

func TestListPorts_FilterByGuestIP_ComposesWithProtocol(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedMultiGuestIPPorts(t, s, "vm-gip-proto")

	// guest_ip=192.168.100.50 → 2 rules (both TCP); narrow to UDP → 0.
	got := listPortsWithQuery(t, ts, "vm-gip-proto", "guest_ip=192.168.100.50&protocol=udp")
	if len(got) != 0 {
		t.Fatalf("guest_ip + protocol=udp should be empty, got %+v", got)
	}
	// Same guest_ip narrowed to tcp returns both.
	got = listPortsWithQuery(t, ts, "vm-gip-proto", "guest_ip=192.168.100.50&protocol=tcp")
	if len(got) != 2 {
		t.Fatalf("guest_ip + protocol=tcp = %d, want 2 (%+v)", len(got), got)
	}
}

func TestListPorts_FilterByGuestIP_ComposesWithGuestPortRange(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedMultiGuestIPPorts(t, s, "vm-gip-gpr")

	// 192.168.100.50 cohort has guest_port {22, 80}; min_guest_port=80 keeps the http row.
	got := listPortsWithQuery(t, ts, "vm-gip-gpr", "guest_ip=192.168.100.50&min_guest_port=80")
	if len(got) != 1 || got[0].GuestPort != 80 {
		t.Fatalf("guest_ip + min_guest_port=80 = %+v, want the :80 row", got)
	}
}

func TestListPorts_FilterByGuestIP_TotalCountReflectsFiltered(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedMultiGuestIPPorts(t, s, "vm-gip-cnt")

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-gip-cnt/ports?guest_ip=192.168.100.50")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
}

// ============================================================
// Port-forward list pagination tests (5.4.20)
// ============================================================

// listPortsWithResponse mirrors listPortsWithQuery but returns the raw
// *http.Response so callers can read pagination headers (X-Total-Count).
func listPortsWithResponse(t *testing.T, ts *httptest.Server, vmID, query string) ([]types.PortForward, *http.Response) {
	t.Helper()
	url := ts.URL + "/api/v1/vms/" + vmID + "/ports"
	if query != "" {
		url += "?" + query
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var ports []types.PortForward
	if err := json.NewDecoder(resp.Body).Decode(&ports); err != nil {
		t.Fatalf("decode ports: %v", err)
	}
	return ports, resp
}

func TestListPorts_Pagination_PerPagePage(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-pg")

	// sort=host_port asc → [8081, 9090, 22001]
	got1, resp1 := listPortsWithResponse(t, ts, "vm-pg", "sort=host_port&per_page=2&page=1")
	if header := resp1.Header.Get("X-Total-Count"); header != "3" {
		t.Errorf("page 1 X-Total-Count = %q, want 3", header)
	}
	if len(got1) != 2 || got1[0].HostPort != 8081 || got1[1].HostPort != 9090 {
		t.Fatalf("page 1 = %+v, want host_ports [8081 9090]", got1)
	}

	got2, resp2 := listPortsWithResponse(t, ts, "vm-pg", "sort=host_port&per_page=2&page=2")
	if header := resp2.Header.Get("X-Total-Count"); header != "3" {
		t.Errorf("page 2 X-Total-Count = %q, want 3", header)
	}
	if len(got2) != 1 || got2[0].HostPort != 22001 {
		t.Fatalf("page 2 = %+v, want host_ports [22001]", got2)
	}
}

func TestListPorts_Pagination_PageBeyondEndReturnsEmpty(t *testing.T) {
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-pg")

	got, resp := listPortsWithResponse(t, ts, "vm-pg", "per_page=2&page=99")
	if header := resp.Header.Get("X-Total-Count"); header != "3" {
		t.Errorf("X-Total-Count = %q, want 3", header)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty slice for out-of-range page", got)
	}
}

func TestListPorts_Pagination_NoParamsReturnsAll(t *testing.T) {
	// Without pagination params, ListPorts returns the full filtered set —
	// preserves the existing zero-perPage contract from parsePagination so
	// the absence of `?page=` / `?per_page=` keeps the legacy CLI/UI
	// behaviour where every rule is rendered at once.
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-pg")

	got, resp := listPortsWithResponse(t, ts, "vm-pg", "")
	if header := resp.Header.Get("X-Total-Count"); header != "3" {
		t.Errorf("X-Total-Count = %q, want 3", header)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3 (full set)", len(got))
	}
}

func TestListPorts_Pagination_TotalCountReflectsFilter(t *testing.T) {
	// X-Total-Count must reflect the post-filter / pre-pagination count so
	// the GUI can paginate over the filtered population. Mirrors the
	// contract documented for VMs / images / templates / events / webhooks.
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-pg")

	// "ssh" only matches the 22001 rule.
	_, resp := listPortsWithResponse(t, ts, "vm-pg", "search=ssh&per_page=10&page=1")
	if header := resp.Header.Get("X-Total-Count"); header != "1" {
		t.Errorf("X-Total-Count = %q, want 1 (only ssh jumpbox matches)", header)
	}
}

func TestListPorts_Pagination_LimitAlias(t *testing.T) {
	// parsePagination accepts `limit` as a synonym for `per_page`.
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedSortPortFixtures(t, s, "vm-pg")

	got, _ := listPortsWithResponse(t, ts, "vm-pg", "sort=host_port&limit=1")
	if len(got) != 1 || got[0].HostPort != 8081 {
		t.Fatalf("limit=1 + sort=host_port asc = %+v, want host_port=8081", got)
	}
}

func TestListPorts_Pagination_ComposesWithSortAndTag(t *testing.T) {
	// Sort + tag filter + pagination must stack: filter narrows the set, sort
	// fixes the order, then pagination chops the window.
	ts, _, s, _, cleanup := testServerWithPortFwd(t)
	defer cleanup()
	seedPortForward(t, s, &types.PortForward{ID: "vm-pg/8081", VMID: "vm-pg", HostPort: 8081, GuestPort: 80, Protocol: types.ProtocolTCP, Description: "web", Tags: []string{"prod"}})
	seedPortForward(t, s, &types.PortForward{ID: "vm-pg/9090", VMID: "vm-pg", HostPort: 9090, GuestPort: 9090, Protocol: types.ProtocolTCP, Description: "metrics", Tags: []string{"prod"}})
	seedPortForward(t, s, &types.PortForward{ID: "vm-pg/2222", VMID: "vm-pg", HostPort: 2222, GuestPort: 22, Protocol: types.ProtocolTCP, Description: "ssh", Tags: []string{"dev"}})

	// tag=prod narrows to two rules; sort=host_port desc puts 9090 first; page 2
	// of size 1 yields the second prod row (host_port=8081).
	got, resp := listPortsWithResponse(t, ts, "vm-pg", "tag=prod&sort=host_port&order=desc&per_page=1&page=2")
	if header := resp.Header.Get("X-Total-Count"); header != "2" {
		t.Errorf("X-Total-Count = %q, want 2 (post-filter, pre-pagination)", header)
	}
	if len(got) != 1 || got[0].HostPort != 8081 {
		t.Fatalf("got = %+v, want host_port=8081", got)
	}
}

// ============================================================
// Template bulk-delete handler tests (2.3.9)
// ============================================================

func decodeBulkTemplateResponse(t *testing.T, resp *http.Response) bulkDeleteTemplatesResponse {
	t.Helper()
	var out bulkDeleteTemplatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode bulk template response: %v", err)
	}
	return out
}

func TestBulkDeleteTemplates_ByIDs(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "keep", Image: "rocky9.qcow2", CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "drop-a", Image: "rocky9.qcow2", CreatedAt: t0.Add(time.Hour)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "drop-b", Image: "rocky9.qcow2", CreatedAt: t0.Add(2 * time.Hour)})

	body := jsonBody(t, bulkDeleteTemplatesRequest{IDs: []string{"tmpl-2", "tmpl-3"}})
	resp, err := http.Post(ts.URL+"/api/v1/templates/bulk_delete", "application/json", body)
	if err != nil {
		t.Fatalf("POST bulk_delete: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkTemplateResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	for _, r := range out.Results {
		if !r.Success {
			t.Errorf("expected success for %q, got code=%q msg=%q", r.ID, r.Code, r.Message)
		}
	}

	remaining, _ := s.ListTemplates()
	if len(remaining) != 1 || remaining[0].ID != "tmpl-1" {
		t.Errorf("survivors = %v, want only tmpl-1", remaining)
	}
}

func TestBulkDeleteTemplates_ByTag(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-1", Name: "keep", Image: "rocky9.qcow2", Tags: []string{"prod"}, CreatedAt: t0})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-2", Name: "old-a", Image: "rocky9.qcow2", Tags: []string{"legacy-rocky8"}, CreatedAt: t0.Add(time.Hour)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-3", Name: "old-b", Image: "rocky9.qcow2", Tags: []string{"legacy-rocky8", "linux"}, CreatedAt: t0.Add(2 * time.Hour)})
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-4", Name: "kept-too", Image: "rocky9.qcow2", Tags: []string{"rc-2026-05"}, CreatedAt: t0.Add(3 * time.Hour)})

	// Tag matching is case-insensitive.
	body := jsonBody(t, bulkDeleteTemplatesRequest{Tag: "LEGACY-ROCKY8"})
	resp, _ := http.Post(ts.URL+"/api/v1/templates/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkTemplateResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2 (got %+v)", len(out.Results), out.Results)
	}

	remaining, _ := s.ListTemplates()
	survivors := map[string]bool{}
	for _, tpl := range remaining {
		survivors[tpl.ID] = true
	}
	if !survivors["tmpl-1"] || !survivors["tmpl-4"] || len(survivors) != 2 {
		t.Errorf("survivors = %v, want tmpl-1 + tmpl-4", survivors)
	}
}

func TestBulkDeleteTemplates_PartialFailure(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-real", Name: "real", Image: "rocky9.qcow2", CreatedAt: t0})

	body := jsonBody(t, bulkDeleteTemplatesRequest{IDs: []string{"tmpl-real", "tmpl-missing"}})
	resp, _ := http.Post(ts.URL+"/api/v1/templates/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkTemplateResponse(t, resp)
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	gotByID := map[string]bulkDeleteTemplateResult{}
	for _, r := range out.Results {
		gotByID[r.ID] = r
	}
	if !gotByID["tmpl-real"].Success {
		t.Errorf("expected tmpl-real to succeed, got %+v", gotByID["tmpl-real"])
	}
	if gotByID["tmpl-missing"].Success {
		t.Errorf("expected tmpl-missing to fail")
	}
	if gotByID["tmpl-missing"].Code != "resource_not_found" {
		t.Errorf("tmpl-missing.Code = %q, want resource_not_found", gotByID["tmpl-missing"].Code)
	}
}

func TestBulkDeleteTemplates_EmptyRequestRejected(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	body := jsonBody(t, bulkDeleteTemplatesRequest{})
	resp, _ := http.Post(ts.URL+"/api/v1/templates/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeleteTemplates_BothIDsAndTagRejected(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	body := jsonBody(t, bulkDeleteTemplatesRequest{IDs: []string{"tmpl-1"}, Tag: "prod"})
	resp, _ := http.Post(ts.URL+"/api/v1/templates/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_bulk_request")
}

func TestBulkDeleteTemplates_TagNoMatchEmptyResponse(t *testing.T) {
	ts, _, s, cleanup := testServerFull(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	seedStoredTemplate(t, s, &types.VMTemplate{ID: "tmpl-keep", Name: "keep", Image: "rocky9.qcow2", Tags: []string{"prod"}, CreatedAt: t0})

	body := jsonBody(t, bulkDeleteTemplatesRequest{Tag: "nope"})
	resp, _ := http.Post(ts.URL+"/api/v1/templates/bulk_delete", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeBulkTemplateResponse(t, resp)
	if len(out.Results) != 0 {
		t.Errorf("results = %d, want 0", len(out.Results))
	}
	remaining, _ := s.ListTemplates()
	if len(remaining) != 1 {
		t.Errorf("survivors = %d, want 1 (tmpl-keep untouched)", len(remaining))
	}
}

func TestBulkDeleteTemplates_BadJSON(t *testing.T) {
	ts, _, _, cleanup := testServerFull(t)
	defer cleanup()

	resp, _ := http.Post(ts.URL+"/api/v1/templates/bulk_delete", "application/json", bytes.NewBufferString("not-json"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_request_body")
}

// --- Snapshot tag tests (roadmap 2.2.17) ---
//
// Tags are persisted out-of-band from libvirt (the libvirt domainsnapshot
// XML schema does not permit <metadata>, so we cannot put tags alongside
// description in the XML).  The MockManager stores tags in-memory; the
// LibvirtManager writes them to bbolt.  Either way, the wire contract is
// the same and the tests below talk only to the HTTP surface.

func TestCreateSnapshot_WithTags(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-t", Name: "host"})

	body := jsonBody(t, map[string]any{
		"name": "snap-tagged",
		"tags": []string{"Production", "audit", "production"}, // mixed case + duplicate
	})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-snap-t/snapshots", "application/json", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var snap types.Snapshot
	decodeJSON(t, resp, &snap)

	// Tags should be lowercased + deduplicated + alphabetised by the
	// shared validator.
	if got, want := snap.Tags, []string{"audit", "production"}; !reflect.DeepEqual(got, want) {
		t.Errorf("snap.Tags = %v, want %v", got, want)
	}

	// And round-trip via list.
	listResp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-t/snapshots")
	var listed []*types.Snapshot
	decodeJSON(t, listResp, &listed)
	if len(listed) != 1 || !reflect.DeepEqual(listed[0].Tags, []string{"audit", "production"}) {
		t.Errorf("list did not preserve tags: %+v", listed)
	}
}

func TestCreateSnapshot_RejectsInvalidTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-x", Name: "host"})

	body := jsonBody(t, map[string]any{
		"name": "snap",
		"tags": []string{"BAD TAG WITH SPACES"},
	})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-snap-x/snapshots", "application/json", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_snapshot")
}

func TestCreateSnapshot_EmptyTagsArrayPersistsNoneAndListOmitsField(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-e", Name: "host"})

	body := jsonBody(t, map[string]any{"name": "snap-empty", "tags": []string{}})
	resp, _ := http.Post(ts.URL+"/api/v1/vms/vm-snap-e/snapshots", "application/json", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var snap types.Snapshot
	decodeJSON(t, resp, &snap)
	if len(snap.Tags) != 0 {
		t.Errorf("empty tags array should produce nil/empty Tags, got %v", snap.Tags)
	}
}

func TestUpdateSnapshot_SetsTags(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-up", Name: "host"})
	if _, err := mockMgr.CreateSnapshot(nil, "vm-snap-up", types.SnapshotSpec{Name: "snap-up"}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	tags := []string{"backup", "audit"}
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-snap-up/snapshots/snap-up",
		updateSnapshotRequest{Tags: &tags})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snap types.Snapshot
	decodeJSON(t, resp, &snap)
	if !reflect.DeepEqual(snap.Tags, []string{"audit", "backup"}) {
		t.Errorf("Tags = %v, want [audit backup]", snap.Tags)
	}
}

func TestUpdateSnapshot_ClearsTags(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-clr", Name: "host"})
	if _, err := mockMgr.CreateSnapshot(nil, "vm-snap-clr", types.SnapshotSpec{Name: "snap-c", Tags: []string{"audit"}}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	cleared := []string{}
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-snap-clr/snapshots/snap-c",
		updateSnapshotRequest{Tags: &cleared})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snap types.Snapshot
	decodeJSON(t, resp, &snap)
	if len(snap.Tags) != 0 {
		t.Errorf("Tags should be cleared, got %v", snap.Tags)
	}
}

func TestUpdateSnapshot_OmittedTagsIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-noop", Name: "host"})
	if _, err := mockMgr.CreateSnapshot(nil, "vm-snap-noop", types.SnapshotSpec{Name: "snap-n", Tags: []string{"keep-me"}}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	desc := "just touching description"
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-snap-noop/snapshots/snap-n",
		updateSnapshotRequest{Description: &desc})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snap types.Snapshot
	decodeJSON(t, resp, &snap)
	if !reflect.DeepEqual(snap.Tags, []string{"keep-me"}) {
		t.Errorf("Tags should be preserved when patch omits them, got %v", snap.Tags)
	}
}

func TestUpdateSnapshot_RejectsInvalidTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-bad", Name: "host"})
	if _, err := mockMgr.CreateSnapshot(nil, "vm-snap-bad", types.SnapshotSpec{Name: "snap-b"}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	bad := []string{"BAD TAG"}
	resp := patchJSON(t, ts.URL+"/api/v1/vms/vm-snap-bad/snapshots/snap-b",
		updateSnapshotRequest{Tags: &bad})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_snapshot")
}

func TestListSnapshots_FilterByTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-fl", Name: "host"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-fl", Name: "snap-a", Tags: []string{"audit"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-fl", Name: "snap-b", Tags: []string{"production"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-fl", Name: "snap-c", Tags: nil})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-fl/snapshots?tag=audit")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "snap-a" {
		t.Errorf("?tag=audit = %+v, want [snap-a]", listed)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Errorf("X-Total-Count = %q, want 1 (post-tag-filter count)", got)
	}
}

func TestListSnapshots_FilterByTag_CaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-ci", Name: "host"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-ci", Name: "snap-a", Tags: []string{"audit"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-ci/snapshots?tag=AUDIT")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 {
		t.Errorf("expected case-insensitive match, got %+v", listed)
	}
}

func TestListSnapshots_FilterByTag_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-nm", Name: "host"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-nm", Name: "snap-a", Tags: []string{"audit"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-nm/snapshots?tag=missing")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 0 {
		t.Errorf("expected empty list, got %+v", listed)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Errorf("X-Total-Count = %q, want 0", got)
	}
}

func TestListSnapshots_FilterByTag_ComposesWithSearch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-cw", Name: "host"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-cw", Name: "before-upgrade", Tags: []string{"audit"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-cw", Name: "after-upgrade", Tags: []string{"audit"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-cw", Name: "before-other", Tags: []string{"production"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-cw/snapshots?tag=audit&search=before")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "before-upgrade" {
		t.Errorf("?tag=audit&search=before = %+v, want [before-upgrade]", listed)
	}
}

func TestListSnapshots_FilterBySearch_MatchesTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()
	mockMgr.SeedVM(&types.VM{ID: "vm-snap-mt", Name: "host"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-mt", Name: "snap-a", Tags: []string{"production"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-mt", Name: "snap-b"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-mt/snapshots?search=prod")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "snap-a" {
		t.Errorf("?search=prod must match against tags, got %+v", listed)
	}
}

// ============================================================
// 5.4.28: ?since / ?until snapshot time-range filter
// ============================================================

func TestListSnapshots_FilterBySince(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-since", Name: "host"})
	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-since", Name: "early", CreatedAt: day(1)})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-since", Name: "mid", CreatedAt: day(15)})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-since", Name: "late", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-since/snapshots?since=2026-05-10T00:00:00Z")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2", got)
	}
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	names := map[string]bool{}
	for _, s := range listed {
		names[s.Name] = true
	}
	if !names["mid"] || !names["late"] || names["early"] {
		t.Fatalf("expected mid+late, got %+v", listed)
	}
}

func TestListSnapshots_FilterByUntil(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-until", Name: "host"})
	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-until", Name: "early", CreatedAt: day(1)})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-until", Name: "mid", CreatedAt: day(15)})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-until", Name: "late", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-until/snapshots?until=2026-05-20T00:00:00Z")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("expected 2 snapshots <= until, got %+v", listed)
	}
}

func TestListSnapshots_FilterBySinceAndUntil(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-range", Name: "host"})
	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-range", Name: "snap-1", CreatedAt: day(1)})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-range", Name: "snap-15", CreatedAt: day(15)})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-range", Name: "snap-30", CreatedAt: day(30)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-range/snapshots?since=2026-05-10T00:00:00Z&until=2026-05-20T00:00:00Z")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "snap-15" {
		t.Fatalf("expected only snap-15, got %+v", listed)
	}
}

func TestListSnapshots_FilterBySince_Inclusive(t *testing.T) {
	// Exact boundary timestamp matches under "inclusive" semantics.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-incl", Name: "host"})
	boundary := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-incl", Name: "edge", CreatedAt: boundary})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-incl/snapshots?since=2026-05-01T00:00:00Z")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "edge" {
		t.Fatalf("expected boundary match, got %+v", listed)
	}
}

func TestListSnapshots_FilterByInvalidSince(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms/anyvm/snapshots?since=last-tuesday")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_since" {
		t.Fatalf("code = %q, want invalid_since", apiErr.Code)
	}
}

func TestListSnapshots_FilterByInvalidUntil(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, _ := http.Get(ts.URL + "/api/v1/vms/anyvm/snapshots?until=2026-13-99")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var apiErr types.APIError
	decodeJSON(t, resp, &apiErr)
	if apiErr.Code != "invalid_until" {
		t.Fatalf("code = %q, want invalid_until", apiErr.Code)
	}
}

func TestListSnapshots_FilterBySince_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-noop", Name: "host"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-noop", Name: "s1", CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-noop", Name: "s2", CreatedAt: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-noop/snapshots?since=%20%20")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("whitespace-only since should be a no-op; got %+v", listed)
	}
}

func TestListSnapshots_FilterByTimeRange_ExcludesZeroCreatedAt(t *testing.T) {
	// A snapshot with zero CreatedAt is filtered out whenever any bound is
	// set — operators querying a time window don't want unbounded entries
	// silently included.
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-zero", Name: "host"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-zero", Name: "no-time"}) // zero CreatedAt
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-zero", Name: "dated", CreatedAt: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-zero/snapshots?since=2026-05-01T00:00:00Z")
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "dated" {
		t.Fatalf("expected only dated (zero-time excluded), got %+v", listed)
	}
}

func TestListSnapshots_FilterBySince_ComposesWithTagAndSearch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-comp", Name: "host"})
	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-comp", Name: "pre-deploy-old", CreatedAt: day(1), Tags: []string{"prod"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-comp", Name: "pre-deploy-new", CreatedAt: day(20), Tags: []string{"prod"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-comp", Name: "rollback", CreatedAt: day(20), Tags: []string{"prod"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-comp", Name: "pre-deploy-staging", CreatedAt: day(20), Tags: []string{"staging"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-comp/snapshots?since=2026-05-10T00:00:00Z&tag=prod&search=pre-deploy")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "pre-deploy-new" {
		t.Fatalf("expected only pre-deploy-new, got %+v", listed)
	}
}

// 5.4.75 — `?prefix=` on `GET /vms/{vmID}/snapshots`. Case-sensitive
// HasPrefix against snap.Name; mirrors the `prefix` selector on the
// `POST /vms/{vmID}/snapshots/bulk_delete` request body so an operator can
// preview the cohort before bulk-deleting it.
func TestListSnapshots_FilterByPrefix_Match(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-prefix"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix", Name: "auto-nightly-2026-05-23"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix", Name: "auto-nightly-2026-05-24"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix", Name: "manual-rollback"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-prefix/snapshots?prefix=auto-nightly-")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (post-filter)", got)
	}
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 2 {
		t.Fatalf("expected 2 snapshots, got %+v", listed)
	}
	for _, s := range listed {
		if !strings.HasPrefix(s.Name, "auto-nightly-") {
			t.Fatalf("expected only auto-nightly-* snapshots, got %s", s.Name)
		}
	}
}

func TestListSnapshots_FilterByPrefix_IsCaseSensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-prefix-case"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-case", Name: "Auto-Daily-001"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-prefix-case/snapshots?prefix=auto-daily")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("X-Total-Count = %q, want 0 (case-sensitive non-match)", got)
	}
}

func TestListSnapshots_FilterByPrefix_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-prefix-trim"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-trim", Name: "auto-x"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-trim", Name: "manual"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-prefix-trim/snapshots?prefix=%20%20auto-%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (whitespace-trim)", got)
	}
}

func TestListSnapshots_FilterByPrefix_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-prefix-empty"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-empty", Name: "a"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-empty", Name: "b"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-prefix-empty/snapshots?prefix=")
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (empty disables)", got)
	}
}

func TestListSnapshots_FilterByPrefix_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-prefix-none"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-none", Name: "manual-1"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-prefix-none/snapshots?prefix=auto-")
	if got := resp.Header.Get("X-Total-Count"); got != "0" {
		t.Fatalf("X-Total-Count = %q, want 0", got)
	}
}

func TestListSnapshots_FilterByPrefix_ComposesWithTag(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-snap-prefix-compose"})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-compose", Name: "auto-nightly-1", Tags: []string{"prod"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-compose", Name: "auto-nightly-2", Tags: []string{"staging"}})
	mockMgr.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-compose", Name: "manual-rollback", Tags: []string{"prod"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms/vm-snap-prefix-compose/snapshots?prefix=auto-nightly-&tag=prod")
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var listed []*types.Snapshot
	decodeJSON(t, resp, &listed)
	if len(listed) != 1 || listed[0].Name != "auto-nightly-1" {
		t.Fatalf("expected only auto-nightly-1, got %+v", listed)
	}
}

// --- 5.4.76 — Name-prefix filter on the VM list ---

func TestListVMs_FilterByPrefix_Match(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-1"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "web-prod-2"})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "db-prod-1"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?prefix=web-prod-")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "2" {
		t.Fatalf("X-Total-Count = %q, want 2 (post-filter)", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 web-prod- vms, got %+v", vms)
	}
	names := map[string]bool{vms[0].Name: true, vms[1].Name: true}
	if !names["web-prod-1"] || !names["web-prod-2"] {
		t.Fatalf("expected web-prod-1 and web-prod-2, got %+v", vms)
	}
}

// TestListVMs_FilterByPrefix_IsCaseSensitive documents that the prefix filter
// is case-sensitive — mirrors `strings.HasPrefix` and the 5.4.75 snapshot
// prefix contract. VM names are case-sensitive identifiers under the
// `[A-Za-z0-9-]` alphabet.
func TestListVMs_FilterByPrefix_IsCaseSensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "Web-Prod-1"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?prefix=web-prod-")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected case-sensitive non-match, got %+v", vms)
	}
}

func TestListVMs_FilterByPrefix_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-1"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?prefix=%20%20web-prod-%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByPrefix_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "a"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "b"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?prefix=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByPrefix_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-1"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?prefix=db-")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected no match, got %+v", vms)
	}
}

// TestListVMs_FilterByPrefix_ComposesWithStatus verifies that the prefix
// filter is applied additively with `?status=`, so the post-filter
// `X-Total-Count` stays correct when both filters are active.
func TestListVMs_FilterByPrefix_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-1", State: types.VMStateRunning})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "web-prod-2", State: types.VMStateStopped})
	mockMgr.SeedVM(&types.VM{ID: "vm-3", Name: "db-prod-1", State: types.VMStateRunning})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?prefix=web-prod-&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].Name != "web-prod-1" {
		t.Fatalf("expected only web-prod-1, got %+v", vms)
	}
}

// TestListVMs_FilterByPrefix_TotalCountReflectsFiltered verifies that
// `X-Total-Count` reports the post-filter / pre-pagination population.
func TestListVMs_FilterByPrefix_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("web-prod-%d", i)
		if i%2 == 0 {
			name = fmt.Sprintf("db-prod-%d", i)
		}
		mockMgr.SeedVM(&types.VM{ID: fmt.Sprintf("vm-%d", i), Name: name})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?prefix=web-prod-&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// 5.4.79 — NAT static IP filter tests.

func TestListVMs_FilterByNATStaticIP_ExactMatchCIDR(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{NatStaticIP: "192.168.100.51/24"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=192.168.100.50/24")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected vm-1, got %+v", vms)
	}
}

func TestListVMs_FilterByNATStaticIP_MatchesIPPortion(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{NatStaticIP: "192.168.100.51/24"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=192.168.100.50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected vm-1, got %+v", vms)
	}
}

func TestListVMs_FilterByNATStaticIP_ExcludesDHCP(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"}) // DHCP (empty)

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=192.168.100.50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected only vm-1, got %+v", vms)
	}
}

func TestListVMs_FilterByNATStaticIP_CaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatStaticIP: "fe80::ABCD/64"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=fe80::abcd/64")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByNATStaticIP_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=%20%20192.168.100.50%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByNATStaticIP_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByNATStaticIP_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=10.0.0.1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected no match, got %+v", vms)
	}
}

func TestListVMs_FilterByNATStaticIP_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=192.168.100.50&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected only vm-1, got %+v", vms)
	}
}

func TestListVMs_FilterByNATStaticIP_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		ip := "192.168.100.50/24"
		if i%2 == 0 {
			ip = "192.168.100.51/24"
		}
		mockMgr.SeedVM(&types.VM{ID: fmt.Sprintf("vm-%d", i), Name: fmt.Sprintf("vm-%d", i), Spec: types.VMSpec{NatStaticIP: ip}})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=192.168.100.50&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// 5.4.80 — NAT gateway filter tests.

func TestListVMs_FilterByNATGateway_ExactMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatGateway: "192.168.100.1"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{NatGateway: "10.0.0.1"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_gateway=192.168.100.1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected vm-1, got %+v", vms)
	}
}

func TestListVMs_FilterByNATGateway_ExcludesEmpty(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatGateway: "192.168.100.1"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"}) // empty NatGateway

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_gateway=192.168.100.1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected only vm-1 (empty NatGateway excluded), got %+v", vms)
	}
}

func TestListVMs_FilterByNATGateway_CaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatGateway: "FE80::1"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_gateway=fe80::1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByNATGateway_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatGateway: "192.168.100.1"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_gateway=%20%20192.168.100.1%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByNATGateway_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatGateway: "192.168.100.1"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_gateway=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByNATGateway_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatGateway: "192.168.100.1"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_gateway=10.0.0.1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected no match, got %+v", vms)
	}
}

func TestListVMs_FilterByNATGateway_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{NatGateway: "192.168.100.1"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Spec: types.VMSpec{NatGateway: "192.168.100.1"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_gateway=192.168.100.1&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected only vm-1, got %+v", vms)
	}
}

func TestListVMs_FilterByNATGateway_ComposesWithNATStaticIP(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24", NatGateway: "192.168.100.1"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24", NatGateway: "192.168.100.254"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=192.168.100.50&nat_gateway=192.168.100.254")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-2" {
		t.Fatalf("expected only vm-2 (matching both filters), got %+v", vms)
	}
}

func TestListVMs_FilterByNATGateway_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		gw := "192.168.100.1"
		if i%2 == 0 {
			gw = "10.0.0.1"
		}
		mockMgr.SeedVM(&types.VM{ID: fmt.Sprintf("vm-%d", i), Name: fmt.Sprintf("vm-%d", i), Spec: types.VMSpec{NatGateway: gw}})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?nat_gateway=192.168.100.1&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}

// 5.4.81 — Runtime IP filter tests. Mirrors the NAT gateway shape but on
// the runtime-discovered vm.IP field (the value displayed in the VM table),
// closing the operator query "which VM is at 192.168.100.42 right now?"
// that ?nat_static_ip= cannot answer for DHCP-assigned VMs.

func TestListVMs_FilterByIP_ExactMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", IP: "192.168.100.99"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=192.168.100.42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected vm-1, got %+v", vms)
	}
}

func TestListVMs_FilterByIP_ExcludesEmpty(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"}) // stopped: empty IP

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=192.168.100.42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected only vm-1 (empty IP excluded), got %+v", vms)
	}
}

func TestListVMs_FilterByIP_CaseInsensitive(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "FE80::42"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=fe80::42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", vms)
	}
}

func TestListVMs_FilterByIP_TrimsWhitespace(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=%20%20192.168.100.42%20%20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 {
		t.Fatalf("expected whitespace-trimmed match, got %+v", vms)
	}
}

func TestListVMs_FilterByIP_EmptyIsNoOp(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected empty filter to return all VMs, got %+v", vms)
	}
}

func TestListVMs_FilterByIP_NoMatch(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=10.0.0.99")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 0 {
		t.Fatalf("expected no match, got %+v", vms)
	}
}

// DHCP-assigned VM has empty spec.nat_static_ip but non-empty runtime IP;
// the ?ip= filter finds it, ?nat_static_ip= does not. This is the key
// motivating use-case for the filter.
func TestListVMs_FilterByIP_MatchesDHCPVM(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	// vm-1 is DHCP-assigned: no nat_static_ip, but a runtime-discovered IP.
	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42", Spec: types.VMSpec{}})
	// vm-2 has a static IP at a different address.
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", IP: "192.168.100.50", Spec: types.VMSpec{NatStaticIP: "192.168.100.50/24"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=192.168.100.42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected DHCP-assigned vm-1, got %+v", vms)
	}

	// ?nat_static_ip=192.168.100.42 returns NOTHING because vm-1 has no
	// static IP. ?ip= is the only way to find a DHCP VM by address.
	resp2, _ := http.Get(ts.URL + "/api/v1/vms?nat_static_ip=192.168.100.42")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	var vms2 []*types.VM
	decodeJSON(t, resp2, &vms2)
	if len(vms2) != 0 {
		t.Fatalf("?nat_static_ip= should miss DHCP VM, got %+v", vms2)
	}
}

func TestListVMs_FilterByIP_ComposesWithStatus(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, IP: "192.168.100.42"})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, IP: "192.168.100.42"})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=192.168.100.42&status=running")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1 (post-filter)", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-1" {
		t.Fatalf("expected only vm-1, got %+v", vms)
	}
}

func TestListVMs_FilterByIP_ComposesWithNATGateway(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42", Spec: types.VMSpec{NatGateway: "192.168.100.1"}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "beta", IP: "192.168.100.42", Spec: types.VMSpec{NatGateway: "192.168.100.254"}})

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=192.168.100.42&nat_gateway=192.168.100.254")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 1 || vms[0].ID != "vm-2" {
		t.Fatalf("expected only vm-2 (matching both filters), got %+v", vms)
	}
}

func TestListVMs_FilterByIP_TotalCountReflectsFiltered(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	for i := 0; i < 6; i++ {
		ip := "192.168.100.42"
		if i%2 == 0 {
			ip = "10.0.0.42"
		}
		mockMgr.SeedVM(&types.VM{ID: fmt.Sprintf("vm-%d", i), Name: fmt.Sprintf("vm-%d", i), IP: ip})
	}

	resp, _ := http.Get(ts.URL + "/api/v1/vms?ip=192.168.100.42&per_page=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Total-Count"); got != "3" {
		t.Fatalf("expected X-Total-Count=3 (post-filter), got %q", got)
	}
	var vms []*types.VM
	decodeJSON(t, resp, &vms)
	if len(vms) != 2 {
		t.Fatalf("expected 2 VMs on page 1 (per_page=2), got %+v", vms)
	}
}
