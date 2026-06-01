package types

import (
	"regexp"
	"strings"
	"time"
)

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
// Matching is case-insensitive and whitespace is trimmed so a raw JSON POST
// with "Windows-Server-2022" behaves the same as the CLI (which lowercases).
func IsKnownWindowsVariant(v string) bool {
	needle := strings.ToLower(strings.TrimSpace(v))
	for _, known := range KnownWindowsVariants {
		if known == needle {
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

	// ClockOffset overrides the libvirt domain clock offset. Allowed values
	// are "utc" and "localtime" (case-insensitive). Empty resolves to the
	// OS-family default: utc for Linux, localtime for Windows. Operators
	// running Windows guests alongside Linux peers (NTP-synced fleet, hybrid
	// labs) can pin "utc" to stop the hourly RTC drift that the default
	// localtime behaviour produces. Mutable post-create via VMUpdateSpec.
	ClockOffset string `json:"clock_offset,omitempty" yaml:"clock_offset,omitempty"`

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

	// DiskBus overrides the system-disk bus emitted into the libvirt domain
	// XML. Allowed values are "virtio" and "sata" (case-insensitive). Empty
	// falls back to the OS-family default — "virtio" for Linux, "sata" for
	// Windows — set in DomainParamsFromSpec. When the bus is overridden the
	// disk target letter is adjusted to match ("vda" for virtio, "sda" for
	// sata). Baked at create time and ignored on PATCH; resend on a new
	// create to change.
	DiskBus string `json:"disk_bus,omitempty" yaml:"disk_bus,omitempty"`

	// NICModel overrides the model attribute on every libvirt
	// <interface><model type='...'/></interface> entry — the primary NAT
	// interface plus any additional macvtap / bridge / nat attachments.
	// Allowed values are "virtio" and "e1000e" (case-insensitive). Empty
	// falls back to the OS-family default — "virtio" for Linux, "e1000e"
	// for Windows. Operators who have installed the virtio-net drivers in a
	// Windows guest can pin "virtio" here to unlock the higher throughput.
	NICModel string `json:"nic_model,omitempty" yaml:"nic_model,omitempty"`

	// Machine overrides the libvirt <os><type machine='...'/></os> machine
	// type. Empty falls back to vmsmith's default ("pc-q35-6.2"). Useful for
	// pinning a specific QEMU machine version when a host's libvirt has
	// retired the default.
	Machine string `json:"machine,omitempty" yaml:"machine,omitempty"`

	// Firmware overrides the libvirt firmware selection. Allowed values are
	// "bios" (default; SeaBIOS / no firmware attribute), "uefi" and "ovmf"
	// (both resolve to libvirt's firmware='efi' shorthand, which auto-picks
	// the host's OVMF code/vars pair). UEFI is required to install Windows
	// 11 and is recommended for Server 2022/2025. Note: this does NOT enable
	// Secure Boot or attach a virtual TPM — see roadmap 5.6.9.
	Firmware string `json:"firmware,omitempty" yaml:"firmware,omitempty"`

	// VirtioWinISO overrides the daemon-wide storage.virtio_win_iso config
	// path for this VM. Ignored for Linux guests. When set, the path must
	// exist on the daemon host at create time and is attached as a cdrom
	// to the Windows guest so the operator can install virtio drivers
	// in-guest. Empty falls back to the daemon config / probe.
	VirtioWinISO string `json:"virtio_win_iso,omitempty" yaml:"virtio_win_iso,omitempty"`
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

	// ClockOffset overrides the libvirt domain clock offset on an existing VM.
	// Allowed values are "utc" and "localtime" (case-insensitive). Empty means
	// "no change". Applying a change triggers a domain redefine and restarts
	// the VM, mirroring the cpus / ram_mb change path. Use a pointer so we
	// can distinguish "not provided" (no change) from "explicitly clear",
	// where clearing returns the OS-family default at next render. A nil
	// pointer or empty-pointer string is treated as no-change.
	ClockOffset *string `json:"clock_offset,omitempty"`

	// OSType is intentionally a pointer so the API surface can detect when a
	// client tries to change the guest OS family on an existing VM and return
	// `os_type_immutable` instead of silently ignoring it. The device profile
	// (disk bus, NIC model, clock, Hyper-V, video, provisioning ISO format)
	// is baked at create time. Any value here — including the empty string —
	// is rejected by validateVMUpdateSpec.
	OSType *string `json:"os_type,omitempty"`

	// OSVariant follows the same immutability contract as OSType: the field
	// is captured at create time and validateVMUpdateSpec rejects any attempt
	// to change it on PATCH.
	OSVariant *string `json:"os_variant,omitempty"`
}

// ResolvedOSType returns the guest OS family, defaulting an empty value to
// OSTypeLinux so VMs created before the field existed behave as before.
// Matching is case-insensitive and whitespace is trimmed so a raw JSON POST
// with "Windows" (initial cap) resolves to OSTypeWindows just like "windows".
func (s VMSpec) ResolvedOSType() OSType {
	if OSType(strings.ToLower(strings.TrimSpace(string(s.OSType)))) == OSTypeWindows {
		return OSTypeWindows
	}
	return OSTypeLinux
}

// IsWindows reports whether this spec targets a Windows guest.
func (s VMSpec) IsWindows() bool {
	return s.ResolvedOSType() == OSTypeWindows
}

// ClockOffsetUTC and ClockOffsetLocaltime are the only accepted values for
// VMSpec.ClockOffset and VMUpdateSpec.ClockOffset. They map 1:1 to the
// libvirt <clock offset='...'/> attribute.
const (
	ClockOffsetUTC       = "utc"
	ClockOffsetLocaltime = "localtime"
)

// Device override vocabularies for VMSpec.DiskBus / NICModel / Firmware.
// Validated at the API boundary so typos surface as a 400 rather than
// silently picking the OS-family default.
const (
	DiskBusVirtio  = "virtio"
	DiskBusSATA    = "sata"
	NICModelVirtio = "virtio"
	NICModelE1000e = "e1000e"
	FirmwareBIOS   = "bios"
	FirmwareUEFI   = "uefi"
	FirmwareOVMF   = "ovmf"
)

// machineTypeRe bounds the VMSpec.Machine override to a conservative
// character set that matches every machine name libvirt's QEMU driver
// exposes (e.g. "pc-q35-6.2", "pc-q35-rhel9.6.0", "q35", "virt-7.2"). It
// rejects shell metacharacters / quotes / whitespace so a malformed value
// cannot break the domain XML template.
var machineTypeRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// IsValidMachineType reports whether v passes the conservative
// pc-q35-style alphabet check above. Empty returns true so the empty
// override is treated as "no opinion".
func IsValidMachineType(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	return machineTypeRe.MatchString(v)
}

// ResolvedDiskBus returns the effective system-disk bus for this spec. An
// explicit DiskBus (case-insensitive, whitespace-trimmed) always wins.
// Otherwise the OS family decides: "sata" for Windows, "virtio" for Linux.
func (s VMSpec) ResolvedDiskBus() string {
	if v := strings.ToLower(strings.TrimSpace(s.DiskBus)); v != "" {
		return v
	}
	if s.IsWindows() {
		return DiskBusSATA
	}
	return DiskBusVirtio
}

// ResolvedNICModel returns the effective NIC model emitted into every
// libvirt <interface><model type='...'/></interface> entry. Explicit
// NICModel wins; otherwise Windows → "e1000e", everything else → "virtio".
func (s VMSpec) ResolvedNICModel() string {
	if v := strings.ToLower(strings.TrimSpace(s.NICModel)); v != "" {
		return v
	}
	if s.IsWindows() {
		return NICModelE1000e
	}
	return NICModelVirtio
}

// ResolvedFirmwareAttr returns the libvirt <os firmware='...'> attribute
// value for this spec, or empty for "no firmware attribute" (BIOS /
// SeaBIOS default). "uefi" and "ovmf" both resolve to libvirt's "efi"
// shorthand; "bios" and "" both resolve to empty.
func (s VMSpec) ResolvedFirmwareAttr() string {
	switch strings.ToLower(strings.TrimSpace(s.Firmware)) {
	case FirmwareUEFI, FirmwareOVMF:
		return "efi"
	default:
		return ""
	}
}

// ResolvedClockOffset returns the effective libvirt clock offset for this
// spec. An explicit ClockOffset (case-insensitive, whitespace-trimmed)
// always wins. Otherwise the OS family decides: localtime for Windows
// (matches the Windows RTC convention so the system clock reads correctly
// before NTP catches up) and utc for everything else.
func (s VMSpec) ResolvedClockOffset() string {
	if v := strings.ToLower(strings.TrimSpace(s.ClockOffset)); v != "" {
		return v
	}
	if s.IsWindows() {
		return ClockOffsetLocaltime
	}
	return ClockOffsetUTC
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

	// GeneratedAdminPassword is set on the create response only when the
	// caller created a Windows VM without supplying an AdminPassword and
	// vmsmith generated one. It is shown exactly once in the create response
	// and never persisted — Get/List will not return it. Empty for every
	// other path (Linux VMs, explicit admin_password, non-Windows clones).
	GeneratedAdminPassword string `json:"generated_admin_password,omitempty"`
}
