package compair

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
)

var groupCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		if err := client.CreateGroup(args[0], "", "", "", ""); err != nil {
			return err
		}
		printer.Success("Created group: " + args[0])
		return nil
	},
}

func init() { groupCmd.AddCommand(groupCreateCmd) }
