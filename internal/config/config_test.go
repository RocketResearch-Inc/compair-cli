package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectConfigPathDefaultsToRepoConfig(t *testing.T) {
	t.Setenv(projectConfigPathEnv, "")
	root := "/tmp/demo-repo"

	got := ProjectConfigPath(root)
	want := filepath.Join(root, ".compair", "config.yaml")
	if got != want {
		t.Fatalf("expected default project config path %q, got %q", want, got)
	}
}

func TestProjectConfigPathUsesAbsoluteEnvOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "compair-ci-config.yaml")
	if !filepath.IsAbs(want) {
		t.Fatalf("expected temp path %q to be absolute on separator %q", want, string(os.PathSeparator))
	}
	t.Setenv(projectConfigPathEnv, want)

	got := ProjectConfigPath("/tmp/demo-repo")
	if got != want {
		t.Fatalf("expected env override %q, got %q", want, got)
	}
}

func TestProjectConfigPathResolvesRelativeEnvOverrideAgainstRepoRoot(t *testing.T) {
	t.Setenv(projectConfigPathEnv, ".github/compair-ci-config.yaml")
	root := "/tmp/demo-repo"

	got := ProjectConfigPath(root)
	want := filepath.Join(root, ".github", "compair-ci-config.yaml")
	if got != want {
		t.Fatalf("expected repo-relative env override %q, got %q", want, got)
	}
}
