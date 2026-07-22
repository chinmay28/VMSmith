package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// OVA import/export commands (roadmap 5.3).

var vmExportCmd = &cobra.Command{
	Use:   "export <vm-id>",
	Short: "Export a stopped VM as an OVA appliance",
	Long: `Export a stopped VM as a single-file OVA (OVF descriptor +
streamOptimized VMDK disk + SHA256 manifest) for use with other
virtualization platforms.

The VM must be stopped so the disk is quiescent. The qcow2 disk (including
its backing chain) is flattened during conversion.

Example:
  vmsmith vm stop vm-12345
  vmsmith vm export vm-12345 --output /tmp/myvm.ova`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		output, _ := cmd.Flags().GetString("output")

		logger.Info("cli", "vm export", "id", vmID, "output", output)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		storageMgr, storageCleanup, err := newStorageManager()
		if err != nil {
			return err
		}
		defer storageCleanup()

		machine, err := mgr.Get(context.Background(), vmID)
		if err != nil {
			return fmt.Errorf("looking up VM: %w", err)
		}
		if machine.State != types.VMStateStopped {
			return fmt.Errorf("vm %s is %s; stop it before exporting (vmsmith vm stop %s)", vmID, machine.State, vmID)
		}

		if output == "" {
			output = machine.Name + ".ova"
		}
		absOutput, err := filepath.Abs(output)
		if err != nil {
			return err
		}

		fmt.Printf("Exporting %s (%s) to %s...\n", machine.Name, vmID, absOutput)
		lastPercent := -10.0
		progress := func(p float64) {
			if p-lastPercent >= 10 || p >= 100 {
				fmt.Printf("  converting disk: %.0f%%\n", p)
				lastPercent = p
			}
		}
		if err := storageMgr.ExportOVA(machine, absOutput, progress); err != nil {
			return fmt.Errorf("exporting OVA: %w", err)
		}

		fmt.Printf("Exported %s\n", absOutput)
		return nil
	},
}

var vmImportCmd = &cobra.Command{
	Use:   "import <path.ova|path.ovf>",
	Short: "Import a VM from an OVA/OVF appliance",
	Long: `Import a VM from an OVA archive (or a bare OVF descriptor whose disk
sits alongside it). The appliance disk is converted to qcow2 and registered
as a VMSmith image, and a VM is created with the descriptor's CPU / RAM /
disk sizing (daemon defaults fill any gaps).

Examples:
  vmsmith vm import appliance.ova
  vmsmith vm import appliance.ova --name imported-vm --ssh-key "$(cat ~/.ssh/id_ed25519.pub)"
  vmsmith vm import exported/machine.ovf --name from-ovf`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		name, _ := cmd.Flags().GetString("name")
		imageName, _ := cmd.Flags().GetString("image-name")
		sshKey, _ := cmd.Flags().GetString("ssh-key")
		defaultUser, _ := cmd.Flags().GetString("default-user")

		logger.Info("cli", "vm import", "path", path, "name", name)

		storageMgr, storageCleanup, err := newStorageManager()
		if err != nil {
			return err
		}
		defer storageCleanup()

		if imageName == "" {
			if name != "" {
				imageName = name + "-ova"
			} else {
				stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				imageName = stem + "-ova"
			}
		}

		fmt.Printf("Importing %s (image %q)...\n", path, imageName)
		result, err := storageMgr.ImportOVA(path, imageName)
		if err != nil {
			return fmt.Errorf("importing appliance: %w", err)
		}

		if name == "" {
			name = result.Name
		}
		if name == "" {
			return fmt.Errorf("descriptor carries no VM name; pass --name")
		}

		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		spec := types.VMSpec{
			Name:        name,
			Image:       result.Image.Name,
			CPUs:        result.CPUs,
			RAMMB:       result.RAMMB,
			DiskGB:      result.DiskGB,
			SSHPubKey:   strings.TrimSpace(sshKey),
			DefaultUser: strings.TrimSpace(defaultUser),
			Description: fmt.Sprintf("Imported from %s", filepath.Base(path)),
		}

		machine, err := mgr.Create(context.Background(), spec)
		if err != nil {
			return fmt.Errorf("creating VM from appliance: %w", err)
		}

		fmt.Printf("Imported VM %s (%s)\n", machine.Name, machine.ID)
		fmt.Printf("  Image: %s\n", result.Image.Name)
		fmt.Printf("  CPUs: %d  RAM: %d MB  Disk: %d GB\n", machine.Spec.CPUs, machine.Spec.RAMMB, machine.Spec.DiskGB)
		return nil
	},
}

func init() {
	vmExportCmd.Flags().String("output", "", "output path for the OVA (default: <vm-name>.ova)")

	vmImportCmd.Flags().String("name", "", "name for the imported VM (default: appliance name from the descriptor)")
	vmImportCmd.Flags().String("image-name", "", "name for the imported base image (default: <name>-ova)")
	vmImportCmd.Flags().String("ssh-key", "", "SSH public key content to inject into the imported VM")
	vmImportCmd.Flags().String("default-user", "", "create this sudo user instead of enabling root SSH")

	vmCmd.AddCommand(vmExportCmd)
	vmCmd.AddCommand(vmImportCmd)
}
