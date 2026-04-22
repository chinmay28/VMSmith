package vm

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
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
	conn                *libvirt.Connect
	store               *store.Store
	cfg                 *config.Config
	lifecycleCallbackID int
	lifecycleRegistered bool
	lifecycleStopCh     chan struct{}
}

// NewLibvirtManager creates a new libvirt-backed VM manager.
func NewLibvirtManager(cfg *config.Config, store *store.Store) (*LibvirtManager, error) {
	if err := ensureLibvirtEventLoop(); err != nil {
		return nil, fmt.Errorf("initializing libvirt event loop: %w", err)
	}

	conn, err := libvirt.NewConnect(cfg.Libvirt.URI)
	if err != nil {
		return nil, fmt.Errorf("connecting to libvirt (%s): %w", cfg.Libvirt.URI, err)
	}

	mgr := &LibvirtManager{
		conn:  conn,
		store: store,
		cfg:   cfg,
	}
	if err := mgr.startLifecycleMonitor(); err != nil {
		conn.Close()
		return nil, err
	}

	return mgr, nil
}

// Close releases the libvirt connection.
func (m *LibvirtManager) Close() error {
	m.stopLifecycleMonitor()
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
	// DefaultUser intentionally left empty here: empty means "use root".
	// A non-empty DefaultUser creates a named sudo user and disables root.

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

	// Pre-assign a static IP from the DHCP range before creating the
	// cloud-init ISO.  On Rocky 9 (and other RHEL-based images), NetworkManager
	// may issue its DHCP request before dnsmasq responds, or the startIPMonitor
	// goroutine may kill the VM before cloud-init has had a chance to write the
	// NM keyfile — leaving the second boot with no network configuration at all.
	// Embedding a static IP directly in the NM keyfile (method=manual) removes
	// this race entirely: the interface comes up deterministically on first boot
	// without any DHCP exchange.
	//
	// If the caller already specified a static IP, or if the DHCP range is
	// exhausted / the reservation fails, we fall back to the existing dynamic
	// assignment path (startIPMonitor with DHCP-then-static-fallback).
	if spec.NatStaticIP == "" {
		if staticIP, err := m.findAvailableIP(); err == nil {
			gw := gatewayFromSubnet(m.cfg.Network.Subnet)
			// Remove any stale reservation with this VM name (left by a previous
			// failed create attempt) before adding the new one.
			netMgr.RemoveDHCPHostByName(spec.Name)
			if err := netMgr.AddDHCPHost(natMAC, staticIP, spec.Name); err == nil {
				spec.NatStaticIP = staticIP + "/24"
				spec.NatGateway = gw
			} else {
				logger.Warn("daemon", "failed to reserve DHCP IP; falling back to dynamic assignment",
					"vm", spec.Name, "ip", staticIP, "error", err.Error())
			}
		} else {
			logger.Warn("daemon", "DHCP range exhausted; falling back to dynamic IP assignment",
				"vm", spec.Name, "error", err.Error())
		}
	}

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
		candidate := filepath.Join(m.cfg.Storage.ImagesDir, spec.Image)
		// Images must have a .qcow2 extension so libvirt's AppArmor driver
		// correctly follows the backing-file chain and allows QEMU to open them.
		// Try the name as-is first; if it doesn't exist, append .qcow2.
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			withExt := candidate + ".qcow2"
			if _, err2 := os.Stat(withExt); err2 == nil {
				candidate = withExt
			}
		}
		baseImage = candidate
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
	if err := createCloudInitISO(cloudInitISO, spec, natMAC, ""); err != nil {
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
		// Clean up the DHCP reservation we made — otherwise the next create
		// attempt for the same VM name will fail with a name conflict.
		if spec.NatStaticIP != "" {
			if ip, _, parseErr := net.ParseCIDR(spec.NatStaticIP); parseErr == nil {
				netMgr.RemoveDHCPHost(natMAC, ip.String())
			}
		}
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("starting domain: %w", err)
	}

	vm := &types.VM{
		ID:          id,
		Name:        spec.Name,
		Description: spec.Description,
		Tags:        append([]string(nil), spec.Tags...),
		Spec:        spec,
		State:       types.VMStateRunning,
		NatMAC:      natMAC,
		DiskPath:    diskPath,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Persist to store
	if err := m.store.PutVM(vm); err != nil {
		return nil, fmt.Errorf("storing VM metadata: %w", err)
	}

	// Monitor in background: wait for DHCP IP; if none after 60 s apply a
	// static IP fallback via libvirt DHCP reservation + VM restart.
	go m.startIPMonitor(id, spec.Name, vmDir, natMAC, spec)

	return vm, nil
}

func (m *LibvirtManager) Clone(ctx context.Context, sourceID string, newName string) (*types.VM, error) {
	return nil, fmt.Errorf("vm clone not implemented")
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

	// Remove any DHCP host reservation we may have added for this VM.
	if vm.NatMAC != "" && vm.Spec.NatStaticIP != "" {
		natIP, _, _ := net.ParseCIDR(vm.Spec.NatStaticIP)
		if natIP != nil {
			netMgr := network.NewManager(m.conn, m.cfg)
			netMgr.RemoveDHCPHost(vm.NatMAC, natIP.String())
		}
	}

	// Remove VM directory (disk, cloud-init, etc.)
	vmDir := filepath.Dir(vm.DiskPath)
	os.RemoveAll(vmDir)

	return m.store.DeleteVM(id)
}

// Update modifies the CPU count, RAM, or disk size of a VM.
// The VM is stopped if running, the changes are applied, then it is restarted.
// Disk can only grow (qemu-img resize), not shrink.
func (m *LibvirtManager) Update(ctx context.Context, id string, patch types.VMUpdateSpec) (*types.VM, error) {
	storedVM, err := m.store.GetVM(id)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.LookupDomainByName(storedVM.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain %s: %w", storedVM.Name, err)
	}
	defer dom.Free()

	// Determine whether VM is currently running.
	state, _, _ := dom.GetState()
	wasRunning := state == libvirt.DOMAIN_RUNNING

	// Resolve new values (zero means no change).
	newDescription := storedVM.Description
	if patch.Description != "" {
		newDescription = patch.Description
	}
	newTags := append([]string(nil), storedVM.Tags...)
	if patch.Tags != nil {
		newTags = append([]string(nil), patch.Tags...)
	}

	newCPUs := storedVM.Spec.CPUs
	if patch.CPUs > 0 {
		newCPUs = patch.CPUs
	}
	newRAMMB := storedVM.Spec.RAMMB
	if patch.RAMMB > 0 {
		newRAMMB = patch.RAMMB
	}
	newDiskGB := storedVM.Spec.DiskGB
	if patch.DiskGB > 0 {
		if patch.DiskGB < storedVM.Spec.DiskGB {
			return nil, fmt.Errorf("disk can only grow: requested %d GB is less than current %d GB", patch.DiskGB, storedVM.Spec.DiskGB)
		}
		newDiskGB = patch.DiskGB
	}

	// Handle static IP change.
	newNatStaticIP := storedVM.Spec.NatStaticIP
	newNatGateway := storedVM.Spec.NatGateway
	ipChanged := false
	if patch.NatStaticIP != "" {
		parsedIP, _, err := net.ParseCIDR(patch.NatStaticIP)
		if err != nil {
			return nil, fmt.Errorf("invalid nat_static_ip %q: must be CIDR notation e.g. 192.168.100.50/24", patch.NatStaticIP)
		}
		normalized := parsedIP.String() + "/24"
		if normalized != storedVM.Spec.NatStaticIP {
			newNatStaticIP = normalized
			newNatGateway = patch.NatGateway
			if newNatGateway == "" {
				newNatGateway = gatewayFromSubnet(m.cfg.Network.Subnet)
			}
			ipChanged = true
		}
	}

	// Nothing to do?
	if newCPUs == storedVM.Spec.CPUs && newRAMMB == storedVM.Spec.RAMMB && newDiskGB == storedVM.Spec.DiskGB && newDescription == storedVM.Description && strings.Join(newTags, ",") == strings.Join(storedVM.Tags, ",") && !ipChanged {
		return storedVM, nil
	}

	// Stop the VM if it is running.
	if wasRunning {
		if err := dom.Shutdown(); err != nil {
			// Graceful shutdown failed; force-stop.
			if err2 := dom.Destroy(); err2 != nil {
				return nil, fmt.Errorf("force-stopping domain: %w", err2)
			}
		}
		// Wait up to 60 s for the domain to reach the shut-off state.
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			s, _, _ := dom.GetState()
			if s == libvirt.DOMAIN_SHUTOFF {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	// Update DHCP reservation and regenerate cloud-init ISO if IP changed.
	// The new instance-id forces cloud-init to re-run on the next boot so it
	// overwrites the NM keyfile with the new static IP address.
	if ipChanged {
		netMgr := network.NewManager(m.conn, m.cfg)
		if storedVM.Spec.NatStaticIP != "" {
			if oldIP, _, err := net.ParseCIDR(storedVM.Spec.NatStaticIP); err == nil {
				netMgr.RemoveDHCPHost(storedVM.NatMAC, oldIP.String())
			}
		}
		newIPHost, _, _ := net.ParseCIDR(newNatStaticIP)
		if newIPHost != nil {
			if err := netMgr.AddDHCPHost(storedVM.NatMAC, newIPHost.String(), storedVM.Name); err != nil {
				return nil, fmt.Errorf("updating DHCP reservation: %w", err)
			}
		}
		updatedSpec := storedVM.Spec
		updatedSpec.NatStaticIP = newNatStaticIP
		updatedSpec.NatGateway = newNatGateway
		cloudInitISO := filepath.Join(filepath.Dir(storedVM.DiskPath), "cidata.iso")
		newInstanceID := fmt.Sprintf("%s-ip-%d", storedVM.Name, time.Now().UnixNano())
		if err := createCloudInitISO(cloudInitISO, updatedSpec, storedVM.NatMAC, newInstanceID); err != nil {
			return nil, fmt.Errorf("regenerating cloud-init ISO: %w", err)
		}
	}

	// Redefine the domain XML with updated CPU/RAM.
	if newCPUs != storedVM.Spec.CPUs || newRAMMB != storedVM.Spec.RAMMB {
		// Preserve the existing domain UUID so libvirt accepts the redefinition.
		existingUUID, _ := dom.GetUUIDString()

		updatedSpec := storedVM.Spec
		updatedSpec.CPUs = newCPUs
		updatedSpec.RAMMB = newRAMMB
		cloudInitISO := filepath.Join(filepath.Dir(storedVM.DiskPath), "cidata.iso")
		params := DomainParamsFromSpec(updatedSpec, storedVM.DiskPath, cloudInitISO, m.cfg.Network.Name, storedVM.NatMAC)
		params.UUID = existingUUID
		params.Machine = detectMachineType(m.conn)
		xmlDoc, err := GenerateDomainXML(params)
		if err != nil {
			return nil, fmt.Errorf("generating domain XML: %w", err)
		}
		if _, err := m.conn.DomainDefineXML(xmlDoc); err != nil {
			return nil, fmt.Errorf("redefining domain: %w", err)
		}
	}

	// Grow the disk if requested.
	if newDiskGB > storedVM.Spec.DiskGB {
		cmd := exec.Command("qemu-img", "resize", storedVM.DiskPath, fmt.Sprintf("%dG", newDiskGB))
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("resizing disk: %s: %w", string(out), err)
		}
	}

	// Persist updated spec.
	storedVM.Description = newDescription
	storedVM.Tags = append([]string(nil), newTags...)
	storedVM.Spec.Description = newDescription
	storedVM.Spec.Tags = append([]string(nil), newTags...)
	storedVM.Spec.CPUs = newCPUs
	storedVM.Spec.RAMMB = newRAMMB
	storedVM.Spec.DiskGB = newDiskGB
	if ipChanged {
		storedVM.Spec.NatStaticIP = newNatStaticIP
		storedVM.Spec.NatGateway = newNatGateway
		if newIPHost, _, _ := net.ParseCIDR(newNatStaticIP); newIPHost != nil {
			storedVM.IP = newIPHost.String()
		}
	}
	storedVM.UpdatedAt = time.Now()
	if err := m.store.PutVM(storedVM); err != nil {
		return nil, fmt.Errorf("storing updated VM: %w", err)
	}

	// Restart if it was running.
	if wasRunning {
		dom2, err := m.conn.LookupDomainByName(storedVM.Name)
		if err != nil {
			return nil, fmt.Errorf("looking up domain after update: %w", err)
		}
		defer dom2.Free()
		if err := dom2.Create(); err != nil {
			return nil, fmt.Errorf("restarting domain: %w", err)
		}
		storedVM.State = types.VMStateRunning
	} else {
		storedVM.State = types.VMStateStopped
	}

	return storedVM, nil
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

	// If no IP detected and VM has a stored static IP, return that.
	if vm.IP == "" && vm.Spec.NatStaticIP != "" {
		if parsed, _, err := net.ParseCIDR(vm.Spec.NatStaticIP); err == nil {
			vm.IP = parsed.String()
		}
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
		if vm.IP == "" && vm.Spec.NatStaticIP != "" {
			if parsed, _, err := net.ParseCIDR(vm.Spec.NatStaticIP); err == nil {
				vm.IP = parsed.String()
			}
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

// createCloudInitISO writes a NoCloud cloud-init ISO to isoPath.
// instanceID overrides the cloud-init instance identifier; empty defaults to
// spec.Name.  Pass a unique value (e.g. "name-ip-<nano>") to force cloud-init
// to re-run on next boot (cloud-init re-runs when the instance-id changes).
func createCloudInitISO(isoPath string, spec types.VMSpec, natMAC, instanceID string) error {
	if instanceID == "" {
		instanceID = spec.Name
	}

	tmpDir, err := os.MkdirTemp("", "vmsmith-ci-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// meta-data
	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", instanceID, spec.Name)
	if err := os.WriteFile(filepath.Join(tmpDir, "meta-data"), []byte(metaData), 0644); err != nil {
		return err
	}

	// user-data: prefer a custom file, otherwise generate one with an NM
	// connection keyfile embedded via write_files.  Writing the keyfile
	// directly is more reliable on Rocky/RHEL than cloud-init's NM renderer
	// interpreting the v2 network-config (which may silently do nothing).
	var userData string
	if spec.CloudInitFile != "" {
		custom, err := os.ReadFile(spec.CloudInitFile)
		if err != nil {
			return fmt.Errorf("reading cloud-init file: %w", err)
		}
		userData = string(custom)
	} else {
		userData = buildCloudConfig(spec, natMAC)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "user-data"), []byte(userData), 0644); err != nil {
		return err
	}

	// network-config (v2) as belt-and-suspenders for Ubuntu/netplan.
	// Rocky/RHEL rely on the NM keyfile written via user-data above.
	netCfg := generateNetworkConfig(spec.Networks, natMAC, spec.NatStaticIP, spec.NatGateway)
	if err := os.WriteFile(filepath.Join(tmpDir, "network-config"), []byte(netCfg), 0644); err != nil {
		return err
	}

	return writeCloudInitISO(isoPath, tmpDir)
}

// buildNMKeyfile returns the body of a NetworkManager keyfile connection for
// the primary NAT interface.  When staticIP (CIDR) and gateway are provided
// the interface is configured with a static address; otherwise DHCP is used.
func buildNMKeyfile(mac, staticIP, gateway string) string {
	var sb strings.Builder
	sb.WriteString("[connection]\n")
	sb.WriteString("id=vmsmith-nat\n")
	sb.WriteString("type=ethernet\n")
	sb.WriteString("autoconnect=true\n")
	sb.WriteString("autoconnect-priority=200\n")
	sb.WriteString("\n[ethernet]\n")
	sb.WriteString("mac-address=" + mac + "\n")
	if staticIP != "" {
		sb.WriteString("\n[ipv4]\n")
		sb.WriteString("method=manual\n")
		sb.WriteString("addresses=" + staticIP + "\n")
		if gateway != "" {
			sb.WriteString("gateway=" + gateway + "\n")
			sb.WriteString("dns=" + gateway + ";8.8.8.8;\n")
		}
	} else {
		sb.WriteString("\n[ipv4]\n")
		sb.WriteString("method=auto\n")
	}
	sb.WriteString("\n[ipv6]\n")
	sb.WriteString("method=ignore\n")
	return sb.String()
}

// buildCloudConfig generates a cloud-config user-data string that writes an
// NM connection keyfile for the primary NAT interface and activates it.
// This approach is reliable on Rocky/RHEL where the cloud-init v2
// network-config renderer may fail to configure NetworkManager correctly.
func buildCloudConfig(spec types.VMSpec, natMAC string) string {
	nmContent := buildNMKeyfile(natMAC, spec.NatStaticIP, spec.NatGateway)

	// Indent each line of the NM keyfile by 6 spaces so YAML block scalar
	// indentation is handled correctly (YAML strips those 6 leading spaces).
	var indented strings.Builder
	for _, line := range strings.Split(strings.TrimRight(nmContent, "\n"), "\n") {
		indented.WriteString("      " + line + "\n")
	}

	var sb strings.Builder
	sb.WriteString("#cloud-config\n")
	if spec.DefaultUser != "" {
		// A named user was requested: create it with SSH key + sudo and disable root.
		sb.WriteString("disable_root: true\n")
		if spec.SSHPubKey != "" {
			sb.WriteString(fmt.Sprintf("users:\n  - default\n  - name: %s\n    ssh_authorized_keys:\n      - %s\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    shell: /bin/bash\n    lock_passwd: true\n", spec.DefaultUser, spec.SSHPubKey))
		} else {
			sb.WriteString(fmt.Sprintf("users:\n  - default\n  - name: %s\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    shell: /bin/bash\n    lock_passwd: false\n", spec.DefaultUser))
		}
	} else {
		// Default: enable root login. Inject SSH key into root if provided.
		sb.WriteString("disable_root: false\n")
		if spec.SSHPubKey != "" {
			sb.WriteString(fmt.Sprintf("users:\n  - name: root\n    ssh_authorized_keys:\n      - %s\n", spec.SSHPubKey))
		}
	}
	sb.WriteString("write_files:\n")
	sb.WriteString("  - path: /etc/NetworkManager/system-connections/vmsmith-nat.nmconnection\n")
	sb.WriteString("    permissions: '0600'\n")
	sb.WriteString("    owner: root:root\n")
	sb.WriteString("    makedirs: true\n")
	sb.WriteString("    content: |\n")
	sb.WriteString(indented.String())
	if spec.DefaultUser == "" {
		// Drop-in sshd config to allow key-based root login across all distros.
		sb.WriteString("  - path: /etc/ssh/sshd_config.d/99-vmsmith-root.conf\n")
		sb.WriteString("    permissions: '0600'\n")
		sb.WriteString("    owner: root:root\n")
		sb.WriteString("    makedirs: true\n")
		sb.WriteString("    content: |\n")
		sb.WriteString("      PermitRootLogin prohibit-password\n")
	}
	sb.WriteString("runcmd:\n")
	sb.WriteString("  - chmod 600 /etc/NetworkManager/system-connections/vmsmith-nat.nmconnection\n")
	// restorecon fixes SELinux file context on Rocky/RHEL so NetworkManager can read the keyfile.
	// Without this, NM may silently ignore the file due to SELinux type mismatch.
	sb.WriteString("  - restorecon -v /etc/NetworkManager/system-connections/vmsmith-nat.nmconnection 2>/dev/null || true\n")
	if spec.DefaultUser == "" {
		sb.WriteString("  - restorecon -v /etc/ssh/sshd_config.d/99-vmsmith-root.conf 2>/dev/null || true\n")
		sb.WriteString("  - systemctl reload-or-restart sshd 2>/dev/null || systemctl reload-or-restart ssh 2>/dev/null || true\n")
	}
	sb.WriteString("  - nmcli connection reload\n")
	sb.WriteString("  - nmcli connection up vmsmith-nat 2>/dev/null || true\n")
	return sb.String()
}

// writeCloudInitISO creates the cidata ISO from files in tmpDir.
// Tries genisoimage first, then falls back to mkisofs (available on Rocky/RHEL).
func writeCloudInitISO(isoPath, tmpDir string) error {
	files := []string{
		filepath.Join(tmpDir, "meta-data"),
		filepath.Join(tmpDir, "user-data"),
		filepath.Join(tmpDir, "network-config"),
	}
	baseArgs := []string{"-output", isoPath, "-volid", "cidata", "-joliet", "-rock"}
	args := append(baseArgs, files...)

	for _, bin := range []string{"genisoimage", "mkisofs"} {
		cmd := exec.Command(bin, args...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		if isExecNotFound(err) {
			continue
		}
		return fmt.Errorf("%s: %s: %w", bin, strings.TrimSpace(string(out)), err)
	}
	return fmt.Errorf("neither genisoimage nor mkisofs found; install one (e.g. yum install genisoimage)")
}

// isExecNotFound returns true when err indicates the binary was not in PATH.
func isExecNotFound(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) && errors.Is(execErr.Err, exec.ErrNotFound)
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
	for i, att := range networks {
		id := fmt.Sprintf("eth%d", i+1)
		sb.WriteString(fmt.Sprintf("  %s:\n", id))
		sb.WriteString(fmt.Sprintf("    match:\n      macaddress: \"%s\"\n", att.MacAddress))

		if att.StaticIP != "" {
			sb.WriteString(fmt.Sprintf("    addresses:\n      - %s\n", att.StaticIP))
			if att.Gateway != "" {
				sb.WriteString(fmt.Sprintf("    routes:\n      - to: 0.0.0.0/0\n        via: %s\n        metric: %d\n",
					att.Gateway, 200+i)) // higher metric than NAT default route
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
	for _, n := range networks {
		if n.StaticIP != "" {
			return true
		}
	}
	return false
}

// startIPMonitor runs in a goroutine after VM creation.  It waits up to 60 s
// for the VM to acquire a pingable IP via DHCP.  If that times out it finds
// an available IP, adds a libvirt DHCP host reservation so dnsmasq always
// gives that IP to the VM's MAC, restarts the VM, and waits another 60 s to
// verify the IP is reachable.
func (m *LibvirtManager) startIPMonitor(vmID, vmName, vmDir, natMAC string, spec types.VMSpec) {
	// 120 s gives Rocky 9 (and other RHEL-based images) enough time for cloud-init to
	// finish writing the NM keyfile and bring the interface up.  Ubuntu typically
	// completes in ~30 s, so this longer window does not hurt.
	const dhcpTimeout = 120 * time.Second
	const pollInterval = 5 * time.Second

	// For user-specified static IP: just verify it becomes pingable.
	if spec.NatStaticIP != "" {
		ip, _, err := net.ParseCIDR(spec.NatStaticIP)
		if err != nil {
			return
		}
		deadline := time.Now().Add(dhcpTimeout)
		for time.Now().Before(deadline) {
			time.Sleep(pollInterval)
			if pingable(ip.String()) {
				logger.Info("daemon", "VM static IP verified pingable", "vm", vmName, "ip", ip.String())
				return
			}
		}
		logger.Warn("daemon", "VM static IP not pingable after 60s", "vm", vmName, "ip", ip.String())
		return
	}

	// Auto-assign path: wait for DHCP.
	logger.Info("daemon", "waiting for VM to get DHCP IP", "vm", vmName)
	deadline := time.Now().Add(dhcpTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		dom, err := m.conn.LookupDomainByName(vmName)
		if err != nil {
			continue
		}
		ip := getDomainIP(dom, m.conn)
		dom.Free()
		if ip != "" && pingable(ip) {
			logger.Info("daemon", "VM got pingable DHCP IP", "vm", vmName, "ip", ip)
			return
		}
	}

	// DHCP timed out — fall back to a static IP via DHCP reservation.
	logger.Warn("daemon", "DHCP timeout: applying static IP fallback", "vm", vmName)
	staticIP, err := m.findAvailableIP()
	if err != nil {
		logger.Error("daemon", "could not find available static IP", "vm", vmName, "error", err.Error())
		return
	}
	if err := m.applyStaticIPFallback(vmID, vmName, natMAC, spec, staticIP); err != nil {
		logger.Error("daemon", "static IP fallback failed", "vm", vmName, "ip", staticIP, "error", err.Error())
		return
	}
	logger.Info("daemon", "static IP fallback succeeded", "vm", vmName, "ip", staticIP)
}

// findAvailableIP returns an unallocated IP from the NAT network's DHCP range.
// It checks active DHCP leases and existing host reservations.
func (m *LibvirtManager) findAvailableIP() (string, error) {
	libvirtNet, err := m.conn.LookupNetworkByName(m.cfg.Network.Name)
	if err != nil {
		return "", fmt.Errorf("looking up network: %w", err)
	}
	defer libvirtNet.Free()

	// Collect IPs currently leased or reserved.
	used := make(map[string]bool)
	if leases, err := libvirtNet.GetDHCPLeases(); err == nil {
		for _, l := range leases {
			used[l.IPaddr] = true
		}
	}
	if xmlStr, err := libvirtNet.GetXMLDesc(0); err == nil {
		for _, ip := range parseNetworkHostIPs(xmlStr) {
			used[ip] = true
		}
	}

	start := net.ParseIP(m.cfg.Network.DHCPStart).To4()
	end := net.ParseIP(m.cfg.Network.DHCPEnd).To4()
	if start == nil || end == nil {
		return "", fmt.Errorf("invalid DHCP range in config: %s - %s",
			m.cfg.Network.DHCPStart, m.cfg.Network.DHCPEnd)
	}
	for i := int(start[3]); i <= int(end[3]); i++ {
		candidate := fmt.Sprintf("%d.%d.%d.%d", start[0], start[1], start[2], i)
		if !used[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free IP in DHCP range %s-%s",
		m.cfg.Network.DHCPStart, m.cfg.Network.DHCPEnd)
}

// applyStaticIPFallback adds a DHCP host reservation for staticIP, restarts
// the VM so it picks up the reserved IP, and waits up to 60 s to verify.
func (m *LibvirtManager) applyStaticIPFallback(vmID, vmName, natMAC string, spec types.VMSpec, staticIP string) error {
	netMgr := network.NewManager(m.conn, m.cfg)
	if err := netMgr.AddDHCPHost(natMAC, staticIP, vmName); err != nil {
		return fmt.Errorf("adding DHCP reservation: %w", err)
	}

	// Restart the VM so it requests DHCP again and gets the reserved IP.
	dom, err := m.conn.LookupDomainByName(vmName)
	if err != nil {
		return fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()
	dom.Destroy() //nolint:errcheck — force stop regardless of state
	if err := dom.Create(); err != nil {
		return fmt.Errorf("restarting domain: %w", err)
	}

	// Wait for the reserved IP to become pingable.
	const waitTimeout = 60 * time.Second
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		if pingable(staticIP) {
			// Persist the auto-assigned IP in the VM spec so Get/List can
			// return it even when DHCP lease lookup is unavailable.
			if vm, err := m.store.GetVM(vmID); err == nil {
				vm.Spec.NatStaticIP = staticIP + "/24"
				vm.Spec.NatGateway = gatewayFromSubnet(m.cfg.Network.Subnet)
				vm.UpdatedAt = time.Now()
				_ = m.store.PutVM(vm)
			}
			return nil
		}
	}
	return fmt.Errorf("VM still not pingable at %s after 60s", staticIP)
}

// pingable returns true if ip responds to a single ICMP echo within 3 s.
func pingable(ip string) bool {
	cmd := exec.Command("ping", "-c", "1", "-W", "3", ip)
	return cmd.Run() == nil
}

// gatewayFromSubnet derives the first host address from a CIDR subnet string
// (e.g. "192.168.100.0/24" → "192.168.100.1").
func gatewayFromSubnet(subnet string) string {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return ""
	}
	gw := make(net.IP, len(ipNet.IP))
	copy(gw, ipNet.IP)
	gw[len(gw)-1]++
	return gw.String()
}

// parseNetworkHostIPs extracts <host ip='...'> addresses from a libvirt
// network XML description, used to avoid assigning already-reserved IPs.
func parseNetworkHostIPs(xmlStr string) []string {
	type hostEntry struct {
		IP string `xml:"ip,attr"`
	}
	type dhcpBlock struct {
		Hosts []hostEntry `xml:"host"`
	}
	type ipElem struct {
		DHCP dhcpBlock `xml:"dhcp"`
	}
	type networkXML struct {
		IPs []ipElem `xml:"ip"`
	}

	var n networkXML
	if err := xml.Unmarshal([]byte(xmlStr), &n); err != nil {
		return nil
	}
	var ips []string
	for _, ipEl := range n.IPs {
		for _, h := range ipEl.DHCP.Hosts {
			if h.IP != "" {
				ips = append(ips, h.IP)
			}
		}
	}
	return ips
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
