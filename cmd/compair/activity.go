package compair

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var activityPage int
var activityPageSize int
var activityIncludeOwn bool
var activityUser string
var activityJSON bool

var activityCmd = &cobra.Command{
	Use:   "activity",
	Short: "Show recent activity across your groups",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		opts := api.ActivityFeedOptions{
			UserID:     activityUser,
			Page:       activityPage,
			PageSize:   activityPageSize,
			IncludeOwn: activityIncludeOwn,
		}
		resp, err := client.GetActivityFeed(opts)
		if err != nil {
			return err
		}
		if activityJSON {
			printer.PrintJSON(resp)
			return nil
		}
		if len(resp.Activities) == 0 {
			fmt.Println("No activity found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Time\tUser\tAction\tObject\tGroup")
		for _, item := range resp.Activities {
			ts := formatTimestamp(item.Timestamp)
			user := item.User
			if user == "" {
				user = item.UserID
			}
			obj := item.Object
			if obj == "" {
				obj = item.ObjectName
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", ts, user, item.Action, obj, item.GroupID)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(activityCmd)
	activityCmd.Flags().IntVar(&activityPage, "page", 1, "Page number")
	activityCmd.Flags().IntVar(&activityPageSize, "page-size", 20, "Items per page")
	activityCmd.Flags().BoolVar(&activityIncludeOwn, "include-own", true, "Include your own activity entries")
	activityCmd.Flags().StringVar(&activityUser, "user", "", "Filter activity by user id")
	activityCmd.Flags().BoolVar(&activityJSON, "json", false, "Output raw JSON")
}
