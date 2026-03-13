package compair

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"strings"
)

var groupCmd = &cobra.Command{Use: "group", Short: "Group management"}

var groupLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List groups",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		items, err := client.ListGroups(true)
		if err != nil {
			return err
		}
		active := ""
		if id, err := config.ResolveActiveGroup(viper.GetString("group")); err == nil {
			active = id
		}
		// Normalize active to an ID if it's a name
		normalized := active
		if normalized != "" {
			// if it's not an ID, try match by name
			if !(len(normalized) == 36 && strings.Count(normalized, "-") == 4) && !strings.HasPrefix(normalized, "grp_") {
				for _, g := range items {
					if strings.EqualFold(strings.TrimSpace(g.Name), strings.TrimSpace(normalized)) {
						id := g.ID
						if id == "" {
							id = g.GroupID
						}
						normalized = id
						break
					}
				}
			}
		}
		// Header
		fmt.Println("ID\tName\tVisibility")
		for _, g := range items {
			id := g.ID
			if id == "" {
				id = g.GroupID
			}
			name := g.Name
			if normalized != "" && id == normalized {
				name += " (active)"
			}
			vis := g.Visibility
			fmt.Printf("%s\t%s\t%s\n", id, name, vis)
		}
		return nil
	},
}

var groupUseCmd = &cobra.Command{
	Use:   "use <group-id|group-name>",
	Short: "Set active group",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		id, err := groups.ResolveID(client, args[0], viper.GetString("group"))
		if err != nil {
			return err
		}
		return config.WriteActiveGroup(id)
	},
}

var groupCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Print active group",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		id, _, err := groups.ResolveWithAuto(client, "", viper.GetString("group"))
		if err != nil {
			return err
		}
		fmt.Println(id)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(groupCmd)
	groupCmd.AddCommand(groupLsCmd)
	groupCmd.AddCommand(groupUseCmd)
	groupCmd.AddCommand(groupCurrentCmd)
}
