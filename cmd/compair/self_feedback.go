package compair

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var selfFeedbackCmd = &cobra.Command{
	Use:   "self-feedback <on|off>",
	Short: "Enable or disable using your own published documents as feedback references",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		enabled, err := parseSelfFeedbackState(args[0])
		if err != nil {
			return err
		}
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.UpdateUser(map[string]string{
			"include_own_documents_in_feedback": strconv.FormatBool(enabled),
		}); err != nil {
			return err
		}
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		printer.Success("Self-feedback " + state + ".")
		return nil
	},
}

func parseSelfFeedbackState(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "enable", "enabled", "true", "yes":
		return true, nil
	case "off", "disable", "disabled", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("expected one of: on, off")
	}
}

func init() {
	rootCmd.AddCommand(selfFeedbackCmd)
}
