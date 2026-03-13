package compair

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	fsutil "github.com/RocketResearch-Inc/compair-cli/internal/fs"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
)

var statsAll bool
var statsJSON bool

type repoStats struct {
	Root       string         `json:"root"`
	Remote     string         `json:"remote"`
	Head       string         `json:"head"`
	Languages  map[string]int `json:"languages"`
	TotalFiles int            `json:"total_files"`
	Stats      snapshotStats  `json:"snapshot"`
}

var statsCmd = &cobra.Command{
	Use:   "stats [PATH ...]",
	Short: "Summarize repo language mix and snapshot coverage",
	RunE: func(cmd *cobra.Command, args []string) error {
		groupID := ""
		var err error
		if statsAll {
			groupID, err = config.ResolveActiveGroup(viper.GetString("group"))
			if err != nil {
				return err
			}
		}
		roots, err := collectRepoRoots(args, groupID, statsAll)
		if err != nil {
			return err
		}
		if len(roots) == 0 {
			printer.Info("No repositories found.")
			return nil
		}
		opts := resolveSnapshotOptions(cmd)
		ids := make([]string, 0, len(roots))
		for root := range roots {
			ids = append(ids, root)
		}
		sort.Strings(ids)
		results := []repoStats{}
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
				printer.Warn(fmt.Sprintf("Stats failed for %s: %v", root, err))
				continue
			}
			langs, total, err := collectLanguageCounts(root, opts)
			if err != nil {
				printer.Warn(fmt.Sprintf("Language scan failed for %s: %v", root, err))
				continue
			}
			remote := repoCfg.Remote
			if remote == "" {
				remote = filepath.Base(root)
			}
			results = append(results, repoStats{
				Root:       root,
				Remote:     remote,
				Head:       res.Head,
				Languages:  langs,
				TotalFiles: total,
				Stats:      res.Stats,
			})
		}
		if statsJSON {
			out, _ := json.MarshalIndent(results, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		for _, r := range results {
			fmt.Println("Repo:", r.Remote)
			fmt.Println("Root:", r.Root)
			if r.Head != "" {
				fmt.Println("Head:", r.Head)
			}
			fmt.Printf("Tracked files: %d (snapshot budget: %s, included: %d files)\n", r.TotalFiles, describeSnapshotLimitBytes(r.Stats.BudgetBytes), r.Stats.IncludedFiles)
			if len(r.Languages) > 0 {
				fmt.Println("Languages:")
				keys := make([]string, 0, len(r.Languages))
				for k := range r.Languages {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Printf("  %-12s %d\n", k, r.Languages[k])
				}
			}
			fmt.Println()
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statsCmd)
	statsCmd.Flags().BoolVar(&statsAll, "all", false, "Include all tracked repos in the active group")
	statsCmd.Flags().BoolVar(&statsJSON, "json", false, "Output JSON")
	defaultSnapshot := defaultSnapshotOptions()
	statsCmd.Flags().IntVar(&snapshotMaxTree, "snapshot-max-tree", defaultSnapshot.MaxTreeEntries, "Snapshot limit: max tree entries (0 = full repo)")
	statsCmd.Flags().IntVar(&snapshotMaxFiles, "snapshot-max-files", defaultSnapshot.MaxFiles, "Snapshot limit: max included files (0 = full repo)")
	statsCmd.Flags().IntVar(&snapshotMaxTotalBytes, "snapshot-max-total-bytes", defaultSnapshot.MaxTotalBytes, "Snapshot limit: total content budget in bytes (0 = full repo)")
	statsCmd.Flags().IntVar(&snapshotMaxFileBytes, "snapshot-max-file-bytes", defaultSnapshot.MaxFileBytes, "Snapshot limit: max bytes per file (0 = full file)")
	statsCmd.Flags().IntVar(&snapshotMaxFileRead, "snapshot-max-file-read", defaultSnapshot.MaxFileRead, "Snapshot limit: max bytes read per file (0 = no read cap)")
	statsCmd.Flags().StringArrayVar(&snapshotInclude, "snapshot-include", nil, "Snapshot include glob (repeatable)")
	statsCmd.Flags().StringArrayVar(&snapshotExclude, "snapshot-exclude", nil, "Snapshot exclude glob (repeatable)")
}

func collectLanguageCounts(root string, opts snapshotOptions) (map[string]int, int, error) {
	files, err := listTrackedFiles(root)
	if err != nil {
		return nil, 0, err
	}
	ig := fsutil.LoadIgnore(root)
	counts := map[string]int{}
	total := 0
	includeGlobs := normalizeGlobs(opts.IncludeGlobs)
	excludeGlobs := normalizeGlobs(opts.ExcludeGlobs)
	for _, rel := range files {
		relSlash := filepath.ToSlash(rel)
		if matchesAnyGlob(relSlash, excludeGlobs) || matchesAnyGlob(filepath.Base(relSlash), excludeGlobs) {
			continue
		}
		if len(includeGlobs) > 0 && !matchesAnyGlob(relSlash, includeGlobs) && !matchesAnyGlob(filepath.Base(relSlash), includeGlobs) {
			continue
		}
		if ig.ShouldIgnore(rel, false) {
			continue
		}
		full := filepath.Join(root, rel)
		fi, err := os.Stat(full)
		if err != nil || fi.IsDir() {
			continue
		}
		total++
		if !looksLikeTextFile(full) {
			counts["binary"]++
			continue
		}
		lang := languageForFile(rel)
		counts[lang]++
	}
	return counts, total, nil
}
