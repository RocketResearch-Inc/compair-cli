package compair

import (
	"fmt"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var notificationsAllGroups bool
var notificationsIncludeAck bool
var notificationsIncludeDismiss bool
var notificationsPage int
var notificationsPageSize int
var notificationsJSON bool
var notificationsShareNote string

var notificationsCmd = &cobra.Command{
	Use:   "notifications",
	Short: "List notification events for your groups",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		groupID := ""
		if !notificationsAllGroups {
			var err error
			groupID, _, err = groups.ResolveWithAuto(client, "", viper.GetString("group"))
			if err != nil {
				return err
			}
		}
		opts := api.NotificationEventsOptions{
			GroupID:             groupID,
			Page:                notificationsPage,
			PageSize:            notificationsPageSize,
			IncludeAcknowledged: notificationsIncludeAck,
			IncludeDismissed:    notificationsIncludeDismiss,
		}
		resp, err := client.ListNotificationEvents(opts)
		if err != nil {
			return err
		}
		if notificationsJSON {
			printer.PrintJSON(resp)
			return nil
		}
		if len(resp.Events) == 0 {
			fmt.Println("No notification events found.")
			return nil
		}
		rendered, err := renderMarkdown(renderNotificationEventsMarkdown(resp.Events, notificationsAllGroups))
		if err != nil {
			return err
		}
		fmt.Print(rendered)
		return nil
	},
}

var notificationsAckCmd = &cobra.Command{
	Use:   "ack <event_id>",
	Short: "Acknowledge a notification event",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.AcknowledgeNotificationEvent(args[0]); err != nil {
			return err
		}
		printer.Success("Acknowledged " + args[0])
		return nil
	},
}

var notificationsDismissCmd = &cobra.Command{
	Use:   "dismiss <event_id>",
	Short: "Dismiss a notification event",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.DismissNotificationEvent(args[0]); err != nil {
			return err
		}
		printer.Success("Dismissed " + args[0])
		return nil
	},
}

var notificationsShareCmd = &cobra.Command{
	Use:   "share <event_id>",
	Short: "Share a notification event with group members",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.ShareNotificationEvent(args[0], notificationsShareNote); err != nil {
			return err
		}
		printer.Success("Shared " + args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(notificationsCmd)
	notificationsCmd.Flags().BoolVar(&notificationsAllGroups, "all-groups", false, "Include events from all groups")
	notificationsCmd.Flags().BoolVar(&notificationsIncludeAck, "include-ack", false, "Include acknowledged events")
	notificationsCmd.Flags().BoolVar(&notificationsIncludeDismiss, "include-dismiss", false, "Include dismissed events")
	notificationsCmd.Flags().IntVar(&notificationsPage, "page", 1, "Page number")
	notificationsCmd.Flags().IntVar(&notificationsPageSize, "page-size", 20, "Items per page")
	notificationsCmd.Flags().BoolVar(&notificationsJSON, "json", false, "Output raw JSON")

	notificationsCmd.AddCommand(notificationsAckCmd)
	notificationsCmd.AddCommand(notificationsDismissCmd)
	notificationsCmd.AddCommand(notificationsShareCmd)
	notificationsShareCmd.Flags().StringVar(&notificationsShareNote, "note", "", "Optional note to include when sharing")
}

func printNotificationEvent(event api.NotificationEvent, includeGroup bool) {
	severity := strings.ToUpper(strings.TrimSpace(event.Severity))
	if severity == "" {
		severity = "UNKNOWN"
	}
	intent := strings.TrimSpace(event.Intent)
	if intent == "" {
		intent = "unknown"
	}
	created := formatTimestamp(event.CreatedAt)
	if created == "" {
		created = "unknown"
	}
	fmt.Printf("%s  %s  %s  %s  %s\n", event.EventID, severity, intent, notificationStatus(event), created)
	if docID := strings.TrimSpace(event.TargetDocID); docID != "" {
		fmt.Println("  Doc:", docID)
	}
	if includeGroup {
		if groupID := strings.TrimSpace(event.GroupID); groupID != "" {
			fmt.Println("  Group:", groupID)
		}
	}
	if delivery := strings.TrimSpace(event.DeliveryAction); delivery != "" {
		line := "  Delivery: " + delivery
		if channel := strings.TrimSpace(event.Channel); channel != "" {
			line += " via " + channel
		}
		fmt.Println(line)
	}
	if len(event.PeerDocIDs) > 0 {
		fmt.Println("  Peer docs:", strings.Join(event.PeerDocIDs, ", "))
	}
	rationale := nonEmptyLines(event.Rationale)
	if len(rationale) > 0 {
		fmt.Println("  Rationale:")
		for _, line := range rationale {
			fmt.Println("   -", line)
		}
	}
	if snippet := truncateText(event.EvidenceTarget, 180); snippet != "" {
		fmt.Println("  Target evidence:", snippet)
	}
	if snippet := truncateText(event.EvidencePeer, 180); snippet != "" {
		fmt.Println("  Peer evidence:", snippet)
	}
}

func notificationStatus(event api.NotificationEvent) string {
	if formatTimestamp(event.DismissedAt) != "" {
		return "dismissed"
	}
	if formatTimestamp(event.AcknowledgedAt) != "" {
		return "acknowledged"
	}
	return "new"
}

func renderNotificationEventsMarkdown(events []api.NotificationEvent, includeGroup bool) string {
	lines := []string{
		"Generated: " + time.Now().Format(time.RFC3339),
		"",
		"## Summary",
		"",
		fmt.Sprintf("- %d notification(s).", len(events)),
		"",
	}

	for idx, event := range events {
		appendMarkdownHeading(&lines, fmt.Sprintf("## Notification %d", idx+1))
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("**Event ID:** `%s`", strings.TrimSpace(event.EventID)))
		lines = append(lines, fmt.Sprintf("**Time:** %s", fallbackString(formatTimestamp(event.CreatedAt), "unknown")))
		lines = append(lines, fmt.Sprintf("**Severity:** %s", fallbackString(strings.ToUpper(strings.TrimSpace(event.Severity)), "UNKNOWN")))
		lines = append(lines, fmt.Sprintf("**Intent:** %s", fallbackString(strings.TrimSpace(event.Intent), "unknown")))
		lines = append(lines, fmt.Sprintf("**Status:** %s", notificationStatus(event)))
		if docID := strings.TrimSpace(event.TargetDocID); docID != "" {
			lines = append(lines, fmt.Sprintf("**Target Doc:** `%s`", docID))
		}
		if includeGroup {
			if groupID := strings.TrimSpace(event.GroupID); groupID != "" {
				lines = append(lines, fmt.Sprintf("**Group:** `%s`", groupID))
			}
		}
		if delivery := strings.TrimSpace(event.DeliveryAction); delivery != "" {
			line := "**Delivery:** " + delivery
			if channel := strings.TrimSpace(event.Channel); channel != "" {
				line += " via " + channel
			}
			lines = append(lines, line)
		}
		if len(event.PeerDocIDs) > 0 {
			lines = append(lines, fmt.Sprintf("**Peer Docs:** `%s`", strings.Join(event.PeerDocIDs, "`, `")))
		}
		rationale := nonEmptyLines(event.Rationale)
		if len(rationale) > 0 {
			lines = append(lines, "", "**Rationale**", "")
			for _, line := range rationale {
				lines = append(lines, "- "+line)
			}
		}
		if snippet := strings.TrimSpace(event.EvidenceTarget); snippet != "" {
			lines = append(lines, "", "**Target Evidence**", "")
			appendFencedMarkdownBlock(&lines, "text", snippet)
		}
		if snippet := strings.TrimSpace(event.EvidencePeer); snippet != "" {
			lines = append(lines, "", "**Peer Evidence**", "")
			appendFencedMarkdownBlock(&lines, "text", snippet)
		}
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

func fallbackString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func nonEmptyLines(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}
