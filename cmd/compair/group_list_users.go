package compair

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var groupListUsersCmd = &cobra.Command{
	Use:   "list-users [group]",
	Short: "List users in a group",
	Args:  cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		ident := ""
		if len(args) == 1 {
			ident = args[0]
		}
		groupID, _, err := groups.ResolveWithAuto(client, ident, viper.GetString("group"))
		if err != nil {
			return err
		}
		users, err := client.ListGroupUsers(groupID)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tName\tUsername\tRole\tStatus")
		for _, u := range users {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", u.ID, u.Name, u.Username, u.Role, u.Status)
		}
		return w.Flush()
	},
}

func init() { groupCmd.AddCommand(groupListUsersCmd) }
