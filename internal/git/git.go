package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func RepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func RepoRootAt(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func OriginURL() (string, error) {
	out, err := exec.Command("git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func OriginURLAt(dir string) (string, error) {
	if dir == "" {
		return OriginURL()
	}
	out, err := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func DefaultBranch() string {
	out, err := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), "/")
		return parts[len(parts)-1]
	}
	return "main"
}

func DefaultBranchAt(dir string) string {
	if dir == "" {
		return DefaultBranch()
	}
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), "/")
		return parts[len(parts)-1]
	}
	return "main"
}

func IsGitRepo(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && fi.IsDir()
}

func GuessProvider(remote string) string {
	if strings.Contains(remote, "github.com") {
		return "github"
	}
	if strings.Contains(remote, "gitlab.com") {
		return "gitlab"
	}
	return "generic"
}

func ShortenRemote(remote string) string {
	r := remote
	if strings.HasSuffix(r, ".git") {
		r = strings.TrimSuffix(r, ".git")
	}
	r = strings.TrimPrefix(r, "git@")
	r = strings.TrimPrefix(r, "https://")
	r = strings.TrimPrefix(r, "ssh://")
	r = strings.ReplaceAll(r, "github.com:", "github.com/")
	r = strings.ReplaceAll(r, "gitlab.com:", "gitlab.com/")
	parts := strings.Split(r, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return filepath.Base(r)
}

func CollectChangeText(sinceSHA string) (text, latestSHA string) {
	return CollectChangeTextAtWithLimit("", sinceSHA, 10, false)
}

func CollectChangeTextAt(root string, sinceSHA string) (text, latestSHA string) {
	return CollectChangeTextAtWithLimit(root, sinceSHA, 10, false)
}

// CollectChangeTextAtWithLimit returns a combined text of commits and diffs between sinceSHA..HEAD.
// If sinceSHA is empty, it limits the history to the last 'limit' commits.
func CollectChangeTextAtWithLimit(root string, sinceSHA string, limit int, extDetail bool) (text, latestSHA string) {
	latest, err := headSHA(root)
	if err != nil {
		return "", ""
	}
	if limit <= 0 {
		limit = 10
	}

	var sections []string
	if sinceSHA == "" {
		logText := runGitString(root, "log", "-n", strconv.Itoa(limit), "--pretty=format:%H %s")
		appendSection(&sections, logText)

		commits := listRecentCommits(root, limit)
		if len(commits) == 0 {
			return strings.Join(sections, "\n\n"), latest
		}

		showArgs := []string{"show", "--stat", "--find-renames", "--find-copies", "--patch", "--format="}
		showArgs = append(showArgs, commits...)
		appendSection(&sections, runGitString(root, showArgs...))

		if extDetail {
			appendSection(&sections, runGitString(root, "log", "-n", strconv.Itoa(limit), "--date=iso", "--name-status", "--find-renames", "--patch"))
		}
		return strings.Join(sections, "\n\n"), latest
	}

	rangeSpec := sinceSHA + "..HEAD"
	appendSection(&sections, runGitString(root, "log", "--pretty=format:%H %s", rangeSpec))
	appendSection(&sections, runGitString(root, "diff", "--stat", rangeSpec))
	appendSection(&sections, runGitString(root, "diff", "--find-renames", "--find-copies", "--patch", rangeSpec))
	if extDetail {
		appendSection(&sections, runGitString(root, "log", "--date=iso", "--name-status", "--find-renames", "--patch", rangeSpec))
	}
	return strings.Join(sections, "\n\n"), latest
}

func headSHA(root string) (string, error) {
	args := []string{"rev-parse", "HEAD"}
	if root != "" {
		args = append([]string{"-C", root}, args...)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func runGitString(root string, args ...string) string {
	var cmdArgs []string
	if root != "" {
		cmdArgs = append(cmdArgs, "-C", root)
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("git", cmdArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()
	return strings.TrimSpace(out.String())
}

func listRecentCommits(root string, limit int) []string {
	args := []string{"rev-list", "--max-count", strconv.Itoa(limit), "HEAD"}
	if root != "" {
		args = append([]string{"-C", root}, args...)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) == 0 {
		return nil
	}
	return lines
}

func appendSection(sections *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	*sections = append(*sections, value)
}
