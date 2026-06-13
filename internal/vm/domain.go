package vm

import (
	"bytes"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/vmsmith/vmsmith/pkg/types"
)

const domainXMLTemplate = `<domain type='kvm'>
  <name>{{.Name}}</name>
  {{- if .UUID}}
  <uuid>{{.UUID}}</uuid>
  {{- end}}
  <memory unit='MiB'>{{.RAMMB}}</memory>
  <vcpu placement='static'>{{.CPUs}}</vcpu>
  <os{{if .FirmwareAttr}} firmware='{{.FirmwareAttr}}'{{end}}>
    <type arch='x86_64' machine='{{.Machine}}'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
    {{- if .Hyperv}}
    <hyperv>
      <relaxed state='on'/>
      <vapic state='on'/>
      <spinlocks state='on' retries='8191'/>
      <vpindex state='on'/>
      <synic state='on'/>
      <stimer state='on'/>
      <frequencies state='on'/>
    </hyperv>
    {{- end}}
  </features>
  <cpu mode='host-passthrough'/>
  <clock offset='{{.ClockOffset}}'>
    <timer name='rtc' tickpolicy='catchup'/>
    <timer name='pit' tickpolicy='delay'/>
    <timer name='hpet' present='no'/>
    {{- if .Hyperv}}
    <timer name='hypervclock' present='yes'/>
    {{- end}}
  </clock>
  <devices>
    <emulator>{{.Emulator}}</emulator>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2' discard='unmap'/>
      <source file='{{.DiskPath}}'/>
      <target dev='{{.DiskTarget}}' bus='{{.DiskBus}}'/>
    </disk>
    {{- if .CloudInitISO}}
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='{{.CloudInitISO}}'/>
      <target dev='{{.CloudInitTarget}}' bus='sata'/>
      <readonly/>
    </disk>
    {{- end}}
    {{- if .VirtioWinISO}}
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='{{.VirtioWinISO}}'/>
      <target dev='{{.VirtioWinTarget}}' bus='sata'/>
      <readonly/>
    </disk>
    {{- end}}
    {{- range .Interfaces}}
    {{.XML}}
    {{- end}}
    <serial type='pty'>
      <target port='0'/>
    </serial>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
    {{- if .Tablet}}
    <input type='tablet' bus='usb'/>
    {{- end}}
    {{- if .VideoModel}}
    <video>
      <model type='{{.VideoModel}}'/>
    </video>
    {{- end}}
    <graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>
    <channel type='unix'>
      <target type='virtio' name='org.qemu.guest_agent.0'/>
    </channel>
    <rng model='virtio'>
      <backend model='random'>/dev/urandom</backend>
    </rng>
    {{- range .GPUHostdevs}}
    {{.}}
    {{- end}}
  </devices>
</domain>`

// InterfaceEntry holds the rendered XML for a single network interface.
type InterfaceEntry struct {
	XML string
}

// DomainParams holds parameters for generating libvirt domain XML.
type DomainParams struct {
	Name         string
	UUID         string // preserve existing UUID on redefinition; empty = let libvirt assign
	CPUs         int
	RAMMB        int
	DiskPath     string
	CloudInitISO string
	Interfaces   []InterfaceEntry
	Machine      string // e.g. "pc-q35-6.2" or "pc-q35-rhel9.6.0"
	Emulator     string // path to QEMU binary, e.g. /usr/libexec/qemu-kvm

	// OS-family-dependent device tuning. Empty values are normalised to the
	// Linux defaults (virtio disk on vda, SATA cdrom on sda, utc clock) in
	// GenerateDomainXML, so callers constructing DomainParams directly keep the
	// historical Linux behaviour without setting these fields.
	DiskBus         string // "virtio" (Linux) | "sata" (Windows)
	DiskTarget      string // "vda" (virtio) | "sda" (sata)
	CloudInitTarget string // cdrom target dev: "sda" (Linux) | "sdb" (Windows, where the system disk takes sda)
	VirtioWinISO    string // path to the virtio-win driver ISO; attached as an extra cdrom for Windows
	VirtioWinTarget string // cdrom target dev for the virtio-win ISO (e.g. "sdc")
	ClockOffset     string // "utc" (Linux) | "localtime" (Windows)
	Hyperv          bool   // emit Hyper-V enlightenments + hypervclock timer (Windows)
	Tablet          bool   // attach a USB tablet for usable VNC mouse tracking (Windows)
	VideoModel      string // explicit video model (e.g. "qxl" for Windows); empty leaves the libvirt default
	// FirmwareAttr is the value emitted into the libvirt <os firmware='...'>
	// attribute (e.g. "efi"). Empty omits the attribute entirely, falling
	// back to the libvirt default firmware (SeaBIOS on x86_64). Set
	// indirectly from VMSpec.Firmware ("uefi"/"ovmf" → "efi"; "bios"/"" →
	// empty) via VMSpec.ResolvedFirmwareAttr.
	FirmwareAttr string
	// NICModel is the libvirt <interface><model type='...'/></interface>
	// value used for the primary NAT interface and every additional
	// attachment. Populated in DomainParamsFromSpec via
	// VMSpec.ResolvedNICModel. Exposed on DomainParams (rather than
	// embedded only in the rendered interface XML strings) so tests can
	// assert it without grepping XML.
	NICModel string

	// GPUAddresses lists normalized host PCI addresses to attach as VFIO
	// passthrough <hostdev> entries — the GPU plus its IOMMU-group
	// companions (e.g. the HDMI audio function). The caller (LibvirtManager)
	// is responsible for expanding requested GPUs to full IOMMU groups; the
	// pure DomainParamsFromSpec path populates this from VMSpec.ResolvedGPUs
	// without expansion. Empty = no passthrough. GenerateDomainXML renders
	// each address into GPUHostdevs.
	GPUAddresses []string

	// GPUHostdevs holds the rendered <hostdev> XML fragments, computed from
	// GPUAddresses by GenerateDomainXML. Not set by callers.
	GPUHostdevs []string
}

// qemuBinaryCandidates is the ordered list of QEMU binary paths to probe.
// RHEL/Rocky use /usr/libexec/qemu-kvm; Debian/Ubuntu use /usr/bin/qemu-system-x86_64.
var qemuBinaryCandidates = []string{
	"/usr/libexec/qemu-kvm",
	"/usr/bin/qemu-system-x86_64",
	"/usr/bin/qemu-kvm",
}

// detectQEMUBinary returns the first QEMU binary that exists on the system.
// Falls back to /usr/bin/qemu-system-x86_64 if none are found.
func detectQEMUBinary() string {
	for _, path := range qemuBinaryCandidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "/usr/bin/qemu-system-x86_64"
}

// DomainParamsFromSpec converts a VMSpec into DomainParams, building all
// network interface XML: the default NAT interface plus any extra attachments.
// natMAC must be pre-generated by the caller so the same value can also be
// written into the cloud-init network-config for MAC-based interface matching.
func DomainParamsFromSpec(spec types.VMSpec, diskPath, cloudInitISO, networkName, natMAC string) DomainParams {
	windows := spec.IsWindows()

	// NIC model: virtio for Linux (drivers ship in-tree); e1000e for Windows
	// so the guest has a working NIC out of the box without virtio-net drivers.
	// VMSpec.NICModel overrides both defaults (5.6.15) so an operator who has
	// installed the virtio-net drivers in-guest can pin "virtio" for
	// throughput, or vice-versa.
	nicModel := spec.ResolvedNICModel()

	var ifaces []InterfaceEntry

	// 1. Always attach the vmsmith NAT network as the first interface.
	//    Explicit MAC is required so cloud-init can match by macaddress rather
	//    than by name (Rocky/RHEL use predictable names like enp1s0, not eth0).
	ifaces = append(ifaces, InterfaceEntry{
		XML: fmt.Sprintf(`<interface type='network'>
      <source network='%s'/>
      <mac address='%s'/>
      <model type='%s'/>
    </interface>`, networkName, natMAC, nicModel),
	})

	// 2. Attach any additional networks requested by the user.
	//    MACs must already be set on each attachment (done in lifecycle.go).
	for _, net := range spec.Networks {
		mac := net.MacAddress

		switch net.Mode {
		case types.NetworkModeMacvtap, "":
			// macvtap: attach directly to a host physical interface
			// The VM gets its own MAC on the host's network segment.
			// Uses "bridge" macvtap mode (most common — works with standard switches).
			iface := net.HostInterface
			if iface == "" {
				continue // skip invalid entry
			}
			ifaces = append(ifaces, InterfaceEntry{
				XML: fmt.Sprintf(`<interface type='direct'>
      <source dev='%s' mode='bridge'/>
      <mac address='%s'/>
      <model type='%s'/>
    </interface>`, iface, mac, nicModel),
			})

		case types.NetworkModeBridge:
			// Linux bridge: attach to a pre-configured bridge on the host
			bridge := net.Bridge
			if bridge == "" {
				bridge = "br-" + net.HostInterface // convention: br-eth1
			}
			ifaces = append(ifaces, InterfaceEntry{
				XML: fmt.Sprintf(`<interface type='bridge'>
      <source bridge='%s'/>
      <mac address='%s'/>
      <model type='%s'/>
    </interface>`, bridge, mac, nicModel),
			})

		case types.NetworkModeNAT:
			// Extra NAT network (unusual but supported)
			ifaces = append(ifaces, InterfaceEntry{
				XML: fmt.Sprintf(`<interface type='network'>
      <source network='%s'/>
      <mac address='%s'/>
      <model type='%s'/>
    </interface>`, networkName, mac, nicModel),
			})
		}
	}

	machine := spec.ResolvedMachine()

	params := DomainParams{
		Name:         spec.Name,
		CPUs:         spec.CPUs,
		RAMMB:        spec.RAMMB,
		DiskPath:     diskPath,
		CloudInitISO: cloudInitISO,
		Interfaces:   ifaces,
		Machine:      machine,
		Emulator:     detectQEMUBinary(),
		NICModel:     nicModel,
		GPUAddresses: spec.ResolvedGPUs(),
	}

	if windows {
		// Windows tuning: SATA system disk (native AHCI driver always present,
		// so the guest boots even without virtio storage drivers), localtime
		// RTC, Hyper-V enlightenments + hypervclock for performance, a USB
		// tablet for usable VNC mouse tracking, and a QXL display. The cloudbase
		// provisioning cdrom moves to sdb so it does not collide with the SATA
		// system disk on sda; the virtio-win ISO (when attached) takes sdc.
		params.DiskBus = "sata"
		params.DiskTarget = "sda"
		params.CloudInitTarget = "sdb"
		params.VirtioWinTarget = "sdc"
		params.ClockOffset = "localtime"
		params.Hyperv = true
		params.Tablet = true
		params.VideoModel = "qxl"
	}

	// Apply an explicit ClockOffset override last so it wins over the OS
	// family default. This lets operators pin "utc" on a Windows guest that
	// is NTP-synced with a Linux fleet, or "localtime" on a Linux dual-boot
	// guest sharing an RTC with Windows.
	params.ClockOffset = spec.ResolvedClockOffset()

	// Apply DiskBus override (5.6.15). The disk target letter follows the
	// bus (virtio → vd*, sata → sd*) so the libvirt XML stays consistent.
	// We adjust both the system-disk target and, for SATA, push the
	// cloud-init cdrom to sdb so it does not collide with the disk on sda
	// (matches the Windows tuning above).
	if v := spec.ResolvedDiskBus(); v != params.DiskBus {
		params.DiskBus = v
		switch v {
		case types.DiskBusVirtio:
			params.DiskTarget = "vda"
			// virtio disk on vda frees up sda for the cdrom regardless of OS.
			params.CloudInitTarget = "sda"
		case types.DiskBusSATA:
			params.DiskTarget = "sda"
			params.CloudInitTarget = "sdb"
		}
	}

	// Apply Firmware override (5.6.15). "uefi"/"ovmf" resolve to libvirt's
	// firmware='efi' shorthand which auto-selects the host's OVMF code/vars
	// pair from the QEMU firmware descriptors; "bios"/"" emit no attribute.
	// This is the minimal-viable UEFI path — Secure Boot + virtual TPM are
	// out of scope for 5.6.15 (tracked separately under roadmap 5.6.9).
	params.FirmwareAttr = spec.ResolvedFirmwareAttr()

	return params
}

// GenerateDomainXML renders the libvirt domain XML from the given parameters.
// Device fields left empty are normalised to the historical Linux defaults
// (virtio system disk on vda, SATA cdrom on sda, utc clock) so callers that
// build DomainParams directly — and pre-Windows code paths — keep working
// unchanged.
func GenerateDomainXML(params DomainParams) (string, error) {
	if params.DiskBus == "" {
		params.DiskBus = "virtio"
	}
	if params.DiskTarget == "" {
		params.DiskTarget = "vda"
	}
	if params.CloudInitTarget == "" {
		params.CloudInitTarget = "sda"
	}
	if params.VirtioWinTarget == "" {
		params.VirtioWinTarget = "sdc"
	}
	if params.ClockOffset == "" {
		params.ClockOffset = "utc"
	}

	// Render VFIO GPU passthrough hostdev fragments from the PCI addresses.
	params.GPUHostdevs = params.GPUHostdevs[:0]
	for _, addr := range params.GPUAddresses {
		hostdev, err := gpuHostdevXML(addr)
		if err != nil {
			return "", err
		}
		params.GPUHostdevs = append(params.GPUHostdevs, hostdev)
	}

	tmpl, err := template.New("domain").Parse(domainXMLTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing domain template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("executing domain template: %w", err)
	}

	return buf.String(), nil
}

// machineTypeFromCaps parses a libvirt capabilities XML string and returns the
// best pc-q35-* machine type for x86_64 KVM guests.
//
// Selection order:
//  1. The canonical name of the "q35" alias (e.g. "pc-q35-rhel9.6.0")
//  2. The first machine whose name starts with "pc-q35-"
//  3. The provided fallback value
func machineTypeFromCaps(capsXMLStr, fallback string) string {
	type capsMachine struct {
		Canonical string `xml:"canonical,attr"`
		Name      string `xml:",chardata"`
	}
	type capsDomain struct {
		Type string `xml:"type,attr"`
	}
	type capsArch struct {
		Name     string        `xml:"name,attr"`
		Machines []capsMachine `xml:"machine"`
		Domains  []capsDomain  `xml:"domain"`
	}
	type capsGuest struct {
		OSType string   `xml:"os_type"`
		Arch   capsArch `xml:"arch"`
	}
	type capsRoot struct {
		Guests []capsGuest `xml:"guest"`
	}

	var caps capsRoot
	if err := xml.Unmarshal([]byte(capsXMLStr), &caps); err != nil {
		return fallback
	}

	for _, guest := range caps.Guests {
		if guest.OSType != "hvm" || guest.Arch.Name != "x86_64" {
			continue
		}
		hasKVM := false
		for _, d := range guest.Arch.Domains {
			if d.Type == "kvm" {
				hasKVM = true
				break
			}
		}
		if !hasKVM {
			continue
		}
		// Prefer the canonical target of the "q35" alias.
		for _, m := range guest.Arch.Machines {
			if m.Name == "q35" && m.Canonical != "" {
				return m.Canonical
			}
		}
		// Fall back to the first pc-q35-* machine listed.
		for _, m := range guest.Arch.Machines {
			if strings.HasPrefix(m.Name, "pc-q35-") {
				return m.Name
			}
		}
	}

	return fallback
}

// gpuHostdevXML renders a single VFIO PCI passthrough <hostdev> element for the
// given PCI address. managed='yes' tells libvirt to detach the device from its
// host driver and bind it to vfio-pci when the domain starts, then reattach it
// on shutdown. Returns an error for a malformed PCI address so a bad value
// fails the create rather than producing invalid domain XML.
func gpuHostdevXML(addr string) (string, error) {
	domain, bus, slot, function, ok := types.PCIAddressParts(addr)
	if !ok {
		return "", fmt.Errorf("invalid GPU PCI address %q", addr)
	}
	return fmt.Sprintf(`<hostdev mode='subsystem' type='pci' managed='yes'>
      <source>
        <address domain='%s' bus='%s' slot='%s' function='%s'/>
      </source>
    </hostdev>`, domain, bus, slot, function), nil
}

// generateMAC creates a random MAC address with the local/unicast prefix 52:54:00
// (standard KVM/QEMU prefix).
func generateMAC() string {
	b := make([]byte, 3)
	rand.Read(b)
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2])
}

// ValidateNetworkAttachments checks that network attachments are well-formed.
func ValidateNetworkAttachments(nets []types.NetworkAttachment) error {
	seen := make(map[string]bool)
	var errs []string

	for i, net := range nets {
		// Validate mode
		switch net.Mode {
		case types.NetworkModeMacvtap, types.NetworkModeBridge, types.NetworkModeNAT, "":
			// ok
		default:
			errs = append(errs, fmt.Sprintf("network[%d]: unknown mode %q (use macvtap, bridge, or nat)", i, net.Mode))
		}

		// macvtap and bridge require a host interface
		if (net.Mode == types.NetworkModeMacvtap || net.Mode == "") && net.HostInterface == "" {
			errs = append(errs, fmt.Sprintf("network[%d] (%s): host_interface is required for macvtap mode", i, net.Name))
		}

		if net.Mode == types.NetworkModeBridge && net.HostInterface == "" && net.Bridge == "" {
			errs = append(errs, fmt.Sprintf("network[%d] (%s): bridge or host_interface is required for bridge mode", i, net.Name))
		}

		// Check for duplicate interface bindings
		key := net.HostInterface
		if key != "" {
			if seen[key] {
				errs = append(errs, fmt.Sprintf("network[%d]: duplicate host_interface %q", i, key))
			}
			seen[key] = true
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid network config:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
