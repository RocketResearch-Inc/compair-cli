//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package baseline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/appdir"
)

func TestBaselineStoresShareConfiguredApplicationRootAndSecret(t *testing.T) {
	home := t.TempDir()
	homeSentinel := filepath.Join(home, "sentinel")
	if err := os.WriteFile(homeSentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "isolated-state")
	t.Cleanup(func() { _ = appdir.InitializeFromEnvironment() })
	t.Setenv("HOME", home)
	t.Setenv(appdir.EnvironmentVariable, root)
	if err := appdir.InitializeFromEnvironment(); err != nil {
		t.Fatal(err)
	}

	repositoryStore, err := newRepositoryBindingStore("")
	if err != nil {
		t.Fatal(err)
	}
	uploadStore, err := newUploadStateStore("")
	if err != nil {
		t.Fatal(err)
	}
	indexStore, err := newIndexStateStore("")
	if err != nil {
		t.Fatal(err)
	}
	runStore, err := newRunStateStore("")
	if err != nil {
		t.Fatal(err)
	}
	if err := repositoryStore.save(&RepositoryBinding{BindingID: "00000000-0000-4000-8000-000000000501"}); err != nil {
		t.Fatal(err)
	}
	if err := uploadStore.save("upload-state", &uploadState{}); err != nil {
		t.Fatal(err)
	}
	if err := indexStore.save("index-state", &indexState{}); err != nil {
		t.Fatal(err)
	}
	if err := runStore.save("run-state", &runState{}); err != nil {
		t.Fatal(err)
	}

	stateRoot := filepath.Join(root, "state")
	wantDirectories := map[string]string{
		repositoryStore.directory: filepath.Join(stateRoot, "baseline-repositories"),
		uploadStore.directory:     filepath.Join(stateRoot, "baseline-uploads"),
		indexStore.directory:      filepath.Join(stateRoot, "baseline-indexes"),
		runStore.directory:        filepath.Join(stateRoot, "baseline-runs"),
	}
	for got, want := range wantDirectories {
		if got != want {
			t.Fatalf("state directory = %q, want configured-root category", got)
		}
		info, err := os.Lstat(got)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("state directory info = %v, %v", info, err)
		}
	}
	for _, relative := range []string{
		filepath.Join("baseline-repositories", "00000000-0000-4000-8000-000000000501.json"),
		filepath.Join("baseline-uploads", "upload-state.json"),
		filepath.Join("baseline-indexes", "index-state.json"),
		filepath.Join("baseline-runs", "run-state.json"),
	} {
		info, err := os.Lstat(filepath.Join(stateRoot, relative))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("resume state info = %v, %v", info, err)
		}
	}
	secretPath := filepath.Join(stateRoot, "baseline-upload-install-secret.v1")
	secretInfo, err := os.Lstat(secretPath)
	if err != nil || !secretInfo.Mode().IsRegular() || secretInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("secret info = %v, %v", secretInfo, err)
	}
	if content, err := os.ReadFile(homeSentinel); err != nil || string(content) != "unchanged" {
		t.Fatalf("HOME sentinel = %q, %v", content, err)
	}
}

func TestExplicitBaselineStateDirectoryPrecedesConfiguredApplicationRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "isolated-state")
	t.Cleanup(func() { _ = appdir.InitializeFromEnvironment() })
	t.Setenv(appdir.EnvironmentVariable, root)
	if err := appdir.InitializeFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	explicit := filepath.Join(t.TempDir(), "explicit-uploads")
	store, err := newUploadStateStore(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if store.directory != explicit {
		t.Fatalf("directory = %q", store.directory)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(explicit), "baseline-upload-install-secret.v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "baseline-uploads")); !os.IsNotExist(err) {
		t.Fatalf("configured root used despite explicit injection: %v", err)
	}
}
