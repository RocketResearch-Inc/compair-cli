//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package appdir

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func resetResolutionForTest() {
	resolved.Lock()
	resolved.initialized = false
	resolved.root = ""
	resolved.err = nil
	resolved.Unlock()
}

func TestUnsetApplicationRootPreservesLegacyLocation(t *testing.T) {
	resetResolutionForTest()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Unsetenv(EnvironmentVariable); err != nil {
		t.Fatal(err)
	}
	if err := InitializeFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".compair"); root != want {
		t.Fatalf("root = %q, want legacy suffix", root)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy initialization changed creation behavior: %v", err)
	}
}

func TestConfiguredApplicationRootIsCreatedPrivateAndCanonical(t *testing.T) {
	resetResolutionForTest()
	root := filepath.Join(t.TempDir(), "compair-state")
	t.Setenv(EnvironmentVariable, root)
	if err := InitializeFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	got, err := Root()
	if err != nil || got != root {
		t.Fatalf("root = %q, err = %v", got, err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

func TestConfiguredApplicationRootRejectsInvalidValuesWithoutPathLeak(t *testing.T) {
	parent := t.TempDir()
	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(parent, "link")
	if err := os.Symlink(parent, symlink); err != nil {
		t.Fatal(err)
	}
	unsafe := filepath.Join(parent, "unsafe")
	if err := os.Mkdir(unsafe, 0o755); err != nil {
		t.Fatal(err)
	}
	inaccessible := filepath.Join(parent, "inaccessible")
	if err := os.Mkdir(inaccessible, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(inaccessible, 0o700) })

	tests := []struct {
		name   string
		value  string
		reason string
	}{
		{name: "empty", value: "", reason: "empty"},
		{name: "whitespace", value: " \t", reason: "empty"},
		{name: "relative", value: "relative/state", reason: "not_absolute"},
		{name: "surrounding whitespace", value: " " + filepath.Join(parent, "state"), reason: "malformed"},
		{name: "control character", value: filepath.Join(parent, "state") + "\nchild", reason: "malformed"},
		{name: "noncanonical", value: parent + string(os.PathSeparator) + "state" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "state", reason: "not_canonical"},
		{name: "missing parent", value: filepath.Join(parent, "missing", "state"), reason: "unavailable"},
		{name: "file", value: file, reason: "not_directory"},
		{name: "symlink", value: symlink, reason: "symlink"},
		{name: "unsafe permissions", value: unsafe, reason: "unsafe_permissions"},
		{name: "inaccessible", value: inaccessible, reason: "inaccessible"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetResolutionForTest()
			t.Setenv(EnvironmentVariable, test.value)
			err := InitializeFromEnvironment()
			var appErr *Error
			if !errors.As(err, &appErr) || appErr.Reason != test.reason {
				t.Fatalf("error = %#v, want %q", err, test.reason)
			}
			if test.value != "" && strings.Contains(err.Error(), test.value) {
				t.Fatalf("error leaked configured path: %q", err)
			}
			if _, rootErr := Root(); rootErr == nil {
				t.Fatal("invalid configured root fell back")
			}
		})
	}
}

func TestConcurrentConfiguredApplicationRootInitialization(t *testing.T) {
	resetResolutionForTest()
	root := filepath.Join(t.TempDir(), "concurrent-state")
	t.Setenv(EnvironmentVariable, root)
	const workers = 32
	var wait sync.WaitGroup
	errorsSeen := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- InitializeFromEnvironment()
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("root info = %v, %v", info, err)
	}
}
