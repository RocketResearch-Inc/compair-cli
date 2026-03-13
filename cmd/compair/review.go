package compair

import (
	"os"
	"time"

	"github.com/spf13/cobra"
)

var reviewOpenSystem bool

var reviewCmd = &cobra.Command{
	Use:   "review [PATH ...]",
	Short: "Run a full Compair review and write the latest report",
	RunE: func(cmd *cobra.Command, args []string) error {
		reportPath := writeMD
		if reportPath == "" {
			reportPath = defaultReportPath()
			writeMD = reportPath
		}

		var before time.Time
		if info, err := os.Stat(reportPath); err == nil {
			before = info.ModTime()
		}

		if err := runSyncCommand(cmd, args, syncInvocationMode{}); err != nil {
			return err
		}

		info, err := os.Stat(reportPath)
		if err != nil {
			return nil
		}
		if !before.IsZero() && !info.ModTime().After(before) {
			return nil
		}
		if reviewOpenSystem {
			return openWithSystem(reportPath)
		}
		return renderSingle(feedbackReport{Path: reportPath, ModTime: info.ModTime().UnixNano()})
	},
}

func init() {
	rootCmd.AddCommand(reviewCmd)
	addSyncFlags(reviewCmd, true)
	reviewCmd.Flags().BoolVar(&reviewOpenSystem, "system", false, "Open the generated report using the system default viewer")
	hideCommandFlags(reviewCmd,
		"write-md",
		"push-only",
		"fetch-only",
	)
}
