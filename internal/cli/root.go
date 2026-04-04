package cli

import (
	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
)

var cfgFile string
var apiKey string

var rootCmd = &cobra.Command{
	Use:   "vmsmith",
	Short: "vmSmith — lightweight VM provisioning and management",
	Long: `vmSmith is a CLI tool and daemon for provisioning and managing
QEMU/KVM virtual machines on Linux hosts.

It supports VM lifecycle management, snapshots, image distribution,
NAT networking with port forwarding, and a REST API for integration.`,
	// Initialise the logger before any subcommand runs so that all CLI
	// operations are written to vmsmith.log.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			// Non-fatal; logger will fall back to stderr.
			_ = logger.Init("", logger.LevelInfo)
			return nil
		}
		_ = logger.Init(cfg.Daemon.LogFile, logger.LevelInfo)
		return nil
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.vmsmith/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&apiKey, "api-key", "", "API key for authenticated remote daemon HTTP requests")

	rootCmd.AddCommand(vmCmd)
	rootCmd.AddCommand(snapshotCmd)
	rootCmd.AddCommand(imageCmd)
	rootCmd.AddCommand(portCmd)
	rootCmd.AddCommand(netCmd)
	rootCmd.AddCommand(daemonCmd)
}
