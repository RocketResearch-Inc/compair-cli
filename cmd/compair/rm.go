package compair

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	fsutil "github.com/RocketResearch-Inc/compair-cli/internal/fs"
)

var rmCmd = &cobra.Command{
	Use:   "rm [PATH ...]",
	Short: "Untrack files/dirs/repos in the active group",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		group, err := config.ResolveActiveGroup(viper.GetString("group"))
		if err != nil {
			return err
		}
		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()
		ctx := context.Background()
		for _, p := range args {
			can, err := fsutil.CanonicalPath(p, true)
			if err != nil {
				return err
			}
			n, err := store.DeleteByPathGroup(ctx, can, group)
			if err != nil {
				return err
			}
			if n == 0 {
				fmt.Println("Not tracked:", can)
			} else {
				fmt.Println("Removed:", can)
			}
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(rmCmd) }
