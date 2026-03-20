package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/network"
)

var netCmd = &cobra.Command{
	Use:   "net",
	Short: "Network utilities",
}

var netListIfacesCmd = &cobra.Command{
	Use:     "interfaces",
	Short:   "List host network interfaces available for VM attachment",
	Aliases: []string{"ifaces", "if"},
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		logger.Info("cli", "net interfaces", "all", fmt.Sprintf("%v", all))

		ifaces, err := network.DiscoverInterfaces()
		if err != nil {
			logger.Error("cli", "net interfaces failed", "error", err.Error())
			return err
		}
		logger.Info("cli", "net interfaces result", "count", fmt.Sprintf("%d", len(ifaces)))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "INTERFACE\tIPs\tMAC\tSTATUS\tTYPE")
		for _, iface := range ifaces {
			if !all && !iface.IsUp {
				continue
			}

			status := "DOWN"
			if iface.IsUp {
				status = "UP"
			}

			ifType := "virtual"
			if iface.IsPhys {
				ifType = "physical"
			}

			ips := "-"
			if len(iface.IPs) > 0 {
				ips = strings.Join(iface.IPs, ", ")
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				iface.Name, ips, iface.MAC, status, ifType)
		}
		w.Flush()

		fmt.Println()
		fmt.Println("Use --network <interface> with 'vmsmith vm create' to attach VMs to these networks.")
		fmt.Println("Example: vmsmith vm create myvm --image ubuntu --network eth1 --network eth2:ip=192.168.2.50/24")
		return nil
	},
}

func init() {
	netListIfacesCmd.Flags().Bool("all", false, "show all interfaces including DOWN")

	netCmd.AddCommand(netListIfacesCmd)
}
