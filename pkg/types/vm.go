package types

import "time"

// VMState represents the current state of a virtual machine.
type VMState string

const (
	VMStateCreating VMState = "creating"
	VMStateRunning  VMState = "running"
	VMStateStopped  VMState = "stopped"
	VMStateDeleted  VMState = "deleted"
	VMStateUnknown  VMState = "unknown"
)

// VMSpec defines the desired configuration for a new VM.
type VMSpec struct {
	Name          string `json:"name" yaml:"name"`
	Image         string `json:"image" yaml:"image"`
	CPUs          int    `json:"cpus" yaml:"cpus"`
	RAMMB         int    `json:"ram_mb" yaml:"ram_mb"`
	DiskGB        int    `json:"disk_gb" yaml:"disk_gb"`
	CloudInitFile string `json:"cloud_init_file,omitempty" yaml:"cloud_init_file,omitempty"`
	SSHPubKey     string `json:"ssh_pub_key,omitempty" yaml:"ssh_pub_key,omitempty"`
	DefaultUser   string `json:"default_user,omitempty" yaml:"default_user,omitempty"`

	// Networks defines additional network attachments beyond the default NAT.
	// The vmsmith NAT network is always attached as the first interface.
	// These are extra interfaces for reaching private/data networks on the host.
	Networks []NetworkAttachment `json:"networks,omitempty" yaml:"networks,omitempty"`

	// NatStaticIP optionally sets a static IP for the primary NAT interface
	// in CIDR notation (e.g. "192.168.100.50/24"). Leave empty for DHCP.
	NatStaticIP string `json:"nat_static_ip,omitempty" yaml:"nat_static_ip,omitempty"`

	// NatGateway is the gateway for NatStaticIP (e.g. "192.168.100.1").
	// Only used when NatStaticIP is set.
	NatGateway string `json:"nat_gateway,omitempty" yaml:"nat_gateway,omitempty"`
}

// NetworkIPUpdate specifies a new static IP for an extra network interface.
// Index is the zero-based position in VMSpec.Networks.
type NetworkIPUpdate struct {
	Index    int    `json:"index"`
	StaticIP string `json:"static_ip"`          // CIDR notation; empty to switch to DHCP
	Gateway  string `json:"gateway,omitempty"`
}

// VMUpdateSpec defines fields that can be changed on an existing VM.
// Zero / empty values are ignored (no change). Disk can only grow, not shrink.
type VMUpdateSpec struct {
	CPUs   int `json:"cpus,omitempty"`
	RAMMB  int `json:"ram_mb,omitempty"`
	DiskGB int `json:"disk_gb,omitempty"`

	// NetworkIPs updates the static IP of one or more extra network interfaces
	// (spec.networks). Each entry targets an interface by zero-based index.
	// The cloud-init ISO is regenerated so the new address takes effect on
	// the next boot. The NAT (primary) interface is not affected here as it
	// uses a DHCP reservation managed at creation time.
	NetworkIPs []NetworkIPUpdate `json:"network_ips,omitempty"`
}

// VM represents a virtual machine and its current state.
type VM struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Spec      VMSpec    `json:"spec"`
	State     VMState   `json:"state"`
	IP        string    `json:"ip,omitempty"`
	NatMAC    string    `json:"nat_mac,omitempty"`
	DiskPath  string    `json:"disk_path"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
