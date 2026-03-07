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

	// Networks defines additional network attachments beyond the default NAT.
	// The vmsmith NAT network is always attached as the first interface.
	// These are extra interfaces for reaching private/data networks on the host.
	Networks []NetworkAttachment `json:"networks,omitempty" yaml:"networks,omitempty"`
}

// VM represents a virtual machine and its current state.
type VM struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Spec      VMSpec    `json:"spec"`
	State     VMState   `json:"state"`
	IP        string    `json:"ip,omitempty"`
	DiskPath  string    `json:"disk_path"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

