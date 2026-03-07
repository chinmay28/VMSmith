package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/daemon"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the vmSmith daemon",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the vmSmith daemon (REST API server)",
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetString("port")

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if port != "" {
			cfg.Daemon.Listen = "0.0.0.0:" + port
		}

		if err := cfg.EnsureDirs(); err != nil {
			return err
		}

		d, err := daemon.New(cfg)
		if err != nil {
			return fmt.Errorf("initializing daemon: %w", err)
		}

		return d.Run()
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the vmSmith daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		return daemon.Stop(cfg.Daemon.PIDFile)
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		running, pid := daemon.Status(cfg.Daemon.PIDFile)
		if running {
			fmt.Printf("vmSmith daemon is running (PID %d)\n", pid)
		} else {
			fmt.Println("vmSmith daemon is not running")
		}
		return nil
	},
}

func init() {
	daemonStartCmd.Flags().String("port", "", "listen port (default: 8080)")

	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
}
