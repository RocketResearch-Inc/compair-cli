package compair

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
)

var groupFilesCmd = &cobra.Command{
	Use:   "files [group]",
	Short: "List local tracked files for a group (with document IDs)",
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
		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()
		items, err := store.ListByGroup(cmd.Context(), gid)
		if err != nil {
			return err
		}
		fmt.Println("Path\tKind\tDocumentID\tPublished")
		for _, it := range items {
			if it.Kind != "file" {
				continue
			}
			pub := "no"
			if it.Published == 1 {
				pub = "yes"
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", it.Path, it.Kind, it.DocumentID, pub)
		}
		return nil
	},
}

func init() { groupCmd.AddCommand(groupFilesCmd) }
