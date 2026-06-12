package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// issueConsoleTicketIntent mirrors issueConsoleTicket but requests an
// explicit ?intent= flavour (5.1.9).
func issueConsoleTicketIntent(t *testing.T, tsURL, vmID, intent string) types.ConsoleTicket {
	t.Helper()
	resp, err := http.Post(tsURL+"/api/v1/vms/"+vmID+"/console/ticket?intent="+intent, "application/json", nil)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("issue ticket status = %d, body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var ticket types.ConsoleTicket
	if err := json.NewDecoder(resp.Body).Decode(&ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	return ticket
}

func TestIssueConsoleTicket_IntentField(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-run", Name: "run", State: types.VMStateRunning})

	// Default intent is vnc.
	vncTicket := issueConsoleTicket(t, ts, "vm-run")
	if vncTicket.Intent != types.ConsoleIntentVNC {
		t.Errorf("default intent = %q, want vnc", vncTicket.Intent)
	}

	serialTicket := issueConsoleTicketIntent(t, ts.URL, "vm-run", "serial")
	if serialTicket.Intent != types.ConsoleIntentSerial {
		t.Errorf("serial intent = %q, want serial", serialTicket.Intent)
	}
}

func TestIssueConsoleTicket_InvalidIntent(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-run", Name: "run", State: types.VMStateRunning})

	resp, err := http.Post(ts.URL+"/api/v1/vms/vm-run/console/ticket?intent=rdp", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "invalid_console_intent") {
		t.Errorf("body = %s, want invalid_console_intent", body)
	}
}

// TestProxyConsole_SerialTextRoundTrip drives the full 5.1.9 path: serial
// ticket → websocket upgrade on the `text` subprotocol → keystrokes pumped
// into the manager's serial stream → console output pushed back as text
// frames. The mock manager is seeded with one end of a net.Pipe so the
// test can script the pty conversation.
func TestProxyConsole_SerialTextRoundTrip(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-serial", Name: "serial", State: types.VMStateRunning})
	proxySide, testSide := net.Pipe()
	mockMgr.SeedSerialConsole("vm-serial", proxySide)
	defer testSide.Close()

	ticket := issueConsoleTicketIntent(t, ts.URL, "vm-serial", "serial")
	wsURL := wsURLFromHTTP(ts.URL, ticket.WebsocketURL)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"text"}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("Dial = %v (status=%d)", err, status)
	}
	defer conn.Close()

	if got := conn.Subprotocol(); got != "text" {
		t.Fatalf("subprotocol = %q, want text", got)
	}

	// Keystrokes: ws text frame → serial stream.
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ls\n")); err != nil {
		t.Fatalf("write keystrokes: %v", err)
	}
	_ = testSide.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 16)
	n, err := testSide.Read(buf)
	if err != nil {
		t.Fatalf("read keystrokes from serial side: %v", err)
	}
	if got := string(buf[:n]); got != "ls\n" {
		t.Errorf("serial received %q, want %q", got, "ls\n")
	}

	// Console output: serial stream → ws text frame.
	if _, err := testSide.Write([]byte("file-a file-b\n")); err != nil {
		t.Fatalf("write console output: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msgType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws frame: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Errorf("frame type = %d, want text", msgType)
	}
	if string(payload) != "file-a file-b\n" {
		t.Errorf("frame payload = %q, want console output", payload)
	}
}

// TestProxyConsole_VNCTicketCannotOpenSerial pins the intent-binding
// property: the ws proxy follows the *ticket's* intent, so a VNC ticket
// keeps hitting the VNC endpoint path (and vice versa) regardless of what
// the caller hoped to reach.
func TestProxyConsole_SerialTicketIgnoresVNCEndpoint(t *testing.T) {
	ts, _, mockMgr, _, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-x", Name: "x", State: types.VMStateRunning})
	// No VNC listener is seeded: the synthetic 127.0.0.1:5900 endpoint is
	// unreachable, so a VNC-flavoured proxy attempt would 502. A serial
	// ticket must not touch it — the seeded serial pipe keeps it alive.
	proxySide, testSide := net.Pipe()
	mockMgr.SeedSerialConsole("vm-x", proxySide)
	defer testSide.Close()

	ticket := issueConsoleTicketIntent(t, ts.URL, "vm-x", "serial")
	wsURL := wsURLFromHTTP(ts.URL, ticket.WebsocketURL)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"text"}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("serial dial should succeed without a VNC listener: %v", err)
	}
	conn.Close()
}

func TestProxyConsole_SerialStoppedVM(t *testing.T) {
	ts, _, mockMgr, consoleStore, cleanup := consoleWebSocketTestServer(t, nil, time.Minute)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-stop", Name: "stop", State: types.VMStateStopped})

	// Ticket issuance refuses stopped VMs, so mint one directly against
	// the store to exercise the proxy's own state check.
	token, _, err := consoleStore.IssueTicketIntent("vm-stop", "", types.ConsoleIntentSerial)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-stop/console?ticket=" + token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409; body=%s", resp.StatusCode, body)
	}
}
