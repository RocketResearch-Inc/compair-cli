package compair

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
)

var groupVisibility string

var groupUpdateCmd = &cobra.Command{
	Use:   "update [group]",
	Short: "Update group attributes",
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		ident := ""
		if len(args) == 1 {
			ident = args[0]
		}
		gid, _, err := groups.ResolveWithAuto(client, ident, viper.GetString("group"))
		if err != nil {
			return err
		}
		attrs := map[string]string{}
		if groupVisibility != "" {
			attrs["visibility"] = groupVisibility
		}
		if len(attrs) == 0 {
			return fmt.Errorf("no attributes provided (use --visibility)")
		}
		if err := client.UpdateGroup(gid, attrs); err != nil {
			return err
		}
		fmt.Println("Updated group:", gid)
		return nil
	},
}

func init() {
	groupCmd.AddCommand(groupUpdateCmd)
	groupUpdateCmd.Flags().StringVar(&groupVisibility, "visibility", "", "Group visibility: private|public|internal")
}
