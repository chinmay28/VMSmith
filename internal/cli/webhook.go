package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		apiURL, _ := cmd.Flags().GetString("api-url")
		apiKey, _ := cmd.Flags().GetString("api-key")

		url = strings.TrimSpace(url)
		secret = strings.TrimSpace(secret)
		if url == "" {
			return fmt.Errorf("--url is required")
		}
		if secret == "" {
			return fmt.Errorf("--secret is required")
		}

		body, err := json.Marshal(types.WebhookCreateRequest{
			URL:        url,
			Secret:     secret,
			EventTypes: eventTypes,
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
		return nil
	},
}

var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered webhooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL, _ := cmd.Flags().GetString("api-url")
		apiKey, _ := cmd.Flags().GetString("api-key")

		resolved := resolveAPIURL(apiURL)
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet,
			resolved+"/api/v1/webhooks", nil)
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
		fmt.Fprintln(tw, "ID\tURL\tACTIVE\tTYPES\tLAST_STATUS\tLAST_ERROR")
		for _, h := range hooks {
			lastStatus := "-"
			if h.LastStatus != 0 {
				lastStatus = fmt.Sprintf("%d", h.LastStatus)
			}
			etypes := "(all)"
			if len(h.EventTypes) > 0 {
				etypes = strings.Join(h.EventTypes, ",")
			}
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\t%s\n", h.ID, h.URL, h.Active, etypes, lastStatus, h.LastError)
		}
		return tw.Flush()
	},
}

var webhookDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a registered webhook",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := strings.TrimSpace(args[0])
		apiURL, _ := cmd.Flags().GetString("api-url")
		apiKey, _ := cmd.Flags().GetString("api-key")

		resolved := resolveAPIURL(apiURL)
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodDelete,
			resolved+"/api/v1/webhooks/"+id, nil)
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
		if resp.StatusCode == http.StatusNoContent {
			fmt.Printf("Webhook %s deleted.\n", id)
			return nil
		}
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("daemon rejected delete (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	},
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
	for _, c := range []*cobra.Command{webhookAddCmd, webhookListCmd, webhookDeleteCmd} {
		c.Flags().String("api-url", "", "daemon API URL (defaults to http://<daemon.listen>)")
		c.Flags().String("api-key", os.Getenv("VMSMITH_API_KEY"), "Bearer token for daemons with auth enabled (defaults to $VMSMITH_API_KEY)")
	}
	webhookCmd.AddCommand(webhookAddCmd)
	webhookCmd.AddCommand(webhookListCmd)
	webhookCmd.AddCommand(webhookDeleteCmd)
}

