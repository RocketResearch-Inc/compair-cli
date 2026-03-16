package compair

import (
	"fmt"
	"strings"

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
		for idx, event := range resp.Events {
			if idx > 0 {
				fmt.Println()
			}
			printNotificationEvent(event, notificationsAllGroups)
		}
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

func nonEmptyLines(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}
