package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
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
		hostPort, _ := cmd.Flags().GetInt("host")
		guestPort, _ := cmd.Flags().GetInt("guest")
		proto, _ := cmd.Flags().GetString("proto")

		vmMgr, vmCleanup, err := newVMManager()
		if err != nil {
			return err
		}
		defer vmCleanup()

		// Get VM IP
		vm, err := vmMgr.Get(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("VM not found: %w", err)
		}
		if vm.IP == "" {
			return fmt.Errorf("VM %s does not have an IP yet; is it running?", vm.Name)
		}

		pf, cleanup, err := newPortForwarder()
		if err != nil {
			return err
		}
		defer cleanup()

		rule, err := pf.Add(args[0], hostPort, guestPort, vm.IP, types.Protocol(proto))
		if err != nil {
			return err
		}

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
		pf, cleanup, err := newPortForwarder()
		if err != nil {
			return err
		}
		defer cleanup()

		ports, err := pf.List(args[0])
		if err != nil {
			return err
		}

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
		pf, cleanup, err := newPortForwarder()
		if err != nil {
			return err
		}
		defer cleanup()

		if err := pf.Remove(args[0]); err != nil {
			return err
		}
		fmt.Printf("Port forward %s removed\n", args[0])
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

func newPortForwarder() (*network.PortForwarder, func(), error) {
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
