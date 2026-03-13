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
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
)

var diffAll bool
var diffWrite string

var diffCmd = &cobra.Command{
	Use:   "diff [PATH ...]",
	Short: "Show the payload that would be sent during sync",
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, err := normalizeSnapshotMode(snapshotMode)
		if err != nil {
			return err
		}
		snapshotMode = mode
		groupID := ""
		if diffAll {
			groupID, err = config.ResolveActiveGroup(viper.GetString("group"))
			if err != nil {
				return err
			}
		}
		roots, err := collectRepoRoots(args, groupID, diffAll)
		if err != nil {
			return err
		}
		if len(roots) == 0 {
			printer.Info("No repositories found to diff.")
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
			repo := loadRepoConfig(root)
			activeGroup := groupID
			if activeGroup == "" && repo.GroupID != "" {
				activeGroup = repo.GroupID
			}
			payload, label, err := buildSyncPayload(root, activeGroup, repo, opts, snapshotMode)
			if err != nil {
				printer.Warn(fmt.Sprintf("Diff failed for %s: %v", root, err))
				continue
			}
			if strings.TrimSpace(payload) == "" {
				printer.Info("No payload for " + root)
				continue
			}
			outPath, err := outputPathForRepo(diffWrite, root, "diff", multi)
			if err != nil {
				return err
			}
			if outPath == "" {
				if multi {
					fmt.Println("----- " + label + " -----")
				}
				fmt.Println(payload)
				if multi {
					fmt.Println()
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(outPath, []byte(payload), 0o644); err != nil {
				return err
			}
			printer.Success("Diff written to " + outPath)
		}
		return nil
	},
}

type repoConfig struct {
	GroupID          string
	Remote           string
	DocumentID       string
	DefaultBranch    string
	LastSyncedCommit string
}

func loadRepoConfig(root string) repoConfig {
	cfg, err := config.ReadProjectConfig(root)
	if err != nil {
		return repoConfig{Remote: safeRemote(root)}
	}
	repo := repoConfig{Remote: safeRemote(root)}
	if cfg.Group.ID != "" {
		repo.GroupID = cfg.Group.ID
	}
	if len(cfg.Repos) > 0 {
		r := cfg.Repos[0]
		if r.RemoteURL != "" {
			repo.Remote = r.RemoteURL
		}
		repo.DocumentID = r.DocumentID
		repo.DefaultBranch = r.DefaultBranch
		repo.LastSyncedCommit = r.LastSyncedCommit
	}
	return repo
}

func safeRemote(root string) string {
	if remote, err := git.OriginURLAt(root); err == nil {
		return remote
	}
	return filepath.Base(root)
}

func buildSyncPayload(root, groupID string, repo repoConfig, opts snapshotOptions, mode string) (string, string, error) {
	label := repo.Remote
	if label == "" {
		label = filepath.Base(root)
	}
	var payload string
	switch mode {
	case "snapshot":
		res, err := buildRepoSnapshot(root, groupID, &config.Repo{
			Provider:         "",
			RemoteURL:        repo.Remote,
			DefaultBranch:    repo.DefaultBranch,
			LastSyncedCommit: repo.LastSyncedCommit,
			DocumentID:       repo.DocumentID,
		}, opts)
		if err != nil {
			return "", "", err
		}
		payload = res.Text
		label = label + " (snapshot)"
	case "diff":
		text, _ := git.CollectChangeTextAtWithLimit(root, repo.LastSyncedCommit, commitLimit, extDetail)
		payload = text
		label = label + " (diff)"
	case "auto":
		if strings.TrimSpace(repo.LastSyncedCommit) == "" {
			res, err := buildRepoSnapshot(root, groupID, &config.Repo{
				Provider:         "",
				RemoteURL:        repo.Remote,
				DefaultBranch:    repo.DefaultBranch,
				LastSyncedCommit: repo.LastSyncedCommit,
				DocumentID:       repo.DocumentID,
			}, opts)
			if err != nil {
				return "", "", err
			}
			payload = res.Text
			label = label + " (snapshot)"
		} else {
			text, _ := git.CollectChangeTextAtWithLimit(root, repo.LastSyncedCommit, commitLimit, extDetail)
			payload = text
			label = label + " (diff)"
		}
	default:
		return "", "", fmt.Errorf("invalid snapshot mode %q", mode)
	}
	return payload, label, nil
}

func init() {
	rootCmd.AddCommand(diffCmd)
	diffCmd.Flags().BoolVar(&diffAll, "all", false, "Diff all tracked repos in the active group")
	diffCmd.Flags().StringVar(&diffWrite, "write", "", "Write output to a file (or directory when diffing multiple repos)")
	diffCmd.Flags().StringVar(&snapshotMode, "snapshot-mode", "auto", "Snapshot mode: auto (baseline if no last_synced_commit), snapshot, diff")
	defaultSnapshot := defaultSnapshotOptions()
	diffCmd.Flags().IntVar(&snapshotMaxTree, "snapshot-max-tree", defaultSnapshot.MaxTreeEntries, "Snapshot limit: max tree entries (0 = full repo)")
	diffCmd.Flags().IntVar(&snapshotMaxFiles, "snapshot-max-files", defaultSnapshot.MaxFiles, "Snapshot limit: max included files (0 = full repo)")
	diffCmd.Flags().IntVar(&snapshotMaxTotalBytes, "snapshot-max-total-bytes", defaultSnapshot.MaxTotalBytes, "Snapshot limit: total content budget in bytes (0 = full repo)")
	diffCmd.Flags().IntVar(&snapshotMaxFileBytes, "snapshot-max-file-bytes", defaultSnapshot.MaxFileBytes, "Snapshot limit: max bytes per file (0 = full file)")
	diffCmd.Flags().IntVar(&snapshotMaxFileRead, "snapshot-max-file-read", defaultSnapshot.MaxFileRead, "Snapshot limit: max bytes read per file (0 = no read cap)")
	diffCmd.Flags().StringArrayVar(&snapshotInclude, "snapshot-include", nil, "Snapshot include glob (repeatable)")
	diffCmd.Flags().StringArrayVar(&snapshotExclude, "snapshot-exclude", nil, "Snapshot exclude glob (repeatable)")
	diffCmd.Flags().IntVar(&commitLimit, "commits", 10, "Number of commits to include when no prior sync exists")
	diffCmd.Flags().BoolVar(&extDetail, "ext-detail", false, "Include per-commit detailed patches and names in diff payload")
}
