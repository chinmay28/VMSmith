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
	Name          string   `json:"name" yaml:"name"`
	Image         string   `json:"image" yaml:"image"`
	TemplateID    string   `json:"template_id,omitempty" yaml:"template_id,omitempty"`
	CPUs          int      `json:"cpus" yaml:"cpus"`
	RAMMB         int      `json:"ram_mb" yaml:"ram_mb"`
	DiskGB        int      `json:"disk_gb" yaml:"disk_gb"`
	Description   string   `json:"description,omitempty" yaml:"description,omitempty"`
	Tags          []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	CloudInitFile string   `json:"cloud_init_file,omitempty" yaml:"cloud_init_file,omitempty"`
	SSHPubKey     string   `json:"ssh_pub_key,omitempty" yaml:"ssh_pub_key,omitempty"`
	DefaultUser   string   `json:"default_user,omitempty" yaml:"default_user,omitempty"`

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

	// AutoStart, when true, asks the daemon to start this VM automatically
	// after vmsmith starts up. The sweep runs once at daemon boot; VMs that
	// are already running are left untouched. Always serialised so clients
	// can distinguish "off" from "field absent".
	AutoStart bool `json:"auto_start" yaml:"auto_start"`
}

// VMUpdateSpec defines fields that can be changed on an existing VM.
// Zero / empty values are ignored (no change). Disk can only grow, not shrink.
type VMUpdateSpec struct {
	CPUs        int      `json:"cpus,omitempty"`
	RAMMB       int      `json:"ram_mb,omitempty"`
	DiskGB      int      `json:"disk_gb,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`

	// NatStaticIP reassigns the primary NAT interface IP address in CIDR
	// notation (e.g. "192.168.100.50/24"). The DHCP host reservation is
	// updated, the cloud-init ISO is regenerated with a new instance-id so
	// cloud-init re-runs on the next boot and writes the updated NM keyfile.
	// Leave empty for no change.
	NatStaticIP string `json:"nat_static_ip,omitempty"`

	// NatGateway overrides the gateway when NatStaticIP is also set.
	// Defaults to the subnet gateway (e.g. 192.168.100.1) when omitted.
	NatGateway string `json:"nat_gateway,omitempty"`

	// AutoStart toggles whether the daemon will start this VM automatically
	// at daemon boot. Use a pointer so we can distinguish "not provided"
	// (no change) from "explicitly false" (turn off).
	AutoStart *bool `json:"auto_start,omitempty"`
}

// VM represents a virtual machine and its current state.
type VM struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Spec        VMSpec    `json:"spec"`
	State       VMState   `json:"state"`
	IP          string    `json:"ip,omitempty"`
	NatMAC      string    `json:"nat_mac,omitempty"`
	DiskPath    string    `json:"disk_path"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
