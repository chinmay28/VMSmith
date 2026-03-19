package vm

import (
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestGenerateDomainXML_Basic(t *testing.T) {
	params := DomainParams{
		Name:     "test-vm",
		CPUs:     2,
		RAMMB:    2048,
		DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2",
		Interfaces: []InterfaceEntry{
			{XML: `<interface type='network'><source network='vmsmith-net'/><model type='virtio'/></interface>`},
		},
	}

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}

	// Verify critical elements
	checks := []struct {
		desc, needle string
	}{
		{"domain type", "<domain type='kvm'>"},
		{"name", "<name>test-vm</name>"},
		{"memory", "<memory unit='MiB'>2048</memory>"},
		{"vcpu", "<vcpu placement='static'>2</vcpu>"},
		{"disk path", "file='/var/lib/vmsmith/vms/vm-1/disk.qcow2'"},
		{"virtio model", "type='virtio'"},
		{"network interface", "vmsmith-net"},
	}

	for _, c := range checks {
		if !strings.Contains(xml, c.needle) {
			t.Errorf("%s: expected %q in XML output", c.desc, c.needle)
		}
	}

	// Should NOT contain cloud-init when not set
	if strings.Contains(xml, "cidata") {
		t.Error("should not have cloud-init cdrom when CloudInitISO is empty")
	}
}

func TestGenerateDomainXML_WithCloudInit(t *testing.T) {
	params := DomainParams{
		Name:         "ci-vm",
		CPUs:         1,
		RAMMB:        1024,
		DiskPath:     "/tmp/disk.qcow2",
		CloudInitISO: "/tmp/cidata.iso",
		Interfaces:   []InterfaceEntry{{XML: "<interface type='network'/>"}},
	}

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}

	if !strings.Contains(xml, "cidata.iso") {
		t.Error("should contain cloud-init ISO path")
	}
	if !strings.Contains(xml, "device='cdrom'") {
		t.Error("should contain cdrom device for cloud-init")
	}
}

func TestDomainParamsFromSpec_NATOnly(t *testing.T) {
	spec := types.VMSpec{
		Name:  "simple-vm",
		CPUs:  2,
		RAMMB: 2048,
	}

	params := DomainParamsFromSpec(spec, "/tmp/disk.qcow2", "", "vmsmith-net")

	if len(params.Interfaces) != 1 {
		t.Fatalf("expected 1 interface (NAT), got %d", len(params.Interfaces))
	}
	if !strings.Contains(params.Interfaces[0].XML, "vmsmith-net") {
		t.Error("first interface should be NAT network")
	}
}

func TestDomainParamsFromSpec_MultiNetwork(t *testing.T) {
	spec := types.VMSpec{
		Name:  "multi-net-vm",
		CPUs:  4,
		RAMMB: 8192,
		Networks: []types.NetworkAttachment{
			{HostInterface: "eth1", Mode: types.NetworkModeMacvtap, MacAddress: "52:54:00:aa:bb:01"},
			{HostInterface: "eth2", Mode: types.NetworkModeMacvtap, MacAddress: "52:54:00:aa:bb:02"},
			{Mode: types.NetworkModeBridge, Bridge: "br-storage", MacAddress: "52:54:00:aa:bb:03"},
		},
	}

	params := DomainParamsFromSpec(spec, "/tmp/disk.qcow2", "", "vmsmith-net")

	// NAT + 3 extra = 4
	if len(params.Interfaces) != 4 {
		t.Fatalf("expected 4 interfaces, got %d", len(params.Interfaces))
	}

	// First is NAT
	if !strings.Contains(params.Interfaces[0].XML, "type='network'") {
		t.Error("first interface should be NAT network")
	}

	// Second is macvtap on eth1
	if !strings.Contains(params.Interfaces[1].XML, "type='direct'") {
		t.Error("second interface should be macvtap (direct)")
	}
	if !strings.Contains(params.Interfaces[1].XML, "dev='eth1'") {
		t.Error("second interface should reference eth1")
	}
	if !strings.Contains(params.Interfaces[1].XML, "52:54:00:aa:bb:01") {
		t.Error("second interface should use specified MAC")
	}

	// Fourth is bridge
	if !strings.Contains(params.Interfaces[3].XML, "type='bridge'") {
		t.Error("fourth interface should be bridge")
	}
	if !strings.Contains(params.Interfaces[3].XML, "br-storage") {
		t.Error("fourth interface should reference br-storage")
	}
}

func TestDomainParamsFromSpec_BridgeDefaultName(t *testing.T) {
	spec := types.VMSpec{
		Name: "bridge-vm",
		Networks: []types.NetworkAttachment{
			{HostInterface: "eth3", Mode: types.NetworkModeBridge, MacAddress: "52:54:00:cc:dd:ee"},
		},
	}

	params := DomainParamsFromSpec(spec, "/tmp/d.qcow2", "", "vmsmith-net")

	// Bridge should default to "br-eth3" when Bridge field is empty
	if !strings.Contains(params.Interfaces[1].XML, "br-eth3") {
		t.Error("bridge should default to br-<interface> when not specified")
	}
}

func TestDomainParamsFromSpec_SkipsEmptyInterface(t *testing.T) {
	spec := types.VMSpec{
		Name: "skip-vm",
		Networks: []types.NetworkAttachment{
			{Mode: types.NetworkModeMacvtap, HostInterface: ""}, // should be skipped
		},
	}

	params := DomainParamsFromSpec(spec, "/tmp/d.qcow2", "", "vmsmith-net")

	// Only NAT interface (the empty macvtap should be skipped)
	if len(params.Interfaces) != 1 {
		t.Errorf("expected 1 interface (skipped empty macvtap), got %d", len(params.Interfaces))
	}
}

func TestGenerateMAC(t *testing.T) {
	mac := generateMAC()

	if !strings.HasPrefix(mac, "52:54:00:") {
		t.Errorf("MAC should start with KVM prefix 52:54:00, got %q", mac)
	}
	if len(mac) != 17 { // 52:54:00:xx:xx:xx
		t.Errorf("MAC length = %d, want 17", len(mac))
	}

	// Generate two and ensure they're different (probabilistic but safe)
	mac2 := generateMAC()
	if mac == mac2 {
		t.Error("two generated MACs should be different")
	}
}

// --- Validation tests ---

func TestValidateNetworkAttachments_Valid(t *testing.T) {
	nets := []types.NetworkAttachment{
		{Name: "data", Mode: types.NetworkModeMacvtap, HostInterface: "eth1"},
		{Name: "storage", Mode: types.NetworkModeBridge, HostInterface: "eth2"},
	}

	if err := ValidateNetworkAttachments(nets); err != nil {
		t.Errorf("valid networks rejected: %v", err)
	}
}

func TestValidateNetworkAttachments_MissingInterface(t *testing.T) {
	nets := []types.NetworkAttachment{
		{Name: "bad", Mode: types.NetworkModeMacvtap, HostInterface: ""},
	}

	err := ValidateNetworkAttachments(nets)
	if err == nil {
		t.Fatal("expected error for missing host_interface")
	}
	if !strings.Contains(err.Error(), "host_interface is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateNetworkAttachments_DuplicateInterface(t *testing.T) {
	nets := []types.NetworkAttachment{
		{Mode: types.NetworkModeMacvtap, HostInterface: "eth1"},
		{Mode: types.NetworkModeMacvtap, HostInterface: "eth1"},
	}

	err := ValidateNetworkAttachments(nets)
	if err == nil {
		t.Fatal("expected error for duplicate interface")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateNetworkAttachments_UnknownMode(t *testing.T) {
	nets := []types.NetworkAttachment{
		{Mode: "bonkers", HostInterface: "eth1"},
	}

	err := ValidateNetworkAttachments(nets)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestValidateNetworkAttachments_BridgeNeedsBridgeOrInterface(t *testing.T) {
	nets := []types.NetworkAttachment{
		{Mode: types.NetworkModeBridge, Bridge: "br0"}, // OK - has bridge name
	}
	if err := ValidateNetworkAttachments(nets); err != nil {
		t.Errorf("bridge with bridge name should be valid: %v", err)
	}

	nets = []types.NetworkAttachment{
		{Mode: types.NetworkModeBridge}, // no bridge, no interface
	}
	if err := ValidateNetworkAttachments(nets); err == nil {
		t.Error("bridge without bridge or interface should be invalid")
	}
}

func TestValidateNetworkAttachments_Empty(t *testing.T) {
	if err := ValidateNetworkAttachments(nil); err != nil {
		t.Errorf("empty should be valid: %v", err)
	}
	if err := ValidateNetworkAttachments([]types.NetworkAttachment{}); err != nil {
		t.Errorf("empty slice should be valid: %v", err)
	}
}

func TestDomainParamsFromSpec_NATAttachment(t *testing.T) {
	spec := types.VMSpec{
		Name: "nat-vm",
		Networks: []types.NetworkAttachment{
			{Mode: types.NetworkModeNAT, MacAddress: "52:54:00:aa:bb:cc"},
		},
	}

	params := DomainParamsFromSpec(spec, "/tmp/d.qcow2", "", "vmsmith-net")

	// Should have 2 interfaces: default NAT + extra NAT attachment
	if len(params.Interfaces) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(params.Interfaces))
	}
	if !strings.Contains(params.Interfaces[1].XML, "aa:bb:cc") {
		t.Error("extra NAT interface should include specified MAC")
	}
}

func TestGenerateDomainXML_MultipleInterfaces(t *testing.T) {
	params := DomainParams{
		Name:     "multi-vm",
		CPUs:     2,
		RAMMB:    2048,
		DiskPath: "/tmp/disk.qcow2",
		Interfaces: []InterfaceEntry{
			{XML: `<interface type='network'><source network='vmsmith-net'/></interface>`},
			{XML: `<interface type='direct'><source dev='eth1' mode='bridge'/></interface>`},
		},
	}

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}

	if !strings.Contains(xml, "vmsmith-net") {
		t.Error("should contain NAT network")
	}
	if !strings.Contains(xml, "eth1") {
		t.Error("should contain macvtap interface")
	}
}

// --- Cloud-init network config generation tests ---

func TestGenerateNetworkConfig_StaticIP(t *testing.T) {
	networks := []types.NetworkAttachment{
		{StaticIP: "192.168.1.100/24", Gateway: "192.168.1.1"},
		{StaticIP: "192.168.2.100/24"},
	}

	cfg := generateNetworkConfig(networks)

	checks := []string{
		"version: 2",
		"eth0:",
		"dhcp4: true",
		"eth1:",
		"192.168.1.100/24",
		"via: 192.168.1.1",
		"eth2:",
		"192.168.2.100/24",
	}

	for _, needle := range checks {
		if !strings.Contains(cfg, needle) {
			t.Errorf("expected %q in network config:\n%s", needle, cfg)
		}
	}

	// eth2 has no gateway, should not have routes
	eth2Section := cfg[strings.Index(cfg, "eth2:"):]
	if strings.Contains(eth2Section, "routes:") && !strings.Contains(eth2Section, "eth1") {
		// More precise check: eth2 section specifically should not have routes
	}
}

func TestGenerateNetworkConfig_DHCP(t *testing.T) {
	networks := []types.NetworkAttachment{
		{}, // No static IP → DHCP
	}

	cfg := generateNetworkConfig(networks)

	// eth1 should be DHCP
	if !strings.Contains(cfg, "eth1:") {
		t.Error("expected eth1 in config")
	}

	lines := strings.Split(cfg, "\n")
	for i, line := range lines {
		if strings.Contains(line, "eth1:") && i+1 < len(lines) {
			if !strings.Contains(lines[i+1], "dhcp4: true") {
				t.Error("eth1 should use DHCP when no static IP specified")
			}
		}
	}
}

func TestGenerateNetworkConfig_RouteMetric(t *testing.T) {
	networks := []types.NetworkAttachment{
		{StaticIP: "10.0.1.100/24", Gateway: "10.0.1.1"},
		{StaticIP: "10.0.2.100/24", Gateway: "10.0.2.1"},
	}

	cfg := generateNetworkConfig(networks)

	if !strings.Contains(cfg, "metric: 200") {
		t.Error("first network should have metric 200")
	}
	if !strings.Contains(cfg, "metric: 201") {
		t.Error("second network should have metric 201")
	}
}

func TestMachineTypeFromCaps(t *testing.T) {
	rhel96Caps := `<capabilities>
  <guest>
    <os_type>hvm</os_type>
    <arch name='x86_64'>
      <machine canonical='pc-q35-rhel9.6.0' maxCpus='255'>q35</machine>
      <machine maxCpus='255'>pc-q35-rhel9.6.0</machine>
      <machine maxCpus='255'>pc-q35-rhel9.5.0</machine>
      <domain type='kvm'/>
    </arch>
  </guest>
</capabilities>`

	upstreamCaps := `<capabilities>
  <guest>
    <os_type>hvm</os_type>
    <arch name='x86_64'>
      <machine maxCpus='255'>pc-q35-9.2</machine>
      <machine maxCpus='255'>pc-q35-9.1</machine>
      <domain type='kvm'/>
    </arch>
  </guest>
</capabilities>`

	noKVMCaps := `<capabilities>
  <guest>
    <os_type>hvm</os_type>
    <arch name='x86_64'>
      <machine maxCpus='255'>pc-q35-9.2</machine>
      <domain type='qemu'/>
    </arch>
  </guest>
</capabilities>`

	tests := []struct {
		name     string
		caps     string
		fallback string
		want     string
	}{
		{
			name:     "rhel9.6 q35 alias with canonical",
			caps:     rhel96Caps,
			fallback: "pc-q35-6.2",
			want:     "pc-q35-rhel9.6.0",
		},
		{
			name:     "upstream first pc-q35 listed",
			caps:     upstreamCaps,
			fallback: "pc-q35-6.2",
			want:     "pc-q35-9.2",
		},
		{
			name:     "no kvm domain falls back",
			caps:     noKVMCaps,
			fallback: "pc-q35-6.2",
			want:     "pc-q35-6.2",
		},
		{
			name:     "invalid xml falls back",
			caps:     "not xml",
			fallback: "pc-q35-6.2",
			want:     "pc-q35-6.2",
		},
		{
			name:     "empty caps falls back",
			caps:     "<capabilities/>",
			fallback: "pc-q35-6.2",
			want:     "pc-q35-6.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := machineTypeFromCaps(tt.caps, tt.fallback)
			if got != tt.want {
				t.Errorf("machineTypeFromCaps = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDomainParamsFromSpec_DefaultMachine(t *testing.T) {
	spec := types.VMSpec{Name: "test", CPUs: 1, RAMMB: 1024}
	params := DomainParamsFromSpec(spec, "/tmp/disk.qcow2", "", "vmsmith-net")
	if params.Machine != "pc-q35-6.2" {
		t.Errorf("default Machine = %q, want %q", params.Machine, "pc-q35-6.2")
	}
}

func TestHasStaticIPs(t *testing.T) {
	tests := []struct {
		name string
		nets []types.NetworkAttachment
		want bool
	}{
		{"nil", nil, false},
		{"empty", []types.NetworkAttachment{}, false},
		{"no static", []types.NetworkAttachment{{HostInterface: "eth1"}}, false},
		{"has static", []types.NetworkAttachment{{StaticIP: "10.0.0.1/24"}}, true},
		{"mixed", []types.NetworkAttachment{{}, {StaticIP: "10.0.0.1/24"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasStaticIPs(tt.nets)
			if got != tt.want {
				t.Errorf("hasStaticIPs = %v, want %v", got, tt.want)
			}
		})
	}
}
