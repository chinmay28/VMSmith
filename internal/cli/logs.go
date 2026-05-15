package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/logger"
)

// logsCmd hosts the `vmsmith logs` subtree.  Today the only subcommand is
// `list`, which talks to the daemon's GET /api/v1/logs ring-buffer endpoint
// — the same endpoint the GUI's Log Viewer page already consumes.  This
// closes the CLI follow-up that was deferred when the `search` filter
// landed (roadmap 5.4.13).
var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Query the daemon's structured log ring buffer",
	Long: `Query the daemon's in-memory log ring buffer over HTTP.

The daemon keeps the most recent 2000 structured log entries available via
GET /api/v1/logs.  This subcommand is a thin client wrapper so operators
can grep, tail, and triage from a shell without opening the GUI.

Filters mirror the API one-to-one:

  --level <l>          Minimum level: debug | info | warn | error
                       (case-insensitive; default debug = all entries)
  --source <s>         Restrict to a single source: cli | api | daemon
                       (case-insensitive; empty = every source)
  --since <when>       Show entries strictly after this point in time.
                       Accepts a Go duration (e.g. 5m, 2h) or an RFC3339
                       timestamp.
  --search <text>      Case-insensitive substring match across each
                       entry's message, source, level, and every value
                       in the structured fields map.  Field *keys* are
                       intentionally excluded — the small repeating
                       vocabulary (vm_id, method, error, ...) would
                       generate noisy matches against operator-supplied
                       values.
  --sort <field>       Sort entries by: timestamp | level | source
                       (default timestamp).  level orders by severity
                       rank (debug < info < warn < error), not
                       alphabetically.
  --order <asc|desc>   Sort order (default asc — oldest first).
  --limit <n>          Max rows to fetch per request (forwarded as the
                       per_page query param; capped at 2000 daemon-side).
  --page <n>           1-indexed page number (default 1).
  --fields             Include structured fields as a trailing column.
                       Off by default to keep the table narrow.

Connectivity flags follow the rest of the CLI:

  --api-url <url>      Daemon base URL (defaults to http://<daemon.listen>).
  --api-key <key>      Bearer token for daemons with auth enabled
                       (also reads $VMSMITH_API_KEY).`,
}

var logsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent log entries from the daemon ring buffer",
	RunE: func(cmd *cobra.Command, args []string) error {
		level, _ := cmd.Flags().GetString("level")
		source, _ := cmd.Flags().GetString("source")
		since, _ := cmd.Flags().GetString("since")
		search, _ := cmd.Flags().GetString("search")
		sortField, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		limit, _ := cmd.Flags().GetInt("limit")
		page, _ := cmd.Flags().GetInt("page")
		showFields, _ := cmd.Flags().GetBool("fields")
		apiURL, _ := cmd.Flags().GetString("api-url")
		flagAPIKey, _ := cmd.Flags().GetString("api-key")

		canonicalLevel, err := validateLogLevel(level)
		if err != nil {
			return err
		}

		canonicalSort, canonicalOrder, err := validateLogSortFlags(sortField, order)
		if err != nil {
			return err
		}

		canonicalSource := strings.ToLower(strings.TrimSpace(source))

		// Reuse the events.go since-parser so `5m`, `2h`, and RFC3339
		// timestamps all behave identically across the CLI.
		var sinceParam string
		if t, perr := parseSinceFlag(since); perr != nil {
			return perr
		} else if !t.IsZero() {
			sinceParam = t.UTC().Format(time.RFC3339Nano)
		}

		if limit < 0 {
			return fmt.Errorf("--limit must be >= 0 (0 = daemon default)")
		}
		if page < 0 {
			return fmt.Errorf("--page must be >= 0 (0/1 = first page)")
		}

		resolved := resolveAPIURL(apiURL)
		endpoint := strings.TrimRight(resolved, "/") + "/api/v1/logs"
		q := url.Values{}
		if canonicalLevel != "" {
			q.Set("level", canonicalLevel)
		}
		if canonicalSource != "" {
			q.Set("source", canonicalSource)
		}
		if sinceParam != "" {
			q.Set("since", sinceParam)
		}
		if needle := strings.ToLower(strings.TrimSpace(search)); needle != "" {
			q.Set("search", needle)
		}
		if canonicalSort != "" {
			q.Set("sort", canonicalSort)
		}
		if canonicalOrder != "" {
			q.Set("order", canonicalOrder)
		}
		if limit > 0 {
			q.Set("per_page", fmt.Sprintf("%d", limit))
		}
		if page > 0 {
			q.Set("page", fmt.Sprintf("%d", page))
		}
		if encoded := q.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}

		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		// CLI conventions: `--api-key` flag wins; fall back to the persistent
		// root flag (also fed by $VMSMITH_API_KEY in the webhook init pattern).
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
			return fmt.Errorf("connecting to %s: %w", endpoint, err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("daemon returned HTTP %d: %s",
				resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var out struct {
			Entries []logger.Entry `json:"entries"`
			Total   int            `json:"total"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return fmt.Errorf("decoding /logs response: %w", err)
		}

		if len(out.Entries) == 0 {
			fmt.Println("No log entries.")
			return nil
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		header := "TIME\tLEVEL\tSOURCE\tMESSAGE"
		if showFields {
			header += "\tFIELDS"
		}
		fmt.Fprintln(tw, header)
		for _, e := range out.Entries {
			row := fmt.Sprintf("%s\t%s\t%s\t%s",
				formatLogTime(e.Timestamp),
				strings.ToUpper(orDash(e.Level)),
				orDash(e.Source),
				truncMessage(e.Message, 120),
			)
			if showFields {
				row += "\t" + formatLogFields(e.Fields)
			}
			fmt.Fprintln(tw, row)
		}
		return tw.Flush()
	},
}

// validateLogSortFlags mirrors the daemon's parseLogSort whitelist so the
// CLI returns a clear local error rather than round-tripping to surface a
// 400 from the API.  Empty values mean "leave unset" — the daemon then
// applies its default (timestamp+asc, preserving the legacy oldest-first
// contract).  Returns the canonical lowercase form for the URL params.
func validateLogSortFlags(sortField, order string) (string, string, error) {
	canonicalSort := strings.ToLower(strings.TrimSpace(sortField))
	switch canonicalSort {
	case "", logger.EntrySortTimestamp, logger.EntrySortLevel, logger.EntrySortSource:
	default:
		return "", "", fmt.Errorf("invalid --sort %q (want one of: timestamp, level, source)", sortField)
	}

	canonicalOrder := strings.ToLower(strings.TrimSpace(order))
	switch canonicalOrder {
	case "", logger.EntrySortOrderAsc, logger.EntrySortOrderDesc:
	default:
		return "", "", fmt.Errorf("invalid --order %q (want one of: asc, desc)", order)
	}
	return canonicalSort, canonicalOrder, nil
}

// validateLogLevel matches the daemon's parseLevel whitelist
// (debug|info|warn|warning|error) and returns the canonical lowercase form
// the API expects.  Empty input means "leave unset" — the daemon then
// defaults to debug = every entry, which is the most useful default for a
// shell client.
func validateLogLevel(level string) (string, error) {
	lvl := strings.ToLower(strings.TrimSpace(level))
	switch lvl {
	case "":
		return "", nil
	case "debug", "info", "warn", "warning", "error":
		// Normalise "warning" → "warn" so the wire form is single-word.
		if lvl == "warning" {
			return "warn", nil
		}
		return lvl, nil
	default:
		return "", fmt.Errorf("invalid --level %q (want one of: debug, info, warn, error)", level)
	}
}

// formatLogTime renders the entry timestamp in the operator-local timezone
// at second resolution.  The wire format from the daemon is already UTC
// RFC3339 — converting to local mirrors the GUI's "ts" column.
func formatLogTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// formatLogFields renders the structured fields map as "k=v k=v ..." with
// keys sorted for deterministic test output.  Empty / nil map → "-".
func formatLogFields(fields map[string]string) string {
	if len(fields) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+fields[k])
	}
	return strings.Join(parts, " ")
}

func init() {
	logsListCmd.Flags().String("level", "", "minimum level: debug|info|warn|error (default debug = all)")
	logsListCmd.Flags().String("source", "", "filter by source: cli|api|daemon (empty = all)")
	logsListCmd.Flags().String("since", "", "show entries since (Go duration like 5m, or RFC3339 timestamp)")
	logsListCmd.Flags().String("search", "", "case-insensitive substring match across message, source, level, and structured field values")
	logsListCmd.Flags().String("sort", "", "sort entries by: timestamp|level|source (empty = daemon default = timestamp)")
	logsListCmd.Flags().String("order", "", "sort order: asc|desc (empty = daemon default = asc)")
	logsListCmd.Flags().Int("limit", 0, "max rows per page (0 = daemon default of 200; capped at 2000)")
	logsListCmd.Flags().Int("page", 0, "1-indexed page number (0 = first page)")
	logsListCmd.Flags().Bool("fields", false, "include the structured fields map as a trailing column")
	logsListCmd.Flags().String("api-url", "", "daemon API URL (defaults to http://<daemon.listen>)")
	logsListCmd.Flags().String("api-key", os.Getenv("VMSMITH_API_KEY"), "Bearer token for daemons with auth enabled (defaults to $VMSMITH_API_KEY)")

	logsCmd.AddCommand(logsListCmd)
}
