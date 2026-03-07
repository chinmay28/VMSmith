package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

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
		name, _ := cmd.Flags().GetString("name")
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		snap, err := mgr.CreateSnapshot(context.Background(), args[0], name)
		if err != nil {
			return err
		}
		fmt.Printf("Snapshot created: %s\n", snap.Name)
		return nil
	},
}

var snapRestoreCmd = &cobra.Command{
	Use:   "restore <vm-id>",
	Short: "Restore a VM to a snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := mgr.RestoreSnapshot(context.Background(), args[0], name); err != nil {
			return err
		}
		fmt.Printf("VM %s restored to snapshot %s\n", args[0], name)
		return nil
	},
}

var snapListCmd = &cobra.Command{
	Use:   "list <vm-id>",
	Short: "List snapshots for a VM",
	Aliases: []string{"ls"},
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		snaps, err := mgr.ListSnapshots(context.Background(), args[0])
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tCREATED")
		for _, s := range snaps {
			fmt.Fprintf(w, "%s\t%s\n", s.Name, s.CreatedAt.Format("2006-01-02 15:04:05"))
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
		name, _ := cmd.Flags().GetString("name")
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := mgr.DeleteSnapshot(context.Background(), args[0], name); err != nil {
			return err
		}
		fmt.Printf("Snapshot %s deleted\n", name)
		return nil
	},
}

func init() {
	snapCreateCmd.Flags().String("name", "", "snapshot name (required)")
	snapCreateCmd.MarkFlagRequired("name")

	snapRestoreCmd.Flags().String("name", "", "snapshot name to restore (required)")
	snapRestoreCmd.MarkFlagRequired("name")

	snapDeleteCmd.Flags().String("name", "", "snapshot name to delete (required)")
	snapDeleteCmd.MarkFlagRequired("name")

	snapshotCmd.AddCommand(snapCreateCmd)
	snapshotCmd.AddCommand(snapRestoreCmd)
	snapshotCmd.AddCommand(snapListCmd)
	snapshotCmd.AddCommand(snapDeleteCmd)
}
