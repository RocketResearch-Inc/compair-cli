package compair

import (
	"fmt"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var feedbackLengthCmd = &cobra.Command{
	Use:   "feedback-length <brief|detailed|verbose>",
	Short: "Set the preferred length for generated feedback on your documents",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		value, err := normalizeFeedbackLength(args[0])
		if err != nil {
			return err
		}
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.UpdateUser(map[string]string{
			"preferred_feedback_length": value,
		}); err != nil {
			return err
		}
		printer.Success("Feedback length set to " + value + ".")
		return nil
	},
}

func normalizeFeedbackLength(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "brief":
		return "Brief", nil
	case "detailed", "detail":
		return "Detailed", nil
	case "verbose", "full":
		return "Verbose", nil
	default:
		return "", fmt.Errorf("expected one of: brief, detailed, verbose")
	}
}

func init() {
	rootCmd.AddCommand(feedbackLengthCmd)
}
