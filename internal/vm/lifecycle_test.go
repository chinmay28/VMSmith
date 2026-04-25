package vm

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// --- buildNMKeyfile tests ---

func TestBuildNMKeyfile_DHCP(t *testing.T) {
	mac := "52:54:00:aa:bb:cc"
	kf := buildNMKeyfile(mac, "", "")

	checks := []string{
		"[connection]",
		"id=vmsmith-nat",
		"type=ethernet",
		"autoconnect=true",
		"mac-address=" + mac,
		"method=auto",
		"[ipv6]",
		"method=ignore",
	}
	for _, c := range checks {
		if !strings.Contains(kf, c) {
			t.Errorf("missing %q in keyfile:\n%s", c, kf)
		}
	}
	if strings.Contains(kf, "method=manual") {
		t.Error("DHCP keyfile should not contain method=manual")
	}
}

func TestBuildNMKeyfile_Static(t *testing.T) {
	mac := "52:54:00:aa:bb:cc"
	kf := buildNMKeyfile(mac, "192.168.100.50/24", "192.168.100.1")

	checks := []string{
		"method=manual",
		"addresses=192.168.100.50/24",
		"gateway=192.168.100.1",
		"dns=192.168.100.1;8.8.8.8;",
		"mac-address=" + mac,
	}
	for _, c := range checks {
		if !strings.Contains(kf, c) {
			t.Errorf("missing %q in keyfile:\n%s", c, kf)
		}
	}
	if strings.Contains(kf, "method=auto") {
		t.Error("static keyfile should not contain method=auto")
	}
}

func TestBuildNMKeyfile_StaticNoGateway(t *testing.T) {
	kf := buildNMKeyfile("52:54:00:aa:bb:cc", "10.0.0.5/24", "")

	if !strings.Contains(kf, "method=manual") {
		t.Error("should use manual method")
	}
	if !strings.Contains(kf, "addresses=10.0.0.5/24") {
		t.Error("should contain static address")
	}
	if strings.Contains(kf, "gateway=") {
		t.Error("should not contain gateway when none given")
	}
	if strings.Contains(kf, "dns=") {
		t.Error("should not contain dns when no gateway")
	}
}

// --- buildCloudConfig tests ---

func TestBuildCloudConfig_RootDefault(t *testing.T) {
	spec := types.VMSpec{
		Name:      "test-vm",
		SSHPubKey: "ssh-ed25519 AAAA...",
	}
	cc := buildCloudConfig(spec, "52:54:00:aa:bb:cc")

	checks := []string{
		"#cloud-config",
		"disable_root: false",
		"name: root",
		"ssh_authorized_keys",
		"ssh-ed25519 AAAA...",
		"write_files:",
		"vmsmith-nat.nmconnection",
		"permissions: '0600'",
		"PermitRootLogin prohibit-password",
		"99-vmsmith-root.conf",
		"runcmd:",
		"nmcli connection reload",
		"nmcli connection up vmsmith-nat",
		"restorecon",
		"systemctl reload-or-restart sshd",
	}
	for _, c := range checks {
		if !strings.Contains(cc, c) {
			t.Errorf("missing %q in cloud-config:\n%s", c, cc)
		}
	}
}

func TestBuildCloudConfig_RootNoSSHKey(t *testing.T) {
	spec := types.VMSpec{Name: "nokey-vm"}
	cc := buildCloudConfig(spec, "52:54:00:aa:bb:cc")

	if !strings.Contains(cc, "disable_root: false") {
		t.Error("should disable_root: false for root default")
	}
	if strings.Contains(cc, "ssh_authorized_keys") {
		t.Error("should not have ssh_authorized_keys without a key")
	}
	// Should still have sshd root config
	if !strings.Contains(cc, "PermitRootLogin prohibit-password") {
		t.Error("should have root login config")
	}
}

func TestBuildCloudConfig_NamedUser(t *testing.T) {
	spec := types.VMSpec{
		Name:        "user-vm",
		DefaultUser: "deploy",
		SSHPubKey:   "ssh-rsa AAAA...",
	}
	cc := buildCloudConfig(spec, "52:54:00:aa:bb:cc")

	checks := []string{
		"disable_root: true",
		"name: deploy",
		"ssh_authorized_keys",
		"ssh-rsa AAAA...",
		"sudo: ALL=(ALL) NOPASSWD:ALL",
		"shell: /bin/bash",
		"lock_passwd: true",
	}
	for _, c := range checks {
		if !strings.Contains(cc, c) {
			t.Errorf("missing %q in cloud-config:\n%s", c, cc)
		}
	}
	// Should NOT have root login config
	if strings.Contains(cc, "PermitRootLogin") {
		t.Error("named user config should not have PermitRootLogin")
	}
	if strings.Contains(cc, "99-vmsmith-root.conf") {
		t.Error("named user config should not have root sshd config")
	}
}

func TestBuildCloudConfig_NamedUserNoKey(t *testing.T) {
	spec := types.VMSpec{
		Name:        "user-vm",
		DefaultUser: "admin",
	}
	cc := buildCloudConfig(spec, "52:54:00:aa:bb:cc")

	if !strings.Contains(cc, "disable_root: true") {
		t.Error("should disable root for named user")
	}
	if !strings.Contains(cc, "name: admin") {
		t.Error("should contain user name")
	}
	if !strings.Contains(cc, "lock_passwd: false") {
		t.Error("should have lock_passwd: false when no SSH key (allows password login)")
	}
	if strings.Contains(cc, "ssh_authorized_keys") {
		t.Error("should not have ssh_authorized_keys without a key")
	}
}

func TestBuildCloudConfig_ContainsNMKeyfile(t *testing.T) {
	spec := types.VMSpec{
		Name:        "net-vm",
		NatStaticIP: "192.168.100.50/24",
		NatGateway:  "192.168.100.1",
	}
	cc := buildCloudConfig(spec, "52:54:00:aa:bb:cc")

	// The NM keyfile should be embedded with proper indentation
	if !strings.Contains(cc, "method=manual") {
		t.Error("cloud-config should contain static NM keyfile content")
	}
	if !strings.Contains(cc, "addresses=192.168.100.50/24") {
		t.Error("cloud-config should contain static IP in NM keyfile")
	}
}

// --- gatewayFromSubnet tests ---

func TestGatewayFromSubnet(t *testing.T) {
	tests := []struct {
		subnet string
		want   string
	}{
		{"192.168.100.0/24", "192.168.100.1"},
		{"10.0.0.0/8", "10.0.0.1"},
		{"172.16.0.0/16", "172.16.0.1"},
		{"invalid", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := gatewayFromSubnet(tt.subnet)
		if got != tt.want {
			t.Errorf("gatewayFromSubnet(%q) = %q, want %q", tt.subnet, got, tt.want)
		}
	}
}

// --- parseNetworkHostIPs tests ---

func TestParseNetworkHostIPs(t *testing.T) {
	xmlStr := `<network>
  <ip address='192.168.100.1' netmask='255.255.255.0'>
    <dhcp>
      <range start='192.168.100.10' end='192.168.100.254'/>
      <host mac='52:54:00:aa:bb:01' name='vm1' ip='192.168.100.50'/>
      <host mac='52:54:00:aa:bb:02' name='vm2' ip='192.168.100.51'/>
    </dhcp>
  </ip>
</network>`

	ips := parseNetworkHostIPs(xmlStr)
	if len(ips) != 2 {
		t.Fatalf("expected 2 IPs, got %d: %v", len(ips), ips)
	}
	if ips[0] != "192.168.100.50" {
		t.Errorf("ips[0] = %q, want 192.168.100.50", ips[0])
	}
	if ips[1] != "192.168.100.51" {
		t.Errorf("ips[1] = %q, want 192.168.100.51", ips[1])
	}
}

func TestParseNetworkHostIPs_NoDHCP(t *testing.T) {
	xmlStr := `<network><ip address='192.168.100.1'/></network>`
	ips := parseNetworkHostIPs(xmlStr)
	if len(ips) != 0 {
		t.Errorf("expected 0 IPs, got %d", len(ips))
	}
}

func TestParseNetworkHostIPs_InvalidXML(t *testing.T) {
	ips := parseNetworkHostIPs("not xml at all")
	if ips != nil {
		t.Errorf("expected nil for invalid XML, got %v", ips)
	}
}

func TestParseNetworkHostIPs_Empty(t *testing.T) {
	ips := parseNetworkHostIPs("<network/>")
	if len(ips) != 0 {
		t.Errorf("expected 0 IPs, got %d", len(ips))
	}
}

func TestCloneVMSpec(t *testing.T) {
	source := types.VMSpec{
		Name:        "source-vm",
		Image:       "ubuntu-24.04",
		CPUs:        4,
		RAMMB:       8192,
		DiskGB:      80,
		Description: "source description",
		Tags:        []string{"prod", "web"},
		NatStaticIP: "192.168.100.50/24",
		NatGateway:  "192.168.100.1",
		Networks: []types.NetworkAttachment{
			{
				Mode:        types.NetworkModeMacvtap,
				HostInterface: "eth1",
				StaticIP:    "10.0.0.10/24",
				Gateway:     "10.0.0.1",
				MacAddress:  "52:54:00:aa:bb:cc",
			},
		},
	}

	cloned := cloneVMSpec(source, "clone-a")
	if cloned.Name != "clone-a" {
		t.Fatalf("clone name = %q, want clone-a", cloned.Name)
	}
	if cloned.Image != source.Image || cloned.CPUs != source.CPUs || cloned.RAMMB != source.RAMMB || cloned.DiskGB != source.DiskGB {
		t.Fatalf("clone spec lost source resources: %+v", cloned)
	}
	if cloned.NatStaticIP != "" || cloned.NatGateway != "" {
		t.Fatalf("clone NAT config = %q / %q, want empty", cloned.NatStaticIP, cloned.NatGateway)
	}
	if len(cloned.Tags) != 2 || cloned.Tags[0] != "prod" || cloned.Tags[1] != "web" {
		t.Fatalf("clone tags = %#v", cloned.Tags)
	}
	if len(cloned.Networks) != 1 {
		t.Fatalf("clone networks len = %d, want 1", len(cloned.Networks))
	}
	if cloned.Networks[0].StaticIP != "" || cloned.Networks[0].Gateway != "" {
		t.Fatalf("clone network static config = %+v, want cleared", cloned.Networks[0])
	}
	if cloned.Networks[0].MacAddress == "" || cloned.Networks[0].MacAddress == source.Networks[0].MacAddress {
		t.Fatalf("clone network MAC = %q, want new generated MAC", cloned.Networks[0].MacAddress)
	}

	cloned.Tags[0] = "changed"
	cloned.Networks[0].HostInterface = "eth9"
	if source.Tags[0] != "prod" {
		t.Fatal("source tags mutated after clone")
	}
	if source.Networks[0].HostInterface != "eth1" {
		t.Fatal("source networks mutated after clone")
	}
}

func TestParseNetworkHostIPs_MultipleIPBlocks(t *testing.T) {
	xmlStr := `<network>
  <ip address='192.168.100.1'>
    <dhcp>
      <host mac='aa:bb:cc:dd:ee:01' ip='192.168.100.10'/>
    </dhcp>
  </ip>
  <ip address='10.0.0.1'>
    <dhcp>
      <host mac='aa:bb:cc:dd:ee:02' ip='10.0.0.10'/>
    </dhcp>
  </ip>
</network>`
	ips := parseNetworkHostIPs(xmlStr)
	if len(ips) != 2 {
		t.Fatalf("expected 2 IPs from 2 blocks, got %d: %v", len(ips), ips)
	}
}

// --- isExecNotFound tests ---

func TestIsExecNotFound(t *testing.T) {
	// Test with a real exec.Error wrapping ErrNotFound
	err := &exec.Error{Name: "nosuchtool", Err: exec.ErrNotFound}
	if !isExecNotFound(err) {
		t.Errorf("expected isExecNotFound to return true for missing binary, got false")
	}
}

func TestIsExecNotFound_OtherError(t *testing.T) {
	if isExecNotFound(nil) {
		t.Error("expected false for nil error")
	}
	if isExecNotFound(fmt.Errorf("some other error")) {
		t.Error("expected false for generic error")
	}
}

// --- generateNetworkConfig with NAT static IP ---

func TestGenerateNetworkConfig_NATStaticIP(t *testing.T) {
	natMAC := "52:54:00:aa:bb:cc"
	cfg := generateNetworkConfig(nil, natMAC, "192.168.100.50/24", "192.168.100.1")

	checks := []string{
		natMAC,
		"addresses:",
		"192.168.100.50/24",
		"via: 192.168.100.1",
		"dhcp4: false",
	}
	for _, c := range checks {
		if !strings.Contains(cfg, c) {
			t.Errorf("missing %q in NAT static config:\n%s", c, cfg)
		}
	}
	if strings.Contains(cfg, "dhcp4: true") {
		t.Error("NAT with static IP should not have dhcp4: true")
	}
}
