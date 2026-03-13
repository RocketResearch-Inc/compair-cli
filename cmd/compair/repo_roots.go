package compair

import (
	"context"
	"os"
	"path/filepath"

	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
)

func collectRepoRoots(args []string, group string, includeAll bool) (map[string]struct{}, error) {
	roots := map[string]struct{}{}
	if len(args) > 0 {
		for _, p := range args {
			ap, _ := filepath.Abs(p)
			dir := ap
			fi, err := os.Stat(ap)
			if err == nil && !fi.IsDir() {
				dir = filepath.Dir(ap)
			}
			if r, err := git.RepoRootAt(dir); err == nil {
				roots[r] = struct{}{}
			}
		}
		return roots, nil
	}
	if includeAll {
		store, err := db.Open()
		if err != nil {
			return nil, err
		}
		defer store.Close()
		rs, err := store.ListRepoRoots(context.Background(), group)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			roots[r] = struct{}{}
		}
		return roots, nil
	}
	if r, err := git.RepoRoot(); err == nil {
		roots[r] = struct{}{}
	}
	return roots, nil
}
