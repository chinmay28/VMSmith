package vm

import (
	"crypto/rand"
	"fmt"
	"strings"
	"text/template"
	"bytes"

	"github.com/vmsmith/vmsmith/pkg/types"
)

const domainXMLTemplate = `<domain type='kvm'>
  <name>{{.Name}}</name>
  <memory unit='MiB'>{{.RAMMB}}</memory>
  <vcpu placement='static'>{{.CPUs}}</vcpu>
  <os>
    <type arch='x86_64' machine='pc-q35-6.2'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <cpu mode='host-passthrough'/>
  <clock offset='utc'>
    <timer name='rtc' tickpolicy='catchup'/>
    <timer name='pit' tickpolicy='delay'/>
    <timer name='hpet' present='no'/>
  </clock>
  <devices>
    <emulator>/usr/bin/qemu-system-x86_64</emulator>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2' discard='unmap'/>
      <source file='{{.DiskPath}}'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    {{- if .CloudInitISO}}
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='{{.CloudInitISO}}'/>
      <target dev='sda' bus='sata'/>
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
    <graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>
    <channel type='unix'>
      <target type='virtio' name='org.qemu.guest_agent.0'/>
    </channel>
    <rng model='virtio'>
      <backend model='random'>/dev/urandom</backend>
    </rng>
  </devices>
</domain>`

// InterfaceEntry holds the rendered XML for a single network interface.
type InterfaceEntry struct {
	XML string
}

// DomainParams holds parameters for generating libvirt domain XML.
type DomainParams struct {
	Name         string
	CPUs         int
	RAMMB        int
	DiskPath     string
	CloudInitISO string
	Interfaces   []InterfaceEntry
}

// DomainParamsFromSpec converts a VMSpec into DomainParams, building all
// network interface XML: the default NAT interface plus any extra attachments.
func DomainParamsFromSpec(spec types.VMSpec, diskPath, cloudInitISO, networkName string) DomainParams {
	var ifaces []InterfaceEntry

	// 1. Always attach the vmsmith NAT network as the first interface
	ifaces = append(ifaces, InterfaceEntry{
		XML: fmt.Sprintf(`<interface type='network'>
      <source network='%s'/>
      <model type='virtio'/>
    </interface>`, networkName),
	})

	// 2. Attach any additional networks requested by the user
	for _, net := range spec.Networks {
		mac := net.MacAddress
		if mac == "" {
			mac = generateMAC()
		}

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
      <model type='virtio'/>
    </interface>`, iface, mac),
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
      <model type='virtio'/>
    </interface>`, bridge, mac),
			})

		case types.NetworkModeNAT:
			// Extra NAT network (unusual but supported)
			ifaces = append(ifaces, InterfaceEntry{
				XML: fmt.Sprintf(`<interface type='network'>
      <source network='%s'/>
      <mac address='%s'/>
      <model type='virtio'/>
    </interface>`, networkName, mac),
			})
		}
	}

	return DomainParams{
		Name:         spec.Name,
		CPUs:         spec.CPUs,
		RAMMB:        spec.RAMMB,
		DiskPath:     diskPath,
		CloudInitISO: cloudInitISO,
		Interfaces:   ifaces,
	}
}

// GenerateDomainXML renders the libvirt domain XML from the given parameters.
func GenerateDomainXML(params DomainParams) (string, error) {
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
