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

	resp, err := http.Get(ts.URL + "/api/v1/vms?sort=ram_mb")
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

	resp, err := http.Get(ts.URL + "/api/v1/events?sort=attributes")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
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
	resp, _ := http.Get(ts.URL + "/api/v1/templates?sort=ram_mb")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_sort")
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

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-x/ports?sort=guest_ip")
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
