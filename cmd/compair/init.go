package compair

import (
	"fmt"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var initGroup string
var initInitialSync bool
var initNoFeedback bool
var initCommitLimit int
var initExtDetail bool
var initUnpublished bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Compair in the current git repo (creates a document)",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := git.RepoRoot()
		if err != nil {
			return fmt.Errorf("not a git repo")
		}
		remote, err := git.OriginURL()
		if err != nil {
			return err
		}
		if initGroup == "" {
			if g, _, err := groups.ResolveWithAuto(api.NewClient(viper.GetString("api.base")), "", viper.GetString("group")); err == nil {
				initGroup = g
			} else {
				return fmt.Errorf("--group <group> is required (or set/auto-select active group)")
			}
		}

		client := api.NewClient(viper.GetString("api.base"))
		_, err = registerRepoDocument(client, initGroup, remote, root, repoRegistrationOptions{
			InitialSync:       initInitialSync,
			InitialNoFeedback: initNoFeedback,
			CommitLimit:       initCommitLimit,
			ExtDetail:         initExtDetail,
			Unpublished:       initUnpublished,
		})
		return err
	},
}

func init() {
	initCmd.Flags().StringVarP(&initGroup, "group", "g", "", "Group ID to associate")
	initCmd.Flags().BoolVar(&initInitialSync, "initial-sync", false, "Perform an initial sync after creating the document")
	initCmd.Flags().BoolVar(&initNoFeedback, "no-feedback", false, "When used with --initial-sync, upload the baseline without generating feedback")
	initCmd.Flags().IntVar(&initCommitLimit, "commits", 10, "Number of commits for initial sync if no prior sync exists")
	initCmd.Flags().BoolVar(&initExtDetail, "ext-detail", false, "Include detailed per-commit patches in initial sync")
	initCmd.Flags().BoolVar(&initUnpublished, "unpublished", false, "Keep the repo document unpublished (default: publish so other repos can reference it)")
}
