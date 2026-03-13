package compair

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/git"
)

var invalidSlugChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func outputPathForRepo(basePath, root, prefix string, multi bool) (string, error) {
	if strings.TrimSpace(basePath) == "" {
		return "", nil
	}
	basePath = filepath.Clean(basePath)
	if strings.HasSuffix(basePath, string(os.PathSeparator)) {
		return filepath.Join(basePath, fmt.Sprintf("%s_%s.md", prefix, repoSlug(root))), nil
	}
	if info, err := os.Stat(basePath); err == nil && info.IsDir() {
		return filepath.Join(basePath, fmt.Sprintf("%s_%s.md", prefix, repoSlug(root))), nil
	}
	if multi {
		ext := filepath.Ext(basePath)
		base := strings.TrimSuffix(filepath.Base(basePath), ext)
		dir := filepath.Dir(basePath)
		if ext == "" {
			ext = ".md"
		}
		return filepath.Join(dir, fmt.Sprintf("%s_%s%s", base, repoSlug(root), ext)), nil
	}
	if filepath.Ext(basePath) == "" {
		return basePath + ".md", nil
	}
	return basePath, nil
}

func repoSlug(root string) string {
	if remote, err := git.OriginURLAt(root); err == nil && strings.TrimSpace(remote) != "" {
		return sanitizeSlug(git.ShortenRemote(remote))
	}
	return sanitizeSlug(filepath.Base(root))
}

func sanitizeSlug(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, " ", "_")
	value = invalidSlugChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "repo"
	}
	return value
}
