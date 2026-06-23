package vm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/host"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

// cloneProgressKey is the context key under which a clone progress callback may
// be attached. It lets the API layer observe qemu-img convert progress during
// Clone without widening the Manager interface (the CLI passes a plain context
// and simply gets no callback).
type cloneProgressKey struct{}

// WithCloneProgress returns a context that carries a clone progress callback,
// invoked with the qemu-img convert completion percentage (0-100).
func WithCloneProgress(ctx context.Context, fn func(percent float64)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, cloneProgressKey{}, fn)
}

func cloneProgressFromContext(ctx context.Context) func(percent float64) {
	if ctx == nil {
		return nil
	}
	if fn, ok := ctx.Value(cloneProgressKey{}).(func(percent float64)); ok {
		return fn
	}
	return nil
}

// LibvirtManager implements the Manager interface using libvirt.
type LibvirtManager struct {
	conn                *libvirt.Connect
	store               *store.Store
	cfg                 *config.Config
	lifecycleCallbackID int
	lifecycleRegistered bool
	lifecycleStopCh     chan struct{}
	eventBus            *events.EventBus
	consoleTerminator   ConsoleSessionTerminator
}

// SetEventBus wires an event bus so the manager can emit system events for
// failure modes that would otherwise only surface as warnings (e.g. DHCP
// range exhaustion).  Safe to call before or after Create/Clone; nil is
// treated as "no bus configured" and no events are emitted.
func (m *LibvirtManager) SetEventBus(bus *events.EventBus) {
	m.eventBus = bus
}

// SetConsoleSessionTerminator wires an optional callback that is invoked after
// successful stop / force-stop / delete operations so active websocket console
// sessions can be closed even when the lifecycle action did not originate from
// an API handler.
func (m *LibvirtManager) SetConsoleSessionTerminator(fn ConsoleSessionTerminator) {
	m.consoleTerminator = fn
}

func (m *LibvirtManager) notifyConsoleTermination(vmID, reason string) {
	if m.consoleTerminator != nil {
		m.consoleTerminator(vmID, reason)
	}
}

// emitDHCPExhausted publishes a `dhcp.exhausted` system event when the NAT
// network's DHCP range cannot satisfy a static-IP reservation.  No-op when
// no event bus is configured.
func (m *LibvirtManager) emitDHCPExhausted(vmName, reason string) {
	if m.eventBus == nil {
		return
	}
	m.eventBus.Publish(events.NewSystemEventWithAttrs(
		"dhcp.exhausted",
		types.EventSeverityWarn,
		"DHCP range exhausted; falling back to dynamic IP assignment for "+vmName,
		map[string]string{"vm_name": vmName, "reason": reason},
	))
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

	// Windows guests get a one-time auto-generated Administrator password when
	// the caller omits admin_password. The password is shown exactly once on
	// the create response (vm.GeneratedAdminPassword) so the operator can copy
	// it; it is never returned by Get/List and never persisted to bbolt
	// (spec.AdminPassword is redacted after the provisioning ISO is written).
	var generatedAdminPassword string
	if spec.IsWindows() && spec.AdminPassword == "" {
		pw, err := generateAdminPassword()
		if err != nil {
			return nil, fmt.Errorf("generating admin password: %w", err)
		}
		spec.AdminPassword = pw
		generatedAdminPassword = pw
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
			m.emitDHCPExhausted(spec.Name, err.Error())
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
	if err := createProvisioningISO(cloudInitISO, spec, natMAC, ""); err != nil {
		return nil, fmt.Errorf("creating provisioning ISO: %w", err)
	}
	// The Windows Administrator password has now been baked into the
	// provisioning ISO; redact it so it never lands in bbolt or the API
	// response (the VMSpec is persisted and returned verbatim).
	spec.AdminPassword = ""

	// Generate and define domain XML
	params := DomainParamsFromSpec(spec, diskPath, cloudInitISO, m.cfg.Network.Name, natMAC)
	params.Machine = resolveMachine(spec.Machine, func() string { return detectMachineType(m.conn) })
	m.applyVirtioWin(&params, spec)
	m.applyGPUs(&params, spec)
	xmlDoc, err := GenerateDomainXML(params)
	if err != nil {
		return nil, err
	}

	// Reap any orphaned libvirt domain that still carries this name. The API
	// layer already rejects a create whose name still exists in the store, so a
	// libvirt domain surviving here is never a live, tracked VM — it is leftover
	// state from an earlier VM whose bbolt record was removed but whose libvirt
	// definition was not fully torn down (e.g. an undefine that failed during
	// Delete). Without this, recreating a previously-deleted VM name fails at
	// DomainDefineXML with "domain already exists".
	if orphan, lookupErr := m.conn.LookupDomainByName(spec.Name); lookupErr == nil {
		logger.Warn("daemon", "reaping orphaned libvirt domain before recreate",
			"vm", spec.Name)
		_ = forceUndefineDomain(orphan)
		orphan.Free()
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

	// Set the one-time generated password on the response copy AFTER
	// PutVM so it never lands in bbolt. Get/List rehydrate from the store and
	// will not see this field.
	vm.GeneratedAdminPassword = generatedAdminPassword

	return vm, nil
}

func (m *LibvirtManager) Clone(ctx context.Context, sourceID string, newName string) (*types.VM, error) {
	sourceVM, err := m.store.GetVM(sourceID)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.LookupDomainByName(sourceVM.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain %s: %w", sourceVM.Name, err)
	}
	defer dom.Free()

	state, _, err := dom.GetState()
	if err != nil {
		return nil, fmt.Errorf("getting domain state %s: %w", sourceVM.Name, err)
	}
	if state == libvirt.DOMAIN_RUNNING {
		return nil, types.NewAPIError("invalid_vm_state", "source VM must be stopped before cloning")
	}

	netMgr := network.NewManager(m.conn, m.cfg)
	if err := netMgr.EnsureNetwork(); err != nil {
		return nil, fmt.Errorf("ensuring NAT network: %w", err)
	}

	id := fmt.Sprintf("vm-%d", time.Now().UnixNano())
	vmDir := filepath.Join(m.cfg.Storage.BaseDir, id)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return nil, fmt.Errorf("creating VM dir: %w", err)
	}

	clonedSpec := cloneVMSpec(sourceVM.Spec, newName)
	clonedDiskPath := filepath.Join(vmDir, "disk.qcow2")
	if err := createClonedDiskWithProgress(sourceVM.DiskPath, clonedDiskPath, cloneProgressFromContext(ctx)); err != nil {
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("creating overlay disk: %w", err)
	}

	natMAC := generateMAC()
	reservedCloneIP := ""
	if clonedSpec.NatStaticIP == "" {
		if staticIP, err := m.findAvailableIP(); err == nil {
			gw := gatewayFromSubnet(m.cfg.Network.Subnet)
			netMgr.RemoveDHCPHostByName(clonedSpec.Name)
			if err := netMgr.AddDHCPHost(natMAC, staticIP, clonedSpec.Name); err == nil {
				clonedSpec.NatStaticIP = staticIP + "/24"
				clonedSpec.NatGateway = gw
				reservedCloneIP = staticIP
			} else {
				logger.Warn("daemon", "failed to reserve DHCP IP for clone; falling back to dynamic assignment",
					"vm", clonedSpec.Name, "ip", staticIP, "error", err.Error())
			}
		} else {
			logger.Warn("daemon", "DHCP range exhausted for clone; falling back to dynamic IP assignment",
				"vm", clonedSpec.Name, "error", err.Error())
			m.emitDHCPExhausted(clonedSpec.Name, err.Error())
		}
	}

	cleanupCloneReservation := func() {
		if reservedCloneIP != "" {
			netMgr.RemoveDHCPHost(natMAC, reservedCloneIP)
		}
	}

	cloudInitISO := filepath.Join(vmDir, "cidata.iso")
	if err := createProvisioningISO(cloudInitISO, clonedSpec, natMAC, ""); err != nil {
		cleanupCloneReservation()
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("creating provisioning ISO: %w", err)
	}

	params := DomainParamsFromSpec(clonedSpec, clonedDiskPath, cloudInitISO, m.cfg.Network.Name, natMAC)
	params.Machine = resolveMachine(clonedSpec.Machine, func() string { return detectMachineType(m.conn) })
	m.applyVirtioWin(&params, clonedSpec)
	m.applyGPUs(&params, clonedSpec)
	xmlDoc, err := GenerateDomainXML(params)
	if err != nil {
		cleanupCloneReservation()
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("generating domain XML: %w", err)
	}

	cloneDom, err := m.conn.DomainDefineXML(xmlDoc)
	if err != nil {
		cleanupCloneReservation()
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("defining domain: %w", err)
	}
	defer cloneDom.Free()

	cloned := &types.VM{
		ID:          id,
		Name:        newName,
		Description: sourceVM.Description,
		Tags:        append([]string(nil), sourceVM.Tags...),
		Spec:        clonedSpec,
		State:       types.VMStateStopped,
		IP:          "",
		NatMAC:      natMAC,
		DiskPath:    clonedDiskPath,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := m.store.PutVM(cloned); err != nil {
		cloneDom.Undefine()
		cleanupCloneReservation()
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("storing VM metadata: %w", err)
	}

	return cloned, nil
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
	if err := m.store.PutVM(vm); err != nil {
		return err
	}
	m.notifyConsoleTermination(id, "vm_stopped")
	return nil
}

// ForceStop immediately destroys the running domain without sending an ACPI
// shutdown signal.  Equivalent to pulling the power cord — used when the guest
// OS is unresponsive or the operator deliberately wants to skip graceful
// shutdown.  Returns a typed vm_already_stopped error (HTTP 409) when the VM
// is already in a non-running state so callers can distinguish "no-op" from
// "real failure".
func (m *LibvirtManager) ForceStop(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain %s: %w", vm.Name, err)
	}
	defer dom.Free()

	state, _, err := dom.GetState()
	if err != nil {
		return fmt.Errorf("getting domain state: %w", err)
	}
	if state == libvirt.DOMAIN_SHUTOFF {
		return types.NewAPIError("vm_already_stopped", "vm is already stopped")
	}

	if err := dom.Destroy(); err != nil {
		return fmt.Errorf("force-stopping domain: %w", err)
	}

	vm.State = types.VMStateStopped
	vm.UpdatedAt = time.Now()
	if err := m.store.PutVM(vm); err != nil {
		return err
	}
	m.notifyConsoleTermination(id, "vm_force_stopped")
	return nil
}

// Restart performs a graceful stop followed by a start.  When the VM is already
// stopped, it just starts it.  The shutdown step falls back to a forced destroy
// after restartShutdownTimeout so a stuck guest doesn't block the operation.
func (m *LibvirtManager) Restart(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain %s: %w", vm.Name, err)
	}
	defer dom.Free()

	state, _, err := dom.GetState()
	if err != nil {
		return fmt.Errorf("getting domain state: %w", err)
	}

	if state == libvirt.DOMAIN_RUNNING {
		if shutdownErr := dom.Shutdown(); shutdownErr != nil {
			if destroyErr := dom.Destroy(); destroyErr != nil {
				return fmt.Errorf("shutting down domain: %w (destroy fallback: %v)", shutdownErr, destroyErr)
			}
		} else if !waitForDomainShutoff(ctx, dom, restartShutdownTimeout) {
			if destroyErr := dom.Destroy(); destroyErr != nil {
				return fmt.Errorf("forcing shutdown after grace period: %w", destroyErr)
			}
		}
	}

	if err := dom.Create(); err != nil {
		return fmt.Errorf("starting domain: %w", err)
	}

	vm.State = types.VMStateRunning
	vm.UpdatedAt = time.Now()
	if err := m.store.PutVM(vm); err != nil {
		return err
	}
	m.notifyConsoleTermination(id, "vm_restarted")
	return nil
}

// Reboot signals the guest OS to perform an in-guest reboot via libvirt's
// dom.Reboot(). Unlike Restart (which is a stop+start cycle that power-cycles
// the QEMU process), Reboot keeps the domain alive and asks the guest to
// reboot itself. The IP, MAC, and DHCP reservation are preserved with no risk
// of the cloud-init ISO being re-applied. Refuses if the VM is not currently
// running so the caller gets a clear typed error.
func (m *LibvirtManager) Reboot(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain %s: %w", vm.Name, err)
	}
	defer dom.Free()

	state, _, err := dom.GetState()
	if err != nil {
		return fmt.Errorf("getting domain state: %w", err)
	}
	if state != libvirt.DOMAIN_RUNNING {
		return types.NewAPIError("vm_not_running", "vm must be running to reboot")
	}

	if err := dom.Reboot(0); err != nil {
		return fmt.Errorf("rebooting domain: %w", err)
	}

	vm.UpdatedAt = time.Now()
	if err := m.store.PutVM(vm); err != nil {
		return err
	}
	m.notifyConsoleTermination(id, "vm_rebooted")
	return nil
}

// Suspend pauses a running VM, freezing CPU + memory state without releasing
// resources. The VM keeps its IP, open files, and RAM contents — Resume picks
// up exactly where Suspend left off. Refuses if the VM is not currently
// running so callers get a clear typed error instead of a no-op.
func (m *LibvirtManager) Suspend(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain %s: %w", vm.Name, err)
	}
	defer dom.Free()

	state, _, err := dom.GetState()
	if err != nil {
		return fmt.Errorf("getting domain state: %w", err)
	}
	if state == libvirt.DOMAIN_PAUSED {
		return types.NewAPIError("vm_already_paused", "vm is already paused")
	}
	if state != libvirt.DOMAIN_RUNNING {
		return types.NewAPIError("vm_not_running", "vm must be running to suspend")
	}

	if err := dom.Suspend(); err != nil {
		return fmt.Errorf("suspending domain: %w", err)
	}

	vm.State = types.VMStatePaused
	vm.UpdatedAt = time.Now()
	return m.store.PutVM(vm)
}

// Resume unpauses a suspended VM, restoring it to the running state.
// Refuses unless the VM is currently paused.
func (m *LibvirtManager) Resume(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return fmt.Errorf("looking up domain %s: %w", vm.Name, err)
	}
	defer dom.Free()

	state, _, err := dom.GetState()
	if err != nil {
		return fmt.Errorf("getting domain state: %w", err)
	}
	if state != libvirt.DOMAIN_PAUSED {
		return types.NewAPIError("vm_not_paused", "vm must be paused to resume")
	}

	if err := dom.Resume(); err != nil {
		return fmt.Errorf("resuming domain: %w", err)
	}

	vm.State = types.VMStateRunning
	vm.UpdatedAt = time.Now()
	return m.store.PutVM(vm)
}

// restartShutdownTimeout is the grace period given to a guest to react to ACPI
// shutdown before Restart force-destroys it.  Kept short relative to libvirt's
// default to avoid hanging an interactive `vmsmith vm restart` call.
const restartShutdownTimeout = 30 * time.Second

// waitForDomainShutoff polls dom.GetState until the domain reports SHUTOFF or
// the deadline passes.  Returns true if the domain shut down within the window.
func waitForDomainShutoff(ctx context.Context, dom *libvirt.Domain, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, _, err := dom.GetState()
		if err == nil && state == libvirt.DOMAIN_SHUTOFF {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}

// Delete removes a VM entirely: stops it, undefines the domain, removes disk files.
func (m *LibvirtManager) Delete(ctx context.Context, id string) error {
	vm, err := m.store.GetVM(id)
	if err != nil {
		return err
	}
	if vm.Spec.Locked {
		return types.NewAPIError("vm_locked", "vm is locked; unlock it before deleting")
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err == nil {
		defer dom.Free()
		if undefErr := forceUndefineDomain(dom); undefErr != nil {
			// A leftover libvirt definition keeps the domain name registered.
			// Because the domain is named after vm.Name (not the unique VM id),
			// a future create that reuses this name would then fail at
			// DomainDefineXML with "domain already exists". Surface it so the
			// failure is diagnosable rather than silent.
			logger.Warn("daemon", "failed to fully undefine domain on delete; recreating a VM with the same name may fail",
				"vm", vm.Name, "error", undefErr.Error())
		}
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

	if err := m.store.DeleteVM(id); err != nil {
		return err
	}
	m.notifyConsoleTermination(id, "vm_deleted")
	return nil
}

// forceUndefineDomain tears a libvirt domain down completely so its name is
// released. It destroys the domain first when it is still active — a merely
// undefined active domain becomes *transient* and keeps its name registered,
// which would later collide with a create that reuses the name — then removes
// the persistent definition together with any managed-save image, NVRAM, and
// snapshot metadata that can otherwise block undefine. The combined-flags
// UndefineFlags is attempted first and falls back to a plain Undefine on older
// libvirt that rejects the flag set. Returns the final undefine error (nil on
// success) so callers can detect a domain that survived teardown.
func forceUndefineDomain(dom *libvirt.Domain) error {
	if state, _, err := dom.GetState(); err == nil && state != libvirt.DOMAIN_SHUTOFF {
		// Active (running/paused/...): destroy so the name is actually freed
		// rather than demoted to a transient domain. Destroy also removes a
		// transient domain outright, in which case the undefine below is a
		// harmless no-op error we ignore via the fallback.
		_ = dom.Destroy()
	}
	const flags = libvirt.DOMAIN_UNDEFINE_MANAGED_SAVE |
		libvirt.DOMAIN_UNDEFINE_SNAPSHOTS_METADATA |
		libvirt.DOMAIN_UNDEFINE_NVRAM
	if err := dom.UndefineFlags(flags); err != nil {
		return dom.Undefine()
	}
	return nil
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

	// Resolve AutoStart change. nil pointer means "no change".
	newAutoStart := storedVM.Spec.AutoStart
	autoStartChanged := false
	if patch.AutoStart != nil && *patch.AutoStart != storedVM.Spec.AutoStart {
		newAutoStart = *patch.AutoStart
		autoStartChanged = true
	}

	// Resolve Locked change. nil pointer means "no change".
	newLocked := storedVM.Spec.Locked
	lockedChanged := false
	if patch.Locked != nil && *patch.Locked != storedVM.Spec.Locked {
		newLocked = *patch.Locked
		lockedChanged = true
	}

	// Resolve ClockOffset change. nil pointer means "no change"; an explicit
	// empty string clears the override so the OS-family default takes over
	// again at next render.
	newClockOffset := storedVM.Spec.ClockOffset
	clockChanged := false
	if patch.ClockOffset != nil {
		desired := strings.ToLower(strings.TrimSpace(*patch.ClockOffset))
		if desired != strings.ToLower(strings.TrimSpace(storedVM.Spec.ClockOffset)) {
			newClockOffset = desired
			clockChanged = true
		}
	}

	// Resolve DiskBus / NICModel changes (roadmap 5.6.12 switch-to-virtio).
	// nil = no change; empty string clears the override; case-insensitive
	// match against the stored value to skip a redefine when the operator
	// re-supplies the existing value with different casing.
	newDiskBus := storedVM.Spec.DiskBus
	diskBusChanged := false
	if patch.DiskBus != nil {
		desired := strings.ToLower(strings.TrimSpace(*patch.DiskBus))
		if desired != strings.ToLower(strings.TrimSpace(storedVM.Spec.DiskBus)) {
			newDiskBus = desired
			diskBusChanged = true
		}
	}
	newNICModel := storedVM.Spec.NICModel
	nicModelChanged := false
	if patch.NICModel != nil {
		desired := strings.ToLower(strings.TrimSpace(*patch.NICModel))
		if desired != strings.ToLower(strings.TrimSpace(storedVM.Spec.NICModel)) {
			newNICModel = desired
			nicModelChanged = true
		}
	}

	// Nothing to do?
	if newCPUs == storedVM.Spec.CPUs && newRAMMB == storedVM.Spec.RAMMB && newDiskGB == storedVM.Spec.DiskGB && newDescription == storedVM.Description && strings.Join(newTags, ",") == strings.Join(storedVM.Tags, ",") && !ipChanged && !autoStartChanged && !lockedChanged && !clockChanged && !diskBusChanged && !nicModelChanged {
		return storedVM, nil
	}

	// Metadata-only changes (AutoStart and/or Locked) skip the stop/restart
	// dance and any libvirt redefinitions — they're pure bbolt writes.
	metadataOnly := (autoStartChanged || lockedChanged) && newCPUs == storedVM.Spec.CPUs && newRAMMB == storedVM.Spec.RAMMB && newDiskGB == storedVM.Spec.DiskGB && newDescription == storedVM.Description && strings.Join(newTags, ",") == strings.Join(storedVM.Tags, ",") && !ipChanged && !clockChanged && !diskBusChanged && !nicModelChanged
	if metadataOnly {
		if autoStartChanged {
			storedVM.Spec.AutoStart = newAutoStart
		}
		if lockedChanged {
			storedVM.Spec.Locked = newLocked
		}
		storedVM.UpdatedAt = time.Now()
		if err := m.store.PutVM(storedVM); err != nil {
			return nil, fmt.Errorf("storing updated VM: %w", err)
		}
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
		if err := createProvisioningISO(cloudInitISO, updatedSpec, storedVM.NatMAC, newInstanceID); err != nil {
			return nil, fmt.Errorf("regenerating provisioning ISO: %w", err)
		}
	}

	// Redefine the domain XML with updated CPU/RAM/clock offset/disk_bus/nic_model.
	if newCPUs != storedVM.Spec.CPUs || newRAMMB != storedVM.Spec.RAMMB || clockChanged || diskBusChanged || nicModelChanged {
		// Preserve the existing domain UUID so libvirt accepts the redefinition.
		existingUUID, _ := dom.GetUUIDString()

		updatedSpec := storedVM.Spec
		updatedSpec.CPUs = newCPUs
		updatedSpec.RAMMB = newRAMMB
		updatedSpec.ClockOffset = newClockOffset
		updatedSpec.DiskBus = newDiskBus
		updatedSpec.NICModel = newNICModel
		cloudInitISO := filepath.Join(filepath.Dir(storedVM.DiskPath), "cidata.iso")
		params := DomainParamsFromSpec(updatedSpec, storedVM.DiskPath, cloudInitISO, m.cfg.Network.Name, storedVM.NatMAC)
		params.UUID = existingUUID
		params.Machine = resolveMachine(updatedSpec.Machine, func() string { return detectMachineType(m.conn) })
		m.applyVirtioWin(&params, updatedSpec)
		m.applyGPUs(&params, updatedSpec)
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
	if autoStartChanged {
		storedVM.Spec.AutoStart = newAutoStart
	}
	if lockedChanged {
		storedVM.Spec.Locked = newLocked
	}
	if clockChanged {
		storedVM.Spec.ClockOffset = newClockOffset
	}
	if diskBusChanged {
		storedVM.Spec.DiskBus = newDiskBus
	}
	if nicModelChanged {
		storedVM.Spec.NICModel = newNICModel
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
func (m *LibvirtManager) CreateSnapshot(ctx context.Context, vmID string, spec types.SnapshotSpec) (*types.Snapshot, error) {
	vm, err := m.store.GetVM(vmID)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	doc := snapshotXMLDoc{Name: spec.Name, Description: spec.Description}
	snapXML, err := xml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encoding snapshot xml: %w", err)
	}
	if _, err := dom.CreateSnapshotXML(string(snapXML), 0); err != nil {
		return nil, fmt.Errorf("creating snapshot: %w", err)
	}

	// Persist tags out-of-band (libvirt's domainsnapshot schema does not
	// permit <metadata>, so tags cannot live alongside description in
	// the XML).  Failure here does not roll the libvirt snapshot back —
	// description + disk state are already on disk, and the worst case
	// is the operator re-applies the tag list via PATCH.
	if len(spec.Tags) > 0 {
		if err := m.store.PutSnapshotTags(vmID, spec.Name, spec.Tags); err != nil {
			return nil, fmt.Errorf("persisting snapshot tags: %w", err)
		}
	}

	snap := &types.Snapshot{
		ID:          fmt.Sprintf("%s/%s", vmID, spec.Name),
		VMID:        vmID,
		Name:        spec.Name,
		Description: spec.Description,
		Tags:        append([]string(nil), spec.Tags...),
		CreatedAt:   time.Now(),
	}

	return snap, nil
}

// snapshotXMLDoc is the libvirt domainsnapshot XML projection used when creating
// or parsing snapshot definitions.  Description and CreationTime are optional in
// libvirt's schema, so we only include populated fields on encode.
type snapshotXMLDoc struct {
	XMLName      xml.Name `xml:"domainsnapshot"`
	Name         string   `xml:"name"`
	Description  string   `xml:"description,omitempty"`
	CreationTime string   `xml:"creationTime,omitempty"`
}

// UpdateSnapshot edits the metadata of an existing snapshot. Only the
// description can change; libvirt has no in-place edit primitive for snapshots,
// so the implementation round-trips the existing XML through
// SnapshotCreateXML(REDEFINE) — which preserves disk/memory state, parent
// pointer, creation timestamp, and runtime state, but swaps out the
// <description> element. All other SnapshotUpdateSpec fields being nil is a
// no-op that returns the current snapshot.
func (m *LibvirtManager) UpdateSnapshot(ctx context.Context, vmID string, snapshotName string, patch types.SnapshotUpdateSpec) (*types.Snapshot, error) {
	vm, err := m.store.GetVM(vmID)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.LookupDomainByName(vm.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	snap, err := dom.SnapshotLookupByName(snapshotName, 0)
	if err != nil {
		return nil, fmt.Errorf("looking up snapshot %s: %w", snapshotName, err)
	}
	defer snap.Free()

	rawXML, err := snap.GetXMLDesc(0)
	if err != nil {
		return nil, fmt.Errorf("dumping snapshot xml: %w", err)
	}

	currentDesc, currentCreated, parseErr := parseSnapshotXML(rawXML)
	if parseErr != nil {
		return nil, fmt.Errorf("parsing snapshot xml: %w", parseErr)
	}

	desc := currentDesc
	if patch.Description != nil {
		desc = strings.TrimSpace(*patch.Description)
	}

	if patch.Description != nil && desc != currentDesc {
		newXML, err := rewriteSnapshotDescription(rawXML, desc)
		if err != nil {
			return nil, fmt.Errorf("rewriting snapshot xml: %w", err)
		}
		if _, err := dom.CreateSnapshotXML(newXML, libvirt.DOMAIN_SNAPSHOT_CREATE_REDEFINE); err != nil {
			return nil, fmt.Errorf("redefining snapshot: %w", err)
		}
	}

	// Tag mutation lives in bbolt; pointer-nil means "leave as-is", a
	// non-nil pointer (including the explicit empty slice) replaces the
	// stored set.  An empty slice removes the record entirely so List
	// no longer emits a tags array for this snapshot.
	currentTags, _ := m.store.GetSnapshotTags(vmID, snapshotName)
	tags := currentTags
	if patch.Tags != nil {
		tags = append([]string(nil), (*patch.Tags)...)
		if err := m.store.PutSnapshotTags(vmID, snapshotName, tags); err != nil {
			return nil, fmt.Errorf("persisting snapshot tags: %w", err)
		}
	}

	return &types.Snapshot{
		ID:          fmt.Sprintf("%s/%s", vmID, snapshotName),
		VMID:        vmID,
		Name:        snapshotName,
		Description: desc,
		Tags:        tags,
		CreatedAt:   currentCreated,
	}, nil
}

// rewriteSnapshotDescription returns a snapshot XML document with the
// <description> element replaced by newDesc, preserving every other element
// (creationTime, state, parent, memory, disks, …) verbatim. Used by
// LibvirtManager.UpdateSnapshot to drive a SnapshotCreateXML(REDEFINE) call
// without losing fields libvirt needs to keep the on-disk snapshot graph
// consistent.
func rewriteSnapshotDescription(raw, newDesc string) (string, error) {
	type rawSnapshotDoc struct {
		XMLName xml.Name   `xml:"domainsnapshot"`
		Attrs   []xml.Attr `xml:",any,attr"`
		Inner   []byte     `xml:",innerxml"`
	}
	var doc rawSnapshotDoc
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		return "", err
	}

	inner := string(doc.Inner)
	// Strip any existing description element(s) — libvirt schema permits at most one,
	// but we defensively remove all occurrences before re-injecting.
	for {
		start := strings.Index(inner, "<description>")
		if start == -1 {
			start = strings.Index(inner, "<description/>")
			if start == -1 {
				break
			}
			end := start + len("<description/>")
			inner = inner[:start] + inner[end:]
			continue
		}
		end := strings.Index(inner[start:], "</description>")
		if end == -1 {
			break
		}
		end = start + end + len("</description>")
		inner = inner[:start] + inner[end:]
	}

	if newDesc != "" {
		var buf strings.Builder
		buf.WriteString("<description>")
		if err := xml.EscapeText(&buf, []byte(newDesc)); err != nil {
			return "", err
		}
		buf.WriteString("</description>")
		// Inject after <name>...</name> if present, so the order matches what
		// libvirt emits on CreateSnapshotXML; otherwise prepend.
		if nameEnd := strings.Index(inner, "</name>"); nameEnd != -1 {
			pos := nameEnd + len("</name>")
			inner = inner[:pos] + buf.String() + inner[pos:]
		} else {
			inner = buf.String() + inner
		}
	}

	var out strings.Builder
	out.WriteString("<domainsnapshot")
	for _, attr := range doc.Attrs {
		out.WriteString(" ")
		if attr.Name.Space != "" {
			out.WriteString(attr.Name.Space)
			out.WriteString(":")
		}
		out.WriteString(attr.Name.Local)
		out.WriteString(`="`)
		if err := xml.EscapeText(&out, []byte(attr.Value)); err != nil {
			return "", err
		}
		out.WriteString(`"`)
	}
	out.WriteString(">")
	out.WriteString(inner)
	out.WriteString("</domainsnapshot>")
	return out.String(), nil
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

	// Pull every tag record under this VM in one cursor walk so the
	// listing path stays O(N) in libvirt round-trips instead of N+1.
	// An error here drops to an empty map — tags are nice-to-have
	// metadata, not core snapshot identity, so a corrupt bucket
	// must not break the list endpoint.
	tagsByName, _ := m.store.ListSnapshotTagsByVM(vmID)

	var result []*types.Snapshot
	for _, s := range snaps {
		name, _ := s.GetName()
		entry := &types.Snapshot{
			ID:   fmt.Sprintf("%s/%s", vmID, name),
			VMID: vmID,
			Name: name,
		}
		if rawXML, xmlErr := s.GetXMLDesc(0); xmlErr == nil {
			if desc, created, parseErr := parseSnapshotXML(rawXML); parseErr == nil {
				entry.Description = desc
				if !created.IsZero() {
					entry.CreatedAt = created
				}
			}
		}
		if tags, ok := tagsByName[name]; ok && len(tags) > 0 {
			entry.Tags = tags
		}
		result = append(result, entry)
		s.Free()
	}

	return result, nil
}

// parseSnapshotXML extracts the operator-supplied description and the libvirt
// creation timestamp from a domainsnapshot XML document.  Either field may be
// absent — callers should treat zero values as "not present".
func parseSnapshotXML(raw string) (string, time.Time, error) {
	var doc snapshotXMLDoc
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		return "", time.Time{}, err
	}
	var created time.Time
	if trimmed := strings.TrimSpace(doc.CreationTime); trimmed != "" {
		if secs, err := strconv.ParseInt(trimmed, 10, 64); err == nil && secs >= 0 {
			created = time.Unix(secs, 0).UTC()
		}
	}
	return strings.TrimSpace(doc.Description), created, nil
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

	if err := snap.Delete(0); err != nil {
		return err
	}
	// Drop the tag record once libvirt confirms the snapshot is gone.
	// A delete error here is not fatal — the libvirt snapshot is already
	// removed and the orphan record will be pruned on the next list cycle.
	_ = m.store.DeleteSnapshotTags(vmID, snapshotName)
	return nil
}

// GetConsoleEndpoint inspects the live libvirt domain XML to discover
// where the daemon's console proxy should dial for the requested intent.
// `vnc` reads the `<graphics type='vnc'>` element; `serial` reads the
// `<console type='pty'>` element (and its `<source path>` companion if
// the parent serial device carries it).  The VM must be running:
// libvirt only allocates a graphics port and pty path while the domain
// is alive, so a stopped VM returns a typed `vm_not_running` error.
func (m *LibvirtManager) GetConsoleEndpoint(ctx context.Context, id string, intent types.ConsoleIntent) (*types.ConsoleEndpoint, error) {
	if !intent.Valid() {
		return nil, types.NewAPIError("invalid_console_intent", fmt.Sprintf("unknown console intent %q", string(intent)))
	}

	storedVM, err := m.store.GetVM(id)
	if err != nil {
		return nil, err
	}

	dom, err := m.conn.LookupDomainByName(storedVM.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	if state := domainStateToVMState(dom); state != types.VMStateRunning {
		return nil, types.NewAPIError("vm_not_running", "vm is not running; start it before requesting a console endpoint")
	}

	xmlStr, err := dom.GetXMLDesc(0)
	if err != nil {
		return nil, fmt.Errorf("reading domain xml: %w", err)
	}

	endpoint, err := parseConsoleEndpointFromXML(xmlStr, intent)
	if err != nil {
		return nil, err
	}
	return endpoint, nil
}

// parseConsoleEndpointFromXML extracts a console endpoint from a
// libvirt domain XML document.  Pure-Go so it can be unit-tested
// without a real libvirt connection.
func parseConsoleEndpointFromXML(rawXML string, intent types.ConsoleIntent) (*types.ConsoleEndpoint, error) {
	type graphicsXML struct {
		Type   string `xml:"type,attr"`
		Port   string `xml:"port,attr"`
		Listen string `xml:"listen,attr"`
	}
	type ptySourceXML struct {
		Path string `xml:"path,attr"`
	}
	type consoleXML struct {
		Type   string       `xml:"type,attr"`
		TTY    string       `xml:"tty,attr"`
		Source ptySourceXML `xml:"source"`
	}
	type devicesXML struct {
		Graphics []graphicsXML `xml:"graphics"`
		Serials  []consoleXML  `xml:"serial"`
		Consoles []consoleXML  `xml:"console"`
	}
	type domainXML struct {
		Devices devicesXML `xml:"devices"`
	}

	var d domainXML
	if err := xml.Unmarshal([]byte(rawXML), &d); err != nil {
		return nil, fmt.Errorf("parsing domain xml: %w", err)
	}

	switch intent {
	case types.ConsoleIntentVNC:
		for _, g := range d.Devices.Graphics {
			if !strings.EqualFold(g.Type, "vnc") {
				continue
			}
			port, perr := strconv.Atoi(strings.TrimSpace(g.Port))
			if perr != nil || port <= 0 {
				// libvirt records "-1" before a domain is started
				// or `autoport='yes'` has been resolved.
				return nil, types.NewAPIError("console_unavailable", "vnc port has not been assigned yet")
			}
			host := strings.TrimSpace(g.Listen)
			if host == "" {
				host = "127.0.0.1"
			}
			return &types.ConsoleEndpoint{
				Intent: types.ConsoleIntentVNC,
				Host:   host,
				Port:   port,
			}, nil
		}
		return nil, types.NewAPIError("console_unavailable", "domain has no vnc graphics device")
	case types.ConsoleIntentSerial:
		// Prefer the `<console>` element because that is what libvirt
		// fills in with the live `tty=` attribute; fall back to a
		// `<serial>` element with a `<source path>` for older XML.
		candidates := append([]consoleXML{}, d.Devices.Consoles...)
		candidates = append(candidates, d.Devices.Serials...)
		for _, c := range candidates {
			if !strings.EqualFold(c.Type, "pty") {
				continue
			}
			path := strings.TrimSpace(c.TTY)
			if path == "" {
				path = strings.TrimSpace(c.Source.Path)
			}
			if path == "" {
				continue
			}
			return &types.ConsoleEndpoint{
				Intent: types.ConsoleIntentSerial,
				Path:   path,
			}, nil
		}
		return nil, types.NewAPIError("console_unavailable", "domain has no serial pty endpoint yet")
	}
	return nil, types.NewAPIError("invalid_console_intent", fmt.Sprintf("unknown console intent %q", string(intent)))
}

// --- helpers ---

func cloneVMSpec(source types.VMSpec, newName string) types.VMSpec {
	cloned := source
	cloned.Name = newName
	cloned.NatStaticIP = ""
	cloned.NatGateway = ""
	cloned.GPUs = nil
	cloned.Tags = append([]string(nil), source.Tags...)
	cloned.Networks = append([]types.NetworkAttachment(nil), source.Networks...)
	for i := range cloned.Networks {
		cloned.Networks[i].MacAddress = generateMAC()
		cloned.Networks[i].StaticIP = ""
		cloned.Networks[i].Gateway = ""
	}
	return cloned
}

func createClonedDisk(sourceDiskPath, diskPath string) error {
	return createClonedDiskWithProgress(sourceDiskPath, diskPath, nil)
}

// createClonedDiskWithProgress copies a VM disk via qemu-img convert. When
// progress is non-nil it adds `-p` and stream-parses the completion percentage
// so callers can surface a live clone progress bar.
func createClonedDiskWithProgress(sourceDiskPath, diskPath string, progress func(percent float64)) error {
	args := []string{"convert", "-f", "qcow2", "-O", "qcow2"}
	if progress != nil {
		args = append(args, "-p")
	}
	args = append(args, sourceDiskPath, diskPath)
	cmd := exec.Command("qemu-img", args...)

	if progress == nil {
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("qemu-img convert: %s: %w", string(out), err)
		}
		return nil
	}
	return runQemuConvertWithProgress(cmd, progress)
}

// qemuProgressRE matches qemu-img's `-p` progress tokens, e.g. "(73.45/100%)".
var qemuProgressRE = regexp.MustCompile(`\(\s*([0-9]+(?:\.[0-9]+)?)/100%\)`)

// runQemuConvertWithProgress runs a qemu-img command, streaming its stdout
// (carriage-return-updated when `-p` is set) and invoking progress with each
// parsed percentage. Stderr is captured separately for a useful error message.
func runQemuConvertWithProgress(cmd *exec.Cmd, progress func(percent float64)) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("qemu-img convert: stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("qemu-img convert: start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Split(scanLinesCR)
	for scanner.Scan() {
		if matches := qemuProgressRE.FindStringSubmatch(scanner.Text()); matches != nil {
			if pct, perr := strconv.ParseFloat(matches[1], 64); perr == nil {
				progress(pct)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("qemu-img convert: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return nil
}

// scanLinesCR is a bufio.SplitFunc that breaks on either a newline or a
// carriage return, so qemu-img's `\r`-updated progress line yields one token
// per update rather than a single buffered blob at EOF.
func scanLinesCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

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

// virtioWinISOPath resolves the virtio-win driver ISO to attach to Windows
// guests. It prefers the explicitly-configured storage.virtio_win_iso path and
// falls back to the conventional package install location. Returns "" when no
// usable ISO is found, in which case the guest boots without it (SATA disk +
// e1000e NIC still work natively).
func (m *LibvirtManager) virtioWinISOPath() string {
	if p := strings.TrimSpace(m.cfg.Storage.VirtioWinISO); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		logger.Warn("daemon", "configured virtio_win_iso not found; skipping attachment",
			"path", p)
		return ""
	}
	if _, err := os.Stat(config.DefaultVirtioWinISOPath); err == nil {
		return config.DefaultVirtioWinISOPath
	}
	return ""
}

// virtioWinISOPathForSpec extends virtioWinISOPath with the per-VM override
// added in roadmap 5.6.15. An explicit spec.VirtioWinISO wins over both
// the daemon config and the auto-probe so an operator can pin a different
// driver bundle (e.g. a newer Fedora virtio-win snapshot, or a downgraded
// stable build) for a single Windows VM without touching the daemon
// config. Missing override files are logged + skipped (matching the
// daemon-config behaviour); the resolver falls back to the daemon-wide
// path so the guest still gets a virtio ISO when one is available.
func (m *LibvirtManager) virtioWinISOPathForSpec(spec types.VMSpec) string {
	if override := strings.TrimSpace(spec.VirtioWinISO); override != "" {
		if _, err := os.Stat(override); err == nil {
			return override
		}
		logger.Warn("daemon", "per-VM virtio_win_iso override not found; falling back to daemon config",
			"path", override, "vm", spec.Name)
	}
	return m.virtioWinISOPath()
}

// applyVirtioWin attaches the virtio-win driver ISO to the domain params when
// the spec targets Windows and an ISO is available. No-op for Linux guests.
func (m *LibvirtManager) applyVirtioWin(params *DomainParams, spec types.VMSpec) {
	if !spec.IsWindows() {
		return
	}
	if iso := m.virtioWinISOPathForSpec(spec); iso != "" {
		params.VirtioWinISO = iso
	}
}

// applyGPUs expands the spec's requested passthrough GPUs to their full IOMMU
// groups (so a GPU's companion functions — typically its HDMI audio device —
// are assigned together, as VFIO requires) and sets the resulting PCI
// addresses on the domain params. The pure DomainParamsFromSpec path already
// populated params.GPUAddresses with the unexpanded request; we overwrite it
// here with the host-resolved group membership. No-op when no GPUs requested.
func (m *LibvirtManager) applyGPUs(params *DomainParams, spec types.VMSpec) {
	requested := spec.ResolvedGPUs()
	if len(requested) == 0 {
		return
	}
	expanded := host.ExpandIOMMUGroups(requested)
	params.GPUAddresses = expanded
	logger.Info("daemon", "attaching GPU passthrough devices",
		"vm", spec.Name, "requested", strings.Join(requested, ","), "attached", strings.Join(expanded, ","))
}

// createProvisioningISO writes the first-boot datasource ISO for a VM. Linux
// guests get a cloud-init NoCloud ISO; Windows guests get a cloudbase-init
// NoCloud ISO. A custom CloudInitFile, when set, overrides the generated
// user-data for either family.
func createProvisioningISO(isoPath string, spec types.VMSpec, natMAC, instanceID string) error {
	if spec.IsWindows() {
		return createWindowsProvisioningISO(isoPath, spec, instanceID)
	}
	return createCloudInitISO(isoPath, spec, natMAC, instanceID)
}

// createWindowsProvisioningISO writes a NoCloud datasource ISO consumed by
// cloudbase-init inside a prepared Windows guest image. It carries meta-data
// (instance-id + hostname, plus the Administrator password) and user-data (a
// #ps1_sysnative PowerShell script that sets the Administrator password,
// enables Remote Desktop, and installs/enables the OpenSSH server with the
// injected public key). The ISO volume label is "cidata", which
// cloudbase-init's NoCloud service matches by default.
func createWindowsProvisioningISO(isoPath string, spec types.VMSpec, instanceID string) error {
	if instanceID == "" {
		instanceID = spec.Name
	}

	tmpDir, err := os.MkdirTemp("", "vmsmith-win-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// meta-data: cloudbase-init reads instance-id (re-runs when it changes),
	// local-hostname (renames the computer), and admin_pass (sets the
	// Administrator password via the SetUserPasswordPlugin).
	var meta strings.Builder
	meta.WriteString(fmt.Sprintf("instance-id: %s\n", instanceID))
	meta.WriteString(fmt.Sprintf("local-hostname: %s\n", spec.Name))
	if spec.AdminPassword != "" {
		meta.WriteString(fmt.Sprintf("admin_pass: %s\n", spec.AdminPassword))
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "meta-data"), []byte(meta.String()), 0644); err != nil {
		return err
	}

	var userData string
	if spec.CloudInitFile != "" {
		custom, err := os.ReadFile(spec.CloudInitFile)
		if err != nil {
			return fmt.Errorf("reading cloud-init file: %w", err)
		}
		userData = string(custom)
	} else {
		userData = buildWindowsUserData(spec)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "user-data"), []byte(userData), 0644); err != nil {
		return err
	}

	return writeNoCloudISO(isoPath, []string{
		filepath.Join(tmpDir, "meta-data"),
		filepath.Join(tmpDir, "user-data"),
	})
}

// buildWindowsUserData returns a cloudbase-init #ps1_sysnative PowerShell
// user-data script. cloudbase-init's UserDataPlugin executes user-data that
// begins with the #ps1_sysnative header as a PowerShell script. The script is
// idempotent and self-contained so it works even on images whose
// cloudbase-init only wires up the UserDataPlugin: it sets the Administrator
// password, enables Remote Desktop (plus the firewall rule), and — when an SSH
// public key is supplied — installs and enables the Windows OpenSSH server and
// authorises the key for administrators.
func buildWindowsUserData(spec types.VMSpec) string {
	var sb strings.Builder
	sb.WriteString("#ps1_sysnative\n")
	sb.WriteString("$ErrorActionPreference = 'Continue'\n")

	if spec.AdminPassword != "" {
		sb.WriteString("# Set the local Administrator password.\n")
		sb.WriteString(fmt.Sprintf("$pw = ConvertTo-SecureString '%s' -AsPlainText -Force\n", psSingleQuote(spec.AdminPassword)))
		sb.WriteString("try { Get-LocalUser -Name 'Administrator' | Set-LocalUser -Password $pw } catch { net user Administrator '" + psSingleQuote(spec.AdminPassword) + "' }\n")
		sb.WriteString("try { Get-LocalUser -Name 'Administrator' | Enable-LocalUser } catch {}\n")
	}

	sb.WriteString("# Enable Remote Desktop and open the firewall.\n")
	sb.WriteString("Set-ItemProperty -Path 'HKLM:\\System\\CurrentControlSet\\Control\\Terminal Server' -Name 'fDenyTSConnections' -Value 0 -ErrorAction SilentlyContinue\n")
	sb.WriteString("Enable-NetFirewallRule -DisplayGroup 'Remote Desktop' -ErrorAction SilentlyContinue\n")

	if spec.SSHPubKey != "" {
		sb.WriteString("# Install and enable the OpenSSH server, then authorise the injected key.\n")
		sb.WriteString("try { Add-WindowsCapability -Online -Name 'OpenSSH.Server~~~~0.0.1.0' -ErrorAction SilentlyContinue } catch {}\n")
		sb.WriteString("Set-Service -Name sshd -StartupType Automatic -ErrorAction SilentlyContinue\n")
		sb.WriteString("Start-Service sshd -ErrorAction SilentlyContinue\n")
		sb.WriteString("$keyPath = \"$env:ProgramData\\ssh\\administrators_authorized_keys\"\n")
		sb.WriteString(fmt.Sprintf("Set-Content -Path $keyPath -Value '%s' -Encoding ascii\n", psSingleQuote(spec.SSHPubKey)))
		sb.WriteString("icacls $keyPath /inheritance:r /grant 'Administrators:F' /grant 'SYSTEM:F' | Out-Null\n")
	}

	return sb.String()
}

// psSingleQuote escapes a value for inclusion inside a PowerShell single-quoted
// string literal: the only metacharacter is the single quote itself, which is
// doubled.
func psSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
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
	return writeNoCloudISO(isoPath, []string{
		filepath.Join(tmpDir, "meta-data"),
		filepath.Join(tmpDir, "user-data"),
		filepath.Join(tmpDir, "network-config"),
	})
}

// writeNoCloudISO builds a NoCloud datasource ISO (volume label "cidata")
// containing the given files. The "cidata" label is what both cloud-init
// (Linux) and cloudbase-init (Windows) match by default.
// Tries genisoimage first, then falls back to mkisofs (available on Rocky/RHEL).
func writeNoCloudISO(isoPath string, files []string) error {
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
		return types.VMStatePaused
	default:
		return types.VMStateUnknown
	}
}

// resolveMachine honours an operator's per-VM machine override (5.6.15) when
// set, and only falls back to the libvirt-capability-derived default when the
// spec leaves it blank. Without this gate every lifecycle entry point would
// silently overwrite the override with detectMachineType.
func resolveMachine(specMachine string, fallback func() string) string {
	if v := strings.TrimSpace(specMachine); v != "" {
		return v
	}
	return fallback()
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
