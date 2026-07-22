package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
)

// Post-create GPU passthrough lifecycle commands (roadmap 5.7.10).

var vmGPUCmd = &cobra.Command{
	Use:   "gpu",
	Short: "Manage a VM's GPU passthrough assignment post-create",
}

var vmGPUAttachCmd = &cobra.Command{
	Use:   "attach <vm-id> <pci-addr>",
	Short: "Attach a host GPU to an existing VM",
	Long: `Attach a host GPU (by PCI address, e.g. 0000:01:00.0 or 01:00.0) to an
existing VM's passthrough set. The persistent domain config is updated, so
the change applies at the VM's next power cycle.

Attaching to a RUNNING VM requires --force-attach, which live-attaches the
device. This is risky: rebinding the device to vfio-pci while a host driver
holds it can wedge either driver, and the guest typically needs a reboot to
initialise the GPU anyway. Prefer stopping the VM first.

Discover assignable GPUs with: vmsmith host gpus`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID, addr := args[0], args[1]
		force, _ := cmd.Flags().GetBool("force-attach")

		logger.Info("cli", "vm gpu attach", "id", vmID, "gpu", addr, "force", fmt.Sprintf("%t", force))

		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		vm, err := mgr.AttachGPU(context.Background(), vmID, addr, force)
		if err != nil {
			return fmt.Errorf("attaching gpu: %w", err)
		}

		fmt.Printf("GPU %s attached to %s (%s)\n", addr, vm.Name, vm.ID)
		fmt.Printf("  GPUs: %s\n", strings.Join(vm.Spec.ResolvedGPUs(), ", "))
		if vm.State == "running" && force {
			fmt.Println("  Note: live-attached to a running VM — reboot the guest to initialise the device.")
		} else if vm.State == "running" {
			fmt.Println("  Note: the change applies at the next power cycle.")
		}
		return nil
	},
}

var vmGPUDetachCmd = &cobra.Command{
	Use:   "detach <vm-id> <pci-addr>",
	Short: "Detach a host GPU from an existing VM",
	Long: `Remove a host GPU from an existing VM's passthrough set. The persistent
domain config is updated; a running VM keeps the device until its next
power cycle (no live detach is attempted).`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID, addr := args[0], args[1]

		logger.Info("cli", "vm gpu detach", "id", vmID, "gpu", addr)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		vm, err := mgr.DetachGPU(context.Background(), vmID, addr)
		if err != nil {
			return fmt.Errorf("detaching gpu: %w", err)
		}

		fmt.Printf("GPU %s detached from %s (%s)\n", addr, vm.Name, vm.ID)
		if gpus := vm.Spec.ResolvedGPUs(); len(gpus) > 0 {
			fmt.Printf("  Remaining GPUs: %s\n", strings.Join(gpus, ", "))
		} else {
			fmt.Println("  No GPUs remain attached.")
		}
		if vm.State == "running" {
			fmt.Println("  Note: the running VM keeps the device until its next power cycle.")
		}
		return nil
	},
}

func init() {
	vmGPUAttachCmd.Flags().Bool("force-attach", false, "live-attach to a running VM (risky: vfio rebinding mid-flight can wedge the host driver)")

	vmGPUCmd.AddCommand(vmGPUAttachCmd)
	vmGPUCmd.AddCommand(vmGPUDetachCmd)
	vmCmd.AddCommand(vmGPUCmd)
}
