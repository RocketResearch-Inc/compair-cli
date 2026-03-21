package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectChangeTextAtWithLimitSinceSHA(t *testing.T) {
	root := initTestRepo(t)
	writeFile(t, filepath.Join(root, "demo.txt"), "one\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "first commit")
	first := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))

	writeFile(t, filepath.Join(root, "demo.txt"), "one\ntwo\n")
	runGit(t, root, "add", "demo.txt")
	runGit(t, root, "commit", "-m", "second commit")

	text, latest := CollectChangeTextAtWithLimit(root, first, 10, false)
	if strings.TrimSpace(latest) == "" {
		t.Fatalf("latest SHA is empty")
	}
	if !strings.Contains(text, "second commit") {
		t.Fatalf("expected second commit in output, got:\n%s", text)
	}
	if !strings.Contains(text, "demo.txt") {
		t.Fatalf("expected changed file in output, got:\n%s", text)
	}
}

func TestCollectChangeTextAtWithLimitWithoutSinceSHA(t *testing.T) {
	root := initTestRepo(t)
	writeFile(t, filepath.Join(root, "demo.txt"), "hello\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial commit")

	text, latest := CollectChangeTextAtWithLimit(root, "", 10, false)
	if strings.TrimSpace(latest) == "" {
		t.Fatalf("latest SHA is empty")
	}
	if !strings.Contains(text, "initial commit") {
		t.Fatalf("expected initial commit in output, got:\n%s", text)
	}
	if !strings.Contains(text, "demo.txt") {
		t.Fatalf("expected demo.txt in output, got:\n%s", text)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "checkout", "-B", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	return root
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}
