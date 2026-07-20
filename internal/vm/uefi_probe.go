package vm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// Host capability probes for the UEFI / Secure Boot / vTPM path (roadmap
// 5.6.9). Both probes are variables so tests can stub them without a host
// that actually carries swtpm/OVMF.

// probeSwtpm verifies the swtpm binary libvirt needs for the emulated TPM
// backend is present on the host.
var probeSwtpm = func() error {
	if _, err := exec.LookPath("swtpm"); err != nil {
		return types.NewAPIError("swtpm_missing",
			"emulated TPM requested but swtpm is not installed on the host; "+
				"install it (e.g. apt install swtpm / dnf install swtpm) or disable the TPM with \"tpm\": false")
	}
	return nil
}

// ovmfSearchDirs are the locations distros ship OVMF firmware images in.
var ovmfSearchDirs = []string{
	"/usr/share/OVMF",
	"/usr/share/edk2/ovmf",
	"/usr/share/edk2-ovmf",
	"/usr/share/edk2/x64",
	"/usr/share/qemu/edk2-x86_64",
}

// probeOVMF verifies an OVMF firmware build is installed; with secureBoot
// it additionally requires a secboot build (the code image distros ship
// with the Microsoft keys enrolled).
var probeOVMF = func(secureBoot bool) error {
	found := false
	secbootFound := false
	for _, dir := range ovmfSearchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			if !strings.Contains(name, "code") || !(strings.HasSuffix(name, ".fd") || strings.HasSuffix(name, ".qcow2")) {
				continue
			}
			found = true
			if strings.Contains(name, "secboot") || strings.Contains(name, "secure") || strings.Contains(name, "ms") {
				secbootFound = true
			}
		}
	}
	if !found {
		return types.NewAPIError("ovmf_missing",
			"UEFI firmware requested but no OVMF build was found on the host; "+
				"install it (e.g. apt install ovmf / dnf install edk2-ovmf)")
	}
	if secureBoot && !secbootFound {
		return types.NewAPIError("ovmf_missing",
			"Secure Boot requested but no secboot OVMF build was found on the host; "+
				"install one (e.g. apt install ovmf / dnf install edk2-ovmf) or disable it with \"secure_boot\": false")
	}
	return nil
}

// probeInstallISO verifies the unattended-install ISO exists on the daemon
// host (roadmap 5.6.11).
var probeInstallISO = func(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return types.NewAPIError("invalid_install_iso",
			"install_iso path "+filepath.Clean(path)+" does not exist on the daemon host")
	}
	return nil
}

// probeUEFIRequirements runs every host probe the spec's firmware / TPM /
// install selections require. Called from Create before any resources are
// allocated so failures surface as clean 4xx-mapped typed errors.
func probeUEFIRequirements(spec types.VMSpec) error {
	if spec.ResolvedTPM() {
		if err := probeSwtpm(); err != nil {
			return err
		}
	}
	if spec.ResolvedFirmwareAttr() == "efi" {
		if err := probeOVMF(spec.ResolvedSecureBoot()); err != nil {
			return err
		}
	}
	if spec.InstallISO != "" {
		if err := probeInstallISO(spec.InstallISO); err != nil {
			return err
		}
	}
	return nil
}
