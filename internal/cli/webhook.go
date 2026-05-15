package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/pkg/types"
)

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage event webhook subscriptions",
	Long: `Register HTTP receivers for the event bus.  Each delivery is signed
with HMAC-SHA256 over the JSON body using the per-webhook secret.  See
docs/ARCHITECTURE.md "Event System" -> "Webhook contract" for the wire
format and signature verification.`,
}

var webhookAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Register a new webhook",
	RunE: func(cmd *cobra.Command, args []string) error {
		url, _ := cmd.Flags().GetString("url")
		secret, _ := cmd.Flags().GetString("secret")
		eventTypes, _ := cmd.Flags().GetStringSlice("event-types")
		description, _ := cmd.Flags().GetString("description")
		apiURL, _ := cmd.Flags().GetString("api-url")
		apiKey, _ := cmd.Flags().GetString("api-key")

		url = strings.TrimSpace(url)
		secret = strings.TrimSpace(secret)
		description = strings.TrimSpace(description)
		if url == "" {
			return fmt.Errorf("--url is required")
		}
		if secret == "" {
			return fmt.Errorf("--secret is required")
		}
		if len(description) > 1024 {
			return fmt.Errorf("--description must be 1024 characters or fewer")
		}

		body, err := json.Marshal(types.WebhookCreateRequest{
			URL:         url,
			Secret:      secret,
			EventTypes:  eventTypes,
			Description: description,
		})
		if err != nil {
			return err
		}

		resolved := resolveAPIURL(apiURL)
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost,
			resolved+"/api/v1/webhooks", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			logger.Error("cli", "webhook add failed", "error", err.Error())
			return err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("daemon rejected webhook (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		var wh types.Webhook
		if err := json.Unmarshal(respBody, &wh); err != nil {
			return fmt.Errorf("decoding webhook response: %w", err)
		}
		fmt.Printf("Webhook registered: %s\n  URL:    %s\n  Active: %t\n",
			wh.ID, wh.URL, wh.Active)
		if len(wh.EventTypes) > 0 {
			fmt.Printf("  Types:  %s\n", strings.Join(wh.EventTypes, ","))
		}
		if wh.Description != "" {
			fmt.Printf("  Description: %s\n", wh.Description)
		}
		return nil
	},
}

var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered webhooks",
	Long: `List registered webhooks (secrets are always redacted).

Optional filters and ordering:

  --search <q>          Case-insensitive substring filter applied to each
                        webhook's URL, description, and event-type list.
                        IDs, secrets, and last_error are intentionally
                        excluded from the haystack.  Trimmed and lowercased
                        before being forwarded to the daemon.

  --sort <field>        Whitelisted to one of:
                          id, url, created_at, last_delivery_at
                        Default: id.

  --order <asc|desc>    Default: asc.  Sort ascending or descending. Unknown
                        values are rejected client-side before contacting the
                        daemon.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL, _ := cmd.Flags().GetString("api-url")
		apiKey, _ := cmd.Flags().GetString("api-key")
		search, _ := cmd.Flags().GetString("search")
		sortField, _ := cmd.Flags().GetString("sort")
		order, _ := cmd.Flags().GetString("order")

		sortField = strings.TrimSpace(strings.ToLower(sortField))
		if sortField != "" {
			switch sortField {
			case types.WebhookSortID,
				types.WebhookSortURL,
				types.WebhookSortCreatedAt,
				types.WebhookSortLastDelivery:
			default:
				return fmt.Errorf("invalid --sort: must be one of id, url, created_at, last_delivery_at")
			}
		}
		order = strings.TrimSpace(strings.ToLower(order))
		if order != "" {
			switch order {
			case types.SortOrderAsc, types.SortOrderDesc:
			default:
				return fmt.Errorf("invalid --order: must be 'asc' or 'desc'")
			}
		}

		resolved := resolveAPIURL(apiURL)
		endpoint := resolved + "/api/v1/webhooks"
		q := url.Values{}
		if needle := strings.ToLower(strings.TrimSpace(search)); needle != "" {
			q.Set("search", needle)
		}
		if sortField != "" {
			q.Set("sort", sortField)
		}
		if order != "" {
			q.Set("order", order)
		}
		if encoded := q.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet,
			endpoint, nil)
		if err != nil {
			return err
		}
		if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("daemon returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var hooks []*types.Webhook
		if err := json.Unmarshal(body, &hooks); err != nil {
			return fmt.Errorf("decoding webhook list: %w", err)
		}
		if len(hooks) == 0 {
			fmt.Println("No webhooks registered.")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tURL\tACTIVE\tTYPES\tDESCRIPTION\tLAST_STATUS\tLAST_ERROR")
		for _, h := range hooks {
			lastStatus := "-"
			if h.LastStatus != 0 {
				lastStatus = fmt.Sprintf("%d", h.LastStatus)
			}
			etypes := "(all)"
			if len(h.EventTypes) > 0 {
				etypes = strings.Join(h.EventTypes, ",")
			}
			description := h.Description
			if description == "" {
				description = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\t%s\t%s\n", h.ID, h.URL, h.Active, etypes, description, lastStatus, h.LastError)
		}
		return tw.Flush()
	},
}

var webhookEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Update fields on an existing webhook",
	Long: `Update one or more editable fields on an existing webhook.  All
flags are optional, but at least one must be set — running 'webhook edit'
with no fields is rejected with a clear error.

Field semantics:

  --url <url>            Replace the receiver URL.  Must use http:// or https://.
  --secret <s>           Rotate the HMAC signing secret.  Empty string is rejected
                         (an unsigned webhook would defeat receiver-side HMAC checks).
  --event-types a,b,c    Replace the filter list.  Use --clear-event-types to
                         subscribe to every event.  --event-types and --clear-event-types
                         are mutually exclusive.
  --clear-event-types    Clear the event filter so the webhook fires on every event.
  --active true|false    Toggle delivery.  --active=false stops the worker
                         without deleting the registration.
  --description <text>   Replace the free-form description.  Pass "" to clear.
                         Capped at 1024 characters.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		apiURL, _ := cmd.Flags().GetString("api-url")
		apiKey, _ := cmd.Flags().GetString("api-key")

		spec := types.WebhookUpdateSpec{}

		if f := cmd.Flags().Lookup("url"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("url")
			spec.URL = &v
		}
		if f := cmd.Flags().Lookup("secret"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("secret")
			spec.Secret = &v
		}
		eventTypesChanged := false
		if f := cmd.Flags().Lookup("event-types"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetStringSlice("event-types")
			spec.EventTypes = &v
			eventTypesChanged = true
		}
		if clear, _ := cmd.Flags().GetBool("clear-event-types"); clear {
			if eventTypesChanged {
				return fmt.Errorf("--event-types and --clear-event-types are mutually exclusive")
			}
			empty := []string{}
			spec.EventTypes = &empty
		}
		if f := cmd.Flags().Lookup("active"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetBool("active")
			spec.Active = &v
		}
		if f := cmd.Flags().Lookup("description"); f != nil && f.Changed {
			v, _ := cmd.Flags().GetString("description")
			v = strings.TrimSpace(v)
			if len(v) > 1024 {
				return fmt.Errorf("--description must be 1024 characters or fewer")
			}
			spec.Description = &v
		}

		if spec.URL == nil && spec.Secret == nil && spec.EventTypes == nil && spec.Active == nil && spec.Description == nil {
			return fmt.Errorf("no fields to update: pass --url, --secret, --event-types, --clear-event-types, --description, or --active")
		}

		body, err := json.Marshal(spec)
		if err != nil {
			return err
		}

		resolved := resolveAPIURL(apiURL)
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPatch,
			resolved+"/api/v1/webhooks/"+id, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			logger.Error("cli", "webhook edit failed", "error", err.Error())
			return err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("daemon rejected edit (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		var wh types.Webhook
		if err := json.Unmarshal(respBody, &wh); err != nil {
			return fmt.Errorf("decoding webhook response: %w", err)
		}
		fmt.Printf("Webhook updated: %s\n  URL:    %s\n  Active: %t\n",
			wh.ID, wh.URL, wh.Active)
		if len(wh.EventTypes) > 0 {
			fmt.Printf("  Types:  %s\n", strings.Join(wh.EventTypes, ","))
		} else {
			fmt.Println("  Types:  (all events)")
		}
		if wh.Description != "" {
			fmt.Printf("  Description: %s\n", wh.Description)
		}
		return nil
	},
}

var webhookDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a registered webhook (or many via --event-type)",
	Long: `Delete one or many registered webhooks.

Single delete:     vmsmith webhook delete <id>
Bulk by event:     vmsmith webhook delete --event-type vm.deleted

Exactly one of <id> or --event-type is required. The --event-type selector
matches webhooks whose event_types filter contains the exact event type;
catch-all webhooks (empty event_types) are not swept — delete them by id.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		eventType, _ := cmd.Flags().GetString("event-type")
		eventType = strings.TrimSpace(eventType)
		var id string
		if len(args) == 1 {
			id = strings.TrimSpace(args[0])
		}

		if id == "" && eventType == "" {
			return fmt.Errorf("exactly one of <id> or --event-type is required")
		}
		if id != "" && eventType != "" {
			return fmt.Errorf("<id> and --event-type are mutually exclusive")
		}

		apiURL, _ := cmd.Flags().GetString("api-url")
		apiKey, _ := cmd.Flags().GetString("api-key")
		resolved := resolveAPIURL(apiURL)
		apiKey = strings.TrimSpace(apiKey)

		if id != "" {
			return runWebhookSingleDelete(cmd.Context(), resolved, apiKey, id)
		}
		return runWebhookBulkDeleteByEventType(cmd.Context(), resolved, apiKey, eventType)
	},
}

func runWebhookSingleDelete(ctx context.Context, apiURL, apiKey, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL+"/api/v1/webhooks/"+id, nil)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		fmt.Printf("Webhook %s deleted.\n", id)
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("daemon rejected delete (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func runWebhookBulkDeleteByEventType(ctx context.Context, apiURL, apiKey, eventType string) error {
	payload, err := json.Marshal(map[string]any{"event_type": eventType})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		apiURL+"/api/v1/webhooks/bulk_delete", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon rejected bulk delete (HTTP %d): %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Results []struct {
			ID      string `json:"id"`
			Success bool   `json:"success"`
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("parsing daemon response: %w", err)
	}
	if len(out.Results) == 0 {
		fmt.Printf("No webhooks subscribed to event type %q\n", eventType)
		return nil
	}
	successes, failures := 0, 0
	for _, r := range out.Results {
		if r.Success {
			fmt.Printf("OK    %s\n", r.ID)
			successes++
			continue
		}
		fmt.Printf("FAIL  %s: %s\n", r.ID, strings.TrimSpace(r.Message))
		failures++
	}
	if failures > 0 {
		return fmt.Errorf("%d of %d webhooks failed to delete", failures, len(out.Results))
	}
	_ = successes
	return nil
}

func resolveAPIURL(flag string) string {
	if flag = strings.TrimSpace(flag); flag != "" {
		return flag
	}
	cfg, err := config.Load(cfgFile)
	if err == nil {
		return "http://" + cfg.Daemon.Listen
	}
	return "http://localhost:8080"
}

func init() {
	webhookAddCmd.Flags().String("url", "", "target URL (http or https) — required")
	webhookAddCmd.Flags().String("secret", "", "HMAC secret — required")
	webhookAddCmd.Flags().StringSlice("event-types", nil, "comma-separated event-type filters (e.g. vm.started,system.*)")
	webhookAddCmd.Flags().String("description", "", "free-form description (≤1024 chars)")

	webhookListCmd.Flags().String("search", "", "case-insensitive substring filter (matches URL, description, and event-type names)")
	webhookListCmd.Flags().String("sort", "", "sort field: id|url|created_at|last_delivery_at (default id)")
	webhookListCmd.Flags().String("order", "", "sort order: asc|desc (default asc)")

	webhookEditCmd.Flags().String("url", "", "replace receiver URL")
	webhookEditCmd.Flags().String("secret", "", "rotate HMAC signing secret (cannot be empty)")
	webhookEditCmd.Flags().StringSlice("event-types", nil, "replace event-type filter list (comma-separated)")
	webhookEditCmd.Flags().Bool("clear-event-types", false, "clear the event filter so the webhook fires on every event")
	webhookEditCmd.Flags().Bool("active", true, "toggle delivery on/off (only takes effect when explicitly set)")
	webhookEditCmd.Flags().String("description", "", "replace the free-form description (pass \"\" to clear; ≤1024 chars)")

	webhookDeleteCmd.Flags().String("event-type", "",
		"delete every webhook whose event_types filter contains this exact event type (mutually exclusive with <id>)")

	for _, c := range []*cobra.Command{webhookAddCmd, webhookListCmd, webhookEditCmd, webhookDeleteCmd} {
		c.Flags().String("api-url", "", "daemon API URL (defaults to http://<daemon.listen>)")
		c.Flags().String("api-key", os.Getenv("VMSMITH_API_KEY"), "Bearer token for daemons with auth enabled (defaults to $VMSMITH_API_KEY)")
	}
	webhookCmd.AddCommand(webhookAddCmd)
	webhookCmd.AddCommand(webhookListCmd)
	webhookCmd.AddCommand(webhookEditCmd)
	webhookCmd.AddCommand(webhookDeleteCmd)
}
