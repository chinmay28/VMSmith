package api

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestEncodeGuacInstruction(t *testing.T) {
	got := encodeGuacInstruction("select", "rdp")
	if got != "6.select,3.rdp;" {
		t.Errorf("encode = %q, want 6.select,3.rdp;", got)
	}
	// Lengths count runes, not bytes.
	got = encodeGuacInstruction("name", "héllo")
	if got != "4.name,5.héllo;" {
		t.Errorf("encode = %q, want 4.name,5.héllo;", got)
	}
	if got := encodeGuacInstruction("audio"); got != "5.audio;" {
		t.Errorf("empty-args encode = %q, want 5.audio;", got)
	}
}

func TestGuacReader_RoundTrip(t *testing.T) {
	wire := encodeGuacInstruction("size", "1024", "768", "96") + encodeGuacInstruction("sync", "12345")
	r := newGuacReader(strings.NewReader(wire))

	inst, err := r.ReadInstruction()
	if err != nil {
		t.Fatalf("ReadInstruction: %v", err)
	}
	if inst.Opcode != "size" || len(inst.Args) != 3 || inst.Args[0] != "1024" {
		t.Fatalf("inst = %+v", inst)
	}
	inst, err = r.ReadInstruction()
	if err != nil {
		t.Fatalf("second ReadInstruction: %v", err)
	}
	if inst.Opcode != "sync" || inst.Args[0] != "12345" {
		t.Fatalf("inst = %+v", inst)
	}
}

func TestGuacReader_RejectsGarbage(t *testing.T) {
	r := newGuacReader(strings.NewReader("not-a-guac-instruction"))
	if _, err := r.ReadInstruction(); err == nil {
		t.Fatal("expected error for garbage input")
	}
}

// startFakeGuacd runs a minimal guacd: performs the server side of the
// handshake and then echoes every subsequent instruction back.
func startFakeGuacd(t *testing.T) (addr string, connected chan map[string]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	connected = make(chan map[string]string, 1)

	argNames := []string{"VERSION_1_5_0", "hostname", "port", "ignore-cert", "security"}

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := newGuacReader(conn)

		sel, err := reader.ReadInstruction()
		if err != nil || sel.Opcode != "select" || len(sel.Args) == 0 || sel.Args[0] != "rdp" {
			return
		}
		if _, err := conn.Write([]byte(encodeGuacInstruction("args", argNames...))); err != nil {
			return
		}

		params := map[string]string{}
		for {
			inst, err := reader.ReadInstruction()
			if err != nil {
				return
			}
			if inst.Opcode == "connect" {
				for i, name := range argNames {
					if i < len(inst.Args) {
						params[name] = inst.Args[i]
					}
				}
				break
			}
			// size / audio / video / image — record and continue.
			params["saw_"+inst.Opcode] = "true"
		}
		connected <- params

		if _, err := conn.Write([]byte(encodeGuacInstruction("ready", "$test-connection"))); err != nil {
			return
		}
		// Echo phase.
		for {
			inst, err := reader.ReadInstruction()
			if err != nil {
				return
			}
			if _, err := conn.Write([]byte(encodeGuacInstruction(inst.Opcode, inst.Args...))); err != nil {
				return
			}
		}
	}()

	return ln.Addr().String(), connected
}

func TestGuacdHandshakeRDP_AgainstFakeGuacd(t *testing.T) {
	addr, connected := startFakeGuacd(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_, ready, err := guacdHandshakeRDP(conn, "192.168.100.50", 3389, 1280, 720)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if len(ready.Args) == 0 || ready.Args[0] != "$test-connection" {
		t.Fatalf("ready = %+v", ready)
	}

	params := <-connected
	if params["hostname"] != "192.168.100.50" || params["port"] != "3389" {
		t.Errorf("connect params = %+v", params)
	}
	if params["ignore-cert"] != "true" || params["security"] != "any" {
		t.Errorf("connect params = %+v", params)
	}
	if params["VERSION_1_5_0"] != "VERSION_1_5_0" {
		t.Errorf("version not echoed: %+v", params)
	}
	if params["saw_size"] != "true" || params["saw_image"] != "true" {
		t.Errorf("pre-connect instructions missing: %+v", params)
	}
}

func TestProxyConsole_RDPRoundTripViaFakeGuacd(t *testing.T) {
	guacdAddr, _ := startFakeGuacd(t)
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, func(cfg *config.Config) {
		cfg.Daemon.Console.GuacdAddress = guacdAddr
	}, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-win", Name: "win", State: types.VMStateRunning, IP: "192.168.100.60"})

	ticket := issueConsoleTicketWithIntent(t, ts, "vm-win", "rdp")
	if ticket.Intent != types.ConsoleIntentRDP {
		t.Fatalf("ticket intent = %q, want rdp", ticket.Intent)
	}

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"guacamole"}
	conn, resp, err := dialer.Dial(wsURLFromHTTP(ts.URL, ticket.WebsocketURL), nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("Dial: %v (status %d)", err, status)
	}
	defer conn.Close()
	if got := conn.Subprotocol(); got != "guacamole" {
		t.Fatalf("subprotocol = %q, want guacamole", got)
	}

	// The daemon must forward guacd's ready instruction first.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}
	if string(payload) != encodeGuacInstruction("ready", "$test-connection") {
		t.Fatalf("first frame = %q, want ready", payload)
	}

	// Instructions we send must round-trip through the guacd echo.
	sync := encodeGuacInstruction("sync", "42")
	if err := conn.WriteMessage(websocket.TextMessage, []byte(sync)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, payload, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(payload) != sync {
		t.Fatalf("echo = %q, want %q", payload, sync)
	}
}

func TestIssueConsoleTicket_RDPWithoutGuacdReturns503(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-win", Name: "win", State: types.VMStateRunning, IP: "192.168.100.60"})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-win/console/ticket?intent=rdp", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "rdp_console_unavailable")
}

func TestProxyConsole_RDPVMWithoutIPUnavailable(t *testing.T) {
	guacdAddr, _ := startFakeGuacd(t)
	ts, _, mockMgr, store, cleanup := consoleWebSocketTestServer(t, func(cfg *config.Config) {
		cfg.Daemon.Console.GuacdAddress = guacdAddr
	}, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-noip", Name: "noip", State: types.VMStateRunning})
	ticket, _, err := store.IssueTicket("vm-noip", "", "rdp")
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"guacamole"}
	_, resp, err := dialer.Dial(wsURLFromHTTP(ts.URL, "/api/v1/vms/vm-noip/console?intent=rdp&ticket="+ticket), nil)
	if err == nil {
		t.Fatal("dial succeeded, want 503")
	}
	if resp == nil || resp.StatusCode != 503 {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("status = %d, want 503", status)
	}
}
