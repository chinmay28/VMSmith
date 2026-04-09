package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
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
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		limit, offset, err := normalizeLimitOffset(limit, offset)
		if err != nil {
			return err
		}
		logger.Info("cli", "image list")
		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "image list: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		imgs, err := mgr.ListImages()
		if err != nil {
			logger.Error("cli", "image list failed", "error", err.Error())
			return err
		}
		sort.SliceStable(imgs, func(i, j int) bool {
			if !imgs[i].CreatedAt.Equal(imgs[j].CreatedAt) {
				return imgs[i].CreatedAt.Before(imgs[j].CreatedAt)
			}
			return imgs[i].ID < imgs[j].ID
		})
		imgs = paginateSlice(imgs, limit, offset)
		logger.Info("cli", "image list result", "count", fmt.Sprintf("%d", len(imgs)))

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
		vmID := args[0]
		name, _ := cmd.Flags().GetString("name")
		logger.Info("cli", "image create", "vm_id", vmID, "name", name)

		vmMgr, vmCleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "image create: failed to init VM manager", "error", err.Error())
			return err
		}
		defer vmCleanup()

		storageMgr, storageCleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "image create: failed to init storage manager", "error", err.Error())
			return err
		}
		defer storageCleanup()

		// Get VM disk path
		vm, err := vmMgr.Get(cmd.Context(), vmID)
		if err != nil {
			logger.Error("cli", "image create: VM not found", "vm_id", vmID, "error", err.Error())
			return fmt.Errorf("VM not found: %w", err)
		}

		fmt.Printf("Creating image %q from VM %s (this may take a while)...\n", name, vm.Name)
		img, err := storageMgr.CreateImage(vm.DiskPath, name, vm.ID)
		if err != nil {
			logger.Error("cli", "image create failed", "vm_id", vmID, "name", name, "error", err.Error())
			return err
		}

		logger.Info("cli", "image created", "id", img.ID, "name", img.Name, "size", humanSize(img.SizeBytes))
		fmt.Printf("Image created: %s (%s)\n", img.Name, humanSize(img.SizeBytes))
		return nil
	},
}

var imagePushCmd = &cobra.Command{
	Use:   "push <image-name> <user@host>",
	Short: "Push an image to a remote host via SCP",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		imageName, dest := args[0], args[1]
		logger.Info("cli", "image push", "name", imageName, "dest", dest)

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "image push: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		fmt.Printf("Pushing image %q to %s...\n", imageName, dest)
		if err := mgr.Push(imageName, dest); err != nil {
			logger.Error("cli", "image push failed", "name", imageName, "dest", dest, "error", err.Error())
			return err
		}
		logger.Info("cli", "image pushed", "name", imageName, "dest", dest)
		return nil
	},
}

var imagePullCmd = &cobra.Command{
	Use:   "pull <source>",
	Short: "Pull an image from a remote host (SCP or HTTP)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := args[0]
		logger.Info("cli", "image pull", "source", src)

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "image pull: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		fmt.Printf("Pulling image from %s...\n", src)
		if err := mgr.Pull(src, apiKey); err != nil {
			logger.Error("cli", "image pull failed", "source", src, "error", err.Error())
			return err
		}
		logger.Info("cli", "image pulled", "source", src)
		return nil
	},
}

var imageDeleteCmd = &cobra.Command{
	Use:   "delete <image-id>",
	Short: "Delete an image",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		logger.Info("cli", "image delete", "id", id)

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "image delete: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		if err := mgr.DeleteImage(id); err != nil {
			logger.Error("cli", "image delete failed", "id", id, "error", err.Error())
			return err
		}
		logger.Info("cli", "image deleted", "id", id)
		fmt.Printf("Image %s deleted\n", id)
		return nil
	},
}

func init() {
	imageCreateCmd.Flags().String("name", "", "image name (required)")
	imageCreateCmd.MarkFlagRequired("name")

	imageListCmd.Flags().Int("limit", 0, "maximum number of images to show (0 = no limit)")
	imageListCmd.Flags().Int("offset", 0, "number of images to skip before printing results")

	imageCmd.AddCommand(imageListCmd)
	imageCmd.AddCommand(imageCreateCmd)
	imageCmd.AddCommand(imagePushCmd)
	imageCmd.AddCommand(imagePullCmd)
	imageCmd.AddCommand(imageDeleteCmd)
}

// storageManagerOverride can be set in tests to bypass real config/store.
var storageManagerOverride func() (*storage.Manager, func(), error)

func newStorageManager() (*storage.Manager, func(), error) {
	if storageManagerOverride != nil {
		return storageManagerOverride()
	}

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
