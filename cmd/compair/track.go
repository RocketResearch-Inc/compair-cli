package compair

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
)

var trackGroup string
var trackInitialSync bool
var trackCommitLimit int
var trackExtDetail bool
var trackUnpublished bool

var trackCmd = &cobra.Command{
	Use:   "track [PATH]",
	Short: "Create a Compair repo document for the current repo (or a local repo path)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := "."
		if len(args) == 1 {
			target = args[0]
		}

		client := api.NewClient(viper.GetString("api.base"))
		groupID, _, err := groups.ResolveWithAuto(client, trackGroup, viper.GetString("group"))
		if err != nil {
			return err
		}
		if err := ensureWritableGroup(client, groupID); err != nil {
			return err
		}

		remote, root, err := resolveLocalRepo(target, "path")
		if err != nil {
			return err
		}
		_, err = registerRepoDocument(client, groupID, remote, root, repoRegistrationOptions{
			InitialSync: trackInitialSync,
			CommitLimit: trackCommitLimit,
			ExtDetail:   trackExtDetail,
			Unpublished: trackUnpublished,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Tracked repo in group %s: %s\n", groupID, root)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(trackCmd)
	trackCmd.Flags().StringVarP(&trackGroup, "group", "g", "", "Group ID or name to associate (defaults to active group)")
	trackCmd.Flags().BoolVar(&trackInitialSync, "initial-sync", false, "Perform an initial sync after creating the document")
	trackCmd.Flags().IntVar(&trackCommitLimit, "commits", 10, "Number of commits for the initial sync if no prior sync exists")
	trackCmd.Flags().BoolVar(&trackExtDetail, "ext-detail", false, "Include detailed per-commit patches in the initial sync")
	trackCmd.Flags().BoolVar(&trackUnpublished, "unpublished", false, "Keep the repo document unpublished (default: publish so other repos can reference it)")
}
