package compair

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
)

var groupJoinCmd = &cobra.Command{
	Use:   "join <group>",
	Short: "Join or request to join a group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		id, err := groups.ResolveID(client, args[0], viper.GetString("group"))
		if err != nil {
			return err
		}
		if err := client.JoinGroup(id); err != nil {
			return err
		}
		printer.Success("Requested to join group: " + id)
		return nil
	},
}

func init() { groupCmd.AddCommand(groupJoinCmd) }
