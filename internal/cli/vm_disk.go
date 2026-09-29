package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/vm"
)

var vmMoveDiskCmd = &cobra.Command{
	Use:   "move-disk <vm-id> <location>",
	Short: "Move a stopped VM's disk to another storage location",
	Long: `Move a VM's disk directory (system disk + provisioning ISO, with any
internal snapshots) to another configured storage location — e.g. to free
space on a physical drive.

<location> is 'default' (storage.base_dir), a name declared under
storage.disk_locations in the daemon config, or the absolute path of an
existing directory under storage.disk_location_roots (by default /mnt,
/media, /var, /srv, /opt, /data and /home — e.g. /mnt/nvme or
/media/alice/USB). System directories (/etc, /usr, /proc, /run, ...) are
always refused. List locations, mounted drives and their free space with:
vmsmith host storage

The VM must be stopped. A move within one filesystem is an instant rename;
across filesystems the files are copied, verified on disk, and the source
is removed only after the VM definition points at the new copy.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID, location := args[0], args[1]
		logger.Info("cli", "vm move-disk", "id", vmID, "location", location)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		lastPct := -1
		ctx := vm.WithDiskMoveProgress(context.Background(), func(p float64) {
			if pct := int(p); pct != lastPct {
				lastPct = pct
				fmt.Fprintf(os.Stderr, "\rCopying disk... %3d%%", pct)
			}
		})
		moved, err := mgr.MoveDisk(ctx, vmID, location)
		if lastPct >= 0 {
			fmt.Fprintln(os.Stderr)
		}
		if err != nil {
			return fmt.Errorf("moving disk: %w", err)
		}

		fmt.Printf("Disk of %s (%s) moved to location %q\n", moved.Name, moved.ID, location)
		fmt.Printf("  Disk Path: %s\n", moved.DiskPath)
		return nil
	},
}

func init() {
	vmCmd.AddCommand(vmMoveDiskCmd)
}
