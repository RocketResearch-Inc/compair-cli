package compair

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
)

var snapshotAll bool
var snapshotOutput string

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Work with baseline snapshots",
}

var snapshotPreviewCmd = &cobra.Command{
	Use:   "preview [PATH ...]",
	Short: "Generate a baseline snapshot without uploading it",
	RunE: func(cmd *cobra.Command, args []string) error {
		groupID := ""
		var err error
		if snapshotAll {
			groupID, err = config.ResolveActiveGroup(viper.GetString("group"))
			if err != nil {
				return err
			}
		}
		roots, err := collectRepoRoots(args, groupID, snapshotAll)
		if err != nil {
			return err
		}
		if len(roots) == 0 {
			printer.Info("No repositories found to snapshot.")
			return nil
		}
		opts := resolveSnapshotOptions(cmd)
		ids := make([]string, 0, len(roots))
		for root := range roots {
			ids = append(ids, root)
		}
		sort.Strings(ids)
		multi := len(ids) > 1
		for _, root := range ids {
			repoCfg := loadRepoConfig(root)
			activeGroup := groupID
			if activeGroup == "" && repoCfg.GroupID != "" {
				activeGroup = repoCfg.GroupID
			}
			res, err := buildRepoSnapshot(root, activeGroup, &config.Repo{
				RemoteURL:        repoCfg.Remote,
				DefaultBranch:    repoCfg.DefaultBranch,
				LastSyncedCommit: repoCfg.LastSyncedCommit,
				DocumentID:       repoCfg.DocumentID,
			}, opts)
			if err != nil {
				printer.Warn(fmt.Sprintf("Snapshot failed for %s: %v", root, err))
				continue
			}
			if strings.TrimSpace(res.Text) == "" {
				printer.Info("No snapshot content for " + root)
				continue
			}
			outPath, err := outputPathForRepo(snapshotOutput, root, "snapshot", multi)
			if err != nil {
				return err
			}
			if outPath == "" {
				if multi {
					fmt.Println("----- Snapshot (" + filepath.Base(root) + ") -----")
				}
				fmt.Println(res.Text)
				if multi {
					fmt.Println()
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(outPath, []byte(res.Text), 0o644); err != nil {
				return err
			}
			printer.Success("Snapshot written to " + outPath)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
	snapshotCmd.AddCommand(snapshotPreviewCmd)
	snapshotPreviewCmd.Flags().BoolVar(&snapshotAll, "all", false, "Snapshot all tracked repos in the active group")
	snapshotPreviewCmd.Flags().StringVar(&snapshotOutput, "output", "", "Write output to a file (or directory when snapshotting multiple repos)")
	defaultSnapshot := defaultSnapshotOptions()
	snapshotPreviewCmd.Flags().IntVar(&snapshotMaxTree, "snapshot-max-tree", defaultSnapshot.MaxTreeEntries, "Snapshot limit: max tree entries (0 = full repo)")
	snapshotPreviewCmd.Flags().IntVar(&snapshotMaxFiles, "snapshot-max-files", defaultSnapshot.MaxFiles, "Snapshot limit: max included files (0 = full repo)")
	snapshotPreviewCmd.Flags().IntVar(&snapshotMaxTotalBytes, "snapshot-max-total-bytes", defaultSnapshot.MaxTotalBytes, "Snapshot limit: total content budget in bytes (0 = full repo)")
	snapshotPreviewCmd.Flags().IntVar(&snapshotMaxFileBytes, "snapshot-max-file-bytes", defaultSnapshot.MaxFileBytes, "Snapshot limit: max bytes per file (0 = full file)")
	snapshotPreviewCmd.Flags().IntVar(&snapshotMaxFileRead, "snapshot-max-file-read", defaultSnapshot.MaxFileRead, "Snapshot limit: max bytes read per file (0 = no read cap)")
	snapshotPreviewCmd.Flags().StringArrayVar(&snapshotInclude, "snapshot-include", nil, "Snapshot include glob (repeatable)")
	snapshotPreviewCmd.Flags().StringArrayVar(&snapshotExclude, "snapshot-exclude", nil, "Snapshot exclude glob (repeatable)")
}
