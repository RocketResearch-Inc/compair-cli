package compair

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var waitOpenSystem bool
var waitTimeout string

func parseWaitTimeoutSeconds(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "10m"
	}
	if trimmed == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid --timeout value %q: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid --timeout value %q: must be >= 0", raw)
	}
	if d == 0 {
		return 0, nil
	}
	return int((d + time.Second - 1) / time.Second), nil
}

var waitCmd = &cobra.Command{
	Use:          "wait [PATH ...]",
	Short:        "Wait for saved pending Compair processing tasks and fetch the resulting feedback",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		oldProcessTimeout := syncProcessTimeoutSec
		defer func() { syncProcessTimeoutSec = oldProcessTimeout }()
		timeoutChanged := false
		if flag := cmd.Flags().Lookup("timeout"); flag != nil {
			timeoutChanged = flag.Changed
		}
		processTimeoutChanged := false
		if flag := cmd.Flags().Lookup("process-timeout-sec"); flag != nil {
			processTimeoutChanged = flag.Changed
		}
		if timeoutChanged || !processTimeoutChanged {
			timeoutSec, err := parseWaitTimeoutSeconds(waitTimeout)
			if err != nil {
				return err
			}
			syncProcessTimeoutSec = timeoutSec
		}

		reportPath := writeMD
		if reportPath == "" {
			reportPath = defaultReportPath()
			writeMD = reportPath
		}

		var before time.Time
		if info, err := os.Stat(reportPath); err == nil {
			before = info.ModTime()
		}

		client := api.NewClient(viper.GetString("api.base"))
		groupID, _, err := groups.ResolveWithAuto(client, "", viper.GetString("group"))
		if err != nil {
			return fmt.Errorf("%w\nTip: run 'compair group ls' then 'compair group use <id>' (or pass --group).", err)
		}
		roots, err := collectRepoRoots(args, groupID, syncAll)
		if err != nil {
			return err
		}
		if len(roots) == 0 {
			return fmt.Errorf("no repositories found to wait on")
		}
		rootList := make([]string, 0, len(roots))
		for root := range roots {
			rootList = append(rootList, root)
		}
		sort.Strings(rootList)

		completed, err := waitForSavedPendingRepoTasks(cmd.Context(), client, groupID, rootList)
		if err != nil {
			return err
		}
		if completed == 0 {
			printer.Info("No saved pending repo tasks found for the selected scope.")
			return nil
		}

		oldFeedbackWait := feedbackWaitSec
		defer func() { feedbackWaitSec = oldFeedbackWait }()
		feedbackWaitSec = 0

		if err := runSyncCommand(cmd, args, syncInvocationMode{
			FetchOnly:           true,
			SkipInitialSyncWait: true,
		}); err != nil {
			return err
		}

		info, err := os.Stat(reportPath)
		if err != nil {
			return nil
		}
		if !before.IsZero() && !info.ModTime().After(before) {
			return nil
		}
		if waitOpenSystem {
			return openWithSystem(reportPath)
		}
		return renderSingle(feedbackReport{Path: reportPath, ModTime: info.ModTime().UnixNano()})
	},
}

func init() {
	rootCmd.AddCommand(waitCmd)
	waitCmd.Flags().BoolVar(&syncAll, "all", false, "Wait on all tracked repos in the active group")
	waitCmd.Flags().StringVar(&writeMD, "write-md", "", "Write the fetched Markdown report to the given path")
	waitCmd.Flags().BoolVar(&syncJSON, "json", false, "Output machine-readable sync summary JSON")
	waitCmd.Flags().StringVar(&waitTimeout, "timeout", "10m", "How long to keep waiting for backend processing (for example: 30s, 10m, 1h, or 0 to wait indefinitely)")
	waitCmd.Flags().IntVar(&syncProcessTimeoutSec, "process-timeout-sec", 600, "Max seconds to wait for backend processing per document (0 waits indefinitely)")
	waitCmd.Flags().BoolVar(&waitOpenSystem, "system", false, "Open the generated report using the system default viewer")
	hideCommandFlags(waitCmd, "process-timeout-sec")
}
