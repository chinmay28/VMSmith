package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	validatepkg "github.com/vmsmith/vmsmith/internal/validate"
	"github.com/vmsmith/vmsmith/pkg/types"
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
		tagFilter, _ := cmd.Flags().GetString("tag")
		tagFilter = strings.ToLower(strings.TrimSpace(tagFilter))
		sourceVMFilter, _ := cmd.Flags().GetString("source-vm")
		sourceVMFilter = strings.ToLower(strings.TrimSpace(sourceVMFilter))
		searchFilter, _ := cmd.Flags().GetString("search")
		searchFilter = strings.ToLower(strings.TrimSpace(searchFilter))
		prefixFilter, _ := cmd.Flags().GetString("prefix")
		prefixFilter = strings.TrimSpace(prefixFilter)
		sinceRaw, _ := cmd.Flags().GetString("since")
		untilRaw, _ := cmd.Flags().GetString("until")
		sinceTime, sinceSet, err := parseCLITimeRange(sinceRaw, "--since")
		if err != nil {
			return err
		}
		untilTime, untilSet, err := parseCLITimeRange(untilRaw, "--until")
		if err != nil {
			return err
		}
		minSizeRaw, _ := cmd.Flags().GetString("min-size")
		maxSizeRaw, _ := cmd.Flags().GetString("max-size")
		minSize, minSizeSet, err := parseCLISizeRange(minSizeRaw, "--min-size")
		if err != nil {
			return err
		}
		maxSize, maxSizeSet, err := parseCLISizeRange(maxSizeRaw, "--max-size")
		if err != nil {
			return err
		}
		sortField, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		sortField = strings.TrimSpace(strings.ToLower(sortField))
		if sortField == "" {
			sortField = types.ImageSortID
		}
		switch sortField {
		case types.ImageSortID, types.ImageSortName, types.ImageSortSize, types.ImageSortCreatedAt:
		default:
			return fmt.Errorf("invalid --sort %q: must be one of id, name, size, created_at", sortField)
		}
		order = strings.TrimSpace(strings.ToLower(order))
		if order == "" {
			order = types.SortOrderAsc
		}
		switch order {
		case types.SortOrderAsc, types.SortOrderDesc:
		default:
			return fmt.Errorf("invalid --order %q: must be 'asc' or 'desc'", order)
		}
		limit, offset, err = normalizeLimitOffset(limit, offset)
		if err != nil {
			return err
		}
		logger.Info("cli", "image list", "tag", tagFilter, "source_vm", sourceVMFilter, "search", searchFilter, "prefix", prefixFilter, "sort", sortField, "order", order)
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
		if tagFilter != "" {
			imgs = storage.FilterImagesByTag(imgs, tagFilter)
		}
		if sourceVMFilter != "" {
			filtered := imgs[:0]
			for _, img := range imgs {
				if strings.EqualFold(img.SourceVM, sourceVMFilter) {
					filtered = append(filtered, img)
				}
			}
			imgs = filtered
		}
		if minSizeSet || maxSizeSet {
			filtered := imgs[:0]
			for _, img := range imgs {
				if !imageInCLISizeRange(img.SizeBytes, minSize, minSizeSet, maxSize, maxSizeSet) {
					continue
				}
				filtered = append(filtered, img)
			}
			imgs = filtered
		}
		// Prefix filter (5.4.77): case-sensitive HasPrefix on img.Name to
		// mirror the snapshot list `--prefix` (5.4.75) and the VM list
		// `--prefix` (5.4.76) so the same `?prefix=rocky-` cohort query
		// round-trips 1:1 across the three name-prefix axes.
		if prefixFilter != "" {
			filtered := imgs[:0]
			for _, img := range imgs {
				if strings.HasPrefix(img.Name, prefixFilter) {
					filtered = append(filtered, img)
				}
			}
			imgs = filtered
		}
		if sinceSet || untilSet {
			filtered := imgs[:0]
			for _, img := range imgs {
				if !snapshotInCLITimeRange(img.CreatedAt, sinceTime, sinceSet, untilTime, untilSet) {
					continue
				}
				filtered = append(filtered, img)
			}
			imgs = filtered
		}
		if searchFilter != "" {
			filtered := imgs[:0]
			for _, img := range imgs {
				if types.ImageMatchesSearch(img, searchFilter) {
					filtered = append(filtered, img)
				}
			}
			imgs = filtered
		}
		types.SortImages(imgs, sortField, order)
		imgs = paginateSlice(imgs, limit, offset)
		logger.Info("cli", "image list result", "count", fmt.Sprintf("%d", len(imgs)))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSIZE\tFORMAT\tTAGS\tCREATED")
		for _, img := range imgs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				img.ID, img.Name, humanSize(img.SizeBytes), img.Format,
				strings.Join(img.Tags, ","),
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
		description, _ := cmd.Flags().GetString("description")
		tags, _ := cmd.Flags().GetStringArray("tag")
		normalizedTags, err := validatepkg.NormalizeTags(tags)
		if err != nil {
			return err
		}
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
		img, err := storageMgr.CreateImage(vm.DiskPath, name, vm.ID, storage.CreateImageOptions{
			Description: description,
			Tags:        normalizedTags,
		})
		if err != nil {
			logger.Error("cli", "image create failed", "vm_id", vmID, "name", name, "error", err.Error())
			return err
		}

		logger.Info("cli", "image created", "id", img.ID, "name", img.Name, "size", humanSize(img.SizeBytes))
		fmt.Printf("Image created: %s (%s)\n", img.Name, humanSize(img.SizeBytes))
		if img.Description != "" {
			fmt.Printf("Description: %s\n", img.Description)
		}
		if len(img.Tags) > 0 {
			fmt.Printf("Tags: %s\n", strings.Join(img.Tags, ", "))
		}
		return nil
	},
}

var imageEditCmd = &cobra.Command{
	Use:   "edit <image-id>",
	Short: "Edit image description or tags",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		descriptionChanged := cmd.Flags().Changed("description")
		tagsChanged := cmd.Flags().Changed("tag")
		if !descriptionChanged && !tagsChanged {
			return fmt.Errorf("nothing to update: pass --description and/or --tag")
		}

		patch := types.ImageUpdateSpec{}
		if descriptionChanged {
			desc, _ := cmd.Flags().GetString("description")
			patch.Description = strings.TrimSpace(desc)
		}
		if tagsChanged {
			rawTags, _ := cmd.Flags().GetStringArray("tag")
			tags, err := validatepkg.NormalizeTags(rawTags)
			if err != nil {
				return err
			}
			if tags == nil {
				tags = []string{}
			}
			patch.Tags = tags
		}

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "image edit: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		img, err := mgr.UpdateImage(id, patch)
		if err != nil {
			logger.Error("cli", "image edit failed", "id", id, "error", err.Error())
			return err
		}
		logger.Info("cli", "image updated", "id", img.ID, "name", img.Name)
		fmt.Printf("Image %s updated\n", img.ID)
		if img.Description != "" {
			fmt.Printf("Description: %s\n", img.Description)
		}
		fmt.Printf("Tags: %s\n", strings.Join(img.Tags, ", "))
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
	Use:   "delete [image-id]",
	Short: "Delete an image (or many via --tag)",
	Long: `Delete an image.

Single delete:    vmsmith image delete <image-id>
Bulk by tag:      vmsmith image delete --tag rc-2026-05
Exactly one of <image-id> or --tag is required.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tag, _ := cmd.Flags().GetString("tag")
		tag = strings.ToLower(strings.TrimSpace(tag))
		var id string
		if len(args) == 1 {
			id = strings.TrimSpace(args[0])
		}

		if id == "" && tag == "" {
			return fmt.Errorf("exactly one of <image-id> or --tag is required")
		}
		if id != "" && tag != "" {
			return fmt.Errorf("<image-id> and --tag are mutually exclusive")
		}

		logger.Info("cli", "image delete", "id", id, "tag", tag)

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "image delete: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		if id != "" {
			if err := mgr.DeleteImage(id); err != nil {
				logger.Error("cli", "image delete failed", "id", id, "error", err.Error())
				return err
			}
			logger.Info("cli", "image deleted", "id", id)
			fmt.Printf("Image %s deleted\n", id)
			return nil
		}

		// Tag path: enumerate matches first, then delete one by one.
		// We accept partial failures (one missing image doesn't abort the
		// rest) and print a per-image status — same shape as the API
		// bulk_delete endpoint.
		return runImageTagDelete(mgr, tag)
	},
}

func runImageTagDelete(mgr *storage.Manager, tag string) error {
	imgs, err := mgr.ListImages()
	if err != nil {
		logger.Error("cli", "image delete: list failed", "tag", tag, "error", err.Error())
		return err
	}
	matches := storage.FilterImagesByTag(imgs, tag)
	if len(matches) == 0 {
		fmt.Printf("No images carry tag %q\n", tag)
		return nil
	}

	successes := 0
	failures := 0
	for _, img := range matches {
		if err := mgr.DeleteImage(img.ID); err != nil {
			fmt.Printf("FAIL  %s (%s): %s\n", img.ID, img.Name, err.Error())
			failures++
			continue
		}
		fmt.Printf("OK    %s (%s)\n", img.ID, img.Name)
		successes++
	}
	logger.Info("cli", "image bulk delete complete",
		"tag", tag,
		"matched", fmt.Sprintf("%d", len(matches)),
		"success", fmt.Sprintf("%d", successes),
		"failed", fmt.Sprintf("%d", failures))
	if failures > 0 {
		return fmt.Errorf("%d of %d images failed to delete", failures, len(matches))
	}
	return nil
}

func init() {
	imageCreateCmd.Flags().String("name", "", "image name (required)")
	imageCreateCmd.MarkFlagRequired("name")
	imageCreateCmd.Flags().String("description", "", "human-readable description")
	imageCreateCmd.Flags().StringArray("tag", nil, "tag (repeatable)")

	imageListCmd.Flags().Int("limit", 0, "maximum number of images to show (0 = no limit)")
	imageListCmd.Flags().Int("offset", 0, "number of images to skip before printing results")
	imageListCmd.Flags().String("tag", "", "filter to images carrying this tag")
	imageListCmd.Flags().String("source-vm", "", "filter to images exported from this source VM ID (case-insensitive exact match)")
	imageListCmd.Flags().String("search", "", "case-insensitive substring filter on name, description, and tags")
	imageListCmd.Flags().String("prefix", "", "case-sensitive HasPrefix filter on image name (e.g. 'rocky-' to slice to every Rocky cohort image)")
	imageListCmd.Flags().String("since", "", "keep images created at or after this RFC3339 timestamp (inclusive; e.g. 2026-05-01T00:00:00Z)")
	imageListCmd.Flags().String("until", "", "keep images created at or before this RFC3339 timestamp (inclusive; e.g. 2026-05-01T23:59:59Z)")
	imageListCmd.Flags().String("min-size", "", "keep images whose size in bytes is at least this value (inclusive)")
	imageListCmd.Flags().String("max-size", "", "keep images whose size in bytes is at most this value (inclusive)")
	imageListCmd.Flags().String("sort", "id", "sort by: id, name, size, created_at")
	imageListCmd.Flags().String("order", "asc", "order: asc or desc")

	imageEditCmd.Flags().String("description", "", "new description (omit to leave unchanged; empty value cannot clear the field)")
	imageEditCmd.Flags().StringArray("tag", nil, "tag value (repeatable; provide --tag with no value or omit any --tag to leave tags unchanged; pass --tag '' once to clear)")

	imageDeleteCmd.Flags().String("tag", "", "delete every image carrying this tag (mutually exclusive with image-id; case-insensitive)")

	imageCmd.AddCommand(imageListCmd)
	imageCmd.AddCommand(imageCreateCmd)
	imageCmd.AddCommand(imageEditCmd)
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
