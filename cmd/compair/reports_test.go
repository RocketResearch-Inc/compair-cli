package compair

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestShouldRenderPlainMarkdownUsesNoColorFlag(t *testing.T) {
	prev := viper.GetBool("no_color")
	viper.Set("no_color", true)
	t.Cleanup(func() {
		viper.Set("no_color", prev)
	})

	if !shouldRenderPlainMarkdown() {
		t.Fatal("expected plain markdown rendering when --no-color is enabled")
	}
}

func TestRenderMarkdownFallsBackToPlainText(t *testing.T) {
	prev := viper.GetBool("no_color")
	viper.Set("no_color", true)
	t.Cleanup(func() {
		viper.Set("no_color", prev)
	})

	out, err := renderMarkdown("# Demo")
	if err != nil {
		t.Fatalf("renderMarkdown returned error: %v", err)
	}
	if out != "# Demo\n" {
		t.Fatalf("unexpected plain markdown output: %q", out)
	}
}

func TestDiscoverReportsIncludesCustomMarkdownReports(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir(%s): %v", root, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	reportDir := filepath.Join(root, ".compair")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", reportDir, err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "latest_feedback_sync.md"), []byte("# latest"), 0o644); err != nil {
		t.Fatalf("write latest report: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(reportDir, "scenario-b.md"), []byte("# scenario"), 0o644); err != nil {
		t.Fatalf("write custom report: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	reports, err := discoverReports()
	if err != nil {
		t.Fatalf("discoverReports: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 markdown reports, got %d", len(reports))
	}
	if got := filepath.Base(reports[0].Path); got != "scenario-b.md" {
		t.Fatalf("expected newest report first, got %s", got)
	}
	if got := filepath.Base(reports[1].Path); got != "latest_feedback_sync.md" {
		t.Fatalf("expected legacy report second, got %s", got)
	}
}
