package cli

import (
	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "vmsmith",
	Short: "vmSmith — lightweight VM provisioning and management",
	Long: `vmSmith is a CLI tool and daemon for provisioning and managing
QEMU/KVM virtual machines on Linux hosts.

It supports VM lifecycle management, snapshots, image distribution,
NAT networking with port forwarding, and a REST API for integration.`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.vmsmith/config.yaml)")

	rootCmd.AddCommand(vmCmd)
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(imageCmd)
	rootCmd.AddCommand(portCmd)
	rootCmd.AddCommand(netCmd)
	rootCmd.AddCommand(daemonCmd)
}
