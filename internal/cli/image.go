package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
)

var imageCmd = &cobra.Command{
	Use:     "image",
	Short:   "Manage VM images",
	Aliases: []string{"img"},
}

var imageListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List available images",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newStorageManager()
		if err != nil {
			return err
		}
		defer cleanup()

		imgs, err := mgr.ListImages()
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSIZE\tFORMAT\tCREATED")
		for _, img := range imgs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				img.ID, img.Name, humanSize(img.SizeBytes), img.Format,
				img.CreatedAt.Format("2006-01-02 15:04:05"))
		}
		w.Flush()
		return nil
	},
}

var imageCreateCmd = &cobra.Command{
	Use:   "create <vm-id>",
	Short: "Create a portable image from a VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")

		vmMgr, vmCleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer vmCleanup()

		storageMgr, storageCleanup, err := newStorageManager()
		if err != nil {
			return err
		}
		defer storageCleanup()

		// Get VM disk path
		vm, err := vmMgr.Get(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("VM not found: %w", err)
		}

		fmt.Printf("Creating image %q from VM %s (this may take a while)...\n", name, vm.Name)
		img, err := storageMgr.CreateImage(vm.DiskPath, name, vm.ID)
		if err != nil {
			return err
		}

		fmt.Printf("Image created: %s (%s)\n", img.Name, humanSize(img.SizeBytes))
		return nil
	},
}

var imagePushCmd = &cobra.Command{
	Use:   "push <image-name> <user@host>",
	Short: "Push an image to a remote host via SCP",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newStorageManager()
		if err != nil {
			return err
		}
		defer cleanup()

		fmt.Printf("Pushing image %q to %s...\n", args[0], args[1])
		return mgr.Push(args[0], args[1])
	},
}

var imagePullCmd = &cobra.Command{
	Use:   "pull <source>",
	Short: "Pull an image from a remote host (SCP or HTTP)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newStorageManager()
		if err != nil {
			return err
		}
		defer cleanup()

		fmt.Printf("Pulling image from %s...\n", args[0])
		return mgr.Pull(args[0])
	},
}

var imageDeleteCmd = &cobra.Command{
	Use:   "delete <image-id>",
	Short: "Delete an image",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newStorageManager()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := mgr.DeleteImage(args[0]); err != nil {
			return err
		}
		fmt.Printf("Image %s deleted\n", args[0])
		return nil
	},
}

func init() {
	imageCreateCmd.Flags().String("name", "", "image name (required)")
	imageCreateCmd.MarkFlagRequired("name")

	imageCmd.AddCommand(imageListCmd)
	imageCmd.AddCommand(imageCreateCmd)
	imageCmd.AddCommand(imagePushCmd)
	imageCmd.AddCommand(imagePullCmd)
	imageCmd.AddCommand(imageDeleteCmd)
}

func newStorageManager() (*storage.Manager, func(), error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, err
	}

	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		return nil, nil, err
	}

	mgr := storage.NewManager(cfg, s)
	return mgr, func() { s.Close() }, nil
}

func humanSize(bytes int64) string {
	const (
		MB = 1024 * 1024
		GB = 1024 * MB
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
