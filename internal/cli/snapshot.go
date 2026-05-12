package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/vm"
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

		sortField, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		sortField = strings.TrimSpace(strings.ToLower(sortField))
		order = strings.TrimSpace(strings.ToLower(order))
		if sortField == "" {
			sortField = types.SnapshotSortID
		}
		switch sortField {
		case types.SnapshotSortID, types.SnapshotSortName, types.SnapshotSortCreatedAt:
		default:
			return fmt.Errorf("invalid --sort %q: must be one of id, name, created_at", sortField)
		}
		if order == "" {
			order = types.SortOrderAsc
		}
		switch order {
		case types.SortOrderAsc, types.SortOrderDesc:
		default:
			return fmt.Errorf("invalid --order %q: must be 'asc' or 'desc'", order)
		}

		logger.Info("cli", "snapshot list", "vm_id", vmID, "sort", sortField, "order", order)

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
		types.SortSnapshots(snaps, sortField, order)
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

var snapEditCmd = &cobra.Command{
	Use:   "edit <vm-id> <snap-name>",
	Short: "Edit metadata on an existing snapshot",
	Long: `Edit the description of an existing snapshot. The underlying disk and
memory state are not touched — only the snapshot's <description> element is
rewritten via libvirt's snapshot REDEFINE primitive.

Pass --description "" to clear the description. Omit --description entirely
for a no-op (useful for confirming the snapshot is reachable).`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		snapName := args[1]

		var descPtr *string
		if cmd.Flags().Changed("description") {
			d, _ := cmd.Flags().GetString("description")
			if len(d) > snapshotDescriptionMaxLen {
				return fmt.Errorf("description must be at most %d characters", snapshotDescriptionMaxLen)
			}
			descPtr = &d
		}

		logger.Info("cli", "snapshot edit", "vm_id", vmID, "name", snapName)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "snapshot edit: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		snap, err := mgr.UpdateSnapshot(context.Background(), vmID, snapName, types.SnapshotUpdateSpec{Description: descPtr})
		if err != nil {
			logger.Error("cli", "snapshot edit failed", "vm_id", vmID, "name", snapName, "error", err.Error())
			return err
		}
		logger.Info("cli", "snapshot updated", "vm_id", vmID, "name", snap.Name)
		fmt.Printf("Snapshot %s updated\n", snap.Name)
		if snap.Description != "" {
			fmt.Printf("Description: %s\n", snap.Description)
		}
		return nil
	},
}

var snapDeleteCmd = &cobra.Command{
	Use:   "delete <vm-id>",
	Short: "Delete a snapshot (or many via --prefix)",
	Long: `Delete a snapshot.

Single delete:    vmsmith snapshot delete <vm-id> --name <snap>
Bulk by prefix:   vmsmith snapshot delete <vm-id> --prefix auto-nightly-
Exactly one of --name or --prefix is required.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		name, _ := cmd.Flags().GetString("name")
		prefix, _ := cmd.Flags().GetString("prefix")
		name = strings.TrimSpace(name)
		prefix = strings.TrimSpace(prefix)

		if name == "" && prefix == "" {
			return fmt.Errorf("exactly one of --name or --prefix is required")
		}
		if name != "" && prefix != "" {
			return fmt.Errorf("--name and --prefix are mutually exclusive")
		}

		logger.Info("cli", "snapshot delete", "vm_id", vmID, "name", name, "prefix", prefix)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "snapshot delete: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		if name != "" {
			if err := mgr.DeleteSnapshot(context.Background(), vmID, name); err != nil {
				logger.Error("cli", "snapshot delete failed", "vm_id", vmID, "name", name, "error", err.Error())
				return err
			}
			logger.Info("cli", "snapshot deleted", "vm_id", vmID, "name", name)
			fmt.Printf("Snapshot %s deleted\n", name)
			return nil
		}

		// Prefix path: enumerate matches first, then delete one by one.
		// We accept partial failures (one missing snapshot doesn't abort the
		// rest) and print a per-snapshot status — same shape as the API
		// bulk_delete endpoint.
		return runSnapshotPrefixDelete(cmd.Context(), mgr, vmID, prefix)
	},
}

func runSnapshotPrefixDelete(ctx context.Context, mgr vm.Manager, vmID, prefix string) error {
	snaps, err := mgr.ListSnapshots(ctx, vmID)
	if err != nil {
		logger.Error("cli", "snapshot delete: list failed", "vm_id", vmID, "error", err.Error())
		return err
	}
	matches := make([]string, 0)
	for _, s := range snaps {
		if strings.HasPrefix(s.Name, prefix) {
			matches = append(matches, s.Name)
		}
	}
	if len(matches) == 0 {
		fmt.Printf("No snapshots match prefix %q\n", prefix)
		return nil
	}

	successes := 0
	failures := 0
	for _, n := range matches {
		if err := mgr.DeleteSnapshot(ctx, vmID, n); err != nil {
			fmt.Printf("FAIL  %s: %s\n", n, err.Error())
			failures++
			continue
		}
		fmt.Printf("OK    %s\n", n)
		successes++
	}
	logger.Info("cli", "snapshot bulk delete complete",
		"vm_id", vmID, "prefix", prefix,
		"matched", fmt.Sprintf("%d", len(matches)),
		"success", fmt.Sprintf("%d", successes),
		"failed", fmt.Sprintf("%d", failures))
	if failures > 0 {
		return fmt.Errorf("%d of %d snapshots failed to delete", failures, len(matches))
	}
	return nil
}

func init() {
	snapCreateCmd.Flags().String("name", "", "snapshot name (required)")
	snapCreateCmd.MarkFlagRequired("name")
	snapCreateCmd.Flags().String("description", "", "free-text description for the snapshot (optional, max 1024 chars)")

	snapRestoreCmd.Flags().String("name", "", "snapshot name to restore (required)")
	snapRestoreCmd.MarkFlagRequired("name")

	snapListCmd.Flags().String("sort", types.SnapshotSortID, "sort field: id, name, or created_at")
	snapListCmd.Flags().String("order", types.SortOrderAsc, "sort order: asc or desc")

	snapEditCmd.Flags().String("description", "", "new description for the snapshot (pass empty string to clear; max 1024 chars)")

	snapDeleteCmd.Flags().String("name", "", "single snapshot name to delete (mutually exclusive with --prefix)")
	snapDeleteCmd.Flags().String("prefix", "", "delete every snapshot whose name starts with this prefix (mutually exclusive with --name)")

	snapshotCmd.AddCommand(snapCreateCmd)
	snapshotCmd.AddCommand(snapRestoreCmd)
	snapshotCmd.AddCommand(snapListCmd)
	snapshotCmd.AddCommand(snapEditCmd)
	snapshotCmd.AddCommand(snapDeleteCmd)
}
