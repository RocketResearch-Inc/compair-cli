package git

import (
    "bytes"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
)

func RepoRoot() (string, error) {
    out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
    if err != nil { return "", err }
    return strings.TrimSpace(string(out)), nil
}

func RepoRootAt(dir string) (string, error) {
    out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
    if err != nil { return "", err }
    return strings.TrimSpace(string(out)), nil
}

func OriginURL() (string, error) {
    out, err := exec.Command("git", "config", "--get", "remote.origin.url").Output()
    if err != nil { return "", err }
    return strings.TrimSpace(string(out)), nil
}

func OriginURLAt(dir string) (string, error) {
    if dir == "" { return OriginURL() }
    out, err := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
    if err != nil { return "", err }
    return strings.TrimSpace(string(out)), nil
}

func DefaultBranch() string {
    out, err := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
    if err == nil {
        parts := strings.Split(strings.TrimSpace(string(out)), "/")
        return parts[len(parts)-1]
    }
    // fallback
    return "main"
}

func DefaultBranchAt(dir string) string {
    if dir == "" { return DefaultBranch() }
    out, err := exec.Command("git", "-C", dir, "symbolic-ref", "refs/remotes/origin/HEAD").Output()
    if err == nil {
        parts := strings.Split(strings.TrimSpace(string(out)), "/")
        return parts[len(parts)-1]
    }
    return "main"
}

func IsGitRepo(path string) bool {
    if path == "" { return false }
    fi, err := os.Stat(filepath.Join(path, ".git"))
    return err == nil && fi.IsDir()
}

func GuessProvider(remote string) string {
    if strings.Contains(remote, "github.com") { return "github" }
    if strings.Contains(remote, "gitlab.com") { return "gitlab" }
    return "generic"
}

func ShortenRemote(remote string) string {
    r := remote
    if strings.HasSuffix(r, ".git") { r = strings.TrimSuffix(r, ".git") }
    r = strings.TrimPrefix(r, "git@"); r = strings.TrimPrefix(r, "https://"); r = strings.TrimPrefix(r, "ssh://")
    r = strings.ReplaceAll(r, "github.com:", "github.com/")
    r = strings.ReplaceAll(r, "gitlab.com:", "gitlab.com/")
    parts := strings.Split(r, "/")
    if len(parts) >= 2 { return parts[len(parts)-2] + "/" + parts[len(parts)-1] }
    return filepath.Base(r)
}

// Collects commit messages and a short diff summary since 'sinceSHA' (or last 10 commits if empty).
func CollectChangeText(sinceSHA string) (text, latestSHA string) {
    head, err := exec.Command("git", "rev-parse", "HEAD").Output()
    if err != nil { return "", "" }
    latest := strings.TrimSpace(string(head))

    var logCmd *exec.Cmd
    if sinceSHA == "" {
        logCmd = exec.Command("sh", "-lc", "git log -n 10 --pretty=format:'%H %s' && echo && git diff --stat HEAD~10..HEAD")
    } else {
        logCmd = exec.Command("sh", "-lc", "git log --pretty=format:'%H %s' "+sinceSHA+"..HEAD && echo && git diff --stat "+sinceSHA+"..HEAD")
    }
    var out bytes.Buffer
    logCmd.Stdout = &out
    _ = logCmd.Run()
    return out.String(), latest
}

func CollectChangeTextAt(root string, sinceSHA string) (text, latestSHA string) {
    head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
    if err != nil { return "", "" }
    latest := strings.TrimSpace(string(head))

    var logCmd *exec.Cmd
    if sinceSHA == "" {
        logCmd = exec.Command("sh", "-lc", "git -C '"+root+"' log -n 10 --pretty=format:'%H %s' && echo && git -C '"+root+"' diff --stat HEAD~10..HEAD && echo && git -C '"+root+"' diff --find-renames --find-copies --patch HEAD~10..HEAD")
    } else {
        logCmd = exec.Command("sh", "-lc", "git -C '"+root+"' log --pretty=format:'%H %s' "+sinceSHA+"..HEAD && echo && git -C '"+root+"' diff --stat "+sinceSHA+"..HEAD && echo && git -C '"+root+"' diff --find-renames --find-copies --patch "+sinceSHA+"..HEAD")
    }
    var out bytes.Buffer
    logCmd.Stdout = &out
    _ = logCmd.Run()
    return out.String(), latest
}

// CollectChangeTextAtWithLimit returns a combined text of commits and diffs between sinceSHA..HEAD.
// If sinceSHA is empty, it limits the history to the last 'limit' commits. If extDetail is true,
// it includes per-commit detailed patches in addition to range patches.
func CollectChangeTextAtWithLimit(root string, sinceSHA string, limit int, extDetail bool) (text, latestSHA string) {
    head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
    if err != nil { return "", "" }
    latest := strings.TrimSpace(string(head))

    if limit <= 0 { limit = 10 }

    var script string
    if sinceSHA == "" {
        // last N commits: include log, diff stat and full patch across range
        script = fmt.Sprintf("git -C '%s' log -n %d --pretty=format:'%%H %%s' && echo && git -C '%s' diff --stat HEAD~%d..HEAD && echo && git -C '%s' diff --find-renames --find-copies --patch HEAD~%d..HEAD", root, limit, root, limit, root, limit)
        if extDetail {
            script += fmt.Sprintf(" && echo && git -C '%s' log -n %d --date=iso --name-status --find-renames --patch", root, limit)
        }
    } else {
        // bounded by sinceSHA..HEAD
        script = fmt.Sprintf("git -C '%s' log --pretty=format:'%%H %%s' %s..HEAD && echo && git -C '%s' diff --stat %s..HEAD && echo && git -C '%s' diff --find-renames --find-copies --patch %s..HEAD", root, sinceSHA, root, sinceSHA, root, sinceSHA)
        if extDetail {
            script += fmt.Sprintf(" && echo && git -C '%s' log --date=iso --name-status --find-renames --patch %s..HEAD", root, sinceSHA)
        }
    }

    var out bytes.Buffer
    cmd := exec.Command("sh", "-lc", script)
    cmd.Stdout = &out
    _ = cmd.Run()
    return out.String(), latest
}
