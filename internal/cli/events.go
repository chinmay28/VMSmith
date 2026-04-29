package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

var eventsCmd = &cobra.Command{
	Use:     "events",
	Short:   "View VM lifecycle and system events",
	Aliases: []string{"event"},
}

var eventsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent events (newest first)",
	Long: `List recent events newest-first.

Reads directly from the local bbolt store (no daemon required); filters
are applied client-side after a full bucket scan. For real-time streaming
or daemon-side filtering, query GET /api/v1/events instead.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vmFilter, _ := cmd.Flags().GetString("vm")
		typeFilter, _ := cmd.Flags().GetString("type")
		sourceFilter, _ := cmd.Flags().GetString("source")
		severityFilter, _ := cmd.Flags().GetString("severity")
		sinceFlag, _ := cmd.Flags().GetString("since")
		limit, _ := cmd.Flags().GetInt("limit")

		sinceTime, err := parseSinceFlag(sinceFlag)
		if err != nil {
			return err
		}

		s, cleanup, err := openStoreForCLI()
		if err != nil {
			return err
		}
		defer cleanup()

		all, err := s.ListEvents()
		if err != nil {
			logger.Error("cli", "events list failed", "error", err.Error())
			return fmt.Errorf("listing events: %w", err)
		}

		filtered := filterEvents(all, eventFilter{
			vmID:     strings.TrimSpace(vmFilter),
			typeStr:  strings.TrimSpace(typeFilter),
			source:   strings.ToLower(strings.TrimSpace(sourceFilter)),
			severity: strings.ToLower(strings.TrimSpace(severityFilter)),
			since:    sinceTime,
		})

		// Newest first.
		sort.Slice(filtered, func(i, j int) bool {
			return eventTimestamp(filtered[i]).After(eventTimestamp(filtered[j]))
		})

		if limit > 0 && len(filtered) > limit {
			filtered = filtered[:limit]
		}

		if len(filtered) == 0 {
			fmt.Println("No events.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TIME\tSEVERITY\tSOURCE\tTYPE\tVM\tMESSAGE")
		for _, e := range filtered {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				formatEventTime(eventTimestamp(e)),
				orDash(e.Severity),
				orDash(e.Source),
				orDash(e.Type),
				orDash(e.VMID),
				truncMessage(e.Message, 80),
			)
		}
		return w.Flush()
	},
}

type eventFilter struct {
	vmID     string
	typeStr  string
	source   string
	severity string
	since    time.Time
}

func filterEvents(events []*types.Event, f eventFilter) []*types.Event {
	out := make([]*types.Event, 0, len(events))
	for _, e := range events {
		if e == nil {
			continue
		}
		if f.vmID != "" && e.VMID != f.vmID {
			continue
		}
		if f.typeStr != "" && e.Type != f.typeStr {
			continue
		}
		if f.source != "" && !strings.EqualFold(e.Source, f.source) {
			continue
		}
		if f.severity != "" && !strings.EqualFold(e.Severity, f.severity) {
			continue
		}
		if !f.since.IsZero() && eventTimestamp(e).Before(f.since) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// eventTimestamp returns the best-available time for an event, falling back
// from OccurredAt → CreatedAt so legacy records still sort correctly.
func eventTimestamp(e *types.Event) time.Time {
	if !e.OccurredAt.IsZero() {
		return e.OccurredAt
	}
	return e.CreatedAt
}

func formatEventTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// parseSinceFlag accepts either a Go duration (e.g. "5m") or an RFC3339 timestamp.
func parseSinceFlag(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --since value %q (want duration like 5m or RFC3339 timestamp)", s)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func truncMessage(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// openStoreForCLI is a small helper that opens the bbolt store using the
// resolved config — used by event-style read-only commands that don't need
// a libvirt connection.  Tests can override storeOverrideForCLI.
var storeOverrideForCLI func() (*store.Store, func(), error)

func openStoreForCLI() (*store.Store, func(), error) {
	if storeOverrideForCLI != nil {
		return storeOverrideForCLI()
	}
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, err
	}
	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening store: %w", err)
	}
	return s, func() { s.Close() }, nil
}

func init() {
	eventsListCmd.Flags().String("vm", "", "filter events by VM ID")
	eventsListCmd.Flags().String("type", "", "filter events by type (e.g. vm_started)")
	eventsListCmd.Flags().String("source", "", "filter events by source (libvirt|app|system)")
	eventsListCmd.Flags().String("severity", "", "filter events by severity (info|warn|error)")
	eventsListCmd.Flags().String("since", "", "show events since (Go duration like 5m, or RFC3339 timestamp)")
	eventsListCmd.Flags().Int("limit", 100, "maximum number of events to show")

	eventsCmd.AddCommand(eventsListCmd)
}
