package compair

import (
	"fmt"
	"os"
	"text/tabwriter"

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
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "EventID\tSeverity\tIntent\tDoc\tStatus\tCreated")
		for _, event := range resp.Events {
			status := "new"
			if formatTimestamp(event.DismissedAt) != "" {
				status = "dismissed"
			} else if formatTimestamp(event.AcknowledgedAt) != "" {
				status = "acknowledged"
			}
			fmt.Fprintf(
				w,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				event.EventID,
				event.Severity,
				event.Intent,
				event.TargetDocID,
				status,
				formatTimestamp(event.CreatedAt),
			)
		}
		return w.Flush()
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
