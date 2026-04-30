package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
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

// eventsFollowCmd implements "vmsmith events follow [filters...]" — a
// long-running SSE consumer that prints events as they arrive on the daemon's
// /api/v1/events/stream endpoint.
//
// The daemon is the single source of truth for live events because the bbolt
// store is opened by it; we therefore stream HTTP rather than tailing the file.
var eventsFollowCmd = &cobra.Command{
	Use:   "follow",
	Short: "Stream events from the daemon as they happen (SSE)",
	Long: `Open a Server-Sent Events stream against the daemon and print events as
they arrive. Press Ctrl-C to stop.

Filters apply client-side after the stream is connected, so the wire still
delivers every event but only matching rows are printed. Reconnects with
Last-Event-ID on transient network errors so no events are skipped.

Examples:
  vmsmith events follow
  vmsmith events follow --vm vm-12345
  vmsmith events follow --type vm.started --severity info
  vmsmith events follow --api-url http://daemon.local:8080`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vmFilter, _ := cmd.Flags().GetString("vm")
		typeFilter, _ := cmd.Flags().GetString("type")
		sourceFilter, _ := cmd.Flags().GetString("source")
		severityFilter, _ := cmd.Flags().GetString("severity")
		apiURL, _ := cmd.Flags().GetString("api-url")
		if apiURL == "" {
			cfg, err := config.Load(cfgFile)
			if err == nil {
				apiURL = "http://" + cfg.Daemon.Listen
			} else {
				apiURL = "http://localhost:8080"
			}
		}

		filter := eventFilter{
			vmID:     strings.TrimSpace(vmFilter),
			typeStr:  strings.TrimSpace(typeFilter),
			source:   strings.ToLower(strings.TrimSpace(sourceFilter)),
			severity: strings.ToLower(strings.TrimSpace(severityFilter)),
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		// Header — identical to `events list` so output remains scriptable.
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TIME\tSEVERITY\tSOURCE\tTYPE\tVM\tMESSAGE")
		w.Flush()

		return followEventsStream(ctx, apiURL, apiKey, filter, os.Stdout)
	},
}

// followEventsStream connects to /api/v1/events/stream and prints matching
// events to out as they arrive. Reconnects on transient errors using the most
// recent observed event ID so no events are missed.
func followEventsStream(ctx context.Context, apiURL, apiKey string, filter eventFilter, out io.Writer) error {
	var lastID string
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		err := streamEventsOnce(ctx, apiURL, apiKey, &lastID, filter, out)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			// Server closed cleanly; reconnect immediately.
			backoff = time.Second
			continue
		}
		if isFatalStreamErr(err) {
			return err
		}
		// Transient — back off, then retry.
		fmt.Fprintf(os.Stderr, "events follow: %v (reconnecting in %s)\n", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// streamErrFatal wraps an error that should terminate the follow loop without
// reconnect (auth failure, 410 replay window exceeded, malformed URL).
type streamErrFatal struct{ err error }

func (e streamErrFatal) Error() string { return e.err.Error() }
func (e streamErrFatal) Unwrap() error { return e.err }

func isFatalStreamErr(err error) bool {
	var f streamErrFatal
	return errors.As(err, &f)
}

// streamEventsOnce opens a single SSE connection and prints events until the
// connection drops or context is cancelled. lastID is updated as events arrive
// so a subsequent call can resume after the last seen ID.
func streamEventsOnce(ctx context.Context, apiURL, apiKey string, lastID *string, filter eventFilter, out io.Writer) error {
	streamURL := strings.TrimRight(apiURL, "/") + "/api/v1/events/stream"
	if *lastID != "" {
		// Pass the last seen seq via ?since= as a uint64 fallback for clients
		// that can't send Last-Event-ID (the daemon honours both).
		if seq, err := lastIDToSeq(*lastID); err == nil {
			q := url.Values{}
			q.Set("since", seq)
			streamURL += "?" + q.Encode()
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return streamErrFatal{err: fmt.Errorf("building request: %w", err)}
	}
	req.Header.Set("Accept", "text/event-stream")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if *lastID != "" {
		req.Header.Set("Last-Event-ID", *lastID)
	}

	client := &http.Client{Timeout: 0} // streaming — no overall timeout
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", streamURL, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return streamErrFatal{err: fmt.Errorf("authentication failed (HTTP %d) — set --api-key", resp.StatusCode)}
	case http.StatusGone:
		return streamErrFatal{err: fmt.Errorf("replay window exceeded; rerun without --since to resume")}
	default:
		return fmt.Errorf("daemon returned HTTP %d", resp.StatusCode)
	}

	return parseSSEStream(ctx, resp.Body, lastID, filter, out)
}

// parseSSEStream reads SSE frames from r and prints matching events to out.
// Updates *lastID as events arrive. Returns nil when the server closes the
// stream cleanly, a non-nil error on read failure.
func parseSSEStream(ctx context.Context, r io.Reader, lastID *string, filter eventFilter, out io.Writer) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	scanner := bufio.NewScanner(r)
	// SSE frames can carry large JSON event payloads; raise the max token size
	// so a single oversized frame doesn't break the stream.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataLines []string
	var frameID, frameEvent string

	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := scanner.Text()

		switch {
		case line == "":
			if len(dataLines) > 0 {
				payload := strings.Join(dataLines, "\n")
				var evt types.Event
				if err := json.Unmarshal([]byte(payload), &evt); err == nil {
					if frameID != "" {
						*lastID = frameID
						if evt.ID == "" {
							evt.ID = frameID
						}
					}
					if matchesEventFilter(&evt, filter) {
						printEventRow(tw, &evt)
						tw.Flush()
					}
				}
			}
			dataLines = dataLines[:0]
			frameID = ""
			frameEvent = ""
		case strings.HasPrefix(line, ":"):
			// Comment / heartbeat — ignore.
		case strings.HasPrefix(line, "id:"):
			frameID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			frameEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			_ = frameEvent // event name is informational; kept for forward compat
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return scanner.Err()
}

func matchesEventFilter(e *types.Event, f eventFilter) bool {
	if e == nil {
		return false
	}
	if f.vmID != "" && e.VMID != f.vmID {
		return false
	}
	if f.typeStr != "" && e.Type != f.typeStr {
		return false
	}
	if f.source != "" && !strings.EqualFold(e.Source, f.source) {
		return false
	}
	if f.severity != "" && !strings.EqualFold(e.Severity, f.severity) {
		return false
	}
	return true
}

func printEventRow(tw *tabwriter.Writer, e *types.Event) {
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
		formatEventTime(eventTimestamp(e)),
		orDash(e.Severity),
		orDash(e.Source),
		orDash(e.Type),
		orDash(e.VMID),
		truncMessage(e.Message, 80),
	)
}

// lastIDToSeq returns the numeric portion of an event ID for ?since= reuse.
// EventBus IDs are stringified uint64; legacy "evt-<unix-nano>" IDs are not
// usable as a seq cursor and return an error so callers fall back to plain GET.
func lastIDToSeq(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("empty id")
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("non-numeric id")
		}
	}
	return id, nil
}

func init() {
	eventsListCmd.Flags().String("vm", "", "filter events by VM ID")
	eventsListCmd.Flags().String("type", "", "filter events by type (e.g. vm_started)")
	eventsListCmd.Flags().String("source", "", "filter events by source (libvirt|app|system)")
	eventsListCmd.Flags().String("severity", "", "filter events by severity (info|warn|error)")
	eventsListCmd.Flags().String("since", "", "show events since (Go duration like 5m, or RFC3339 timestamp)")
	eventsListCmd.Flags().Int("limit", 100, "maximum number of events to show")

	eventsFollowCmd.Flags().String("vm", "", "only print events for this VM ID")
	eventsFollowCmd.Flags().String("type", "", "only print events of this type (exact match)")
	eventsFollowCmd.Flags().String("source", "", "only print events from this source (libvirt|app|system)")
	eventsFollowCmd.Flags().String("severity", "", "only print events at this severity (info|warn|error)")
	eventsFollowCmd.Flags().String("api-url", "", "daemon API URL (default: http://<daemon.listen>)")

	eventsCmd.AddCommand(eventsListCmd)
	eventsCmd.AddCommand(eventsFollowCmd)
}
