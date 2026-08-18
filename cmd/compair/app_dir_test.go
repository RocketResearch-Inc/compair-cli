//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package compair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/appdir"
	"github.com/spf13/viper"
)

func TestRootInitializationRejectsInvalidConfiguredAppDirWithoutFallback(t *testing.T) {
	home := t.TempDir()
	homeState := filepath.Join(home, ".compair")
	if err := os.Mkdir(homeState, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(homeState, "profiles.yaml")
	content := []byte("default: forbidden-home\n")
	if err := os.WriteFile(sentinel, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = appdir.InitializeFromEnvironment() })
	t.Setenv("HOME", home)
	t.Setenv(appdir.EnvironmentVariable, "relative/private-state")

	command, _, err := rootCmd.Find([]string{"baseline", "scan"})
	if err != nil {
		t.Fatal(err)
	}
	err = rootCmd.PersistentPreRunE(command, nil)
	if err == nil || !strings.Contains(err.Error(), "not_absolute") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "relative/private-state") || strings.Contains(err.Error(), home) {
		t.Fatalf("error leaked a path: %q", err)
	}
	after, readErr := os.ReadFile(sentinel)
	if readErr != nil || string(after) != string(content) {
		t.Fatalf("HOME fallback occurred: %q, %v", after, readErr)
	}
}

func TestRootInitializationUsesOnlyConfiguredAppDirForProfiles(t *testing.T) {
	home := t.TempDir()
	homeState := filepath.Join(home, ".compair")
	if err := os.Mkdir(homeState, 0o700); err != nil {
		t.Fatal(err)
	}
	homeProfiles := []byte("default: forbidden\nprofiles:\n  forbidden:\n    api_base: https://home.invalid\n")
	if err := os.WriteFile(filepath.Join(homeState, "profiles.yaml"), homeProfiles, 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "isolated-state")
	t.Cleanup(func() { _ = appdir.InitializeFromEnvironment() })
	t.Setenv("HOME", home)
	t.Setenv(appdir.EnvironmentVariable, root)
	previousBase := viper.GetString("api.base")
	t.Cleanup(func() { viper.Set("api.base", previousBase) })

	command, _, err := rootCmd.Find([]string{"baseline", "scan"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rootCmd.PersistentPreRunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if got := viper.GetString("api.base"); got == "https://home.invalid" {
		t.Fatal("profile resolution fell back to HOME")
	}
	if _, err := os.Stat(filepath.Join(root, "profiles.yaml")); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(homeState, "profiles.yaml"))
	if err != nil || string(after) != string(homeProfiles) {
		t.Fatalf("HOME profiles changed: %q, %v", after, err)
	}
}
