package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// hostCmd hosts operator-facing "what's happening on this host" probes:
// a one-shot view of CPU/RAM/Disk utilisation (`host stats`) and the
// configured quota caps + current allocation (`host quotas`).  Both
// endpoints already power the GUI Dashboard cards — the CLI here is the
// scripting / cron parallel so ops can monitor capacity without
// scraping the web UI.
var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Inspect host capacity, utilisation, and quota usage",
	Long: `Operator probes for the daemon's host endpoints.

Both subcommands are thin HTTP clients (same shape as 'vmsmith logs list'
and 'vmsmith webhook list') so they work against a remote daemon via
--api-url and --api-key, and fall back to http://<daemon.listen> for the
local case.

  host stats   GET /api/v1/host/stats   — VM count, CPU/RAM/Disk usage,
                                          active SSE event-stream clients
  host quotas  GET /api/v1/quotas/usage — current allocation vs configured
                                          caps for vms / cpus / ram / disk`,
}

var hostStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show host capacity and utilisation (VM count, CPU/RAM/Disk, SSE clients)",
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL, _ := cmd.Flags().GetString("api-url")
		flagAPIKey, _ := cmd.Flags().GetString("api-key")
		asJSON, _ := cmd.Flags().GetBool("json")

		logger.Info("cli", "host stats", "api-url", apiURL)

		var stats types.HostStats
		body, err := hostGET(cmd, apiURL, flagAPIKey, "/api/v1/host/stats")
		if err != nil {
			return err
		}
		if asJSON {
			fmt.Println(strings.TrimSpace(string(body)))
			return nil
		}
		if err := json.Unmarshal(body, &stats); err != nil {
			return fmt.Errorf("decoding /host/stats response: %w", err)
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "RESOURCE\tUSED\tTOTAL\tPERCENT")
		fmt.Fprintf(tw, "VMs\t%d\t-\t-\n", stats.VMCount)
		fmt.Fprintf(tw, "CPU\t%d%%\t100%%\t%d%%\n", stats.CPU.Used, stats.CPU.Percentage)
		fmt.Fprintf(tw, "RAM\t%s\t%s\t%d%%\n",
			formatBytes(stats.RAM.Used), formatBytes(stats.RAM.Total), stats.RAM.Percentage)
		fmt.Fprintf(tw, "Disk\t%s\t%s\t%d%%\n",
			formatBytes(stats.Disk.Used), formatBytes(stats.Disk.Total), stats.Disk.Percentage)
		fmt.Fprintf(tw, "SSE clients\t%d\t-\t-\n", stats.EventStreamConnections)
		return tw.Flush()
	},
}

var hostQuotasCmd = &cobra.Command{
	Use:   "quotas",
	Short: "Show current quota allocation vs configured caps",
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL, _ := cmd.Flags().GetString("api-url")
		flagAPIKey, _ := cmd.Flags().GetString("api-key")
		asJSON, _ := cmd.Flags().GetBool("json")

		logger.Info("cli", "host quotas", "api-url", apiURL)

		body, err := hostGET(cmd, apiURL, flagAPIKey, "/api/v1/quotas/usage")
		if err != nil {
			return err
		}
		if asJSON {
			fmt.Println(strings.TrimSpace(string(body)))
			return nil
		}
		var usage types.QuotaUsage
		if err := json.Unmarshal(body, &usage); err != nil {
			return fmt.Errorf("decoding /quotas/usage response: %w", err)
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "QUOTA\tUSED\tLIMIT")
		fmt.Fprintf(tw, "VMs\t%d\t%s\n", usage.VMs.Used, quotaLimit(usage.VMs.Limit, ""))
		fmt.Fprintf(tw, "CPUs\t%d\t%s\n", usage.CPUs.Used, quotaLimit(usage.CPUs.Limit, "vCPU"))
		fmt.Fprintf(tw, "RAM\t%d MB\t%s\n", usage.RAMMB.Used, quotaLimit(usage.RAMMB.Limit, "MB"))
		fmt.Fprintf(tw, "Disk\t%d GB\t%s\n", usage.DiskGB.Used, quotaLimit(usage.DiskGB.Limit, "GB"))
		fmt.Fprintf(tw, "GPUs\t%d\t%s\n", usage.GPUs.Used, quotaLimit(usage.GPUs.Limit, ""))
		return tw.Flush()
	},
}

var hostGPUsCmd = &cobra.Command{
	Use:   "gpus",
	Short: "List host GPUs (PCI display controllers) available for passthrough",
	Long: `List the host's PCI display controllers (GPUs) with their IOMMU group
membership so you can pick which device(s) to pass through to a VM via
'vmsmith vm create --gpu <address>'.

The DRIVER column shows the kernel driver currently bound to the GPU:
"vfio-pci" means it is ready for passthrough; "nvidia"/"nouveau"/"amdgpu"
means the host still owns it (with managed='yes' passthrough libvirt will
rebind it to vfio-pci at VM start, provided it is not driving the host
console). BOOT_VGA marks the firmware-selected primary display adapter;
operators should avoid passing that device through unless they intentionally
want the host console to disappear. GROUP lists every device in the same
IOMMU group — vmsmith attaches the whole group together. See
 docs/GPU_PASSTHROUGH.md.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL, _ := cmd.Flags().GetString("api-url")
		flagAPIKey, _ := cmd.Flags().GetString("api-key")
		asJSON, _ := cmd.Flags().GetBool("json")

		logger.Info("cli", "host gpus", "api-url", apiURL)

		body, err := hostGET(cmd, apiURL, flagAPIKey, "/api/v1/host/gpus")
		if err != nil {
			return err
		}
		if asJSON {
			fmt.Println(strings.TrimSpace(string(body)))
			return nil
		}
		var gpus []types.GPUDevice
		if err := json.Unmarshal(body, &gpus); err != nil {
			return fmt.Errorf("decoding /host/gpus response: %w", err)
		}
		if len(gpus) == 0 {
			fmt.Println("No GPUs (PCI display controllers) found on the host.")
			return nil
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ADDRESS\tVENDOR\tDEVICE\tDRIVER\tBOOT_VGA\tIOMMU\tGROUP")
		for _, g := range gpus {
			driver := g.Driver
			if driver == "" {
				driver = "-"
			}
			bootVGA := "-"
			if g.BootVGA {
				bootVGA = "yes"
			}
			group := strings.Join(g.GroupDevices, " ")
			if group == "" {
				group = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
				g.Address, g.Vendor, g.DeviceID, driver, bootVGA, g.IOMMUGroup, group)
		}
		return tw.Flush()
	},
}

// hostGET centralises the shared "build URL, set Authorization, parse
// daemon error envelope" plumbing.  Returns the raw response body so
// callers can either re-emit it under --json or unmarshal it into a
// typed struct for table rendering.
func hostGET(cmd *cobra.Command, apiURL, flagAPIKey, path string) ([]byte, error) {
	resolved := resolveAPIURL(apiURL)
	endpoint := strings.TrimRight(resolved, "/") + path

	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(flagAPIKey)
	if key == "" {
		key = strings.TrimSpace(apiKey)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// quotaLimit renders the configured quota cap as "unlimited" when the
// daemon reports 0 (the zero-value protobuf convention used by
// QuotaUsageSummary.Limit's omitempty tag — see pkg/types/quota.go).
func quotaLimit(limit int, unit string) string {
	if limit <= 0 {
		return "unlimited"
	}
	if unit == "" {
		return fmt.Sprintf("%d", limit)
	}
	return fmt.Sprintf("%d %s", limit, unit)
}

// formatBytes renders a byte count in the closest binary unit (KiB, MiB,
// GiB, TiB) with one decimal place.  Matches the GUI Dashboard's
// formatBytes shape so the CLI and web UI agree on units.
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffix := "KMGTPE"[exp]
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), suffix)
}

// hostListCmd lists every libvirt host this daemon manages (roadmap
// 5.5.4): the implicit local host plus each `hosts:` config entry, with
// the aggregate resources allocated to VMs placed on it.
var hostListCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed libvirt hosts with per-host resource allocation",
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL, _ := cmd.Flags().GetString("api-url")
		flagAPIKey, _ := cmd.Flags().GetString("api-key")
		asJSON, _ := cmd.Flags().GetBool("json")

		logger.Info("cli", "host list", "api-url", apiURL)

		body, err := hostGET(cmd, apiURL, flagAPIKey, "/api/v1/hosts")
		if err != nil {
			return err
		}
		if asJSON {
			fmt.Println(strings.TrimSpace(string(body)))
			return nil
		}
		var hosts []types.HostStatus
		if err := json.Unmarshal(body, &hosts); err != nil {
			return fmt.Errorf("decoding /hosts response: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tURI\tDEFAULT\tREACHABLE\tVMS\tCPUS\tRAM_MB\tDISK_GB\tGPUS")
		for _, h := range hosts {
			reachable := "-"
			if h.Reachable != nil {
				reachable = fmt.Sprintf("%t", *h.Reachable)
			}
			fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%d\t%d\t%d\t%d\t%d\n",
				h.Name, h.URI, h.Default, reachable, h.VMCount, h.CPUs, h.RAMMB, h.DiskGB, h.GPUs)
		}
		return w.Flush()
	},
}

func init() {
	for _, c := range []*cobra.Command{hostStatsCmd, hostQuotasCmd, hostGPUsCmd, hostListCmd} {
		c.Flags().String("api-url", "", "daemon API URL (defaults to http://<daemon.listen>)")
		c.Flags().String("api-key", os.Getenv("VMSMITH_API_KEY"), "Bearer token for daemons with auth enabled (defaults to $VMSMITH_API_KEY)")
		c.Flags().Bool("json", false, "emit the raw JSON response instead of a table")
	}
	hostCmd.AddCommand(hostStatsCmd)
	hostCmd.AddCommand(hostQuotasCmd)
	hostCmd.AddCommand(hostGPUsCmd)
	hostCmd.AddCommand(hostListCmd)
}
