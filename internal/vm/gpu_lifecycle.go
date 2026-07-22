package vm

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/vmsmith/vmsmith/internal/host"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

// Post-create GPU passthrough lifecycle (roadmap 5.7.10).
//
// Attach and detach both update the VM's stored spec and redefine the
// persistent domain XML (UUID preserved), so the change applies at the
// next power cycle. Attaching to a RUNNING VM additionally requires
// force=true, which live-attaches the device via virDomainAttachDevice —
// risky, because vfio-pci rebinding while the host driver holds the
// device can wedge either driver; the guest typically also needs a reboot
// to initialise the GPU. Detach on a running VM is persistent-config only
// (no live detach — the riskiest operation of all — is attempted).

// AttachGPU adds a host GPU (by PCI address, long or short form) to the
// VM's passthrough set.
func (m *LibvirtManager) AttachGPU(ctx context.Context, id string, pciAddr string, force bool) (*types.VM, error) {
	norm := types.NormalizePCIAddress(pciAddr)
	if norm == "" {
		return nil, types.NewAPIError("invalid_gpu", fmt.Sprintf("%q is not a valid PCI address (want 0000:01:00.0 or 01:00.0)", pciAddr))
	}

	storedVM, err := m.store.GetVM(id)
	if err != nil {
		return nil, err
	}
	for _, existing := range storedVM.Spec.ResolvedGPUs() {
		if existing == norm {
			return nil, types.NewAPIError("gpu_already_attached", fmt.Sprintf("gpu %s is already attached to this vm", norm))
		}
	}

	dom, err := m.conn.LookupDomainByName(storedVM.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()
	running := domainStateToVMState(dom) == types.VMStateRunning

	if running && !force {
		return nil, types.NewAPIError("vm_running",
			"vm is running; stop it first, or pass force to live-attach (risky — vfio rebinding can wedge the host driver, and the guest needs a reboot to initialise the device)")
	}

	newSpec := storedVM.Spec
	newSpec.GPUs = append(append([]string(nil), newSpec.ResolvedGPUs()...), norm)

	if err := m.redefineWithGPUs(dom, storedVM, newSpec); err != nil {
		return nil, err
	}

	if running && force {
		// Live-attach the requested GPU plus its IOMMU-group companions.
		for _, addr := range host.ExpandIOMMUGroups([]string{norm}) {
			hostdev, herr := gpuHostdevXML(addr)
			if herr != nil {
				return nil, herr
			}
			if aerr := dom.AttachDeviceFlags(hostdev, libvirt.DOMAIN_DEVICE_MODIFY_LIVE); aerr != nil {
				return nil, fmt.Errorf("live-attaching %s: %w", addr, aerr)
			}
		}
		logger.Warn("daemon", "GPU live-attached to running vm; guest reboot typically required",
			"vm", storedVM.Name, "gpu", norm)
	}

	storedVM.Spec.GPUs = newSpec.GPUs
	storedVM.UpdatedAt = time.Now()
	if err := m.store.PutVM(storedVM); err != nil {
		return nil, fmt.Errorf("persisting vm: %w", err)
	}

	logger.Info("daemon", "GPU attached", "vm", storedVM.Name, "gpu", norm,
		"applied", map[bool]string{true: "live", false: "next-boot"}[running && force])
	return storedVM, nil
}

// DetachGPU removes a host GPU from the VM's passthrough set. The change
// is persistent-config only: a running VM keeps the device until its next
// power cycle (no live detach is attempted).
func (m *LibvirtManager) DetachGPU(ctx context.Context, id string, pciAddr string) (*types.VM, error) {
	norm := types.NormalizePCIAddress(pciAddr)
	if norm == "" {
		return nil, types.NewAPIError("invalid_gpu", fmt.Sprintf("%q is not a valid PCI address (want 0000:01:00.0 or 01:00.0)", pciAddr))
	}

	storedVM, err := m.store.GetVM(id)
	if err != nil {
		return nil, err
	}

	current := storedVM.Spec.ResolvedGPUs()
	var remaining []string
	found := false
	for _, existing := range current {
		if existing == norm {
			found = true
			continue
		}
		remaining = append(remaining, existing)
	}
	if !found {
		return nil, types.NewAPIError("gpu_not_attached", fmt.Sprintf("gpu %s is not attached to this vm", norm))
	}

	dom, err := m.conn.LookupDomainByName(storedVM.Name)
	if err != nil {
		return nil, fmt.Errorf("looking up domain: %w", err)
	}
	defer dom.Free()

	newSpec := storedVM.Spec
	newSpec.GPUs = remaining

	if err := m.redefineWithGPUs(dom, storedVM, newSpec); err != nil {
		return nil, err
	}

	storedVM.Spec.GPUs = remaining
	storedVM.UpdatedAt = time.Now()
	if err := m.store.PutVM(storedVM); err != nil {
		return nil, fmt.Errorf("persisting vm: %w", err)
	}

	if domainStateToVMState(dom) == types.VMStateRunning {
		logger.Warn("daemon", "GPU detached from persistent config of a running vm; the device stays attached until the next power cycle",
			"vm", storedVM.Name, "gpu", norm)
	}
	logger.Info("daemon", "GPU detached", "vm", storedVM.Name, "gpu", norm)
	return storedVM, nil
}

// redefineWithGPUs re-renders the domain XML from the stored VM with the
// given spec (which carries the new GPU set) and redefines it in libvirt,
// preserving the existing UUID and the injected VNC password.
func (m *LibvirtManager) redefineWithGPUs(dom *libvirt.Domain, storedVM *types.VM, newSpec types.VMSpec) error {
	existingUUID, _ := dom.GetUUIDString()

	cloudInitISO := filepath.Join(filepath.Dir(storedVM.DiskPath), "cidata.iso")
	params := DomainParamsFromSpec(newSpec, storedVM.DiskPath, cloudInitISO, m.cfg.Network.Name, storedVM.NatMAC)
	params.UUID = existingUUID
	params.Machine = resolveMachine(newSpec.Machine, func() string { return detectMachineType(m.conn) })
	m.applyVirtioWin(&params, newSpec)
	m.applyGPUs(&params, newSpec)
	if storedVM.VNCPasswordEnc != "" {
		plain, derr := decryptVNCPassword(m.cfg.Daemon.Console.PasswordKey, storedVM.VNCPasswordEnc)
		if derr != nil {
			return types.NewAPIError("vnc_password_undecryptable", "stored vnc password cannot be decrypted; check daemon.console.password_key or rotate the password while the vm is stopped")
		}
		params.SetVNCPassword(plain)
	}

	xmlDoc, err := GenerateDomainXML(params)
	if err != nil {
		return fmt.Errorf("generating domain XML: %w", err)
	}
	if _, err := m.conn.DomainDefineXML(xmlDoc); err != nil {
		return fmt.Errorf("redefining domain: %w", err)
	}
	return nil
}
