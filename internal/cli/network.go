package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/validate"
	"github.com/vmsmith/vmsmith/pkg/types"
)

const cliMaxPortForwardDescriptionLength = 256

var portCmd = &cobra.Command{
	Use:   "port",
	Short: "Manage port forwarding rules",
}

var portAddCmd = &cobra.Command{
	Use:   "add <vm-id>",
	Short: "Add a port forwarding rule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		hostPort, _ := cmd.Flags().GetInt("host")
		guestPort, _ := cmd.Flags().GetInt("guest")
		proto, _ := cmd.Flags().GetString("proto")
		descriptionRaw, _ := cmd.Flags().GetString("description")
		description := strings.TrimSpace(descriptionRaw)
		tagsRaw, _ := cmd.Flags().GetStringSlice("tag")

		logger.Info("cli", "port add", "vm_id", vmID,
			"host_port", fmt.Sprintf("%d", hostPort),
			"guest_port", fmt.Sprintf("%d", guestPort),
			"proto", proto)

		if err := types.ValidatePortForward(hostPort, guestPort, types.Protocol(proto)); err != nil {
			return err
		}
		if len(description) > cliMaxPortForwardDescriptionLength {
			return fmt.Errorf("description must be at most %d characters", cliMaxPortForwardDescriptionLength)
		}
		tags, err := validate.NormalizeTags(tagsRaw)
		if err != nil {
			return err
		}

		vmMgr, vmCleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "port add: failed to init VM manager", "error", err.Error())
			return err
		}
		defer vmCleanup()

		// Get VM IP
		vm, err := vmMgr.Get(cmd.Context(), vmID)
		if err != nil {
			logger.Error("cli", "port add: VM not found", "vm_id", vmID, "error", err.Error())
			return fmt.Errorf("VM not found: %w", err)
		}
		if vm.IP == "" {
			return fmt.Errorf("VM %s does not have an IP yet; is it running?", vm.Name)
		}

		pf, cleanup, err := newPortForwarder()
		if err != nil {
			logger.Error("cli", "port add: failed to init port forwarder", "error", err.Error())
			return err
		}
		defer cleanup()

		rule, err := pf.Add(vmID, hostPort, guestPort, vm.IP, types.Protocol(proto), network.AddOptions{Description: description, Tags: tags})
		if err != nil {
			logger.Error("cli", "port add failed", "vm_id", vmID,
				"host_port", fmt.Sprintf("%d", hostPort),
				"guest_port", fmt.Sprintf("%d", guestPort),
				"error", err.Error())
			return err
		}

		logger.Info("cli", "port added", "id", rule.ID,
			"host_port", fmt.Sprintf("%d", rule.HostPort),
			"guest", fmt.Sprintf("%s:%d", rule.GuestIP, rule.GuestPort),
			"proto", string(rule.Protocol))

		fmt.Printf("Port forward added: host:%d -> %s:%d (%s)\n",
			rule.HostPort, rule.GuestIP, rule.GuestPort, rule.Protocol)
		if rule.Description != "" {
			fmt.Printf("Description: %s\n", rule.Description)
		}
		if len(rule.Tags) > 0 {
			fmt.Printf("Tags: %s\n", strings.Join(rule.Tags, ", "))
		}
		return nil
	},
}

var portListCmd = &cobra.Command{
	Use:     "list <vm-id>",
	Short:   "List port forwarding rules for a VM",
	Aliases: []string{"ls"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		sortField, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		searchRaw, _ := cmd.Flags().GetString("search")
		tagRaw, _ := cmd.Flags().GetString("tag")
		protocolRaw, _ := cmd.Flags().GetString("protocol")
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		sortField = strings.TrimSpace(strings.ToLower(sortField))
		order = strings.TrimSpace(strings.ToLower(order))
		searchFilter := strings.ToLower(strings.TrimSpace(searchRaw))
		tagFilter := strings.ToLower(strings.TrimSpace(tagRaw))
		protocolFilter := types.Protocol(strings.ToLower(strings.TrimSpace(protocolRaw)))
		if protocolFilter != "" && protocolFilter != types.ProtocolTCP && protocolFilter != types.ProtocolUDP {
			return fmt.Errorf("invalid --protocol %q: must be 'tcp' or 'udp'", protocolRaw)
		}
		if sortField == "" {
			sortField = types.PortForwardSortID
		}
		switch sortField {
		case types.PortForwardSortID,
			types.PortForwardSortHostPort,
			types.PortForwardSortGuestPort,
			types.PortForwardSortProtocol,
			types.PortForwardSortDescription:
		default:
			return fmt.Errorf("invalid --sort %q: must be one of id, host_port, guest_port, protocol, description", sortField)
		}
		if order == "" {
			order = types.SortOrderAsc
		}
		switch order {
		case types.SortOrderAsc, types.SortOrderDesc:
		default:
			return fmt.Errorf("invalid --order %q: must be 'asc' or 'desc'", order)
		}
		limit, offset, err := normalizeLimitOffset(limit, offset)
		if err != nil {
			return err
		}

		logger.Info("cli", "port list", "vm_id", vmID, "sort", sortField, "order", order, "search", searchFilter, "tag", tagFilter, "protocol", string(protocolFilter), "limit", fmt.Sprintf("%d", limit), "offset", fmt.Sprintf("%d", offset))

		pf, cleanup, err := newPortForwarder()
		if err != nil {
			logger.Error("cli", "port list: failed to init port forwarder", "error", err.Error())
			return err
		}
		defer cleanup()

		ports, err := pf.List(vmID)
		if err != nil {
			logger.Error("cli", "port list failed", "vm_id", vmID, "error", err.Error())
			return err
		}
		if tagFilter != "" {
			filtered := ports[:0]
			for _, p := range ports {
				for _, tg := range p.Tags {
					if tg == tagFilter {
						filtered = append(filtered, p)
						break
					}
				}
			}
			ports = filtered
		}
		if protocolFilter != "" {
			filtered := ports[:0]
			for _, p := range ports {
				if p.Protocol == protocolFilter {
					filtered = append(filtered, p)
				}
			}
			ports = filtered
		}
		if searchFilter != "" {
			filtered := ports[:0]
			for _, p := range ports {
				if types.PortForwardMatchesSearch(p, searchFilter) {
					filtered = append(filtered, p)
				}
			}
			ports = filtered
		}
		types.SortPortForwards(ports, sortField, order)
		ports = paginateSlice(ports, limit, offset)
		logger.Info("cli", "port list result", "vm_id", vmID, "count", fmt.Sprintf("%d", len(ports)))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tHOST PORT\tGUEST\tPROTOCOL\tDESCRIPTION\tTAGS")
		for _, p := range ports {
			fmt.Fprintf(w, "%s\t%d\t%s:%d\t%s\t%s\t%s\n",
				p.ID, p.HostPort, p.GuestIP, p.GuestPort, p.Protocol, p.Description, strings.Join(p.Tags, ","))
		}
		w.Flush()
		return nil
	},
}

var portEditCmd = &cobra.Command{
	Use:   "edit <port-forward-id>",
	Short: "Edit metadata on an existing port forwarding rule",
	Long: `Edit free-form metadata on an existing port forwarding rule.

Currently editable fields: --description (free-form label, max 256 chars;
pass "" to clear), --tag (repeatable, replaces the entire tag set), and
--clear-tags (drops every tag from the rule; mutually exclusive with --tag).

At least one of --description / --tag / --clear-tags must be supplied.
The 5-tuple driving the iptables rule (host_port/guest_port/guest_ip/
protocol) is intentionally immutable — to change any of those, delete the
rule and re-add it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		portID := args[0]

		descFlag := cmd.Flags().Lookup("description")
		tagFlag := cmd.Flags().Lookup("tag")
		clearTagsFlag := cmd.Flags().Lookup("clear-tags")

		descChanged := descFlag != nil && descFlag.Changed
		tagChanged := tagFlag != nil && tagFlag.Changed
		clearTagsChanged := clearTagsFlag != nil && clearTagsFlag.Changed

		if tagChanged && clearTagsChanged {
			return fmt.Errorf("--tag and --clear-tags are mutually exclusive")
		}
		if !descChanged && !tagChanged && !clearTagsChanged {
			return fmt.Errorf("at least one of --description, --tag, or --clear-tags is required")
		}

		opts := network.UpdateOptions{}

		var descPtr *string
		if descChanged {
			descRaw, _ := cmd.Flags().GetString("description")
			desc := strings.TrimSpace(descRaw)
			if len(desc) > cliMaxPortForwardDescriptionLength {
				return fmt.Errorf("description must be at most %d characters", cliMaxPortForwardDescriptionLength)
			}
			descPtr = &desc
			opts.Description = descPtr
		}

		var tagsPtr *[]string
		if tagChanged {
			tagsRaw, _ := cmd.Flags().GetStringSlice("tag")
			normalized, err := validate.NormalizeTags(tagsRaw)
			if err != nil {
				return err
			}
			if normalized == nil {
				normalized = []string{}
			}
			tagsPtr = &normalized
			opts.Tags = tagsPtr
		} else if clearTagsChanged {
			empty := []string{}
			tagsPtr = &empty
			opts.Tags = tagsPtr
		}

		logger.Info("cli", "port edit", "id", portID,
			"description_changed", fmt.Sprintf("%v", descChanged),
			"tags_changed", fmt.Sprintf("%v", tagChanged || clearTagsChanged))

		pf, cleanup, err := newPortForwarder()
		if err != nil {
			logger.Error("cli", "port edit: failed to init port forwarder", "error", err.Error())
			return err
		}
		defer cleanup()

		updated, err := pf.Update(portID, opts)
		if err != nil {
			logger.Error("cli", "port edit failed", "id", portID, "error", err.Error())
			return err
		}

		logger.Info("cli", "port edited", "id", updated.ID)
		fmt.Printf("Port forward %s updated (host:%d -> %s:%d/%s)\n",
			updated.ID, updated.HostPort, updated.GuestIP, updated.GuestPort, updated.Protocol)
		if descChanged {
			if updated.Description != "" {
				fmt.Printf("Description: %s\n", updated.Description)
			} else {
				fmt.Println("Description cleared")
			}
		}
		if tagChanged || clearTagsChanged {
			if len(updated.Tags) > 0 {
				fmt.Printf("Tags: %s\n", strings.Join(updated.Tags, ", "))
			} else {
				fmt.Println("Tags cleared")
			}
		}
		return nil
	},
}

var portRemoveCmd = &cobra.Command{
	Use:   "remove [<port-forward-id>]",
	Short: "Remove a port forwarding rule (or many via --vm)",
	Long: `Remove port forwarding rules.

Single delete:    vmsmith port remove <port-forward-id>
Bulk by VM:       vmsmith port remove --vm <vm-id>
Bulk by protocol: vmsmith port remove --vm <vm-id> --protocol tcp
The positional <port-forward-id> and --vm are mutually exclusive.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID, _ := cmd.Flags().GetString("vm")
		proto, _ := cmd.Flags().GetString("protocol")
		vmID = strings.TrimSpace(vmID)
		proto = strings.TrimSpace(strings.ToLower(proto))

		if len(args) == 0 && vmID == "" {
			return fmt.Errorf("either a port-forward-id positional or --vm <vm-id> is required")
		}
		if len(args) > 0 && vmID != "" {
			return fmt.Errorf("port-forward-id and --vm are mutually exclusive")
		}
		if proto != "" && vmID == "" {
			return fmt.Errorf("--protocol requires --vm")
		}
		if proto != "" && proto != "tcp" && proto != "udp" {
			return fmt.Errorf("--protocol must be 'tcp' or 'udp'")
		}

		pf, cleanup, err := newPortForwarder()
		if err != nil {
			logger.Error("cli", "port remove: failed to init port forwarder", "error", err.Error())
			return err
		}
		defer cleanup()

		if len(args) > 0 {
			portID := args[0]
			logger.Info("cli", "port remove", "id", portID)
			if err := pf.Remove(portID); err != nil {
				logger.Error("cli", "port remove failed", "id", portID, "error", err.Error())
				return err
			}
			logger.Info("cli", "port removed", "id", portID)
			fmt.Printf("Port forward %s removed\n", portID)
			return nil
		}

		// Bulk path: enumerate the VM's port forwards (optionally filtered
		// by protocol) and remove each one, printing per-rule status. We
		// reuse PortForwarder.Remove rather than going through the API
		// bulk_delete endpoint so vmsmith CLI works without a running
		// daemon (the existing port commands behave the same way).
		ports, err := pf.List(vmID)
		if err != nil {
			logger.Error("cli", "port remove: list failed", "vm_id", vmID, "error", err.Error())
			return err
		}
		matches := make([]*types.PortForward, 0, len(ports))
		for _, p := range ports {
			if proto != "" && string(p.Protocol) != proto {
				continue
			}
			matches = append(matches, p)
		}
		if len(matches) == 0 {
			if proto != "" {
				fmt.Printf("No port forwards on VM %s match protocol %q\n", vmID, proto)
			} else {
				fmt.Printf("No port forwards on VM %s\n", vmID)
			}
			return nil
		}

		successes := 0
		failures := 0
		for _, p := range matches {
			if err := pf.Remove(p.ID); err != nil {
				fmt.Printf("FAIL  %s (%d/%s): %s\n", p.ID, p.HostPort, p.Protocol, err.Error())
				failures++
				continue
			}
			fmt.Printf("OK    %s (%d/%s)\n", p.ID, p.HostPort, p.Protocol)
			successes++
		}
		logger.Info("cli", "port bulk remove complete",
			"vm_id", vmID, "protocol", proto,
			"matched", fmt.Sprintf("%d", len(matches)),
			"success", fmt.Sprintf("%d", successes),
			"failed", fmt.Sprintf("%d", failures))
		if failures > 0 {
			return fmt.Errorf("%d of %d port forwards failed to delete", failures, len(matches))
		}
		return nil
	},
}

func init() {
	portAddCmd.Flags().Int("host", 0, "host port (required)")
	portAddCmd.Flags().Int("guest", 0, "guest port (required)")
	portAddCmd.Flags().String("proto", "tcp", "protocol (tcp or udp)")
	portAddCmd.Flags().String("description", "", "free-form label for the rule (max 256 chars)")
	portAddCmd.Flags().StringSlice("tag", nil, "tag (repeatable; lowercased, deduplicated, alphabetised; 1-32 chars, alphanumeric/._:-)")
	portAddCmd.MarkFlagRequired("host")
	portAddCmd.MarkFlagRequired("guest")

	portListCmd.Flags().String("sort", types.PortForwardSortID, "sort field: id, host_port, guest_port, protocol, description")
	portListCmd.Flags().String("order", types.SortOrderAsc, "sort order: asc or desc")
	portListCmd.Flags().String("search", "", "case-insensitive substring filter across description, protocol, host_port, guest_port, guest_ip, and tags")
	portListCmd.Flags().String("tag", "", "filter by a single tag (case-insensitive exact match)")
	portListCmd.Flags().String("protocol", "", "filter by transport protocol: tcp or udp (case-insensitive; empty = no filter)")
	portListCmd.Flags().Int("limit", 0, "maximum number of port forwards to show (0 = no limit)")
	portListCmd.Flags().Int("offset", 0, "number of port forwards to skip before printing results")

	portRemoveCmd.Flags().String("vm", "", "delete every port forward on this VM (mutually exclusive with the positional id)")
	portRemoveCmd.Flags().String("protocol", "", "when --vm is set, only delete forwards with this protocol (tcp|udp)")

	portEditCmd.Flags().String("description", "", "free-form label for the rule (max 256 chars). Pass \"\" to clear.")
	portEditCmd.Flags().StringSlice("tag", nil, "replace the tag set with these tags (repeatable)")
	portEditCmd.Flags().Bool("clear-tags", false, "drop every tag from the rule (mutually exclusive with --tag)")

	portCmd.AddCommand(portAddCmd)
	portCmd.AddCommand(portListCmd)
	portCmd.AddCommand(portEditCmd)
	portCmd.AddCommand(portRemoveCmd)
}

// portForwarderOverride can be set in tests to bypass real config/store.
var portForwarderOverride func() (*network.PortForwarder, func(), error)

func newPortForwarder() (*network.PortForwarder, func(), error) {
	if portForwarderOverride != nil {
		return portForwarderOverride()
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, nil, err
	}

	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		return nil, nil, err
	}

	pf := network.NewPortForwarder(s)
	return pf, func() { s.Close() }, nil
}
