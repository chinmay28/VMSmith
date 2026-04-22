package cli

import (
	"fmt"
	"os"
	"sort"
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
		limit, offset, err := normalizeLimitOffset(limit, offset)
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
		sort.SliceStable(templates, func(i, j int) bool {
			if !templates[i].CreatedAt.Equal(templates[j].CreatedAt) {
				return templates[i].CreatedAt.Before(templates[j].CreatedAt)
			}
			return templates[i].ID < templates[j].ID
		})
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

var templateDeleteCmd = &cobra.Command{
	Use:   "delete <template-id>",
	Short: "Delete a VM template",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		mgr, cleanup, err := newStorageManager()
		if err != nil {
			logger.Error("cli", "template delete: failed to init storage manager", "error", err.Error())
			return err
		}
		defer cleanup()

		if err := mgr.DeleteTemplate(id); err != nil {
			logger.Error("cli", "template delete failed", "id", id, "error", err.Error())
			return err
		}

		fmt.Printf("Template %s deleted\n", id)
		return nil
	},
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
	if err := templateCreateCmd.MarkFlagRequired("image"); err != nil {
		panic(err)
	}

	templateListCmd.Flags().Int("limit", 0, "maximum number of templates to show (0 = no limit)")
	templateListCmd.Flags().Int("offset", 0, "number of templates to skip before printing results")

	templateCmd.AddCommand(templateListCmd)
	templateCmd.AddCommand(templateCreateCmd)
	templateCmd.AddCommand(templateDeleteCmd)
}
