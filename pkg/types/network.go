package types

// Protocol represents a network protocol for port forwarding.
type Protocol string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

// NetworkMode controls how a VM attaches to a host network.
type NetworkMode string

const (
	// NetworkModeNAT uses the vmsmith-managed libvirt NAT network (default).
	NetworkModeNAT NetworkMode = "nat"

	// NetworkModeMacvtap attaches directly to a host interface via macvtap.
	// The VM gets its own MAC and IP on the host's physical network.
	// Simple, zero host config, but host-to-VM traffic on the same NIC
	// requires vepa-capable switches or passthrough mode.
	NetworkModeMacvtap NetworkMode = "macvtap"

	// NetworkModeBridge attaches to a pre-existing Linux bridge on the host.
	// Requires the bridge to be set up in advance, but allows full
	// host-to-VM communication on the same subnet.
	NetworkModeBridge NetworkMode = "bridge"
)

// NetworkAttachment defines how a VM connects to a specific network.
type NetworkAttachment struct {
	// Name is a user-friendly label (e.g. "data-net", "storage-net").
	Name string `json:"name" yaml:"name"`

	// Mode determines the attachment method.
	Mode NetworkMode `json:"mode" yaml:"mode"`

	// HostInterface is the host NIC to attach to (required for macvtap/bridge).
	// Examples: "eth1", "ens192", "bond0"
	HostInterface string `json:"host_interface,omitempty" yaml:"host_interface,omitempty"`

	// Bridge is the name of an existing Linux bridge (required for bridge mode).
	// Example: "br-data"
	Bridge string `json:"bridge,omitempty" yaml:"bridge,omitempty"`

	// StaticIP optionally sets a static IP for this interface inside the VM
	// via cloud-init. If empty, the VM will use DHCP.
	StaticIP string `json:"static_ip,omitempty" yaml:"static_ip,omitempty"`

	// Gateway is the optional default gateway for static IP configurations.
	Gateway string `json:"gateway,omitempty" yaml:"gateway,omitempty"`

	// MacAddress optionally sets a specific MAC. If empty, one is generated.
	MacAddress string `json:"mac_address,omitempty" yaml:"mac_address,omitempty"`
}

// PortForward represents a NAT port forwarding rule.
type PortForward struct {
	ID        string   `json:"id"`
	VMID      string   `json:"vm_id"`
	HostPort  int      `json:"host_port"`
	GuestPort int      `json:"guest_port"`
	GuestIP   string   `json:"guest_ip"`
	Protocol  Protocol `json:"protocol"`
}

// NetworkInfo holds network details for a VM.
type NetworkInfo struct {
	InternalIP   string              `json:"internal_ip"`
	NetworkName  string              `json:"network_name"`
	Attachments  []NetworkAttachment `json:"attachments,omitempty"`
	PortForwards []PortForward       `json:"port_forwards"`
}

// HostInterface describes a network interface discovered on the host.
type HostInterface struct {
	Name   string   `json:"name"`
	IPs    []string `json:"ips"`
	MAC    string   `json:"mac"`
	IsUp   bool     `json:"is_up"`
	IsPhys bool     `json:"is_physical"`
}
