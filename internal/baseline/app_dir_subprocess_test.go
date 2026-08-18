//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package baseline

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/appdir"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
)

const appDirHelperEnvironment = "COMPAIR_APP_DIR_SUBPROCESS_HELPER"

func TestApplicationRootSubprocessIsolationSmoke(t *testing.T) {
	home := t.TempDir()
	homeState := filepath.Join(home, ".compair")
	if err := os.Mkdir(homeState, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sentinel"), []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeState, "credentials.json"), []byte(`{"access_token":"forbidden-home-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := subprocessTree(t, home)
	root := filepath.Join(t.TempDir(), "isolated-state")
	command := exec.Command(os.Args[0], "-test.run=^TestApplicationRootSubprocessHelper$")
	command.Env = append(os.Environ(),
		appDirHelperEnvironment+"=1",
		appdir.EnvironmentVariable+"="+root,
		"HOME="+home,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v: %s", err, output)
	}
	for _, forbidden := range []string{root, home, "isolated-secret-token", "forbidden-home-token"} {
		if strings.Contains(string(output), forbidden) {
			t.Fatalf("helper output leaked protected value: %q", output)
		}
	}
	if after := subprocessTree(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("HOME changed:\nbefore=%#v\nafter=%#v", before, after)
	}
	for _, relative := range []string{
		"credentials.json", "config.yaml", "active_group", "profiles.yaml", "core_runtime.yaml", "workspace.db",
		filepath.Join("state", "baseline-upload-install-secret.v1"),
		filepath.Join("state", "baseline-repositories"),
		filepath.Join("state", "baseline-repositories", "00000000-0000-4000-8000-000000000502.json"),
		filepath.Join("state", "baseline-uploads"),
		filepath.Join("state", "baseline-uploads", "upload-state.json"),
		filepath.Join("state", "baseline-indexes"),
		filepath.Join("state", "baseline-indexes", "index-state.json"),
		filepath.Join("state", "baseline-runs"),
		filepath.Join("state", "baseline-runs", "run-state.json"),
	} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("missing subprocess state %s: %v", relative, err)
		}
	}
}

func TestApplicationRootSubprocessHelper(t *testing.T) {
	if os.Getenv(appDirHelperEnvironment) != "1" {
		return
	}
	if err := appdir.InitializeFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(auth.Credentials{AccessToken: "isolated-secret-token"}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteGlobal(config.Global{APIBase: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteActiveGroup("isolated-group"); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProfiles(&config.Profiles{Default: "isolated", Profiles: map[string]config.Profile{"isolated": {APIBase: "http://127.0.0.1:1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveCoreRuntime(&config.CoreRuntime{Image: "isolated-image"}); err != nil {
		t.Fatal(err)
	}
	store, err := db.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
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
	if err := repositoryStore.save(&RepositoryBinding{BindingID: "00000000-0000-4000-8000-000000000502"}); err != nil {
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
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "ok"}); err != nil {
		t.Fatal(err)
	}
}

func subprocessTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + string(content)
		}
		result[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
