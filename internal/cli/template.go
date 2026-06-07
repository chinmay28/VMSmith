package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
	validatepkg "github.com/vmsmith/vmsmith/internal/validate"
	"github.com/vmsmith/vmsmith/pkg/types"
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Manage VM templates",
}

var templateCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a reusable VM template",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(args[0])
		image, _ := cmd.Flags().GetString("image")
		cpus, _ := cmd.Flags().GetInt("cpus")
		ram, _ := cmd.Flags().GetInt("ram")
		disk, _ := cmd.Flags().GetInt("disk")
		description, _ := cmd.Flags().GetString("description")
		tags, _ := cmd.Flags().GetStringSlice("tag")
		defaultUser, _ := cmd.Flags().GetString("default-user")
		networkFlags, _ := cmd.Flags().GetStringSlice("network")

		networks, err := parseNetworkFlags(networkFlags)
		if err != nil {
			return err
		}

		tags, err = validatepkg.NormalizeTags(tags)
		if err != nil {
			return err
		}
		if err := validatepkg.ValidateTemplateRequest(name, image, cpus, ram, disk); err != nil {
			return err
		}

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "template create: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		existing, err := mgr.ListTemplates()
		if err != nil {
			logger.Error("cli", "template create: failed to list templates", "error", err.Error())
			return err
		}
		if err := validatepkg.ValidateUniqueTemplateName(name, existing); err != nil {
			return err
		}

		tpl, err := mgr.CreateTemplate(&types.VMTemplate{
			Name:        name,
			Image:       strings.TrimSpace(image),
			CPUs:        cpus,
			RAMMB:       ram,
			DiskGB:      disk,
			Description: strings.TrimSpace(description),
			Tags:        tags,
			DefaultUser: strings.TrimSpace(defaultUser),
			Networks:    networks,
		})
		if err != nil {
			logger.Error("cli", "template create failed", "name", name, "error", err.Error())
			return err
		}

		fmt.Printf("Template created successfully:\n")
		fmt.Printf("  ID:    %s\n", tpl.ID)
		fmt.Printf("  Name:  %s\n", tpl.Name)
		fmt.Printf("  Image: %s\n", tpl.Image)
		if tpl.CPUs > 0 {
			fmt.Printf("  CPUs:  %d\n", tpl.CPUs)
		}
		if tpl.RAMMB > 0 {
			fmt.Printf("  RAM:   %d MB\n", tpl.RAMMB)
		}
		if tpl.DiskGB > 0 {
			fmt.Printf("  Disk:  %d GB\n", tpl.DiskGB)
		}
		if tpl.DefaultUser != "" {
			fmt.Printf("  User:  %s\n", tpl.DefaultUser)
		}
		if tpl.Description != "" {
			fmt.Printf("  Desc:  %s\n", tpl.Description)
		}
		if len(tpl.Tags) > 0 {
			fmt.Printf("  Tags:  %s\n", strings.Join(tpl.Tags, ", "))
		}
		if len(tpl.Networks) > 0 {
			hostInterfaces := make([]string, 0, len(tpl.Networks))
			for _, network := range tpl.Networks {
				hostInterfaces = append(hostInterfaces, network.HostInterface)
			}
			fmt.Printf("  Networks: %s\n", strings.Join(hostInterfaces, ", "))
		}
		return nil
	},
}

var templateListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List VM templates",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		tagFilter, _ := cmd.Flags().GetString("tag")
		searchFlag, _ := cmd.Flags().GetString("search")
		imageFilter, _ := cmd.Flags().GetString("image")
		defaultUserFilter, _ := cmd.Flags().GetString("default-user")
		osTypeFilterRaw, _ := cmd.Flags().GetString("os-type")
		osVariantFilterRaw, _ := cmd.Flags().GetString("os-variant")
		networkFilter, _ := cmd.Flags().GetString("network")
		prefixFilter, _ := cmd.Flags().GetString("prefix")
		sinceRaw, _ := cmd.Flags().GetString("since")
		untilRaw, _ := cmd.Flags().GetString("until")
		minCPUsRaw, _ := cmd.Flags().GetString("min-cpus")
		maxCPUsRaw, _ := cmd.Flags().GetString("max-cpus")
		minRAMRaw, _ := cmd.Flags().GetString("min-ram-mb")
		maxRAMRaw, _ := cmd.Flags().GetString("max-ram-mb")
		minDiskRaw, _ := cmd.Flags().GetString("min-disk-gb")
		maxDiskRaw, _ := cmd.Flags().GetString("max-disk-gb")
		sinceTime, sinceSet, err := parseCLITimeRange(sinceRaw, "--since")
		if err != nil {
			return err
		}
		untilTime, untilSet, err := parseCLITimeRange(untilRaw, "--until")
		if err != nil {
			return err
		}
		minCPUs, minCPUsSet, err := parseCLICountRange(minCPUsRaw, "--min-cpus")
		if err != nil {
			return err
		}
		maxCPUs, maxCPUsSet, err := parseCLICountRange(maxCPUsRaw, "--max-cpus")
		if err != nil {
			return err
		}
		minRAM, minRAMSet, err := parseCLICountRange(minRAMRaw, "--min-ram-mb")
		if err != nil {
			return err
		}
		maxRAM, maxRAMSet, err := parseCLICountRange(maxRAMRaw, "--max-ram-mb")
		if err != nil {
			return err
		}
		minDisk, minDiskSet, err := parseCLICountRange(minDiskRaw, "--min-disk-gb")
		if err != nil {
			return err
		}
		maxDisk, maxDiskSet, err := parseCLICountRange(maxDiskRaw, "--max-disk-gb")
		if err != nil {
			return err
		}
		osTypeFilter, osTypeSet, err := parseCLIOSType(osTypeFilterRaw, "--os-type")
		if err != nil {
			return err
		}
		osVariantFilter, osVariantSet, err := parseCLIOSVariant(osVariantFilterRaw, "--os-variant")
		if err != nil {
			return err
		}
		sortField, _ := cmd.Flags().GetString("sort")
		orderField, _ := cmd.Flags().GetString("order")
		sortField = strings.TrimSpace(strings.ToLower(sortField))
		if sortField == "" {
			sortField = types.TemplateSortID
		}
		switch sortField {
		case types.TemplateSortID, types.TemplateSortName, types.TemplateSortCreatedAt,
			types.TemplateSortCPUs, types.TemplateSortRAMMB, types.TemplateSortDiskGB:
		default:
			return fmt.Errorf("invalid --sort %q: must be one of id, name, created_at, cpus, ram_mb, disk_gb", sortField)
		}
		orderField = strings.TrimSpace(strings.ToLower(orderField))
		if orderField == "" {
			orderField = types.SortOrderAsc
		}
		switch orderField {
		case types.SortOrderAsc, types.SortOrderDesc:
		default:
			return fmt.Errorf("invalid --order %q: must be 'asc' or 'desc'", orderField)
		}
		limit, offset, err = normalizeLimitOffset(limit, offset)
		if err != nil {
			return err
		}

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "template list: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		templates, err := mgr.ListTemplates()
		if err != nil {
			logger.Error("cli", "template list failed", "error", err.Error())
			return err
		}
		if tagFilter = strings.TrimSpace(tagFilter); tagFilter != "" {
			templates = filterTemplatesByTag(templates, tagFilter)
		}
		if imageQuery := strings.TrimSpace(strings.ToLower(imageFilter)); imageQuery != "" {
			filtered := templates[:0]
			for _, tpl := range templates {
				if strings.EqualFold(tpl.Image, imageQuery) {
					filtered = append(filtered, tpl)
				}
			}
			templates = filtered
		}
		if userQuery := strings.TrimSpace(strings.ToLower(defaultUserFilter)); userQuery != "" {
			filtered := templates[:0]
			for _, tpl := range templates {
				if strings.EqualFold(tpl.DefaultUser, userQuery) {
					filtered = append(filtered, tpl)
				}
			}
			templates = filtered
		}
		if osTypeSet {
			filtered := templates[:0]
			for _, tpl := range templates {
				if tpl.ResolvedOSType() == osTypeFilter {
					filtered = append(filtered, tpl)
				}
			}
			templates = filtered
		}
		if osVariantSet {
			filtered := templates[:0]
			for _, tpl := range templates {
				if strings.EqualFold(tpl.OSVariant, osVariantFilter) {
					filtered = append(filtered, tpl)
				}
			}
			templates = filtered
		}
		if networkQuery := strings.TrimSpace(strings.ToLower(networkFilter)); networkQuery != "" {
			filtered := templates[:0]
			for _, tpl := range templates {
				if types.TemplateMatchesNetwork(tpl, networkQuery) {
					filtered = append(filtered, tpl)
				}
			}
			templates = filtered
		}
		// Prefix filter (5.4.78): case-sensitive HasPrefix on tpl.Name, mirroring
		// the 5.4.75 / 5.4.76 / 5.4.77 prefix-filter family. Whitespace-trimmed
		// only — no ToLower so case-sensitivity is preserved through the wire.
		if prefixQuery := strings.TrimSpace(prefixFilter); prefixQuery != "" {
			filtered := templates[:0]
			for _, tpl := range templates {
				if strings.HasPrefix(tpl.Name, prefixQuery) {
					filtered = append(filtered, tpl)
				}
			}
			templates = filtered
		}
		if sinceSet || untilSet {
			filtered := templates[:0]
			for _, tpl := range templates {
				if !snapshotInCLITimeRange(tpl.CreatedAt, sinceTime, sinceSet, untilTime, untilSet) {
					continue
				}
				filtered = append(filtered, tpl)
			}
			templates = filtered
		}
		if minCPUsSet || maxCPUsSet {
			filtered := templates[:0]
			for _, tpl := range templates {
				if !countInCLIRange(tpl.CPUs, minCPUs, minCPUsSet, maxCPUs, maxCPUsSet) {
					continue
				}
				filtered = append(filtered, tpl)
			}
			templates = filtered
		}
		if minRAMSet || maxRAMSet {
			filtered := templates[:0]
			for _, tpl := range templates {
				if !countInCLIRange(tpl.RAMMB, minRAM, minRAMSet, maxRAM, maxRAMSet) {
					continue
				}
				filtered = append(filtered, tpl)
			}
			templates = filtered
		}
		if minDiskSet || maxDiskSet {
			filtered := templates[:0]
			for _, tpl := range templates {
				if !countInCLIRange(tpl.DiskGB, minDisk, minDiskSet, maxDisk, maxDiskSet) {
					continue
				}
				filtered = append(filtered, tpl)
			}
			templates = filtered
		}
		if searchQuery := strings.ToLower(strings.TrimSpace(searchFlag)); searchQuery != "" {
			filtered := templates[:0]
			for _, tpl := range templates {
				if types.TemplateMatchesSearch(tpl, searchQuery) {
					filtered = append(filtered, tpl)
				}
			}
			templates = filtered
		}
		types.SortTemplates(templates, sortField, orderField)
		templates = paginateSlice(templates, limit, offset)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tIMAGE\tCPUS\tRAM (MB)\tDISK (GB)\tTAGS")
		for _, tpl := range templates {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
				tpl.ID, tpl.Name, tpl.Image, tpl.CPUs, tpl.RAMMB, tpl.DiskGB, strings.Join(tpl.Tags, ","))
		}
		w.Flush()
		return nil
	},
}

var templateEditCmd = &cobra.Command{
	Use:   "edit <template-id>",
	Short: "Edit description and tags on an existing template",
	Long: `Edit the metadata fields (description, tags) on an existing VM template.

Pass --description with a non-empty value to set/replace the description, or
pass --tag (repeatable) to replace the tag set. Use --clear-tags to remove
every tag in one shot. At least one flag must be provided.

Image, resources, name, and network attachments are immutable post-create —
delete and re-create the template to change them.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		descSet := cmd.Flags().Changed("description")
		tagsSet := cmd.Flags().Changed("tag")
		clearTags, _ := cmd.Flags().GetBool("clear-tags")

		if !descSet && !tagsSet && !clearTags {
			return fmt.Errorf("at least one of --description, --tag, or --clear-tags is required")
		}
		if tagsSet && clearTags {
			return fmt.Errorf("--tag and --clear-tags are mutually exclusive")
		}

		patch := types.TemplateUpdateSpec{}
		if descSet {
			desc, _ := cmd.Flags().GetString("description")
			patch.Description = strings.TrimSpace(desc)
			if patch.Description == "" {
				return fmt.Errorf("--description must be a non-empty value")
			}
			if len(patch.Description) > 1024 {
				return fmt.Errorf("--description must be 1024 characters or fewer")
			}
		}
		if tagsSet {
			tags, _ := cmd.Flags().GetStringSlice("tag")
			normalized, err := validatepkg.NormalizeTags(tags)
			if err != nil {
				return err
			}
			if normalized == nil {
				normalized = []string{}
			}
			patch.Tags = normalized
		} else if clearTags {
			patch.Tags = []string{}
		}

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "template edit: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		tpl, _, err := mgr.UpdateTemplate(id, patch)
		if err != nil {
			logger.Error("cli", "template edit failed", "id", id, "error", err.Error())
			return err
		}

		fmt.Printf("Template %s updated\n", tpl.ID)
		fmt.Printf("  Name:  %s\n", tpl.Name)
		if tpl.Description != "" {
			fmt.Printf("  Desc:  %s\n", tpl.Description)
		}
		if len(tpl.Tags) > 0 {
			fmt.Printf("  Tags:  %s\n", strings.Join(tpl.Tags, ", "))
		} else {
			fmt.Printf("  Tags:  (none)\n")
		}
		return nil
	},
}

func filterTemplatesByTag(templates []*types.VMTemplate, tag string) []*types.VMTemplate {
	out := make([]*types.VMTemplate, 0, len(templates))
	for _, tpl := range templates {
		for _, t := range tpl.Tags {
			if strings.EqualFold(t, tag) {
				out = append(out, tpl)
				break
			}
		}
	}
	return out
}

var templateDeleteCmd = &cobra.Command{
	Use:   "delete [template-id]",
	Short: "Delete a VM template (or many via --tag)",
	Long: `Delete a VM template.

Single delete:    vmsmith template delete <template-id>
Bulk by tag:      vmsmith template delete --tag legacy-rocky8
Exactly one of <template-id> or --tag is required.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		tag, _ := cmd.Flags().GetString("tag")
		tag = strings.ToLower(strings.TrimSpace(tag))
		var id string
		if len(args) == 1 {
			id = strings.TrimSpace(args[0])
		}

		if id == "" && tag == "" {
			return fmt.Errorf("exactly one of <template-id> or --tag is required")
		}
		if id != "" && tag != "" {
			return fmt.Errorf("<template-id> and --tag are mutually exclusive")
		}

		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "template delete: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		if id != "" {
			if err := mgr.DeleteTemplate(id); err != nil {
				logger.Error("cli", "template delete failed", "id", id, "error", err.Error())
				return err
			}
			fmt.Printf("Template %s deleted\n", id)
			return nil
		}

		return runTemplateTagDelete(mgr, tag)
	},
}

func runTemplateTagDelete(mgr templateBulkDeleter, tag string) error {
	tpls, err := mgr.ListTemplates()
	if err != nil {
		logger.Error("cli", "template delete: list failed", "tag", tag, "error", err.Error())
		return err
	}
	matches := filterTemplatesByTag(tpls, tag)
	if len(matches) == 0 {
		fmt.Printf("No templates carry tag %q\n", tag)
		return nil
	}

	successes := 0
	failures := 0
	for _, tpl := range matches {
		if err := mgr.DeleteTemplate(tpl.ID); err != nil {
			fmt.Printf("FAIL  %s (%s): %s\n", tpl.ID, tpl.Name, err.Error())
			failures++
			continue
		}
		fmt.Printf("OK    %s (%s)\n", tpl.ID, tpl.Name)
		successes++
	}
	logger.Info("cli", "template bulk delete complete",
		"tag", tag,
		"matched", fmt.Sprintf("%d", len(matches)),
		"success", fmt.Sprintf("%d", successes),
		"failed", fmt.Sprintf("%d", failures))
	if failures > 0 {
		return fmt.Errorf("%d of %d templates failed to delete", failures, len(matches))
	}
	return nil
}

type templateBulkDeleter interface {
	ListTemplates() ([]*types.VMTemplate, error)
	DeleteTemplate(id string) error
}

func init() {
	templateCreateCmd.Flags().String("image", "", "base image name or absolute path to a .qcow2 file (required)")
	templateCreateCmd.Flags().Int("cpus", 0, "default number of vCPUs")
	templateCreateCmd.Flags().Int("ram", 0, "default RAM in MB")
	templateCreateCmd.Flags().Int("disk", 0, "default disk size in GB")
	templateCreateCmd.Flags().String("description", "", "template description")
	templateCreateCmd.Flags().StringSlice("tag", nil, "tag to apply to template-created VMs (repeatable)")
	templateCreateCmd.Flags().String("default-user", "", "default login user for VMs created from this template")
	templateCreateCmd.Flags().StringSlice("network", nil, "attach template VMs to host network (repeatable); same format as vm create --network")
	_ = templateCreateCmd.MarkFlagRequired("image")

	templateListCmd.Flags().Int("limit", 0, "maximum number of templates to show (0 = no limit)")
	templateListCmd.Flags().Int("offset", 0, "number of templates to skip before printing results")
	templateListCmd.Flags().String("tag", "", "filter templates by tag (case-insensitive)")
	templateListCmd.Flags().String("search", "", "case-insensitive substring filter applied to name, description, and tags")
	templateListCmd.Flags().String("image", "", "case-insensitive exact-match filter on the template's base image")
	templateListCmd.Flags().String("default-user", "", "case-insensitive exact-match filter on the template's default login user")
	templateListCmd.Flags().String("os-type", "", "filter templates by guest OS family: 'linux' or 'windows' (case-insensitive; empty stored os_type is treated as 'linux')")
	templateListCmd.Flags().String("os-variant", "", "filter templates by Windows variant (case-insensitive exact match against os_variant; one of windows-10, windows-11, windows-server-2019, windows-server-2022, windows-server-2025; empty os_variant is excluded when the filter is set)")
	templateListCmd.Flags().String("network", "", "filter templates attached to a named network (case-insensitive exact match against networks names)")
	templateListCmd.Flags().String("prefix", "", "filter templates whose name starts with this value (case-sensitive HasPrefix; mirrors snapshot/VM/image --prefix)")
	templateListCmd.Flags().String("since", "", "keep templates created at or after this RFC3339 timestamp (inclusive; e.g. 2026-05-01T00:00:00Z)")
	templateListCmd.Flags().String("until", "", "keep templates created at or before this RFC3339 timestamp (inclusive; e.g. 2026-05-01T23:59:59Z)")
	templateListCmd.Flags().String("min-cpus", "", "keep templates with at least this many vCPUs (inclusive; non-negative integer)")
	templateListCmd.Flags().String("max-cpus", "", "keep templates with at most this many vCPUs (inclusive; non-negative integer)")
	templateListCmd.Flags().String("min-ram-mb", "", "keep templates with at least this much RAM in MB (inclusive; non-negative integer)")
	templateListCmd.Flags().String("max-ram-mb", "", "keep templates with at most this much RAM in MB (inclusive; non-negative integer)")
	templateListCmd.Flags().String("min-disk-gb", "", "keep templates with at least this many GB of disk (inclusive; non-negative integer)")
	templateListCmd.Flags().String("max-disk-gb", "", "keep templates with at most this many GB of disk (inclusive; non-negative integer)")
	templateListCmd.Flags().String("sort", types.TemplateSortID, "sort field: id, name, created_at, cpus, ram_mb, disk_gb")
	templateListCmd.Flags().String("order", types.SortOrderAsc, "sort order: asc or desc")

	templateEditCmd.Flags().String("description", "", "new template description (omit to keep current)")
	templateEditCmd.Flags().StringSlice("tag", nil, "replace template tags (repeatable; omit to keep current)")
	templateEditCmd.Flags().Bool("clear-tags", false, "remove every tag from the template")

	templateDeleteCmd.Flags().String("tag", "", "delete every template carrying this tag (mutually exclusive with template-id; case-insensitive)")

	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateCreateCmd)
	templateCmd.AddCommand(templateEditCmd)
	templateCmd.AddCommand(templateDeleteCmd)
}
