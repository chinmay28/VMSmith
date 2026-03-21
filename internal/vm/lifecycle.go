package vm

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

// LibvirtManager implements the Manager interface using libvirt.
type LibvirtManager struct {
	conn  *libvirt.Connect
	store *store.Store
	cfg   *config.Config
}

// NewLibvirtManager creates a new libvirt-backed VM manager.
func NewLibvirtManager(cfg *config.Config, store *store.Store) (*LibvirtManager, error) {
	conn, err := libvirt.NewConnect(cfg.Libvirt.URI)
	if err != nil {
		return nil, fmt.Errorf("connecting to libvirt (%s): %w", cfg.Libvirt.URI, err)
	}

	return &LibvirtManager{
		conn:  conn,
		store: store,
		cfg:   cfg,
	}, nil
}

// Close releases the libvirt connection.
func (m *LibvirtManager) Close() error {
	if _, err := m.conn.Close(); err != nil {
		return err
	}
	return nil
}

// Create provisions a new VM: creates the overlay disk, cloud-init ISO,
// defines the domain in libvirt, and starts it.
func (m *LibvirtManager) Create(ctx context.Context, spec types.VMSpec) (*types.VM, error) {
	// Ensure the NAT network exists before creating any VM.
	// This is idempotent — safe to call even if the network already exists.
	netMgr := network.NewManager(m.conn, m.cfg)
	if err := netMgr.EnsureNetwork(); err != nil {
		return nil, fmt.Errorf("ensuring NAT network: %w", err)
	}

	// Apply defaults
	if spec.CPUs == 0 {
		spec.CPUs = m.cfg.Defaults.CPUs
	}
	if spec.RAMMB == 0 {
		spec.RAMMB = m.cfg.Defaults.RAMMB
	}
	if spec.DiskGB == 0 {
		spec.DiskGB = m.cfg.Defaults.DiskGB
	}

	// Validate network attachments
	if len(spec.Networks) > 0 {
		if err := ValidateNetworkAttachments(spec.Networks); err != nil {
			return nil, err
		}
		// Default empty mode to macvtap; pre-assign MACs so the same value
		// ends up in both the libvirt XML and the cloud-init network-config.
		for i := range spec.Networks {
			if spec.Networks[i].Mode == "" {
				spec.Networks[i].Mode = types.NetworkModeMacvtap
			}
			if spec.Networks[i].MacAddress == "" {
				spec.Networks[i].MacAddress = generateMAC()
			}
		}
	}

	// Pre-generate the NAT interface MAC so it can be used consistently in
	// both the libvirt domain XML and the cloud-init network-config.  Without
	// a deterministic MAC we cannot match the interface by address, and
	// Rocky/RHEL guests use predictable names (enp1s0, ens3…) not eth0.
	natMAC := generateMAC()

	// Generate a unique ID
	id := fmt.Sprintf("vm-%d", time.Now().UnixNano())

	// Set up VM directory
	vmDir := filepath.Join(m.cfg.Storage.BaseDir, id)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return nil, fmt.Errorf("creating VM dir: %w", err)
	}

	// Create qcow2 overlay backed by the base image
	diskPath := filepath.Join(vmDir, "disk.qcow2")
	baseImage := spec.Image
	if !filepath.IsAbs(spec.Image) {
		baseImage = filepath.Join(m.cfg.Storage.ImagesDir, spec.Image)
	}
	if err := createOverlayDisk(baseImage, diskPath, spec.DiskGB); err != nil {
		return nil, fmt.Errorf("creating overlay disk: %w", err)
	}

	// Always create a cloud-init ISO. Rocky Linux (and other RHEL-based images)
	// rely entirely on cloud-init to bring up networking — without it the
	// primary NAT interface is never configured and the VM gets no IP address.
	// Ubuntu cloud images have fallback network config so they work either way,
	// but generating the ISO unconditionally is correct for all distros.
	// If NatStaticIP is set the NAT interface is configured with a static
	// address instead of DHCP, which avoids Rocky/RHEL DHCP timing issues.
	cloudInitISO := filepath.Join(vmDir, "cidata.iso")
	if err := createCloudInitISO(cloudInitISO, spec, natMAC); err != nil {
		return nil, fmt.Errorf("creating cloud-init ISO: %w", err)
	}

	// Generate and define domain XML
	params := DomainParamsFromSpec(spec, diskPath, cloudInitISO, m.cfg.Network.Name, natMAC)
	params.Machine = detectMachineType(m.conn)
	xmlDoc, err := GenerateDomainXML(params)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.DomainDefineXML(xmlDoc)
	if err != nil {
		return nil, fmt.Errorf("defining domain: %w", err)
	}

	// Start the domain
	if err := dom.Create(); err != nil {
		dom.Undefine()
		return nil, fmt.Errorf("starting domain: %w", err)
	}

	vm := &types.VM{
		ID:        id,
		Name:      spec.Name,
		Spec:      spec,
		State:     types.VMStateRunning,
		DiskPath:  diskPath,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Persist to store
	if err := m.store.PutVM(vm); err != nil {
		return nil, fmt.Errorf("storing VM metadata: %w", err)
	}

	return vm, nil
}

// Start boots a stopped VM.
func (m *LibvirtManager) Start(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain %s: %w", vm.Name, err)
	}
	defer dom.Free()

	if err := dom.Create(); err != nil {
		return fmt.Errorf("starting domain: %w", err)
	}

	vm.State = types.VMStateRunning
	vm.UpdatedAt = time.Now()
	return m.store.PutVM(vm)
}

// Stop shuts down a running VM.
func (m *LibvirtManager) Stop(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain %s: %w", vm.Name, err)
	}
	defer dom.Free()

	if err := dom.Shutdown(); err != nil {
		// If graceful shutdown fails, force destroy
		if err := dom.Destroy(); err != nil {
			return fmt.Errorf("force-stopping domain: %w", err)
		}
	}

	vm.State = types.VMStateStopped
	vm.UpdatedAt = time.Now()
	return m.store.PutVM(vm)
}

// Delete removes a VM entirely: stops it, undefines the domain, removes disk files.
func (m *LibvirtManager) Delete(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err == nil {
		defer dom.Free()
		// Try to destroy if running
		state, _, _ := dom.GetState()
		if state == libvirt.DOMAIN_RUNNING {
			dom.Destroy()
		}
		dom.UndefineFlags(libvirt.DOMAIN_UNDEFINE_SNAPSHOTS_METADATA)
	}

	// Remove VM directory (disk, cloud-init, etc.)
	vmDir := filepath.Dir(vm.DiskPath)
	os.RemoveAll(vmDir)

	return m.store.DeleteVM(id)
}

// Get returns the current state of a VM, refreshing from libvirt.
func (m *LibvirtManager) Get(ctx context.Context, id string) (*types.VM, error) {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return nil, err
	}

	// Refresh state from libvirt
	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err == nil {
		defer dom.Free()
		vm.State = domainStateToVMState(dom)
		vm.IP = getDomainIP(dom, m.conn)
	}

	return vm, nil
}

// List returns all VMs with refreshed state.
func (m *LibvirtManager) List(ctx context.Context) ([]*types.VM, error) {
	vms, err := m.store.ListVMs()
	if err != nil {
		return nil, err
	}

	for _, vm := range vms {
		dom, err := m.conn.LookupDomainByName(vm.Name)
		if err == nil {
			vm.State = domainStateToVMState(dom)
			vm.IP = getDomainIP(dom, m.conn)
			dom.Free()
		}
	}

	return vms, nil
}

// --- Snapshot operations ---

// CreateSnapshot takes a snapshot of a VM.
func (m *LibvirtManager) CreateSnapshot(ctx context.Context, vmID string, name string) (*types.Snapshot, error) {
	vm, err := m.store.GetVM(vmID)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	snapXML := fmt.Sprintf(`<domainsnapshot><name>%s</name></domainsnapshot>`, name)
	_, err = dom.CreateSnapshotXML(snapXML, 0)
	if err != nil {
		return nil, fmt.Errorf("creating snapshot: %w", err)
	}

	snap := &types.Snapshot{
		ID:        fmt.Sprintf("%s/%s", vmID, name),
		VMID:      vmID,
		Name:      name,
		CreatedAt: time.Now(),
	}

	return snap, nil
}

// RestoreSnapshot reverts a VM to a previous snapshot.
func (m *LibvirtManager) RestoreSnapshot(ctx context.Context, vmID string, snapshotName string) error {
	vm, err := m.store.GetVM(vmID)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	snap, err := dom.SnapshotLookupByName(snapshotName, 0)
	if err != nil {
		return fmt.Errorf("looking up snapshot %s: %w", snapshotName, err)
	}
	defer snap.Free()

	return snap.RevertToSnapshot(0)
}

// ListSnapshots returns all snapshots for a VM.
func (m *LibvirtManager) ListSnapshots(ctx context.Context, vmID string) ([]*types.Snapshot, error) {
	vm, err := m.store.GetVM(vmID)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	snaps, err := dom.ListAllSnapshots(0)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}

	var result []*types.Snapshot
	for _, s := range snaps {
		name, _ := s.GetName()
		result = append(result, &types.Snapshot{
			ID:   fmt.Sprintf("%s/%s", vmID, name),
			VMID: vmID,
			Name: name,
		})
		s.Free()
	}

	return result, nil
}

// DeleteSnapshot removes a snapshot.
func (m *LibvirtManager) DeleteSnapshot(ctx context.Context, vmID string, snapshotName string) error {
	vm, err := m.store.GetVM(vmID)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	snap, err := dom.SnapshotLookupByName(snapshotName, 0)
	if err != nil {
		return fmt.Errorf("looking up snapshot: %w", err)
	}
	defer snap.Free()

	return snap.Delete(0)
}

// --- helpers ---

func createOverlayDisk(baseImage, diskPath string, sizeGB int) error {
	// Create a qcow2 overlay backed by the base image
	cmd := exec.Command("qemu-img", "create",
		"-f", "qcow2",
		"-b", baseImage,
		"-F", "qcow2",
		diskPath,
		fmt.Sprintf("%dG", sizeGB),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img create: %s: %w", string(out), err)
	}
	return nil
}

func createCloudInitISO(isoPath string, spec types.VMSpec, natMAC string) error {
	tmpDir, err := os.MkdirTemp("", "vmsmith-ci-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Write meta-data
	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", spec.Name, spec.Name)
	if err := os.WriteFile(filepath.Join(tmpDir, "meta-data"), []byte(metaData), 0644); err != nil {
		return err
	}

	// Write user-data
	userData := "#cloud-config\n"
	if spec.SSHPubKey != "" {
		userData += fmt.Sprintf("ssh_authorized_keys:\n  - %s\n", spec.SSHPubKey)
	}
	if spec.CloudInitFile != "" {
		custom, err := os.ReadFile(spec.CloudInitFile)
		if err != nil {
			return fmt.Errorf("reading cloud-init file: %w", err)
		}
		userData = string(custom)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "user-data"), []byte(userData), 0644); err != nil {
		return err
	}

	// Always write network-config so cloud-init configures the NAT interface
	// on every distro.  We match by MAC address rather than interface name so
	// the config works regardless of whether the guest uses traditional names
	// (eth0) or predictable names (enp1s0, ens3, …) as Rocky/RHEL do.
	netCfg := generateNetworkConfig(spec.Networks, natMAC, spec.NatStaticIP, spec.NatGateway)
	if err := os.WriteFile(filepath.Join(tmpDir, "network-config"), []byte(netCfg), 0644); err != nil {
		return err
	}

	// Generate ISO
	cmd := exec.Command("genisoimage",
		"-output", isoPath,
		"-volid", "cidata",
		"-joliet",
		"-rock",
		filepath.Join(tmpDir, "meta-data"),
		filepath.Join(tmpDir, "user-data"),
	)
	// Include network-config if it exists
	netCfgPath := filepath.Join(tmpDir, "network-config")
	if _, err := os.Stat(netCfgPath); err == nil {
		cmd.Args = append(cmd.Args, netCfgPath)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("genisoimage: %s: %w", string(out), err)
	}

	return nil
}

// generateNetworkConfig produces cloud-init network-config v2 YAML.
// Interfaces are matched by MAC address so the config works on both
// traditional (eth0) and predictable-name (enp1s0, ens3, …) guests.
// natMAC is the MAC of the primary NAT interface.  Extra interfaces must
// have their MAC pre-populated in types.NetworkAttachment.MacAddress.
// natStaticIP (CIDR, e.g. "192.168.100.50/24") and natGateway are optional;
// when set the NAT interface gets a static address instead of DHCP.
func generateNetworkConfig(networks []types.NetworkAttachment, natMAC, natStaticIP, natGateway string) string {
	var sb strings.Builder
	sb.WriteString("version: 2\nethernets:\n")

	// NAT interface: match by MAC, static or DHCP as configured.
	sb.WriteString("  nat0:\n")
	sb.WriteString(fmt.Sprintf("    match:\n      macaddress: \"%s\"\n", natMAC))
	if natStaticIP != "" {
		sb.WriteString(fmt.Sprintf("    addresses:\n      - %s\n", natStaticIP))
		if natGateway != "" {
			sb.WriteString(fmt.Sprintf("    routes:\n      - to: 0.0.0.0/0\n        via: %s\n        metric: 100\n", natGateway))
		}
		sb.WriteString("    dhcp4: false\n")
	} else {
		sb.WriteString("    dhcp4: true\n")
	}

	// Extra attachments: match by MAC, static or DHCP as configured
	for i, net := range networks {
		id := fmt.Sprintf("eth%d", i+1)
		sb.WriteString(fmt.Sprintf("  %s:\n", id))
		sb.WriteString(fmt.Sprintf("    match:\n      macaddress: \"%s\"\n", net.MacAddress))

		if net.StaticIP != "" {
			sb.WriteString(fmt.Sprintf("    addresses:\n      - %s\n", net.StaticIP))
			if net.Gateway != "" {
				sb.WriteString(fmt.Sprintf("    routes:\n      - to: 0.0.0.0/0\n        via: %s\n        metric: %d\n",
					net.Gateway, 200+i)) // higher metric than NAT default route
			}
			sb.WriteString("    dhcp4: false\n")
		} else {
			sb.WriteString("    dhcp4: true\n")
		}
	}

	return sb.String()
}

// hasStaticIPs returns true if any network attachment requires static IP config.
func hasStaticIPs(networks []types.NetworkAttachment) bool {
	for _, net := range networks {
		if net.StaticIP != "" {
			return true
		}
	}
	return false
}

func domainStateToVMState(dom *libvirt.Domain) types.VMState {
	state, _, err := dom.GetState()
	if err != nil {
		return types.VMStateUnknown
	}
	switch state {
	case libvirt.DOMAIN_RUNNING:
		return types.VMStateRunning
	case libvirt.DOMAIN_SHUTOFF:
		return types.VMStateStopped
	case libvirt.DOMAIN_PAUSED:
		return types.VMStateStopped
	default:
		return types.VMStateUnknown
	}
}

// detectMachineType queries libvirt capabilities to find the best pc-q35-*
// machine type for x86_64 KVM guests, falling back to "pc-q35-6.2".
func detectMachineType(conn *libvirt.Connect) string {
	const fallback = "pc-q35-6.2"
	capsXMLStr, err := conn.GetCapabilities()
	if err != nil {
		return fallback
	}
	return machineTypeFromCaps(capsXMLStr, fallback)
}

func getDomainIP(dom *libvirt.Domain, conn *libvirt.Connect) string {
	name, _ := dom.GetName()

	// Try multiple sources in order of reliability.
	sourceNames := []string{"agent", "lease", "arp"}
	sources := []libvirt.DomainInterfaceAddressesSource{
		libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_AGENT, // QEMU guest agent (most accurate)
		libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_LEASE, // libvirt dnsmasq leases
		libvirt.DOMAIN_INTERFACE_ADDRESSES_SRC_ARP,   // host ARP cache
	}
	for i, src := range sources {
		ifaces, err := dom.ListAllInterfaceAddresses(src)
		if err != nil {
			logger.Debug("daemon", "getDomainIP: source failed",
				"vm", name, "source", sourceNames[i], "error", err.Error())
			continue
		}
		for _, iface := range ifaces {
			for _, addr := range iface.Addrs {
				if addr.Type == libvirt.IP_ADDR_TYPE_IPV4 && addr.Addr != "127.0.0.1" {
					logger.Debug("daemon", "getDomainIP: found IP",
						"vm", name, "source", sourceNames[i], "ip", addr.Addr,
						"iface", iface.Name)
					return addr.Addr
				}
			}
		}
		logger.Debug("daemon", "getDomainIP: source returned no IPv4",
			"vm", name, "source", sourceNames[i], "iface_count", fmt.Sprintf("%d", len(ifaces)))
	}

	// Fallback: query DHCP leases from the libvirt network directly.
	// This works even when ListAllInterfaceAddresses fails (e.g. no guest
	// agent, session-mode libvirt, or lease source not linked to domain).
	if ip := getDomainIPFromNetworkLeases(dom, conn); ip != "" {
		return ip
	}

	logger.Debug("daemon", "getDomainIP: no IP found from any source", "vm", name)
	return ""
}

// getDomainIPFromNetworkLeases queries all libvirt networks for DHCP leases
// matching the domain's MAC addresses.
func getDomainIPFromNetworkLeases(dom *libvirt.Domain, conn *libvirt.Connect) string {
	name, _ := dom.GetName()

	// Get the domain's MAC addresses from its XML definition.
	macs := getDomainMACs(dom)
	if len(macs) == 0 {
		return ""
	}

	// List all networks and check their DHCP leases.
	nets, err := conn.ListAllNetworks(libvirt.CONNECT_LIST_NETWORKS_ACTIVE)
	if err != nil {
		logger.Debug("daemon", "getDomainIP: failed to list networks",
			"vm", name, "error", err.Error())
		return ""
	}

	var foundIP string
	for i := range nets {
		netName, _ := nets[i].GetName()
		leases, err := nets[i].GetDHCPLeases()
		if err != nil {
			nets[i].Free()
			continue
		}
		if foundIP == "" {
			for _, lease := range leases {
				for _, mac := range macs {
					if strings.EqualFold(lease.Mac, mac) && lease.IPaddr != "" {
						logger.Debug("daemon", "getDomainIP: found IP via network lease",
							"vm", name, "network", netName, "ip", lease.IPaddr, "mac", mac)
						foundIP = lease.IPaddr
						break
					}
				}
				if foundIP != "" {
					break
				}
			}
		}
		nets[i].Free()
	}

	return foundIP
}

// getDomainMACs extracts all MAC addresses from a domain's XML definition.
func getDomainMACs(dom *libvirt.Domain) []string {
	xmlStr, err := dom.GetXMLDesc(0)
	if err != nil {
		return nil
	}

	type macAddr struct {
		Address string `xml:"address,attr"`
	}
	type iface struct {
		MAC macAddr `xml:"mac"`
	}
	type devices struct {
		Interfaces []iface `xml:"interface"`
	}
	type domainXML struct {
		Devices devices `xml:"devices"`
	}

	var d domainXML
	if err := xml.Unmarshal([]byte(xmlStr), &d); err != nil {
		return nil
	}

	var macs []string
	for _, i := range d.Devices.Interfaces {
		if i.MAC.Address != "" {
			macs = append(macs, i.MAC.Address)
		}
	}
	return macs
}
