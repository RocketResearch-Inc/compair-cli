package compair

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	fsutil "github.com/RocketResearch-Inc/compair-cli/internal/fs"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
)

var addNoFollow bool
var addRecursive bool

var addCmd = &cobra.Command{
	Use:   "add [PATH ...]",
	Short: "Track files/dirs/repos in the active group",
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
		addOne := func(path string) error {
			can, err := fsutil.CanonicalPath(path, !addNoFollow)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			fi, err := os.Lstat(can)
			if err != nil {
				return err
			}
			kind := "file"
			if fi.IsDir() {
				kind = "dir"
			}
			if filepath.Base(can) == ".git" && fi.IsDir() {
				return nil
			}
			sig, _ := fsutil.StatSig(can)
			size, mtime, _ := fsutil.FileTimes(can)
			// detect git repo root for this path
			repoRoot := ""
			dir := can
			if !fi.IsDir() {
				dir = filepath.Dir(can)
			}
			if r, err := git.RepoRootAt(dir); err == nil {
				repoRoot = r
			}
			if fi.IsDir() && repoRoot == can {
				kind = "repo"
			}
			if kind == "file" && repoRoot == "" {
				// For regular files outside git, compute content hash and create a document immediately
				hash, _, _, _ := fsutil.FastHash(can)
				client := api.NewClient(viper.GetString("api.base"))
				content, _ := os.ReadFile(can)
				docID := ""
				if doc, err := client.CreateDoc(filepath.Base(can), "file", string(content), group, false); err == nil {
					docID = doc.DocumentID
				}
				ti := db.TrackedItem{Path: can, Kind: kind, GroupID: group, RepoRoot: repoRoot, DocumentID: docID, FileSig: fsutil.SigString(sig), ContentHash: hash, Size: size, MTime: mtime, LastSeenAt: fsutil.NowSec()}
				if err := store.UpsertItem(ctx, &ti); err != nil {
					return err
				}
				fmt.Println("Added (file doc created):", can)
				return nil
			}
			ti := db.TrackedItem{Path: can, Kind: kind, GroupID: group, RepoRoot: repoRoot, FileSig: fsutil.SigString(sig), Size: size, MTime: mtime, LastSeenAt: fsutil.NowSec()}
			if err := store.UpsertItem(ctx, &ti); err != nil {
				return err
			}
			fmt.Println("Added:", can)
			return nil
		}
		for _, p := range args {
			ap := p
			if abs, err := filepath.Abs(p); err == nil {
				ap = abs
			}
			fi, err := os.Lstat(ap)
			if err != nil {
				return err
			}
			if fi.IsDir() && addRecursive {
				ig := fsutil.LoadIgnore(ap)
				err := filepath.WalkDir(ap, func(path string, d os.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					// skip ignored dirs/files
					if ig.ShouldIgnore(path, d.IsDir()) {
						if d.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
					if d.IsDir() {
						return nil
					}
					return addOne(path)
				})
				if err != nil {
					return err
				}
				continue
			}
			if err := addOne(ap); err != nil {
				return err
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(addCmd)
	addCmd.Flags().BoolVar(&addNoFollow, "no-follow-symlinks", false, "Do not resolve symlink targets; track the link itself")
	addCmd.Flags().BoolVarP(&addRecursive, "recursive", "r", false, "Recursively add files under directories (respects .compairignore and sane defaults)")
}
