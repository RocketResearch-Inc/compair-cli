package config

import (
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
	t.Setenv(projectConfigPathEnv, "/tmp/compair-ci-config.yaml")

	got := ProjectConfigPath("/tmp/demo-repo")
	want := "/tmp/compair-ci-config.yaml"
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
