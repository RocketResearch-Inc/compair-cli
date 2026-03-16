package printer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMarkdownReportPreservesMarkdownLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	lines := []string{
		"## Repo: demo",
		"",
		"### Changes",
		"",
		"~~~diff",
		"diff --git a/file b/file",
		"~~~",
	}

	if err := WriteMarkdownReport(path, "Demo", lines); err != nil {
		t.Fatalf("WriteMarkdownReport returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "## Repo: demo\n\n### Changes\n") {
		t.Fatalf("expected markdown headings to be preserved, got:\n%s", out)
	}
	if strings.Contains(out, "- ## Repo: demo") {
		t.Fatalf("expected raw markdown lines, found legacy bullet formatting:\n%s", out)
	}
}
