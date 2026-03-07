package cli

import (
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestParseNetworkFlags_SimpleInterface(t *testing.T) {
	nets, err := parseNetworkFlags([]string{"eth1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nets) != 1 {
		t.Fatalf("expected 1 network, got %d", len(nets))
	}
	if nets[0].HostInterface != "eth1" {
		t.Errorf("HostInterface = %q, want eth1", nets[0].HostInterface)
	}
	if nets[0].Mode != types.NetworkModeMacvtap {
		t.Errorf("Mode = %q, want macvtap", nets[0].Mode)
	}
	if nets[0].Name != "eth1" {
		t.Errorf("Name = %q, want eth1 (defaults to interface name)", nets[0].Name)
	}
}

func TestParseNetworkFlags_WithStaticIP(t *testing.T) {
	nets, err := parseNetworkFlags([]string{"eth2:ip=192.168.2.100/24,gw=192.168.2.1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nets[0].StaticIP != "192.168.2.100/24" {
		t.Errorf("StaticIP = %q", nets[0].StaticIP)
	}
	if nets[0].Gateway != "192.168.2.1" {
		t.Errorf("Gateway = %q", nets[0].Gateway)
	}
}

func TestParseNetworkFlags_BridgeMode(t *testing.T) {
	nets, err := parseNetworkFlags([]string{"eth3:mode=bridge,bridge=br-storage"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nets[0].Mode != types.NetworkModeBridge {
		t.Errorf("Mode = %q, want bridge", nets[0].Mode)
	}
	if nets[0].Bridge != "br-storage" {
		t.Errorf("Bridge = %q, want br-storage", nets[0].Bridge)
	}
}

func TestParseNetworkFlags_AllOptions(t *testing.T) {
	nets, err := parseNetworkFlags([]string{
		"eth1:name=data-net,mode=macvtap,ip=10.0.1.50/24,gw=10.0.1.1,mac=52:54:00:aa:bb:cc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n := nets[0]
	if n.Name != "data-net" {
		t.Errorf("Name = %q", n.Name)
	}
	if n.Mode != types.NetworkModeMacvtap {
		t.Errorf("Mode = %q", n.Mode)
	}
	if n.StaticIP != "10.0.1.50/24" {
		t.Errorf("StaticIP = %q", n.StaticIP)
	}
	if n.Gateway != "10.0.1.1" {
		t.Errorf("Gateway = %q", n.Gateway)
	}
	if n.MacAddress != "52:54:00:aa:bb:cc" {
		t.Errorf("MacAddress = %q", n.MacAddress)
	}
}

func TestParseNetworkFlags_MultipleNetworks(t *testing.T) {
	nets, err := parseNetworkFlags([]string{"eth1", "eth2:ip=192.168.2.50/24", "eth3", "eth4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nets) != 4 {
		t.Fatalf("expected 4 networks, got %d", len(nets))
	}
	if nets[1].StaticIP != "192.168.2.50/24" {
		t.Errorf("nets[1].StaticIP = %q", nets[1].StaticIP)
	}
}

func TestParseNetworkFlags_GatewayAlias(t *testing.T) {
	nets, err := parseNetworkFlags([]string{"eth1:ip=10.0.0.1/24,gateway=10.0.0.254"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nets[0].Gateway != "10.0.0.254" {
		t.Errorf("gateway alias not recognized: %q", nets[0].Gateway)
	}
}

func TestParseNetworkFlags_EmptySlice(t *testing.T) {
	nets, err := parseNetworkFlags(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nets) != 0 {
		t.Errorf("expected empty result, got %d", len(nets))
	}
}

// --- Error cases ---

func TestParseNetworkFlags_EmptyInterfaceName(t *testing.T) {
	_, err := parseNetworkFlags([]string{""})
	if err == nil {
		t.Fatal("expected error for empty interface name")
	}
}

func TestParseNetworkFlags_InvalidOption(t *testing.T) {
	_, err := parseNetworkFlags([]string{"eth1:badkey=val"})
	if err == nil {
		t.Fatal("expected error for unknown option")
	}
}

func TestParseNetworkFlags_InvalidMode(t *testing.T) {
	_, err := parseNetworkFlags([]string{"eth1:mode=telepathy"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestParseNetworkFlags_MalformedOption(t *testing.T) {
	_, err := parseNetworkFlags([]string{"eth1:noequalssign"})
	if err == nil {
		t.Fatal("expected error for malformed option")
	}
}

// --- humanSize tests ---

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{1024, "1024 B"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{2684354560, "2.5 GB"},
		{536870912, "512.0 MB"},
	}

	for _, tt := range tests {
		got := humanSize(tt.bytes)
		if got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
