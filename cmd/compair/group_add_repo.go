package compair

import (
	"context"
	"fmt"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var branchFlag string

var repoPath string
var addRepoInitialSync bool
var addRepoCommitLimit int
var addRepoExtDetail bool
var addRepoUnpublished bool
var addRepoNoFeedback bool

type repoRegistrationOptions struct {
	InitialSync       bool
	InitialNoFeedback bool
	CommitLimit       int
	ExtDetail         bool
	Unpublished       bool
}

var groupAddRepoCmd = &cobra.Command{
	Use:   "add-repo [group] [remote-or-path]",
	Short: "Create a document for a repo inside a group",
	Args:  cobra.RangeArgs(0, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		groupArg, repoArg, err := parseGroupAndRepoArgs(args)
		if err != nil {
			return err
		}
		if strings.TrimSpace(repoPath) != "" && strings.TrimSpace(repoArg) != "" {
			return fmt.Errorf("cannot use both positional repo argument and --repo")
		}
		groupID, _, err := groups.ResolveWithAuto(client, groupArg, viper.GetString("group"))
		if err != nil {
			return err
		}
		remote, root, err := resolveRemoteAndRoot(repoArg, repoPath)
		if err != nil {
			return err
		}
		_, err = registerRepoDocument(client, groupID, remote, root, repoRegistrationOptions{
			InitialSync:       addRepoInitialSync,
			InitialNoFeedback: addRepoNoFeedback,
			CommitLimit:       addRepoCommitLimit,
			ExtDetail:         addRepoExtDetail,
			Unpublished:       addRepoUnpublished,
		})
		if err != nil {
			return err
		}
		return nil
	},
}

func parseGroupAndRepoArgs(args []string) (groupArg string, repoArg string, err error) {
	switch len(args) {
	case 0:
		return "", "", nil
	case 1:
		if looksLikeRepoArg(args[0]) {
			return "", args[0], nil
		}
		return args[0], "", nil
	case 2:
		return args[0], args[1], nil
	default:
		return "", "", fmt.Errorf("expected at most 2 positional arguments")
	}
}

func looksLikeRepoArg(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}
	if strings.Contains(v, "://") || strings.HasPrefix(v, "git@") || strings.HasSuffix(v, ".git") {
		return true
	}
	if strings.Contains(v, "/") || strings.HasPrefix(v, ".") || strings.HasPrefix(v, "~") {
		return true
	}
	if _, err := os.Stat(v); err == nil {
		return true
	}
	if abs, err := filepath.Abs(v); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return true
		}
	}
	return false
}

func resolveRemoteAndRoot(positionalRepo, flagRepo string) (string, string, error) {
	if strings.TrimSpace(positionalRepo) != "" {
		return resolveRepoArg(positionalRepo)
	}
	if strings.TrimSpace(flagRepo) != "" {
		return resolveLocalRepo(flagRepo, "--repo")
	}
	root, err := git.RepoRoot()
	if err != nil {
		return "", "", fmt.Errorf("remote not provided and current directory is not a git repo")
	}
	remote, err := git.OriginURLAt(root)
	if err != nil {
		return "", "", fmt.Errorf("cannot get origin from %s: %w", root, err)
	}
	return remote, root, nil
}

func resolveRepoArg(candidate string) (string, string, error) {
	if _, err := os.Stat(candidate); err == nil {
		return resolveLocalRepo(candidate, "path")
	}
	if abs, err := filepath.Abs(candidate); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return resolveLocalRepo(abs, "path")
		}
	}
	return candidate, "", nil
}

func resolveLocalRepo(pathValue, label string) (string, string, error) {
	abs := pathValue
	if p, err := filepath.Abs(pathValue); err == nil {
		abs = p
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", fmt.Errorf("%s not found: %s", label, pathValue)
	}
	base := abs
	if !info.IsDir() {
		base = filepath.Dir(abs)
	}
	root := base
	if r, err := git.RepoRootAt(base); err == nil {
		root = r
	}
	if !git.IsGitRepo(root) {
		return "", "", fmt.Errorf("%s is not inside a git repo: %s", label, pathValue)
	}
	remote, err := git.OriginURLAt(root)
	if err != nil {
		return "", "", fmt.Errorf("cannot get origin from %s: %w", pathValue, err)
	}
	return remote, root, nil
}

func registerRepoDocument(client *api.Client, groupID, remote, root string, opts repoRegistrationOptions) (string, error) {
	title := git.ShortenRemote(remote)
	doc, err := client.CreateDoc(title, "code-repo", "", groupID, !opts.Unpublished)
	if err != nil {
		return "", err
	}
	printer.Success("Created document for repo: " + title + " (doc_id=" + doc.DocumentID + ")")
	if root == "" {
		if opts.InitialSync {
			printer.Warn("Initial sync skipped: no local repository path was provided.")
		}
		return doc.DocumentID, nil
	}

	cfg := config.Project{
		Version:     1,
		ProjectName: filepath.Base(root),
		Group:       config.Group{ID: groupID, Name: "Group"},
		Repos: []config.Repo{{
			Provider:         git.GuessProvider(remote),
			RemoteURL:        remote,
			RepoID:           "",
			DefaultBranch:    git.DefaultBranchAt(root),
			LastSyncedCommit: "",
			DocumentID:       doc.DocumentID,
			Unpublished:      opts.Unpublished,
		}},
	}
	if err := config.WriteProjectConfig(root, cfg); err == nil {
		printer.Success("Initialized Compair project at " + filepath.Join(root, ".compair/config.yaml"))
	}
	if !opts.InitialSync {
		upsertRepoWorkspaceBinding(root, groupID, doc.DocumentID, "", opts.Unpublished)
		return doc.DocumentID, nil
	}

	snapshotOpts := defaultSnapshotOptions()
	if prof := loadActiveProfileSnapshot(); prof != nil {
		applySnapshotOverrides(&snapshotOpts, *prof)
	}
	res, snapErr := buildRepoSnapshot(root, groupID, &cfg.Repos[0], snapshotOpts)
	text := ""
	latest := ""
	useClientChunks := false
	if snapErr == nil {
		text = res.Text
		latest = res.Head
		useClientChunks = true
		maybeWarnSnapshotScope(root, res.Stats, snapshotOpts)
	} else {
		printer.Warn(fmt.Sprintf("Initial snapshot failed for %s: %v (falling back to commit diff)", root, snapErr))
		text, latest = git.CollectChangeTextAtWithLimit(root, "", opts.CommitLimit, opts.ExtDetail)
	}
	if strings.TrimSpace(text) != "" {
		var resp api.ProcessDocResp
		generateFeedback := !opts.InitialNoFeedback
		var err error
		if useClientChunks {
			resp, err = client.ProcessDocWithOptions(doc.DocumentID, text, generateFeedback, api.ProcessDocOptions{
				ChunkMode: "client",
			})
		} else {
			resp, err = client.ProcessDoc(doc.DocumentID, text, generateFeedback)
		}
		if err != nil {
			upsertRepoWorkspaceBinding(root, groupID, doc.DocumentID, "", opts.Unpublished)
			return doc.DocumentID, err
		}
		if strings.TrimSpace(resp.TaskID) != "" {
			persistPendingRepoTask(root, cfg, &cfg.Repos[0], resp.TaskID, latest, 0)
			upsertRepoWorkspaceBinding(root, groupID, doc.DocumentID, "", opts.Unpublished)
			printer.Info(
				"Initial sync submitted for " + title + "; indexing continues in the background as server task " + shortTaskID(resp.TaskID),
			)
			printer.Info("Run 'compair sync' or 'compair review' later and Compair will wait for unfinished baseline indexing before feedback.")
			return doc.DocumentID, nil
		}
		finalizeRepoSync(root, groupID, cfg, &cfg.Repos[0], latest)
		printer.Success("Initial sync completed for " + title)
		return doc.DocumentID, nil
	}
	finalizeRepoSync(root, groupID, cfg, &cfg.Repos[0], latest)
	return doc.DocumentID, nil
}

func upsertRepoWorkspaceBinding(root, groupID, documentID, lastSyncedCommit string, unpublished bool) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(groupID) == "" || strings.TrimSpace(documentID) == "" {
		return
	}
	store, err := db.Open()
	if err != nil {
		return
	}
	defer store.Close()
	published := int64(1)
	if unpublished {
		published = 0
	}
	lastSyncedAt := int64(0)
	if strings.TrimSpace(lastSyncedCommit) != "" {
		lastSyncedAt = time.Now().Unix()
	}
	_ = store.UpsertItem(context.Background(), &db.TrackedItem{
		Path:             root,
		Kind:             "repo",
		GroupID:          groupID,
		DocumentID:       documentID,
		RepoRoot:         root,
		LastSyncedCommit: lastSyncedCommit,
		LastSyncedAt:     lastSyncedAt,
		LastSeenAt:       time.Now().Unix(),
		Published:        published,
	})
}

func init() {
	groupAddRepoCmd.Flags().StringVar(&branchFlag, "branch", "", "Default branch (unused for API, kept for config parity)")
	groupAddRepoCmd.Flags().StringVar(&repoPath, "repo", "", "Path to a local git repo (optional)")
	groupAddRepoCmd.Flags().BoolVar(&addRepoInitialSync, "initial-sync", false, "Perform an initial sync after creating the document")
	groupAddRepoCmd.Flags().BoolVar(&addRepoNoFeedback, "no-feedback", false, "When used with --initial-sync, upload the baseline without generating feedback")
	groupAddRepoCmd.Flags().IntVar(&addRepoCommitLimit, "commits", 10, "Number of commits for the initial sync if no prior sync exists")
	groupAddRepoCmd.Flags().BoolVar(&addRepoExtDetail, "ext-detail", false, "Include detailed per-commit patches in initial sync")
	groupAddRepoCmd.Flags().BoolVar(&addRepoUnpublished, "unpublished", false, "Keep the repo document unpublished (default: publish so other repos can reference it)")
}
