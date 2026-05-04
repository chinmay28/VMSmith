package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// snapshotDescriptionMaxLen mirrors the API-side cap on description length so
// the in-process CLI path rejects oversized values before reaching the manager.
const snapshotDescriptionMaxLen = 1024

var snapshotCmd = &cobra.Command{
	Use:     "snapshot",
	Short:   "Manage VM snapshots",
	Aliases: []string{"snap"},
}

var snapCreateCmd = &cobra.Command{
	Use:   "create <vm-id>",
	Short: "Create a snapshot of a VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		name, _ := cmd.Flags().GetString("name")
		description, _ := cmd.Flags().GetString("description")
		if len(description) > snapshotDescriptionMaxLen {
			return fmt.Errorf("description must be at most %d characters", snapshotDescriptionMaxLen)
		}
		logger.Info("cli", "snapshot create", "vm_id", vmID, "name", name)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "snapshot create: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		snap, err := mgr.CreateSnapshot(context.Background(), vmID, types.SnapshotSpec{Name: name, Description: description})
		if err != nil {
			logger.Error("cli", "snapshot create failed", "vm_id", vmID, "name", name, "error", err.Error())
			return err
		}
		logger.Info("cli", "snapshot created", "vm_id", vmID, "snap_name", snap.Name)
		fmt.Printf("Snapshot created: %s\n", snap.Name)
		if snap.Description != "" {
			fmt.Printf("Description: %s\n", snap.Description)
		}
		return nil
	},
}

var snapRestoreCmd = &cobra.Command{
	Use:   "restore <vm-id>",
	Short: "Restore a VM to a snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		name, _ := cmd.Flags().GetString("name")
		logger.Info("cli", "snapshot restore", "vm_id", vmID, "name", name)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "snapshot restore: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		if err := mgr.RestoreSnapshot(context.Background(), vmID, name); err != nil {
			logger.Error("cli", "snapshot restore failed", "vm_id", vmID, "name", name, "error", err.Error())
			return err
		}
		logger.Info("cli", "snapshot restored", "vm_id", vmID, "name", name)
		fmt.Printf("VM %s restored to snapshot %s\n", vmID, name)
		return nil
	},
}

var snapListCmd = &cobra.Command{
	Use:     "list <vm-id>",
	Short:   "List snapshots for a VM",
	Aliases: []string{"ls"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		logger.Info("cli", "snapshot list", "vm_id", vmID)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "snapshot list: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		snaps, err := mgr.ListSnapshots(context.Background(), vmID)
		if err != nil {
			logger.Error("cli", "snapshot list failed", "vm_id", vmID, "error", err.Error())
			return err
		}
		logger.Info("cli", "snapshot list result", "vm_id", vmID, "count", fmt.Sprintf("%d", len(snaps)))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tCREATED\tDESCRIPTION")
		for _, s := range snaps {
			created := ""
			if !s.CreatedAt.IsZero() {
				created = s.CreatedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, created, s.Description)
		}
		w.Flush()
		return nil
	},
}

var snapDeleteCmd = &cobra.Command{
	Use:   "delete <vm-id>",
	Short: "Delete a snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		name, _ := cmd.Flags().GetString("name")
		logger.Info("cli", "snapshot delete", "vm_id", vmID, "name", name)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "snapshot delete: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		if err := mgr.DeleteSnapshot(context.Background(), vmID, name); err != nil {
			logger.Error("cli", "snapshot delete failed", "vm_id", vmID, "name", name, "error", err.Error())
			return err
		}
		logger.Info("cli", "snapshot deleted", "vm_id", vmID, "name", name)
		fmt.Printf("Snapshot %s deleted\n", name)
		return nil
	},
}

func init() {
	snapCreateCmd.Flags().String("name", "", "snapshot name (required)")
	snapCreateCmd.MarkFlagRequired("name")
	snapCreateCmd.Flags().String("description", "", "free-text description for the snapshot (optional, max 1024 chars)")

	snapRestoreCmd.Flags().String("name", "", "snapshot name to restore (required)")
	snapRestoreCmd.MarkFlagRequired("name")

	snapDeleteCmd.Flags().String("name", "", "snapshot name to delete (required)")
	snapDeleteCmd.MarkFlagRequired("name")

	snapshotCmd.AddCommand(snapCreateCmd)
	snapshotCmd.AddCommand(snapRestoreCmd)
	snapshotCmd.AddCommand(snapListCmd)
	snapshotCmd.AddCommand(snapDeleteCmd)
}
