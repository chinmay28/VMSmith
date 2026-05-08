package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/console"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// consoleTestServer mirrors testServer() but installs a console.Store on the
// API server so we can exercise the ticket-issuance endpoint end-to-end.
func consoleTestServer(t *testing.T, withStore bool) (*httptest.Server, *vm.MockManager, *console.Store, func()) {
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

	apiServer := NewServerWithConfig(mockMgr, storageMgr, portFwd, s, cfg, nil)

	var consoleStore *console.Store
	if withStore {
		consoleStore = console.NewStore()
		apiServer.SetConsoleStore(consoleStore)
	}

	ts := httptest.NewServer(apiServer)
	cleanup := func() {
		ts.Close()
		if consoleStore != nil {
			consoleStore.Close()
		}
		s.Close()
	}
	return ts, mockMgr, consoleStore, cleanup
}

func TestIssueConsoleTicket_RunningVM_ReturnsTicket(t *testing.T) {
	ts, mockMgr, store, cleanup := consoleTestServer(t, true)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:    "vm-running",
		Name:  "running",
		State: types.VMStateRunning,
	})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-running/console/ticket", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var ticket types.ConsoleTicket
	decodeJSON(t, resp, &ticket)
	if ticket.Ticket == "" {
		t.Error("Ticket should not be empty")
	}
	if ticket.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should be set")
	}
	if !strings.HasPrefix(ticket.WebsocketURL, "/api/v1/vms/vm-running/console?ticket=") {
		t.Errorf("WebsocketURL = %q", ticket.WebsocketURL)
	}

	// The ticket should consume successfully — single-use, so a second
	// consume should fail with not-found.
	if _, err := store.ConsumeTicket(ticket.Ticket, "vm-running"); err != nil {
		t.Errorf("first consume: %v", err)
	}
	if _, err := store.ConsumeTicket(ticket.Ticket, "vm-running"); err == nil {
		t.Error("second consume should fail (single-use)")
	}
}

func TestIssueConsoleTicket_StoppedVM_Returns409(t *testing.T) {
	ts, mockMgr, _, cleanup := consoleTestServer(t, true)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:    "vm-stopped",
		Name:  "stopped",
		State: types.VMStateStopped,
	})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-stopped/console/ticket", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "vm_not_running")
}

func TestIssueConsoleTicket_UnknownVM_Returns404(t *testing.T) {
	ts, _, _, cleanup := consoleTestServer(t, true)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-missing/console/ticket", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}

func TestIssueConsoleTicket_NoStoreReturns503(t *testing.T) {
	ts, mockMgr, _, cleanup := consoleTestServer(t, false)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:    "vm-running",
		State: types.VMStateRunning,
	})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-running/console/ticket", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "service_unavailable")
}

func TestIssueConsoleTicket_TicketCarriesCallerAPIKey(t *testing.T) {
	ts, mockMgr, store, cleanup := consoleTestServer(t, true)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID:    "vm-keyed",
		State: types.VMStateRunning,
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/api/v1/vms/vm-keyed/console/ticket", nil)
	req.Header.Set("Authorization", "Bearer caller-key-abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var ticket types.ConsoleTicket
	decodeJSON(t, resp, &ticket)

	gotKey, err := store.ConsumeTicket(ticket.Ticket, "vm-keyed")
	if err != nil {
		t.Fatalf("ConsumeTicket: %v", err)
	}
	if gotKey != "caller-key-abc" {
		t.Errorf("apiKey = %q, want caller-key-abc", gotKey)
	}
}
