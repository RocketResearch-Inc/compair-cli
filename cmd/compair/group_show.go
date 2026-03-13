package compair

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
)

var groupShowCmd = &cobra.Command{
	Use:   "show [group]",
	Short: "Show group details (name, id, visibility)",
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
		// try all groups then own groups
		for _, own := range []bool{false, true} {
			items, err := client.ListGroups(own)
			if err != nil {
				continue
			}
			for _, g := range items {
				id := g.ID
				if id == "" {
					id = g.GroupID
				}
				if id == gid {
					fmt.Printf("ID: %s\nName: %s\n", id, g.Name)
					if g.Visibility != "" {
						fmt.Printf("Visibility: %s\n", g.Visibility)
					}
					return nil
				}
			}
		}
		fmt.Println("Group:", gid)
		return nil
	},
}

func init() { groupCmd.AddCommand(groupShowCmd) }
