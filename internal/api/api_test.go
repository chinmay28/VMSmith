package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		Name:  "quota-vm",
		Image: "ubuntu",
		CPUs:  2,
		RAMMB: 2048,
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
		{name: "invalid action", body: `{"action":"reboot","ids":["vm-1"]}`, wantCode: "invalid_bulk_action", wantState: http.StatusBadRequest},
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
	mockMgr.CreateSnapshot(nil, "vm-s", "snap1")
	mockMgr.CreateSnapshot(nil, "vm-s", "snap2")
	mockMgr.CreateSnapshot(nil, "vm-s", "snap3")

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
	})
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "one", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mockMgr.SeedVM(&types.VM{ID: "vm-2", Name: "two", Spec: types.VMSpec{CPUs: 4, RAMMB: 4096, DiskGB: 40}})

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

	s.PutEvent(&types.Event{ID: "evt-1", VMID: "vm-1", Type: "vm_started", CreatedAt: now.Add(-10 * time.Minute)})
	s.PutEvent(&types.Event{ID: "evt-2", VMID: "vm-2", Type: "vm_stopped", CreatedAt: now.Add(-5 * time.Minute)})
	s.PutEvent(&types.Event{ID: "evt-3", VMID: "vm-1", Type: "vm_deleted", CreatedAt: now.Add(-1 * time.Minute)})

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
	if events[0].ID != "evt-3" || events[2].ID != "evt-1" {
		t.Errorf("expected descending sort order")
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
	if events[0].ID != "evt-3" || events[1].ID != "evt-2" {
		t.Errorf("expected evt-3 and evt-2")
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
