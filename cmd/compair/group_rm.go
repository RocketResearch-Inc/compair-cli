package compair

import (
	"bufio"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"os"
	"strings"
)

var groupRmYes bool

var groupRmCmd = &cobra.Command{
	Use:   "rm <group>",
	Short: "Delete a group (admin only)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		id, err := groups.ResolveID(client, args[0], viper.GetString("group"))
		if err != nil {
			return err
		}
		users, err := client.ListGroupUsers(id)
		if err != nil {
			return err
		}
		adminIDs, _ := client.ListAdminGroupIDs()
		isAdmin := false
		for _, gid := range adminIDs {
			if gid == id {
				isAdmin = true
				break
			}
		}
		if !isAdmin {
			return fmt.Errorf("you must be an admin of this group to delete it")
		}
		if !groupRmYes {
			warn := fmt.Sprintf("Group has %d user(s).\nType the group ID '%s' to confirm: ", len(users), id)
			fmt.Fprint(os.Stderr, warn)
			in := bufio.NewReader(os.Stdin)
			line, _ := in.ReadString('\n')
			if strings.TrimSpace(line) != id {
				return fmt.Errorf("confirmation failed; aborting")
			}
		}
		if err := client.DeleteGroup(id); err != nil {
			return err
		}
		fmt.Println("Deleted group:", id)
		return nil
	},
}

func init() {
	groupCmd.AddCommand(groupRmCmd)
	groupRmCmd.Flags().BoolVar(&groupRmYes, "yes", false, "Skip confirmation prompt")
}
