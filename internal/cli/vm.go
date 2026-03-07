package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
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
		cloudInit, _ := cmd.Flags().GetString("cloud-init")
		networkFlags, _ := cmd.Flags().GetStringSlice("network")

		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		// Parse --network flags into NetworkAttachment structs
		networks, err := parseNetworkFlags(networkFlags)
		if err != nil {
			return err
		}

		spec := types.VMSpec{
			Name:          name,
			Image:         image,
			CPUs:          cpus,
			RAMMB:         ram,
			DiskGB:        disk,
			SSHPubKey:     sshKey,
			CloudInitFile: cloudInit,
			Networks:      networks,
		}

		result, err := mgr.Create(context.Background(), spec)
		if err != nil {
			return fmt.Errorf("creating VM: %w", err)
		}

		fmt.Printf("VM created successfully:\n")
		fmt.Printf("  ID:    %s\n", result.ID)
		fmt.Printf("  Name:  %s\n", result.Name)
		fmt.Printf("  State: %s\n", result.State)
		if result.IP != "" {
			fmt.Printf("  IP:    %s\n", result.IP)
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
	Use:   "list",
	Short: "List all VMs",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		vms, err := mgr.List(context.Background())
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSTATE\tIP\tCPUS\tRAM (MB)")
		for _, v := range vms {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\n",
				v.ID, v.Name, v.State, v.IP, v.Spec.CPUs, v.Spec.RAMMB)
		}
		w.Flush()
		return nil
	},
}

var vmStartCmd = &cobra.Command{
	Use:   "start <id>",
	Short: "Start a stopped VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := mgr.Start(context.Background(), args[0]); err != nil {
			return err
		}
		fmt.Printf("VM %s started\n", args[0])
		return nil
	},
}

var vmStopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop a running VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := mgr.Stop(context.Background(), args[0]); err != nil {
			return err
		}
		fmt.Printf("VM %s stopped\n", args[0])
		return nil
	},
}

var vmDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a VM and its resources",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := mgr.Delete(context.Background(), args[0]); err != nil {
			return err
		}
		fmt.Printf("VM %s deleted\n", args[0])
		return nil
	},
}

var vmInfoCmd = &cobra.Command{
	Use:   "info <id>",
	Short: "Show detailed VM information",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, cleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer cleanup()

		v, err := mgr.Get(context.Background(), args[0])
		if err != nil {
			return err
		}

		fmt.Printf("ID:        %s\n", v.ID)
		fmt.Printf("Name:      %s\n", v.Name)
		fmt.Printf("State:     %s\n", v.State)
		fmt.Printf("IP:        %s\n", v.IP)
		fmt.Printf("CPUs:      %d\n", v.Spec.CPUs)
		fmt.Printf("RAM:       %d MB\n", v.Spec.RAMMB)
		fmt.Printf("Disk:      %d GB\n", v.Spec.DiskGB)
		fmt.Printf("Image:     %s\n", v.Spec.Image)
		fmt.Printf("Disk Path: %s\n", v.DiskPath)
		fmt.Printf("Created:   %s\n", v.CreatedAt.Format("2006-01-02 15:04:05"))
		return nil
	},
}

func init() {
	vmCreateCmd.Flags().String("image", "", "base image name (required)")
	vmCreateCmd.Flags().Int("cpus", 0, "number of vCPUs (default from config)")
	vmCreateCmd.Flags().Int("ram", 0, "RAM in MB (default from config)")
	vmCreateCmd.Flags().Int("disk", 0, "disk size in GB (default from config)")
	vmCreateCmd.Flags().String("ssh-key", "", "SSH public key to inject")
	vmCreateCmd.Flags().String("cloud-init", "", "path to cloud-init user-data file")
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

	vmCmd.AddCommand(vmCreateCmd)
	vmCmd.AddCommand(vmListCmd)
	vmCmd.AddCommand(vmStartCmd)
	vmCmd.AddCommand(vmStopCmd)
	vmCmd.AddCommand(vmDeleteCmd)
	vmCmd.AddCommand(vmInfoCmd)
}

// newVMManager is a helper that sets up config, store, and libvirt manager
// for direct CLI usage (non-daemon mode).
func newVMManager() (vm.Manager, func(), error) {
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
