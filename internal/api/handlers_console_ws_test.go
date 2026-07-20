package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/console"
	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type testEventStore struct{ seq uint64 }

func (s *testEventStore) AppendEvent(evt *types.Event) (uint64, error) {
	s.seq++
	return s.seq, nil
}

func consoleWebSocketTestServer(t *testing.T, mutator func(*config.Config), ttl time.Duration) (*httptest.Server, *Server, *vm.MockManager, *console.Store, func()) {
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
	return ts, apiServer, mockMgr, consoleStore, cleanup
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
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
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

func TestProxyConsole_RejectsMissingTicket(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})
	ln, err := mockMgr.SeedConsoleListener("vm-running")
	if err != nil {
		t.Fatalf("SeedConsoleListener: %v", err)
	}
	defer ln.Close()

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	_, resp, err := dialer.Dial(wsURLFromHTTP(ts.URL, "/api/v1/vms/vm-running/console"), nil)
	if err == nil {
		t.Fatal("dial succeeded without ticket, want unauthorized failure")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("missing ticket status = %d, want 401", status)
	}
}

func TestProxyConsole_RejectsTicketReuse(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
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
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, 25*time.Millisecond)
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
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
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
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, func(cfg *config.Config) {
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

func TestProxyConsole_BulkStopClosesActiveSession(t *testing.T) {
	ts, apiServer, mockMgr, store, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	bus := events.New(&testEventStore{})
	bus.Start()
	defer bus.Stop()
	apiServer.SetEventBus(bus)
	ch, cancel := bus.Subscribe("console-bulk-close-test")
	defer cancel()

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

	resp, err := http.Post(ts.URL+"/api/v1/vms/bulk", "application/json", strings.NewReader(`{"action":"stop","ids":["vm-running"]}`))
	if err != nil {
		t.Fatalf("bulk stop: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	readDeadline := time.Now().Add(2 * time.Second)
	if err := conn.SetReadDeadline(readDeadline); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("websocket stayed open after bulk stop")
	}

	events := drainEvents(t, ch, 2)
	for _, evt := range events {
		if evt.Type != "console.session_terminated" {
			continue
		}
		if evt.VMID != "vm-running" {
			t.Fatalf("event VMID = %q, want vm-running", evt.VMID)
		}
		if evt.Attributes["reason"] != "vm_stopped" {
			t.Fatalf("reason = %q, want vm_stopped", evt.Attributes["reason"])
		}
		return
	}
	t.Fatalf("missing console.session_terminated event: %+v", events)
}

func TestProxyConsole_ClosesOnServerShutdownSignal(t *testing.T) {
	ts, apiServer, mockMgr, store, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

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
	backend := <-accepted
	defer backend.Close()

	apiServer.BeginShutdown()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("websocket stayed open after shutdown signal")
		default:
			_, _, err := conn.ReadMessage()
			if err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

func TestProxyConsole_ClosesOnVMLifecycleHandlers(t *testing.T) {
	actions := []struct {
		name       string
		method     string
		path       string
		reasonCode string
	}{
		{name: "stop", method: http.MethodPost, path: "/api/v1/vms/vm-running/stop", reasonCode: "vm_stopped"},
		{name: "force-stop", method: http.MethodPost, path: "/api/v1/vms/vm-running/force-stop", reasonCode: "vm_force_stopped"},
		{name: "restart", method: http.MethodPost, path: "/api/v1/vms/vm-running/restart", reasonCode: "vm_restarted"},
		{name: "reboot", method: http.MethodPost, path: "/api/v1/vms/vm-running/reboot", reasonCode: "vm_rebooted"},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/vms/vm-running", reasonCode: "vm_deleted"},
	}

	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			ts, apiServer, mockMgr, store, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
			defer cleanup()

			bus := events.New(&testEventStore{})
			bus.Start()
			defer bus.Stop()
			apiServer.SetEventBus(bus)
			eventCh, cancelEvents := bus.Subscribe("test")
			defer cancelEvents()

			mockMgr.SeedVM(&types.VM{ID: "vm-running", Name: "running", State: types.VMStateRunning})
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
			dialer := *websocket.DefaultDialer
			dialer.Subprotocols = []string{"binary"}
			conn, _, err := dialer.Dial(wsURLFromHTTP(ts.URL, "/api/v1/vms/vm-running/console?ticket="+ticket), nil)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			backend := <-accepted
			defer backend.Close()

			req, err := http.NewRequest(tc.method, ts.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want 200/204", resp.StatusCode)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			for {
				select {
				case <-ctx.Done():
					t.Fatal("websocket stayed open after VM lifecycle handler")
				default:
					_, _, err := conn.ReadMessage()
					if err != nil {
						_ = conn.Close()
						events := drainEvents(t, eventCh, 1)
						if len(events) != 1 || events[0].Type != "console.session_terminated" {
							t.Fatalf("expected console.session_terminated event, got %+v", events)
						}
						if events[0].VMID != "vm-running" {
							t.Fatalf("event VMID = %q, want vm-running", events[0].VMID)
						}
						if events[0].Attributes["reason"] != tc.reasonCode {
							t.Fatalf("event reason = %q, want %q", events[0].Attributes["reason"], tc.reasonCode)
						}
						return
					}
				}
			}
		})
	}
}

func TestProxyConsole_ClosesOnDirectManagerLifecycleActions(t *testing.T) {
	tests := []struct {
		name       string
		action     func(context.Context, *vm.MockManager, string) error
		reasonCode string
	}{
		{name: "stop", action: func(ctx context.Context, mgr *vm.MockManager, id string) error { return mgr.Stop(ctx, id) }, reasonCode: "vm_stopped"},
		{name: "force-stop", action: func(ctx context.Context, mgr *vm.MockManager, id string) error { return mgr.ForceStop(ctx, id) }, reasonCode: "vm_force_stopped"},
		{name: "restart", action: func(ctx context.Context, mgr *vm.MockManager, id string) error { return mgr.Restart(ctx, id) }, reasonCode: "vm_restarted"},
		{name: "reboot", action: func(ctx context.Context, mgr *vm.MockManager, id string) error { return mgr.Reboot(ctx, id) }, reasonCode: "vm_rebooted"},
		{name: "delete", action: func(ctx context.Context, mgr *vm.MockManager, id string) error { return mgr.Delete(ctx, id) }, reasonCode: "vm_deleted"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts, apiServer, mockMgr, store, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
			defer cleanup()

			bus := events.New(&testEventStore{})
			bus.Start()
			defer bus.Stop()
			ch, cancelEvents := bus.Subscribe("test")
			defer cancelEvents()
			apiServer.SetEventBus(bus)

			mockMgr.SeedVM(&types.VM{ID: "vm-running", Name: "running", State: types.VMStateRunning})
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
			dialer := *websocket.DefaultDialer
			dialer.Subprotocols = []string{"binary"}
			conn, _, err := dialer.Dial(wsURLFromHTTP(ts.URL, "/api/v1/vms/vm-running/console?ticket="+ticket), nil)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			backend := <-accepted
			defer backend.Close()

			if err := tc.action(context.Background(), mockMgr, "vm-running"); err != nil {
				t.Fatalf("manager action: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			for {
				select {
				case <-ctx.Done():
					t.Fatal("websocket stayed open after direct manager lifecycle action")
				default:
					_, _, err := conn.ReadMessage()
					if err != nil {
						_ = conn.Close()
						events := drainEvents(t, ch, 1)
						if len(events) != 1 || events[0].Type != "console.session_terminated" {
							t.Fatalf("expected console.session_terminated event, got %+v", events)
						}
						if events[0].VMID != "vm-running" {
							t.Fatalf("event VMID = %q, want vm-running", events[0].VMID)
						}
						if events[0].Attributes["reason"] != tc.reasonCode {
							t.Fatalf("event reason = %q, want %q", events[0].Attributes["reason"], tc.reasonCode)
						}
						return
					}
				}
			}
		})
	}
}

func TestProxyConsole_NoStoreReturns503(t *testing.T) {
	ts, apiServer, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()
	apiServer.SetConsoleStore(nil)
	mockMgr.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-running/console?ticket=fake")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func TestProxyConsole_HonorsForwardedProtoForTLSDeployments(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, func(cfg *config.Config) {
		cfg.Daemon.TLS.CertFile = "/tmp/test.crt"
		cfg.Daemon.TLS.KeyFile = "/tmp/test.key"
	}, time.Minute)
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
			defer conn.Close()
			buf := make([]byte, 16)
			_, _ = conn.Read(buf)
		}
	}()

	ticket := issueConsoleTicket(t, ts, "vm-running")
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	headers := http.Header{"X-Forwarded-Proto": []string{"https"}}
	conn, resp, err := dialer.Dial(wsURLFromHTTP(ts.URL, ticket.WebsocketURL), headers)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("Dial = %v (status=%d)", err, status)
	}
	_ = conn.Close()
}

func TestProxyConsole_RejectsWhenSessionLimitReached(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, func(cfg *config.Config) {
		cfg.Daemon.Console.MaxConcurrentSessions = 1
	}, time.Minute)
	defer cleanup()

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

	first := issueConsoleTicket(t, ts, "vm-running")
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	firstConn, _, err := dialer.Dial(wsURLFromHTTP(ts.URL, first.WebsocketURL), nil)
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	defer firstConn.Close()
	backend := <-accepted
	defer backend.Close()

	second := issueConsoleTicket(t, ts, "vm-running")
	_, resp, err := dialer.Dial(wsURLFromHTTP(ts.URL, second.WebsocketURL), nil)
	if err == nil {
		t.Fatal("second dial succeeded, want 429")
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 429", status)
	}
}

func TestProxyConsole_ClosesWhenMaxSessionDeadlineExpires(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, func(cfg *config.Config) {
		cfg.Daemon.Console.MaxSessionSeconds = 1
	}, time.Minute)
	defer cleanup()

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

	ticket := issueConsoleTicket(t, ts, "vm-running")
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	conn, _, err := dialer.Dial(wsURLFromHTTP(ts.URL, ticket.WebsocketURL), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	backend := <-accepted
	defer backend.Close()

	readDeadline := time.Now().Add(5 * time.Second)
	if err := conn.SetReadDeadline(readDeadline); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, err := conn.ReadMessage()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			_ = conn.Close()
			return
		}
	}
	t.Fatal("websocket stayed open after max session deadline")
}

func TestProxyConsole_ClosesWhenIdleTimeoutExpires(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, func(cfg *config.Config) {
		cfg.Daemon.Console.IdleTimeoutSeconds = 1
	}, time.Minute)
	defer cleanup()

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

	ticket := issueConsoleTicket(t, ts, "vm-running")
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"binary"}
	conn, _, err := dialer.Dial(wsURLFromHTTP(ts.URL, ticket.WebsocketURL), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	backend := <-accepted
	defer backend.Close()

	readDeadline := time.Now().Add(5 * time.Second)
	if err := conn.SetReadDeadline(readDeadline); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, err := conn.ReadMessage()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			_ = conn.Close()
			return
		}
	}
	t.Fatal("websocket stayed open after idle timeout")
}

// TestProxyConsole_LifecycleHandlersEmitExactlyOneTerminationEvent locks in
// that the manager-callback path (wired via SetConsoleSessionTerminator in
// router.go) is the single source of console.session_terminated emission for
// API-driven stop/delete — an explicit second closeConsoleSessionsForVM call
// in the HTTP handlers would double-fire the event because the sessions map
// is unregistered asynchronously by the proxy goroutine. The earlier
// TestProxyConsole_ClosesOn* tests drain only the first event and cannot see
// a duplicate.
func TestProxyConsole_LifecycleHandlersEmitExactlyOneTerminationEvent(t *testing.T) {
	actions := []struct {
		name       string
		method     string
		path       string
		body       string
		reasonCode string
	}{
		{name: "stop", method: http.MethodPost, path: "/api/v1/vms/vm-running/stop", reasonCode: "vm_stopped"},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/vms/vm-running", reasonCode: "vm_deleted"},
		{name: "bulk-stop", method: http.MethodPost, path: "/api/v1/vms/bulk", body: `{"action":"stop","ids":["vm-running"]}`, reasonCode: "vm_stopped"},
		{name: "bulk-delete", method: http.MethodPost, path: "/api/v1/vms/bulk", body: `{"action":"delete","ids":["vm-running"]}`, reasonCode: "vm_deleted"},
	}

	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			ts, apiServer, mockMgr, store, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
			defer cleanup()

			bus := events.New(&testEventStore{})
			bus.Start()
			defer bus.Stop()
			apiServer.SetEventBus(bus)
			eventCh, cancelEvents := bus.Subscribe("dupe-test")
			defer cancelEvents()

			mockMgr.SeedVM(&types.VM{ID: "vm-running", Name: "running", State: types.VMStateRunning})
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
			dialer := *websocket.DefaultDialer
			dialer.Subprotocols = []string{"binary"}
			conn, _, err := dialer.Dial(wsURLFromHTTP(ts.URL, "/api/v1/vms/vm-running/console?ticket="+ticket), nil)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			defer conn.Close()
			backend := <-accepted
			defer backend.Close()

			var bodyReader io.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			}
			req, err := http.NewRequest(tc.method, ts.URL+tc.path, bodyReader)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want 200/204", resp.StatusCode)
			}

			// Wait for the websocket to be force-closed, then over-drain the
			// event channel so a hypothetical duplicate emission has arrived
			// before we count.
			if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatalf("SetReadDeadline: %v", err)
			}
			if _, _, err := conn.ReadMessage(); err == nil {
				t.Fatal("websocket stayed open after lifecycle handler")
			}

			events := drainEvents(t, eventCh, 3)
			terminated := 0
			for _, evt := range events {
				if evt.Type != "console.session_terminated" {
					continue
				}
				terminated++
				if evt.VMID != "vm-running" {
					t.Fatalf("event VMID = %q, want vm-running", evt.VMID)
				}
				if evt.Attributes["reason"] != tc.reasonCode {
					t.Fatalf("event reason = %q, want %q", evt.Attributes["reason"], tc.reasonCode)
				}
			}
			if terminated != 1 {
				t.Fatalf("expected exactly 1 console.session_terminated event, got %d (events: %+v)", terminated, events)
			}
		})
	}
}
