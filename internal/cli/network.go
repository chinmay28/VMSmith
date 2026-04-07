package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

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

		logger.Info("cli", "port add", "vm_id", vmID,
			"host_port", fmt.Sprintf("%d", hostPort),
			"guest_port", fmt.Sprintf("%d", guestPort),
			"proto", proto)

		if err := types.ValidatePortForward(hostPort, guestPort, types.Protocol(proto)); err != nil {
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

		rule, err := pf.Add(vmID, hostPort, guestPort, vm.IP, types.Protocol(proto))
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
		logger.Info("cli", "port list", "vm_id", vmID)

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
		logger.Info("cli", "port list result", "vm_id", vmID, "count", fmt.Sprintf("%d", len(ports)))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tHOST PORT\tGUEST\tPROTOCOL")
		for _, p := range ports {
			fmt.Fprintf(w, "%s\t%d\t%s:%d\t%s\n",
				p.ID, p.HostPort, p.GuestIP, p.GuestPort, p.Protocol)
		}
		w.Flush()
		return nil
	},
}

var portRemoveCmd = &cobra.Command{
	Use:   "remove <port-forward-id>",
	Short: "Remove a port forwarding rule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		portID := args[0]
		logger.Info("cli", "port remove", "id", portID)

		pf, cleanup, err := newPortForwarder()
		if err != nil {
			logger.Error("cli", "port remove: failed to init port forwarder", "error", err.Error())
			return err
		}
		defer cleanup()

		if err := pf.Remove(portID); err != nil {
			logger.Error("cli", "port remove failed", "id", portID, "error", err.Error())
			return err
		}
		logger.Info("cli", "port removed", "id", portID)
		fmt.Printf("Port forward %s removed\n", portID)
		return nil
	},
}

func init() {
	portAddCmd.Flags().Int("host", 0, "host port (required)")
	portAddCmd.Flags().Int("guest", 0, "guest port (required)")
	portAddCmd.Flags().String("proto", "tcp", "protocol (tcp or udp)")
	portAddCmd.MarkFlagRequired("host")
	portAddCmd.MarkFlagRequired("guest")

	portCmd.AddCommand(portAddCmd)
	portCmd.AddCommand(portListCmd)
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
