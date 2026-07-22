package vm

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestParseConsoleEndpointFromXML_VNC_RunningDomain(t *testing.T) {
	raw := `<domain type='kvm'>
  <name>foo</name>
  <devices>
    <graphics type='vnc' port='5901' autoport='yes' listen='127.0.0.1'/>
  </devices>
</domain>`

	endpoint, err := parseConsoleEndpointFromXML(raw, types.ConsoleIntentVNC)
	if err != nil {
		t.Fatalf("parseConsoleEndpointFromXML returned error: %v", err)
	}
	if endpoint.Intent != types.ConsoleIntentVNC {
		t.Errorf("intent = %q, want %q", endpoint.Intent, types.ConsoleIntentVNC)
	}
	if endpoint.Host != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", endpoint.Host)
	}
	if endpoint.Port != 5901 {
		t.Errorf("port = %d, want 5901", endpoint.Port)
	}
	if endpoint.Path != "" {
		t.Errorf("path = %q, want empty for vnc", endpoint.Path)
	}
}

func TestParseConsoleEndpointFromXML_VNC_DefaultListen(t *testing.T) {
	raw := `<domain>
  <devices>
    <graphics type='vnc' port='5905'/>
  </devices>
</domain>`

	endpoint, err := parseConsoleEndpointFromXML(raw, types.ConsoleIntentVNC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if endpoint.Host != "127.0.0.1" {
		t.Errorf("default host = %q, want 127.0.0.1", endpoint.Host)
	}
	if endpoint.Port != 5905 {
		t.Errorf("port = %d, want 5905", endpoint.Port)
	}
}

func TestParseConsoleEndpointFromXML_VNC_PortNotAssigned(t *testing.T) {
	// libvirt records port=-1 before the domain has been started or the
	// auto-assigned port has been resolved.
	raw := `<domain>
  <devices>
    <graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>
  </devices>
</domain>`

	_, err := parseConsoleEndpointFromXML(raw, types.ConsoleIntentVNC)
	if err == nil {
		t.Fatalf("expected console_unavailable error for unassigned port")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *types.APIError", err)
	}
	if apiErr.Code != "console_unavailable" {
		t.Errorf("code = %q, want console_unavailable", apiErr.Code)
	}
}

func TestParseConsoleEndpointFromXML_VNC_NoGraphicsElement(t *testing.T) {
	raw := `<domain>
  <devices>
  </devices>
</domain>`
	_, err := parseConsoleEndpointFromXML(raw, types.ConsoleIntentVNC)
	if err == nil {
		t.Fatalf("expected error for missing graphics element")
	}
	if !strings.Contains(err.Error(), "vnc") {
		t.Errorf("error message %q should mention vnc", err.Error())
	}
}

func TestParseConsoleEndpointFromXML_Serial_FromConsoleTTY(t *testing.T) {
	raw := `<domain>
  <devices>
    <serial type='pty'>
      <target port='0'/>
    </serial>
    <console type='pty' tty='/dev/pts/3'>
      <target type='serial' port='0'/>
    </console>
  </devices>
</domain>`

	endpoint, err := parseConsoleEndpointFromXML(raw, types.ConsoleIntentSerial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if endpoint.Intent != types.ConsoleIntentSerial {
		t.Errorf("intent = %q, want %q", endpoint.Intent, types.ConsoleIntentSerial)
	}
	if endpoint.Path != "/dev/pts/3" {
		t.Errorf("path = %q, want /dev/pts/3", endpoint.Path)
	}
	if endpoint.Host != "" || endpoint.Port != 0 {
		t.Errorf("host/port should be empty for serial, got host=%q port=%d", endpoint.Host, endpoint.Port)
	}
}

func TestParseConsoleEndpointFromXML_Serial_FromSerialSourcePath(t *testing.T) {
	// Older or unstarted XML may carry the path on the <serial><source>
	// element instead of the live <console tty=...> attribute.
	raw := `<domain>
  <devices>
    <serial type='pty'>
      <source path='/dev/pts/7'/>
      <target port='0'/>
    </serial>
  </devices>
</domain>`

	endpoint, err := parseConsoleEndpointFromXML(raw, types.ConsoleIntentSerial)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if endpoint.Path != "/dev/pts/7" {
		t.Errorf("path = %q, want /dev/pts/7", endpoint.Path)
	}
}

func TestParseConsoleEndpointFromXML_Serial_NotYetAllocated(t *testing.T) {
	// Stopped domains list <console type='pty'> without a tty/path; the
	// pty is only allocated by libvirt at start time.
	raw := `<domain>
  <devices>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
  </devices>
</domain>`

	_, err := parseConsoleEndpointFromXML(raw, types.ConsoleIntentSerial)
	if err == nil {
		t.Fatalf("expected console_unavailable for missing tty/path")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *types.APIError", err)
	}
	if apiErr.Code != "console_unavailable" {
		t.Errorf("code = %q, want console_unavailable", apiErr.Code)
	}
}

func TestParseConsoleEndpointFromXML_RejectsUnknownIntent(t *testing.T) {
	raw := `<domain><devices/></domain>`
	_, err := parseConsoleEndpointFromXML(raw, types.ConsoleIntent("spice"))
	if err == nil {
		t.Fatalf("expected invalid_console_intent for unknown intent")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *types.APIError", err)
	}
	if apiErr.Code != "invalid_console_intent" {
		t.Errorf("code = %q, want invalid_console_intent", apiErr.Code)
	}
}

func TestParseConsoleEndpointFromXML_RejectsMalformedXML(t *testing.T) {
	_, err := parseConsoleEndpointFromXML("not xml", types.ConsoleIntentVNC)
	if err == nil {
		t.Fatalf("expected error for malformed xml")
	}
}

// --- MockManager coverage ---------------------------------------------------

func TestMockManager_GetConsoleEndpoint_RunningVM_VNC(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, err := m.Create(ctx, types.VMSpec{Name: "vnc-test", Image: "ubuntu"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	endpoint, err := m.GetConsoleEndpoint(ctx, vm.ID, types.ConsoleIntentVNC)
	if err != nil {
		t.Fatalf("GetConsoleEndpoint: %v", err)
	}
	if endpoint.Intent != types.ConsoleIntentVNC || endpoint.Host == "" || endpoint.Port == 0 {
		t.Errorf("endpoint = %+v; want a populated vnc endpoint", endpoint)
	}
}

func TestMockManager_GetConsoleEndpoint_RunningVM_Serial(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, err := m.Create(ctx, types.VMSpec{Name: "serial-test", Image: "ubuntu"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	endpoint, err := m.GetConsoleEndpoint(ctx, vm.ID, types.ConsoleIntentSerial)
	if err != nil {
		t.Fatalf("GetConsoleEndpoint: %v", err)
	}
	if endpoint.Intent != types.ConsoleIntentSerial || endpoint.Path == "" {
		t.Errorf("endpoint = %+v; want a populated serial endpoint", endpoint)
	}
}

func TestMockManager_GetConsoleEndpoint_StoppedVM_Returns409(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, err := m.Create(ctx, types.VMSpec{Name: "stopped", Image: "ubuntu"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Stop(ctx, vm.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	_, err = m.GetConsoleEndpoint(ctx, vm.ID, types.ConsoleIntentVNC)
	if err == nil {
		t.Fatalf("expected vm_not_running error for stopped vm")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *types.APIError", err)
	}
	if apiErr.Code != "vm_not_running" {
		t.Errorf("code = %q, want vm_not_running", apiErr.Code)
	}
}

func TestMockManager_GetConsoleEndpoint_UnknownVM_NotFound(t *testing.T) {
	m := NewMockManager()
	_, err := m.GetConsoleEndpoint(context.Background(), "vm-missing", types.ConsoleIntentVNC)
	if err == nil {
		t.Fatalf("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q should mention not found", err.Error())
	}
}

func TestMockManager_GetConsoleEndpoint_RejectsInvalidIntent(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, err := m.Create(ctx, types.VMSpec{Name: "spice-test", Image: "ubuntu"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = m.GetConsoleEndpoint(ctx, vm.ID, types.ConsoleIntent("spice"))
	if err == nil {
		t.Fatalf("expected invalid_console_intent error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *types.APIError", err)
	}
	if apiErr.Code != "invalid_console_intent" {
		t.Errorf("code = %q, want invalid_console_intent", apiErr.Code)
	}
}

func TestMockManager_GetConsoleEndpoint_HookErrorPropagates(t *testing.T) {
	m := NewMockManager()
	want := errors.New("boom")
	m.GetConsoleEndpointErr = want

	_, err := m.GetConsoleEndpoint(context.Background(), "vm-anything", types.ConsoleIntentVNC)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestMockManager_SeedConsoleEndpoint_OverridesDefault(t *testing.T) {
	m := NewMockManager()
	ctx := context.Background()
	vm, err := m.Create(ctx, types.VMSpec{Name: "seeded", Image: "ubuntu"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	pinned := types.ConsoleEndpoint{
		Intent: types.ConsoleIntentVNC,
		Host:   "10.0.0.5",
		Port:   5999,
	}
	m.SeedConsoleEndpoint(vm.ID, types.ConsoleIntentVNC, pinned)

	got, err := m.GetConsoleEndpoint(ctx, vm.ID, types.ConsoleIntentVNC)
	if err != nil {
		t.Fatalf("GetConsoleEndpoint: %v", err)
	}
	if got.Host != pinned.Host || got.Port != pinned.Port {
		t.Errorf("got = %+v, want %+v", got, pinned)
	}
}

func TestMockManager_SeedConsoleListener_BindsRealSocket(t *testing.T) {
	m := NewMockManager()
	defer m.Close()
	ctx := context.Background()
	vm, err := m.Create(ctx, types.VMSpec{Name: "listener", Image: "ubuntu"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ln, err := m.SeedConsoleListener(vm.ID)
	if err != nil {
		t.Fatalf("SeedConsoleListener: %v", err)
	}
	defer ln.Close()

	endpoint, err := m.GetConsoleEndpoint(ctx, vm.ID, types.ConsoleIntentVNC)
	if err != nil {
		t.Fatalf("GetConsoleEndpoint: %v", err)
	}

	// We should be able to dial the address GetConsoleEndpoint gave us.
	addr := net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial seeded listener at %s: %v", addr, err)
	}
	conn.Close()
}

func TestMockManager_SeedConsoleListener_RejectsExternalInterfaceDial(t *testing.T) {
	m := NewMockManager()
	defer m.Close()
	ctx := context.Background()
	vm, err := m.Create(ctx, types.VMSpec{Name: "loopback-only", Image: "ubuntu"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ln, err := m.SeedConsoleListener(vm.ID)
	if err != nil {
		t.Fatalf("SeedConsoleListener: %v", err)
	}
	defer ln.Close()

	endpoint, err := m.GetConsoleEndpoint(ctx, vm.ID, types.ConsoleIntentVNC)
	if err != nil {
		t.Fatalf("GetConsoleEndpoint: %v", err)
	}
	if endpoint.Host != "127.0.0.1" {
		t.Fatalf("host = %q, want loopback 127.0.0.1", endpoint.Host)
	}

	externalIP := firstNonLoopbackIPv4(t)
	if externalIP == "" {
		t.Skip("no non-loopback IPv4 interface available on this host")
	}

	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	externalAddr := net.JoinHostPort(externalIP, strconv.Itoa(endpoint.Port))
	conn, err := dialer.DialContext(ctx, "tcp", externalAddr)
	if err == nil {
		conn.Close()
		t.Fatalf("external dial to %s unexpectedly succeeded; listener should be loopback-only", externalAddr)
	}
}

func firstNonLoopbackIPv4(t *testing.T) string {
	t.Helper()

	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("net.Interfaces: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			return ip.String()
		}
	}
	return ""
}
