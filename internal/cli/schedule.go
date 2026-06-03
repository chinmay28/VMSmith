package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/pkg/types"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage recurring VM-action schedules",
	Long: `Create and manage recurring schedules that fire VM actions
(snapshot, start, stop, restart) on a cron timer.  Schedules are evaluated
by the daemon; see docs/SCHEDULES.md for the cron-spec syntax (6-field,
seconds-required), timezone / DST rules, catch-up policies, and retention
semantics.`,
}

func scheduleHTTP(cmd *cobra.Command, method, path string, body any) ([]byte, int, error) {
	apiURL, _ := cmd.Flags().GetString("api-url")
	apiKey, _ := cmd.Flags().GetString("api-key")
	resolved := resolveAPIURL(apiURL)

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(cmd.Context(), method, resolved+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

var scheduleCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new schedule",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		vmID, _ := cmd.Flags().GetString("vm")
		tags, _ := cmd.Flags().GetStringSlice("tag")
		action, _ := cmd.Flags().GetString("action")
		cron, _ := cmd.Flags().GetString("cron")
		timezone, _ := cmd.Flags().GetString("timezone")
		enabled, _ := cmd.Flags().GetBool("enabled")
		catchUp, _ := cmd.Flags().GetString("catch-up")
		retention, _ := cmd.Flags().GetInt("retention")
		maxConcurrent, _ := cmd.Flags().GetInt("max-concurrent")

		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("--name is required")
		}
		if !types.IsValidScheduleAction(types.ScheduleAction(action)) {
			return fmt.Errorf("--action must be one of: snapshot, start, stop, restart")
		}
		if strings.TrimSpace(cron) == "" {
			return fmt.Errorf("--cron is required")
		}
		if !types.IsValidCatchUpPolicy(types.ScheduleCatchUpPolicy(catchUp)) {
			return fmt.Errorf("--catch-up must be one of: skip, run_once, run_all")
		}
		if strings.TrimSpace(vmID) != "" && len(tags) > 0 {
			return fmt.Errorf("--vm and --tag are mutually exclusive")
		}

		req := types.CreateScheduleRequest{
			Name:           name,
			VMID:           strings.TrimSpace(vmID),
			TagSelector:    tags,
			Action:         types.ScheduleAction(action),
			CronSpec:       strings.TrimSpace(cron),
			Timezone:       strings.TrimSpace(timezone),
			Enabled:        &enabled,
			CatchUpPolicy:  types.ScheduleCatchUpPolicy(catchUp),
			RetentionCount: retention,
			MaxConcurrent:  maxConcurrent,
		}

		data, status, err := scheduleHTTP(cmd, http.MethodPost, "/api/v1/schedules", req)
		if err != nil {
			return err
		}
		if status != http.StatusCreated {
			return fmt.Errorf("daemon rejected schedule (HTTP %d): %s", status, strings.TrimSpace(string(data)))
		}
		var sched types.Schedule
		if err := json.Unmarshal(data, &sched); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
		fmt.Printf("Schedule created: %s\n", sched.ID)
		printScheduleDetail(&sched)
		return nil
	},
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List schedules",
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID, _ := cmd.Flags().GetString("vm")
		tagSelector, _ := cmd.Flags().GetString("tag-selector")
		action, _ := cmd.Flags().GetString("action")
		catchUp, _ := cmd.Flags().GetString("catch-up")
		timezone, _ := cmd.Flags().GetString("timezone")
		enabled, _ := cmd.Flags().GetString("enabled")
		search, _ := cmd.Flags().GetString("search")
		sinceFlag, _ := cmd.Flags().GetString("since")
		untilFlag, _ := cmd.Flags().GetString("until")
		nextFireSinceFlag, _ := cmd.Flags().GetString("next-fire-since")
		nextFireUntilFlag, _ := cmd.Flags().GetString("next-fire-until")
		sortField, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")
		limit, _ := cmd.Flags().GetInt("limit")
		page, _ := cmd.Flags().GetInt("page")

		sortField = strings.ToLower(strings.TrimSpace(sortField))
		if sortField != "" && !types.IsValidScheduleSort(sortField) {
			return fmt.Errorf("invalid --sort: must be one of id, name, created_at, next_fire_at")
		}
		order = strings.ToLower(strings.TrimSpace(order))
		if order != "" && order != types.SortOrderAsc && order != types.SortOrderDesc {
			return fmt.Errorf("invalid --order: must be 'asc' or 'desc'")
		}
		enabled = strings.ToLower(strings.TrimSpace(enabled))
		if enabled != "" && enabled != "true" && enabled != "false" {
			return fmt.Errorf("invalid --enabled: must be 'true' or 'false'")
		}
		catchUp = strings.ToLower(strings.TrimSpace(catchUp))
		if catchUp != "" && !types.IsValidCatchUpPolicy(types.ScheduleCatchUpPolicy(catchUp)) {
			return fmt.Errorf("invalid --catch-up: must be one of skip, run_once, run_all")
		}
		if _, _, err := parseCLITimeRange(sinceFlag, "--since"); err != nil {
			return err
		}
		if _, _, err := parseCLITimeRange(untilFlag, "--until"); err != nil {
			return err
		}
		if _, _, err := parseCLITimeRange(nextFireSinceFlag, "--next-fire-since"); err != nil {
			return err
		}
		if _, _, err := parseCLITimeRange(nextFireUntilFlag, "--next-fire-until"); err != nil {
			return err
		}

		q := url.Values{}
		if v := strings.TrimSpace(vmID); v != "" {
			q.Set("vm_id", v)
		}
		if v := strings.ToLower(strings.TrimSpace(tagSelector)); v != "" {
			q.Set("tag_selector", v)
		}
		if v := strings.ToLower(strings.TrimSpace(action)); v != "" {
			q.Set("action", v)
		}
		if catchUp != "" {
			q.Set("catch_up_policy", catchUp)
		}
		if v := strings.TrimSpace(timezone); v != "" {
			q.Set("timezone", v)
		}
		if enabled != "" {
			q.Set("enabled", enabled)
		}
		if v := strings.ToLower(strings.TrimSpace(search)); v != "" {
			q.Set("search", v)
		}
		if v := strings.TrimSpace(sinceFlag); v != "" {
			q.Set("since", v)
		}
		if v := strings.TrimSpace(untilFlag); v != "" {
			q.Set("until", v)
		}
		if v := strings.TrimSpace(nextFireSinceFlag); v != "" {
			q.Set("next_fire_since", v)
		}
		if v := strings.TrimSpace(nextFireUntilFlag); v != "" {
			q.Set("next_fire_until", v)
		}
		if sortField != "" {
			q.Set("sort", sortField)
		}
		if order != "" {
			q.Set("order", order)
		}
		if limit > 0 {
			q.Set("per_page", strconv.Itoa(limit))
			if page > 1 {
				q.Set("page", strconv.Itoa(page))
			}
		}
		path := "/api/v1/schedules"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}

		data, status, err := scheduleHTTP(cmd, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("daemon returned HTTP %d: %s", status, strings.TrimSpace(string(data)))
		}
		var scheds []*types.Schedule
		if err := json.Unmarshal(data, &scheds); err != nil {
			return fmt.Errorf("decoding schedule list: %w", err)
		}
		if len(scheds) == 0 {
			fmt.Println("No schedules found.")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tACTION\tTARGET\tENABLED\tCRON\tNEXT_FIRE\tLAST_RESULT")
		for _, s := range scheds {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%s\t%s\t%s\n",
				s.ID, s.Name, s.Action, scheduleTarget(s), s.Enabled, s.CronSpec,
				formatNextFire(s.NextFireAt), dashIfEmpty(s.LastResult))
		}
		return tw.Flush()
	},
}

var scheduleShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a schedule definition and its recent runs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		data, status, err := scheduleHTTP(cmd, http.MethodGet, "/api/v1/schedules/"+url.PathEscape(id), nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("daemon returned HTTP %d: %s", status, strings.TrimSpace(string(data)))
		}
		var sched types.Schedule
		if err := json.Unmarshal(data, &sched); err != nil {
			return fmt.Errorf("decoding schedule: %w", err)
		}
		fmt.Printf("Schedule %s\n", sched.ID)
		printScheduleDetail(&sched)

		runData, runStatus, err := scheduleHTTP(cmd, http.MethodGet, "/api/v1/schedules/"+url.PathEscape(id)+"/runs?per_page=20", nil)
		if err != nil {
			return err
		}
		if runStatus != http.StatusOK {
			return nil
		}
		var runs []*types.ScheduleRun
		if err := json.Unmarshal(runData, &runs); err != nil {
			return nil
		}
		if len(runs) == 0 {
			fmt.Println("\nNo runs recorded yet.")
			return nil
		}
		fmt.Println("\nRecent runs:")
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  STARTED\tVM\tSTATUS\tDETAIL")
		for _, r := range runs {
			detail := string(r.SkipReason)
			if r.Error != "" {
				detail = r.Error
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n",
				r.StartedAt.Format(time.RFC3339), dashIfEmpty(r.VMID), r.Status, dashIfEmpty(detail))
		}
		return tw.Flush()
	},
}

var scheduleRunsCmd = &cobra.Command{
	Use:   "runs <id>",
	Short: "List a schedule's run history",
	Long: `List the run history for a schedule (newest first by default).
Filter with --status (running|success|error|skipped), --skip-reason
(vm_not_found|vm_already_stopped|vm_already_running|concurrent_run|
catch_up_skipped|queue_full — narrows the skipped cohort to a single reason,
e.g. "show me every run skipped because the queue was full"), --vm <vm-id>
(exact match — useful for tag-selector schedules that target many VMs),
--search
(case-insensitive substring across each run's error and skip_reason — handy
for triaging "show me every run that timed out"), an inclusive RFC3339
--since / --until window on each run's started_at, an inclusive RFC3339
--finished-since / --finished-until window on each run's finished_at
(still-running runs are filtered OUT when any finished-* bound is set), an
inclusive --min-duration-ms / --max-duration-ms window on each run's
finished_at - started_at duration (also excludes still-running runs when
either bound is set — the symmetric range counterpart to the --sort=duration
axis), and page with --limit / --page. Order with --sort
(id|started_at|finished_at|status|duration; default started_at) and --order
(asc|desc; default desc on bare sort to preserve the newest-first contract).
Still-running runs (nil finished_at) trail an ascending finished_at sort and
lead a descending one; the duration axis (finished_at - started_at) applies
the same nil-trailing semantics — still-running runs sink to the tail in
asc and lead in desc.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		status, _ := cmd.Flags().GetString("status")
		skipReason, _ := cmd.Flags().GetString("skip-reason")
		vmID, _ := cmd.Flags().GetString("vm")
		sinceFlag, _ := cmd.Flags().GetString("since")
		untilFlag, _ := cmd.Flags().GetString("until")
		finishedSinceFlag, _ := cmd.Flags().GetString("finished-since")
		finishedUntilFlag, _ := cmd.Flags().GetString("finished-until")
		minDurationMs, _ := cmd.Flags().GetInt("min-duration-ms")
		maxDurationMs, _ := cmd.Flags().GetInt("max-duration-ms")
		searchFlag, _ := cmd.Flags().GetString("search")
		sortFlag, _ := cmd.Flags().GetString("sort")
		orderFlag, _ := cmd.Flags().GetString("order")
		limit, _ := cmd.Flags().GetInt("limit")
		page, _ := cmd.Flags().GetInt("page")

		status = strings.ToLower(strings.TrimSpace(status))
		if status != "" && !types.IsValidScheduleRunStatus(types.ScheduleRunStatus(status)) {
			return fmt.Errorf("invalid --status: must be one of running, success, error, skipped")
		}
		skipReason = strings.ToLower(strings.TrimSpace(skipReason))
		if skipReason != "" && !types.IsValidScheduleRunSkipReason(types.ScheduleRunSkipReason(skipReason)) {
			return fmt.Errorf("invalid --skip-reason: must be one of vm_not_found, vm_already_stopped, vm_already_running, concurrent_run, catch_up_skipped, queue_full")
		}
		vmID = strings.TrimSpace(vmID)
		if _, _, err := parseCLITimeRange(sinceFlag, "--since"); err != nil {
			return err
		}
		if _, _, err := parseCLITimeRange(untilFlag, "--until"); err != nil {
			return err
		}
		if _, _, err := parseCLITimeRange(finishedSinceFlag, "--finished-since"); err != nil {
			return err
		}
		if _, _, err := parseCLITimeRange(finishedUntilFlag, "--finished-until"); err != nil {
			return err
		}
		if minDurationMs < 0 {
			return fmt.Errorf("invalid --min-duration-ms: must be a non-negative integer")
		}
		if maxDurationMs < 0 {
			return fmt.Errorf("invalid --max-duration-ms: must be a non-negative integer")
		}
		sortField := strings.ToLower(strings.TrimSpace(sortFlag))
		if sortField != "" && !types.IsValidScheduleRunSort(sortField) {
			return fmt.Errorf("invalid --sort: must be one of id, started_at, finished_at, status, duration")
		}
		order := strings.ToLower(strings.TrimSpace(orderFlag))
		if order != "" && order != types.SortOrderAsc && order != types.SortOrderDesc {
			return fmt.Errorf("invalid --order: must be asc or desc")
		}

		q := url.Values{}
		if status != "" {
			q.Set("status", status)
		}
		if skipReason != "" {
			q.Set("skip_reason", skipReason)
		}
		if vmID != "" {
			q.Set("vm_id", vmID)
		}
		if v := strings.TrimSpace(sinceFlag); v != "" {
			q.Set("since", v)
		}
		if v := strings.TrimSpace(untilFlag); v != "" {
			q.Set("until", v)
		}
		if v := strings.TrimSpace(finishedSinceFlag); v != "" {
			q.Set("finished_since", v)
		}
		if v := strings.TrimSpace(finishedUntilFlag); v != "" {
			q.Set("finished_until", v)
		}
		if cmd.Flags().Changed("min-duration-ms") {
			q.Set("min_duration_ms", strconv.Itoa(minDurationMs))
		}
		if cmd.Flags().Changed("max-duration-ms") {
			q.Set("max_duration_ms", strconv.Itoa(maxDurationMs))
		}
		if v := strings.TrimSpace(searchFlag); v != "" {
			q.Set("search", v)
		}
		if sortField != "" {
			q.Set("sort", sortField)
		}
		if order != "" {
			q.Set("order", order)
		}
		if limit > 0 {
			q.Set("per_page", strconv.Itoa(limit))
			if page > 1 {
				q.Set("page", strconv.Itoa(page))
			}
		}
		path := "/api/v1/schedules/" + url.PathEscape(id) + "/runs"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}

		data, statusCode, err := scheduleHTTP(cmd, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if statusCode != http.StatusOK {
			return fmt.Errorf("daemon returned HTTP %d: %s", statusCode, strings.TrimSpace(string(data)))
		}
		var runs []*types.ScheduleRun
		if err := json.Unmarshal(data, &runs); err != nil {
			return fmt.Errorf("decoding runs: %w", err)
		}
		if len(runs) == 0 {
			fmt.Println("No runs found.")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "STARTED\tFINISHED\tVM\tSTATUS\tDETAIL")
		for _, r := range runs {
			detail := string(r.SkipReason)
			if r.Error != "" {
				detail = r.Error
			}
			finished := "-"
			if r.FinishedAt != nil {
				finished = r.FinishedAt.Format(time.RFC3339)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				r.StartedAt.Format(time.RFC3339), finished, dashIfEmpty(r.VMID), r.Status, dashIfEmpty(detail))
		}
		return tw.Flush()
	},
}

var scheduleEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Update fields on an existing schedule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		spec := types.ScheduleUpdateSpec{}

		if f := cmd.Flags().Lookup("name"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("name")
			spec.Name = &v
		}
		if f := cmd.Flags().Lookup("vm"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("vm")
			spec.VMID = &v
		}
		tagsChanged := false
		if f := cmd.Flags().Lookup("tag"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetStringSlice("tag")
			spec.TagSelector = &v
			tagsChanged = true
		}
		if clear, _ := cmd.Flags().GetBool("clear-tags"); clear {
			if tagsChanged {
				return fmt.Errorf("--tag and --clear-tags are mutually exclusive")
			}
			empty := []string{}
			spec.TagSelector = &empty
		}
		if f := cmd.Flags().Lookup("action"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("action")
			if !types.IsValidScheduleAction(types.ScheduleAction(v)) {
				return fmt.Errorf("--action must be one of: snapshot, start, stop, restart")
			}
			a := types.ScheduleAction(v)
			spec.Action = &a
		}
		if f := cmd.Flags().Lookup("cron"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("cron")
			spec.CronSpec = &v
		}
		if f := cmd.Flags().Lookup("timezone"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("timezone")
			spec.Timezone = &v
		}
		if f := cmd.Flags().Lookup("enabled"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetBool("enabled")
			spec.Enabled = &v
		}
		if f := cmd.Flags().Lookup("catch-up"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("catch-up")
			if !types.IsValidCatchUpPolicy(types.ScheduleCatchUpPolicy(v)) {
				return fmt.Errorf("--catch-up must be one of: skip, run_once, run_all")
			}
			p := types.ScheduleCatchUpPolicy(v)
			spec.CatchUpPolicy = &p
		}
		if f := cmd.Flags().Lookup("retention"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetInt("retention")
			spec.RetentionCount = &v
		}
		if f := cmd.Flags().Lookup("max-concurrent"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetInt("max-concurrent")
			spec.MaxConcurrent = &v
		}

		if spec.Name == nil && spec.VMID == nil && spec.TagSelector == nil && spec.Action == nil &&
			spec.CronSpec == nil && spec.Timezone == nil && spec.Enabled == nil && spec.CatchUpPolicy == nil &&
			spec.RetentionCount == nil && spec.MaxConcurrent == nil {
			return fmt.Errorf("no fields to update: pass at least one of --name, --vm, --tag, --clear-tags, --action, --cron, --timezone, --enabled, --catch-up, --retention, --max-concurrent")
		}

		data, status, err := scheduleHTTP(cmd, http.MethodPatch, "/api/v1/schedules/"+url.PathEscape(id), spec)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("daemon rejected edit (HTTP %d): %s", status, strings.TrimSpace(string(data)))
		}
		var sched types.Schedule
		if err := json.Unmarshal(data, &sched); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
		fmt.Printf("Schedule updated: %s\n", sched.ID)
		printScheduleDetail(&sched)
		return nil
	},
}

var scheduleDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a schedule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		data, status, err := scheduleHTTP(cmd, http.MethodDelete, "/api/v1/schedules/"+url.PathEscape(id), nil)
		if err != nil {
			return err
		}
		if status == http.StatusNoContent {
			fmt.Printf("Schedule %s deleted.\n", id)
			return nil
		}
		return fmt.Errorf("daemon rejected delete (HTTP %d): %s", status, strings.TrimSpace(string(data)))
	},
}

var scheduleRunNowCmd = &cobra.Command{
	Use:   "run-now <id>",
	Short: "Fire a schedule immediately",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		data, status, err := scheduleHTTP(cmd, http.MethodPost, "/api/v1/schedules/"+url.PathEscape(id)+"/run-now", nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("daemon rejected run-now (HTTP %d): %s", status, strings.TrimSpace(string(data)))
		}
		fmt.Printf("Schedule %s fired. Use 'vmsmith schedule show %s' to see run results.\n", id, id)
		return nil
	},
}

func printScheduleDetail(s *types.Schedule) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  Name:\t%s\n", s.Name)
	fmt.Fprintf(tw, "  Action:\t%s\n", s.Action)
	fmt.Fprintf(tw, "  Target:\t%s\n", scheduleTarget(s))
	fmt.Fprintf(tw, "  Cron:\t%s\n", s.CronSpec)
	if s.Timezone != "" {
		fmt.Fprintf(tw, "  Timezone:\t%s\n", s.Timezone)
	}
	fmt.Fprintf(tw, "  Enabled:\t%t\n", s.Enabled)
	fmt.Fprintf(tw, "  Catch-up:\t%s\n", dashIfEmpty(string(s.CatchUpPolicy)))
	if s.RetentionCount > 0 {
		fmt.Fprintf(tw, "  Retention:\t%d\n", s.RetentionCount)
	}
	fmt.Fprintf(tw, "  Next fire:\t%s\n", formatNextFire(s.NextFireAt))
	if s.LastResult != "" {
		fmt.Fprintf(tw, "  Last result:\t%s\n", s.LastResult)
	}
	_ = tw.Flush()
}

func scheduleTarget(s *types.Schedule) string {
	if strings.TrimSpace(s.VMID) != "" {
		return s.VMID
	}
	if len(s.TagSelector) > 0 {
		return "tag:" + strings.Join(s.TagSelector, ",")
	}
	return "all"
}

func formatNextFire(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func init() {
	scheduleCreateCmd.Flags().String("name", "", "schedule name (required)")
	scheduleCreateCmd.Flags().String("vm", "", "target VM id (mutually exclusive with --tag; empty = all VMs)")
	scheduleCreateCmd.Flags().StringSlice("tag", nil, "tag selector (repeatable; fans out to matching VMs)")
	scheduleCreateCmd.Flags().String("action", "", "action: snapshot|start|stop|restart (required)")
	scheduleCreateCmd.Flags().String("cron", "", "6-field cron spec with seconds, e.g. \"0 0 2 * * *\" (required)")
	scheduleCreateCmd.Flags().String("timezone", "", "IANA timezone (default: daemon local)")
	scheduleCreateCmd.Flags().Bool("enabled", true, "whether the schedule is active")
	scheduleCreateCmd.Flags().String("catch-up", "skip", "missed-fire policy: skip|run_once|run_all")
	scheduleCreateCmd.Flags().Int("retention", 0, "for snapshot action: keep at most N auto snapshots (0 = unlimited)")
	scheduleCreateCmd.Flags().Int("max-concurrent", 0, "max overlapping fires per schedule (0 = 1)")

	scheduleListCmd.Flags().String("vm", "", "filter by exact VM id")
	scheduleListCmd.Flags().String("tag-selector", "", "filter by exact tag-selector membership (case-insensitive)")
	scheduleListCmd.Flags().String("action", "", "filter by action (snapshot|start|stop|restart)")
	scheduleListCmd.Flags().String("catch-up", "", "filter by catch-up policy (skip|run_once|run_all)")
	scheduleListCmd.Flags().String("timezone", "", "filter by exact IANA timezone (case-sensitive, e.g. UTC, America/New_York)")
	scheduleListCmd.Flags().String("enabled", "", "filter by enabled flag: true|false")
	scheduleListCmd.Flags().String("search", "", "case-insensitive substring filter (name, action, vm_id, tag selector)")
	scheduleListCmd.Flags().String("since", "", "RFC3339 lower bound (inclusive) on created_at")
	scheduleListCmd.Flags().String("until", "", "RFC3339 upper bound (inclusive) on created_at")
	scheduleListCmd.Flags().String("next-fire-since", "", "RFC3339 lower bound (inclusive) on next_fire_at (schedules with no next_fire_at are excluded)")
	scheduleListCmd.Flags().String("next-fire-until", "", "RFC3339 upper bound (inclusive) on next_fire_at (schedules with no next_fire_at are excluded)")
	scheduleListCmd.Flags().String("sort", "", "sort field: id|name|created_at|next_fire_at (default id)")
	scheduleListCmd.Flags().String("order", "", "sort order: asc|desc (default asc)")
	scheduleListCmd.Flags().Int("limit", 0, "page size; 0 returns the full filtered set")
	scheduleListCmd.Flags().Int("page", 1, "1-based page number when --limit is set")

	scheduleRunsCmd.Flags().String("status", "", "filter by run status: running|success|error|skipped")
	scheduleRunsCmd.Flags().String("skip-reason", "", "filter by skip reason (only matches skipped runs persisted with this reason): vm_not_found|vm_already_stopped|vm_already_running|concurrent_run|catch_up_skipped|queue_full")
	scheduleRunsCmd.Flags().String("vm", "", "filter by VM id (exact match; useful for tag-selector schedules)")
	scheduleRunsCmd.Flags().String("since", "", "RFC3339 lower bound (inclusive) on started_at")
	scheduleRunsCmd.Flags().String("until", "", "RFC3339 upper bound (inclusive) on started_at")
	scheduleRunsCmd.Flags().String("finished-since", "", "RFC3339 lower bound (inclusive) on finished_at; excludes still-running runs")
	scheduleRunsCmd.Flags().String("finished-until", "", "RFC3339 upper bound (inclusive) on finished_at; excludes still-running runs")
	scheduleRunsCmd.Flags().Int("min-duration-ms", 0, "lower bound (inclusive) on finished_at - started_at duration in milliseconds; excludes still-running runs")
	scheduleRunsCmd.Flags().Int("max-duration-ms", 0, "upper bound (inclusive) on finished_at - started_at duration in milliseconds; excludes still-running runs")
	scheduleRunsCmd.Flags().String("search", "", "case-insensitive substring match across run error and skip_reason")
	scheduleRunsCmd.Flags().String("sort", "", "sort field: id|started_at|finished_at|status|duration (default started_at)")
	scheduleRunsCmd.Flags().String("order", "", "sort order: asc|desc (default desc on bare sort, else asc)")
	scheduleRunsCmd.Flags().Int("limit", 0, "page size; 0 returns the full filtered set")
	scheduleRunsCmd.Flags().Int("page", 1, "1-based page number when --limit is set")

	scheduleEditCmd.Flags().String("name", "", "replace schedule name")
	scheduleEditCmd.Flags().String("vm", "", "replace target VM id")
	scheduleEditCmd.Flags().StringSlice("tag", nil, "replace tag selector (repeatable)")
	scheduleEditCmd.Flags().Bool("clear-tags", false, "clear the tag selector (mutually exclusive with --tag)")
	scheduleEditCmd.Flags().String("action", "", "replace action")
	scheduleEditCmd.Flags().String("cron", "", "replace cron spec")
	scheduleEditCmd.Flags().String("timezone", "", "replace timezone")
	scheduleEditCmd.Flags().Bool("enabled", true, "toggle enabled (only applied when explicitly set)")
	scheduleEditCmd.Flags().String("catch-up", "", "replace catch-up policy")
	scheduleEditCmd.Flags().Int("retention", 0, "replace snapshot retention count")
	scheduleEditCmd.Flags().Int("max-concurrent", 0, "replace max concurrent fires")

	for _, c := range []*cobra.Command{scheduleCreateCmd, scheduleListCmd, scheduleShowCmd, scheduleRunsCmd, scheduleEditCmd, scheduleDeleteCmd, scheduleRunNowCmd} {
		c.Flags().String("api-url", "", "daemon API URL (defaults to http://<daemon.listen>)")
		c.Flags().String("api-key", os.Getenv("VMSMITH_API_KEY"), "Bearer token for daemons with auth enabled (defaults to $VMSMITH_API_KEY)")
	}
	scheduleCmd.AddCommand(scheduleCreateCmd)
	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleShowCmd)
	scheduleCmd.AddCommand(scheduleRunsCmd)
	scheduleCmd.AddCommand(scheduleEditCmd)
	scheduleCmd.AddCommand(scheduleDeleteCmd)
	scheduleCmd.AddCommand(scheduleRunNowCmd)
}
