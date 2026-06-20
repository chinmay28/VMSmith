package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/console"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
	"net/http/httptest"
	"os"
	"path/filepath"
)

func consoleWebSocketTestServer(t *testing.T, mutator func(*config.Config), ttl time.Duration) (*httptest.Server, *vm.MockManager, *console.Store, func()) {
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
	consoleStore := console.NewStoreWithOptions(ttl, time.Hour)
	apiServer.SetConsoleStore(consoleStore)

	ts := httptest.NewServer(apiServer)
	cleanup := func() {
		ts.Close()
		consoleStore.Close()
		s.Close()
	}
	return ts, mockMgr, consoleStore, cleanup
}

func issueConsoleTicket(t *testing.T, ts *httptest.Server, vmID string) types.ConsoleTicket {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/v1/vms/"+vmID+"/console/ticket", "application/json", nil)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("issue ticket status = %d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	defer resp.Body.Close()
	var ticket types.ConsoleTicket
	if err := json.NewDecoder(resp.Body).Decode(&ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	return ticket
}

func wsURLFromHTTP(raw string, pathAndQuery string) string {
	base, _ := url.Parse(raw)
	rel, _ := url.Parse(pathAndQuery)
	base.Scheme = "ws"
	base.Path = rel.Path
	base.RawQuery = rel.RawQuery
	return base.String()
}

func TestProxyConsole_ProxiesBinaryTraffic(t *testing.T) {
	ts, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", Name: "running", State: types.VMStateRunning})
	ln, err := mockMgr.SeedConsoleListener("vm-running")
	if err != nil {
		t.Fatalf("SeedConsoleListener: %v", err)
	}
	defer ln.Close()

	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 128)
		n, err := conn.Read(buf)
		if err == nil && n > 0 {
			_, _ = conn.Write(buf[:n])
		}
	}()

	ticket := issueConsoleTicket(t, ts, "vm-running")
	wsURL := wsURLFromHTTP(ts.URL, ticket.WebsocketURL)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("Dial = %v (status=%d)", err, status)
	}
	defer conn.Close()

	if got := conn.Subprotocol(); got != "binary" {
		t.Fatalf("subprotocol = %q, want binary", got)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("hello-vnc")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	msgType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("msgType = %d, want binary", msgType)
	}
	if string(payload) != "hello-vnc" {
		t.Fatalf("payload = %q, want hello-vnc", string(payload))
	}
	<-echoDone
}

func TestProxyConsole_RejectsTicketReuse(t *testing.T) {
	ts, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})
	ln, err := mockMgr.SeedConsoleListener("vm-running")
	if err != nil {
		t.Fatalf("SeedConsoleListener: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	ticket := issueConsoleTicket(t, ts, "vm-running")
	wsURL := wsURLFromHTTP(ts.URL, ticket.WebsocketURL)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	first, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	_ = first.Close()

	_, resp, err := dialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("second dial succeeded, want unauthorized failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("reuse status = %d, want 401", status)
	}
}

func TestProxyConsole_RejectsExpiredTicket(t *testing.T) {
	ts, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, 25*time.Millisecond)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})
	ln, err := mockMgr.SeedConsoleListener("vm-running")
	if err != nil {
		t.Fatalf("SeedConsoleListener: %v", err)
	}
	defer ln.Close()

	ticket := issueConsoleTicket(t, ts, "vm-running")
	time.Sleep(40 * time.Millisecond)

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	_, resp, err := dialer.Dial(wsURLFromHTTP(ts.URL, ticket.WebsocketURL), nil)
	if err == nil {
		t.Fatal("dial succeeded, want unauthorized failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expired status = %d, want 401", status)
	}
}

func TestProxyConsole_DialFailureReturns502(t *testing.T) {
	ts, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})
	mockMgr.SeedConsoleEndpoint("vm-running", types.ConsoleIntentVNC, types.ConsoleEndpoint{
		Intent: types.ConsoleIntentVNC,
		Host:   "127.0.0.1",
		Port:   1,
	})

	ticket := issueConsoleTicket(t, ts, "vm-running")
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	_, resp, err := dialer.Dial(wsURLFromHTTP(ts.URL, ticket.WebsocketURL), nil)
	if err == nil {
		t.Fatal("dial succeeded, want bad gateway")
	}
	if resp == nil || resp.StatusCode != http.StatusBadGateway {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 502", status)
	}
}

func TestProxyConsole_RejectsInsecureWebsocketWhenTLSConfigured(t *testing.T) {
	ts, mockMgr, _, cleanup := consoleWebSocketTestServer(t, func(cfg *config.Config) {
		cfg.Daemon.TLS.CertFile = "/tmp/test.crt"
		cfg.Daemon.TLS.KeyFile = "/tmp/test.key"
	}, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})
	ticket := issueConsoleTicket(t, ts, "vm-running")
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}

	_, resp, err := dialer.Dial(wsURLFromHTTP(ts.URL, ticket.WebsocketURL), nil)
	if err == nil {
		t.Fatal("dial succeeded, want forbidden")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 403", status)
	}
}

func TestProxyConsole_ClosesOnServerShutdownSignal(t *testing.T) {
	ts, mockMgr, store, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	srv := ts.Config.Handler.(*Server)
	bus := events.New(memStore{})
	bus.Start()
	defer bus.Stop()
	srv.SetEventBus(bus)
	ch, cancelEvents := bus.Subscribe("console-shutdown-test")
	defer cancelEvents()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})
	ln, err := mockMgr.SeedConsoleListener("vm-running")
	if err != nil {
		t.Fatalf("SeedConsoleListener: %v", err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	ticket, _, err := store.IssueTicket("vm-running", "")
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	wsURL := wsURLFromHTTP(ts.URL, "/api/v1/vms/vm-running/console?ticket="+ticket)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	backend := <-accepted
	defer backend.Close()

	srv.BeginShutdown()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("websocket stayed open after shutdown signal")
		default:
			_, _, err := conn.ReadMessage()
			if err != nil {
				goto closed
			}
		}
	}
closed:

	events := drainEvents(t, ch, 1)
	found := false
	for _, evt := range events {
		if evt.Type == "console.session_terminated" {
			found = true
			if evt.VMID != "vm-running" {
				t.Fatalf("event VMID = %q, want vm-running", evt.VMID)
			}
			if evt.Attributes["reason"] != "server_shutdown" {
				t.Fatalf("reason = %q, want server_shutdown", evt.Attributes["reason"])
			}
		}
	}
	if !found {
		t.Fatalf("missing console.session_terminated event: %+v", events)
	}
}
