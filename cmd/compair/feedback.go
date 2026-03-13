package compair

import (
	"fmt"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var feedbackCmd = &cobra.Command{
	Use:   "feedback",
	Short: "Manage feedback entries",
}

var feedbackRateValue string
var feedbackRateCmd = &cobra.Command{
	Use:   "rate <feedback_id>",
	Short: "Rate feedback as positive or negative (or clear)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		value := strings.ToLower(strings.TrimSpace(feedbackRateValue))
		switch value {
		case "positive", "negative", "clear", "":
		default:
			return fmt.Errorf("invalid value: %s (use positive, negative, or clear)", value)
		}
		if value == "clear" {
			value = ""
		}
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.RateFeedback(args[0], value); err != nil {
			return err
		}
		printer.Success("Rated feedback " + args[0])
		return nil
	},
}

var feedbackUnhide bool
var feedbackHideCmd = &cobra.Command{
	Use:   "hide <feedback_id>",
	Short: "Hide or unhide a feedback entry",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.HideFeedback(args[0], !feedbackUnhide); err != nil {
			return err
		}
		state := "hidden"
		if feedbackUnhide {
			state = "visible"
		}
		printer.Success("Set feedback " + args[0] + " to " + state)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(feedbackCmd)
	feedbackCmd.AddCommand(feedbackRateCmd)
	feedbackCmd.AddCommand(feedbackHideCmd)
	feedbackRateCmd.Flags().StringVar(&feedbackRateValue, "value", "positive", "Feedback rating (positive, negative, clear)")
	feedbackHideCmd.Flags().BoolVar(&feedbackUnhide, "unhide", false, "Unhide the feedback")
}
