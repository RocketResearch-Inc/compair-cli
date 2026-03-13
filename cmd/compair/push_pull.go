package compair

import "github.com/spf13/cobra"

var pushCmd = &cobra.Command{
	Use:   "push [PATH ...]",
	Short: "Upload local repo changes without fetching feedback",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSyncCommand(cmd, args, syncInvocationMode{
			FetchOnly: false,
			PushOnly:  true,
		})
	},
}

var pullCmd = &cobra.Command{
	Use:   "pull [PATH ...]",
	Short: "Fetch feedback without uploading new changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSyncCommand(cmd, args, syncInvocationMode{
			FetchOnly: true,
			PushOnly:  false,
		})
	},
}

func init() {
	rootCmd.AddCommand(pushCmd)
	rootCmd.AddCommand(pullCmd)
	addSyncFlags(pushCmd, false)
	addSyncFlags(pullCmd, false)
	hideCommandFlags(pushCmd,
		"write-md",
		"feedback-wait",
		"gate",
		"fail-on-feedback",
		"fail-on-severity",
		"fail-on-type",
	)
	hideCommandFlags(pullCmd,
		"commits",
		"ext-detail",
		"snapshot-mode",
		"snapshot-max-tree",
		"snapshot-max-files",
		"snapshot-max-total-bytes",
		"snapshot-max-file-bytes",
		"snapshot-max-file-read",
		"snapshot-include",
		"snapshot-exclude",
		"dry-run",
		"process-timeout-sec",
	)
}

func hideCommandFlags(cmd *cobra.Command, names ...string) {
	for _, name := range names {
		if f := cmd.Flags().Lookup(name); f != nil {
			f.Hidden = true
		}
	}
}
