package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/pkg/version"
)

var versionJSONOutput bool

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the vmsmith binary version and build info",
	Long: `Print the version, commit, and build date that the binary was compiled with.

Use --json to emit a machine-readable BuildInfo payload that matches the
GET /api/version response.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		info := version.Info()
		if versionJSONOutput {
			out, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "vmsmith %s\n", info.Version)
		fmt.Fprintf(cmd.OutOrStdout(), "  commit:     %s\n", info.Commit)
		fmt.Fprintf(cmd.OutOrStdout(), "  built:      %s\n", info.BuildDate)
		fmt.Fprintf(cmd.OutOrStdout(), "  go:         %s\n", info.GoVersion)
		fmt.Fprintf(cmd.OutOrStdout(), "  platform:   %s/%s\n", info.OS, info.Arch)
		return nil
	},
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSONOutput, "json", false, "Output machine-readable JSON")
}
