package types

import "time"

// VMState represents the current state of a virtual machine.
type VMState string

const (
	VMStateCreating VMState = "creating"
	VMStateRunning  VMState = "running"
	VMStateStopped  VMState = "stopped"
	VMStatePaused   VMState = "paused"
	VMStateDeleted  VMState = "deleted"
	VMStateUnknown  VMState = "unknown"
)

// OSType identifies the guest operating-system family. It drives the libvirt
// domain XML (disk bus, NIC model, clock offset, Hyper-V enlightenments) and
// the first-boot provisioning mechanism (cloud-init for Linux, cloudbase-init
// for Windows). An empty value is treated as OSTypeLinux for backwards
// compatibility with VMs created before this field existed.
type OSType string

const (
	OSTypeLinux   OSType = "linux"
	OSTypeWindows OSType = "windows"
)

// KnownWindowsVariants enumerates the Windows guest variants vmsmith
// recognises ("2020 version and up": Windows 10/11 workstation and Windows
// Server 2019/2022/2025). The variant is advisory metadata — it does not
// change the domain XML beyond the common Windows tuning — but validating it
// keeps the field honest and gives the GUI a fixed pick-list.
var KnownWindowsVariants = []string{
	"windows-10",
	"windows-11",
	"windows-server-2019",
	"windows-server-2022",
	"windows-server-2025",
}

// IsKnownWindowsVariant reports whether v is one of KnownWindowsVariants.
func IsKnownWindowsVariant(v string) bool {
	for _, known := range KnownWindowsVariants {
		if known == v {
			return true
		}
	}
	return false
}

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

	// OSType selects the guest OS family ("linux" or "windows"). Empty means
	// "linux". Windows guests get a Windows-tuned domain XML (SATA system disk,
	// e1000e NIC, localtime clock, Hyper-V enlightenments, USB tablet, QXL
	// video, and an attached virtio-win driver ISO when configured) and are
	// provisioned via a cloudbase-init NoCloud datasource instead of cloud-init.
	OSType OSType `json:"os_type,omitempty" yaml:"os_type,omitempty"`

	// OSVariant is the specific Windows edition (e.g. "windows-server-2022",
	// "windows-11"). Optional, advisory metadata; only meaningful when
	// OSType is "windows". See types.KnownWindowsVariants.
	OSVariant string `json:"os_variant,omitempty" yaml:"os_variant,omitempty"`

	// AdminPassword is the Windows local Administrator password injected into
	// the cloudbase-init datasource at first boot. It is write-only: the
	// LibvirtManager redacts it from the stored/returned VM record once the
	// provisioning ISO has been written, so it never lingers in bbolt or the
	// API response. Ignored for Linux guests.
	AdminPassword string `json:"admin_password,omitempty" yaml:"admin_password,omitempty"`

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

	// Locked, when true, prevents the VM from being deleted via API/CLI/GUI.
	// Stop, Start, and Restart still work — Lock is a delete-protection flag,
	// not a freeze. Always serialised so clients can distinguish "unlocked"
	// from "field absent".
	Locked bool `json:"locked" yaml:"locked"`
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

	// Locked toggles delete-protection on the VM. Use a pointer so we can
	// distinguish "not provided" (no change) from "explicitly false" (unlock).
	Locked *bool `json:"locked,omitempty"`
}

// ResolvedOSType returns the guest OS family, defaulting an empty value to
// OSTypeLinux so VMs created before the field existed behave as before.
func (s VMSpec) ResolvedOSType() OSType {
	if s.OSType == OSTypeWindows {
		return OSTypeWindows
	}
	return OSTypeLinux
}

// IsWindows reports whether this spec targets a Windows guest.
func (s VMSpec) IsWindows() bool {
	return s.ResolvedOSType() == OSTypeWindows
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
