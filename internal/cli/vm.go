package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

var vmCmd = &cobra.Command{
	Use:   "vm",
	Short: "Manage virtual machines",
}

var vmCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create and start a new VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		image, _ := cmd.Flags().GetString("image")
		cpus, _ := cmd.Flags().GetInt("cpus")
		ram, _ := cmd.Flags().GetInt("ram")
		disk, _ := cmd.Flags().GetInt("disk")
		sshKey, _ := cmd.Flags().GetString("ssh-key")
		defaultUser, _ := cmd.Flags().GetString("default-user")
		cloudInit, _ := cmd.Flags().GetString("cloud-init")
		description, _ := cmd.Flags().GetString("description")
		tags, _ := cmd.Flags().GetStringSlice("tag")
		networkFlags, _ := cmd.Flags().GetStringSlice("network")
		natIP, _ := cmd.Flags().GetString("nat-ip")
		natGW, _ := cmd.Flags().GetString("nat-gw")

		logger.Info("cli", "vm create", "name", name, "image", image,
			"cpus", fmt.Sprintf("%d", cpus), "ram", fmt.Sprintf("%d", ram),
			"disk", fmt.Sprintf("%d", disk))

		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "vm create: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		// Parse --network flags into NetworkAttachment structs
		networks, err := parseNetworkFlags(networkFlags)
		if err != nil {
			logger.Error("cli", "vm create: invalid network flags", "error", err.Error())
			return err
		}

		spec := types.VMSpec{
			Name:          name,
			Image:         image,
			CPUs:          cpus,
			RAMMB:         ram,
			DiskGB:        disk,
			Description:   strings.TrimSpace(description),
			Tags:          normalizeTagsForCLI(tags),
			SSHPubKey:     sshKey,
			DefaultUser:   defaultUser,
			CloudInitFile: cloudInit,
			Networks:      networks,
			NatStaticIP:   natIP,
			NatGateway:    natGW,
		}

		result, err := mgr.Create(context.Background(), spec)
		if err != nil {
			logger.Error("cli", "vm create failed", "name", name, "error", err.Error())
			return fmt.Errorf("creating VM: %w", err)
		}

		logger.Info("cli", "vm created", "id", result.ID, "name", result.Name, "state", string(result.State))

		fmt.Printf("VM created successfully:\n")
		fmt.Printf("  ID:    %s\n", result.ID)
		fmt.Printf("  Name:  %s\n", result.Name)
		fmt.Printf("  State: %s\n", result.State)
		// Show the IP immediately.  When a static IP was pre-assigned the VM
		// record carries it in Spec.NatStaticIP before the interface is up.
		displayIP := result.IP
		if displayIP == "" && result.Spec.NatStaticIP != "" {
			if parsed, _, err := net.ParseCIDR(result.Spec.NatStaticIP); err == nil {
				displayIP = parsed.String()
			}
		}
		if displayIP != "" {
			fmt.Printf("  IP:    %s\n", displayIP)
		}
		if len(spec.Networks) > 0 {
			fmt.Printf("  Extra networks: %d attached\n", len(spec.Networks))
			for _, n := range spec.Networks {
				label := n.Name
				if label == "" {
					label = n.HostInterface
				}
				ip := "dhcp"
				if n.StaticIP != "" {
					ip = n.StaticIP
				}
				fmt.Printf("    - %s via %s (%s, %s)\n", label, n.HostInterface, n.Mode, ip)
			}
		}
		return nil
	},
}

var vmListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all VMs",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		tagFilter, _ := cmd.Flags().GetString("tag")
		tagFilter = strings.TrimSpace(strings.ToLower(tagFilter))
		logger.Info("cli", "vm list")
		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "vm list: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		vms, err := mgr.List(context.Background())
		if err != nil {
			logger.Error("cli", "vm list failed", "error", err.Error())
			return err
		}

		if tagFilter != "" {
			filtered := make([]*types.VM, 0, len(vms))
			for _, v := range vms {
				for _, tag := range v.Tags {
					if strings.EqualFold(tag, tagFilter) {
						filtered = append(filtered, v)
						break
					}
				}
			}
			vms = filtered
		}

		logger.Info("cli", "vm list result", "count", fmt.Sprintf("%d", len(vms)))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSTATE\tIP\tCPUS\tRAM (MB)\tTAGS")
		for _, v := range vms {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
				v.ID, v.Name, v.State, v.IP, v.Spec.CPUs, v.Spec.RAMMB, strings.Join(v.Tags, ","))
		}
		w.Flush()
		return nil
	},
}

var vmStartCmd = &cobra.Command{
	Use:   "start <id>",
	Short: "Start a stopped VM",
	Args:  validateBulkVMActionArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBulkVMAction(cmd, args, "start", types.VMStateStopped, func(ctx context.Context, mgr vm.Manager, id string) error {
			return mgr.Start(ctx, id)
		})
	},
}

var vmStopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop a running VM",
	Args:  validateBulkVMActionArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBulkVMAction(cmd, args, "stop", types.VMStateRunning, func(ctx context.Context, mgr vm.Manager, id string) error {
			return mgr.Stop(ctx, id)
		})
	},
}

var vmDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a VM and its resources",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		logger.Info("cli", "vm delete", "id", id)
		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "vm delete: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		if err := mgr.Delete(context.Background(), id); err != nil {
			logger.Error("cli", "vm delete failed", "id", id, "error", err.Error())
			return err
		}
		logger.Info("cli", "vm deleted", "id", id)
		fmt.Printf("VM %s deleted\n", id)
		return nil
	},
}

var vmEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Edit VM resources (CPU, RAM, disk). VM is stopped, updated, then restarted.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		cpus, _ := cmd.Flags().GetInt("cpus")
		ram, _ := cmd.Flags().GetInt("ram")
		disk, _ := cmd.Flags().GetInt("disk")
		description, _ := cmd.Flags().GetString("description")
		tags, _ := cmd.Flags().GetStringSlice("tag")
		natIP, _ := cmd.Flags().GetString("nat-ip")
		natGW, _ := cmd.Flags().GetString("nat-gw")

		if cpus == 0 && ram == 0 && disk == 0 && natIP == "" && description == "" && len(tags) == 0 {
			return fmt.Errorf("specify at least one of --cpus, --ram, --disk, --nat-ip, --description, or --tag")
		}

		logger.Info("cli", "vm edit", "id", id, "cpus", fmt.Sprintf("%d", cpus),
			"ram", fmt.Sprintf("%d", ram), "disk", fmt.Sprintf("%d", disk), "nat_ip", natIP)

		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "vm edit: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		patch := types.VMUpdateSpec{
			CPUs:        cpus,
			RAMMB:       ram,
			DiskGB:      disk,
			Description: strings.TrimSpace(description),
			Tags:        nil,
			NatStaticIP: natIP,
			NatGateway:  natGW,
		}
		if cmd.Flags().Changed("tag") {
			patch.Tags = normalizeTagsForCLI(tags)
		}

		result, err := mgr.Update(context.Background(), id, patch)
		if err != nil {
			logger.Error("cli", "vm edit failed", "id", id, "error", err.Error())
			return fmt.Errorf("updating VM: %w", err)
		}

		logger.Info("cli", "vm updated", "id", result.ID, "cpus", fmt.Sprintf("%d", result.Spec.CPUs),
			"ram", fmt.Sprintf("%d", result.Spec.RAMMB), "disk", fmt.Sprintf("%d", result.Spec.DiskGB))

		fmt.Printf("VM updated successfully:\n")
		fmt.Printf("  ID:    %s\n", result.ID)
		fmt.Printf("  Name:  %s\n", result.Name)
		fmt.Printf("  State: %s\n", result.State)
		fmt.Printf("  CPUs:  %d\n", result.Spec.CPUs)
		fmt.Printf("  RAM:   %d MB\n", result.Spec.RAMMB)
		fmt.Printf("  Disk:  %d GB\n", result.Spec.DiskGB)
		if result.Description != "" {
			fmt.Printf("  Desc:  %s\n", result.Description)
		}
		if len(result.Tags) > 0 {
			fmt.Printf("  Tags:  %s\n", strings.Join(result.Tags, ", "))
		}
		if result.IP != "" {
			fmt.Printf("  IP:    %s\n", result.IP)
		}
		return nil
	},
}

var vmInfoCmd = &cobra.Command{
	Use:   "info <id>",
	Short: "Show detailed VM information",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		logger.Info("cli", "vm info", "id", id)
		mgr, cleanup, err := newVMManager()
		if err != nil {
			logger.Error("cli", "vm info: failed to init VM manager", "error", err.Error())
			return err
		}
		defer cleanup()

		v, err := mgr.Get(context.Background(), id)
		if err != nil {
			logger.Error("cli", "vm info failed", "id", id, "error", err.Error())
			return err
		}

		logger.Info("cli", "vm info result", "id", v.ID, "name", v.Name, "state", string(v.State))

		fmt.Printf("ID:           %s\n", v.ID)
		fmt.Printf("Name:         %s\n", v.Name)
		fmt.Printf("State:        %s\n", v.State)
		fmt.Printf("IP:           %s\n", v.IP)
		fmt.Printf("CPUs:         %d\n", v.Spec.CPUs)
		fmt.Printf("RAM:          %d MB\n", v.Spec.RAMMB)
		fmt.Printf("Disk:         %d GB\n", v.Spec.DiskGB)
		fmt.Printf("Image:        %s\n", v.Spec.Image)
		if v.Description != "" {
			fmt.Printf("Description:  %s\n", v.Description)
		}
		if len(v.Tags) > 0 {
			fmt.Printf("Tags:         %s\n", strings.Join(v.Tags, ", "))
		}
		sshUser := v.Spec.DefaultUser
		if sshUser == "" {
			sshUser = "root"
		}
		fmt.Printf("Default User: %s\n", sshUser)
		if v.IP != "" {
			fmt.Printf("SSH:          ssh %s@%s\n", sshUser, v.IP)
		}
		fmt.Printf("Disk Path:    %s\n", v.DiskPath)
		fmt.Printf("Created:      %s\n", v.CreatedAt.Format("2006-01-02 15:04:05"))
		return nil
	},
}

func init() {
	vmCreateCmd.Flags().String("image", "", "base image name or absolute path to a .qcow2 file (required)")
	vmCreateCmd.Flags().Int("cpus", 0, "number of vCPUs (default from config)")
	vmCreateCmd.Flags().Int("ram", 0, "RAM in MB (default from config)")
	vmCreateCmd.Flags().Int("disk", 0, "disk size in GB (default from config)")
	vmCreateCmd.Flags().String("ssh-key", "", "SSH public key to inject")
	vmCreateCmd.Flags().String("default-user", "", "create a named sudo user and disable root (omit to use root by default)")
	vmCreateCmd.Flags().String("cloud-init", "", "path to cloud-init user-data file")
	vmCreateCmd.Flags().String("description", "", "free-form VM description")
	vmCreateCmd.Flags().StringSlice("tag", nil, "tag to apply to the VM (repeatable)")
	vmCreateCmd.Flags().String("nat-ip", "",
		"static IP for the primary NAT interface in CIDR notation (e.g. 192.168.100.50/24); leave empty for DHCP")
	vmCreateCmd.Flags().String("nat-gw", "",
		"gateway for --nat-ip (e.g. 192.168.100.1); required when --nat-ip is set")
	vmCreateCmd.Flags().StringSlice("network", nil,
		`attach VM to host network (repeatable)
Format: iface[:key=val,...]
  iface         host interface name (required)
  mode=macvtap  attachment mode: macvtap (default) or bridge
  ip=CIDR       static IP (e.g. 192.168.1.100/24), omit for DHCP
  gw=IP         gateway for static IP
  name=LABEL    friendly label
  mac=ADDR      specific MAC address
  bridge=NAME   bridge name (bridge mode only)

Examples:
  --network eth1
  --network eth2:ip=192.168.2.100/24,gw=192.168.2.1
  --network eth3:mode=bridge,bridge=br-storage
  --network eth1 --network eth2 --network eth3`)
	vmCreateCmd.MarkFlagRequired("image")

	vmEditCmd.Flags().Int("cpus", 0, "new vCPU count (0 = no change)")
	vmEditCmd.Flags().Int("ram", 0, "new RAM in MB (0 = no change)")
	vmEditCmd.Flags().Int("disk", 0, "new disk size in GB — can only grow (0 = no change)")
	vmEditCmd.Flags().String("description", "", "new VM description")
	vmEditCmd.Flags().StringSlice("tag", nil, "replace VM tags with the provided values (repeatable)")
	vmEditCmd.Flags().String("nat-ip", "", "new static IP for the primary NAT interface in CIDR notation (e.g. 192.168.100.50/24)")
	vmEditCmd.Flags().String("nat-gw", "", "gateway for --nat-ip; defaults to subnet gateway when omitted")

	vmListCmd.Flags().String("tag", "", "filter VMs by tag")
	vmStartCmd.Flags().Bool("all", false, "start all stopped VMs")
	vmStartCmd.Flags().String("tag", "", "limit --all to VMs with the given tag")
	vmStopCmd.Flags().Bool("all", false, "stop all running VMs")
	vmStopCmd.Flags().String("tag", "", "limit --all to VMs with the given tag")

	vmCmd.AddCommand(vmCreateCmd)
	vmCmd.AddCommand(vmEditCmd)
	vmCmd.AddCommand(vmListCmd)
	vmCmd.AddCommand(vmStartCmd)
	vmCmd.AddCommand(vmStopCmd)
	vmCmd.AddCommand(vmDeleteCmd)
	vmCmd.AddCommand(vmInfoCmd)
}

// vmManagerOverride can be set in tests to bypass libvirt.
var vmManagerOverride func() (vm.Manager, func(), error)

// newVMManager is a helper that sets up config, store, and libvirt manager
// for direct CLI usage (non-daemon mode).
func newVMManager() (vm.Manager, func(), error) {
	if vmManagerOverride != nil {
		return vmManagerOverride()
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, err
	}

	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening store: %w", err)
	}

	mgr, err := vm.NewLibvirtManager(cfg, s)
	if err != nil {
		s.Close()
		return nil, nil, fmt.Errorf("connecting to libvirt: %w", err)
	}

	cleanup := func() {
		mgr.Close()
		s.Close()
	}

	return mgr, cleanup, nil
}

// parseNetworkFlags parses --network flag values into NetworkAttachment structs.
//
// Format: "iface[:key=val,key=val,...]"
//
// Examples:
//
//	"eth1"                                      → macvtap on eth1, DHCP
//	"eth2:ip=192.168.2.100/24,gw=192.168.2.1"  → macvtap on eth2, static IP
//	"eth3:mode=bridge,bridge=br-data"           → bridge mode
//	"eth1:name=data-net,mac=52:54:00:aa:bb:cc"  → named, specific MAC
func normalizeTagsForCLI(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(strings.ToLower(tag))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func parseNetworkFlags(flags []string) ([]types.NetworkAttachment, error) {
	var result []types.NetworkAttachment

	for _, flag := range flags {
		net := types.NetworkAttachment{
			Mode: types.NetworkModeMacvtap, // default
		}

		// Split "eth1:key=val,key=val" into interface and options
		parts := strings.SplitN(flag, ":", 2)
		net.HostInterface = strings.TrimSpace(parts[0])
		if net.HostInterface == "" {
			return nil, fmt.Errorf("--network: interface name is required (got %q)", flag)
		}

		// Default label to interface name
		net.Name = net.HostInterface

		// Parse key=value options
		if len(parts) > 1 && parts[1] != "" {
			opts := strings.Split(parts[1], ",")
			for _, opt := range opts {
				kv := strings.SplitN(opt, "=", 2)
				if len(kv) != 2 {
					return nil, fmt.Errorf("--network: invalid option %q (expected key=value)", opt)
				}
				key, val := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])

				switch key {
				case "mode":
					switch val {
					case "macvtap":
						net.Mode = types.NetworkModeMacvtap
					case "bridge":
						net.Mode = types.NetworkModeBridge
					default:
						return nil, fmt.Errorf("--network: unknown mode %q (use macvtap or bridge)", val)
					}
				case "ip":
					net.StaticIP = val
				case "gw", "gateway":
					net.Gateway = val
				case "name":
					net.Name = val
				case "mac":
					net.MacAddress = val
				case "bridge":
					net.Bridge = val
				default:
					return nil, fmt.Errorf("--network: unknown option %q", key)
				}
			}
		}

		result = append(result, net)
	}

	return result, nil
}

func validateBulkVMActionArgs(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	if all {
		if len(args) != 0 {
			return fmt.Errorf("cannot specify a VM id when using --all")
		}
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("accepts 1 arg(s), received %d", len(args))
	}
	return nil
}

func runBulkVMAction(cmd *cobra.Command, args []string, verb string, requiredState types.VMState, action func(context.Context, vm.Manager, string) error) error {
	all, _ := cmd.Flags().GetBool("all")
	tagFilter, _ := cmd.Flags().GetString("tag")
	tagFilter = strings.TrimSpace(strings.ToLower(tagFilter))

	logger.Info("cli", "vm "+verb, "all", fmt.Sprintf("%t", all), "tag", tagFilter)
	mgr, cleanup, err := newVMManager()
	if err != nil {
		logger.Error("cli", "vm "+verb+": failed to init VM manager", "error", err.Error())
		return err
	}
	defer cleanup()

	ctx := context.Background()
	if !all {
		id := args[0]
		if err := action(ctx, mgr, id); err != nil {
			logger.Error("cli", "vm "+verb+" failed", "id", id, "error", err.Error())
			return err
		}
		logger.Info("cli", "vm action complete", "action", verb, "id", id)
		fmt.Printf("VM %s %sed\n", id, verb)
		return nil
	}

	vms, err := mgr.List(ctx)
	if err != nil {
		logger.Error("cli", "vm "+verb+": list failed", "error", err.Error())
		return err
	}

	matched := make([]*types.VM, 0, len(vms))
	for _, candidate := range vms {
		if tagFilter != "" {
			matchedTag := false
			for _, tag := range candidate.Tags {
				if strings.EqualFold(tag, tagFilter) {
					matchedTag = true
					break
				}
			}
			if !matchedTag {
				continue
			}
		}
		if candidate.State != requiredState {
			continue
		}
		matched = append(matched, candidate)
	}

	adjective := verb + "able"
	if verb == "stop" {
		adjective = "stoppable"
	}
	if len(matched) == 0 {
		if tagFilter != "" {
			fmt.Printf("No %s VMs matched tag %q\n", adjective, tagFilter)
		} else {
			fmt.Printf("No %s VMs found\n", adjective)
		}
		return nil
	}

	completed := make([]string, 0, len(matched))
	for _, machine := range matched {
		if err := action(ctx, mgr, machine.ID); err != nil {
			logger.Error("cli", "vm bulk "+verb+" failed", "id", machine.ID, "error", err.Error())
			return fmt.Errorf("%s VM %s: %w", verb, machine.ID, err)
		}
		completed = append(completed, machine.ID)
	}

	logger.Info("cli", "vm bulk action complete", "action", verb, "count", fmt.Sprintf("%d", len(completed)))
	label := map[string]string{"start": "Started", "stop": "Stopped"}[verb]
	if label == "" {
		label = strings.ToUpper(verb[:1]) + verb[1:] + "ed"
	}
	fmt.Printf("%s %d VM(s): %s\n", label, len(completed), strings.Join(completed, ", "))
	return nil
}
