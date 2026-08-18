//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package appdir_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/appdir"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
)

func TestConfiguredRootContainsGlobalStateAndNeverTouchesHome(t *testing.T) {
	home := t.TempDir()
	homeState := filepath.Join(home, ".compair")
	if err := os.Mkdir(homeState, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sentinel"), []byte("home-must-not-change"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeState, "credentials.json"), []byte(`{"access_token":"home-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeState, "profiles.yaml"), []byte("default: home\nprofiles:\n  home:\n    api_base: https://home.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, home)

	root := filepath.Join(t.TempDir(), "isolated-state")
	t.Cleanup(func() { _ = appdir.InitializeFromEnvironment() })
	t.Setenv("HOME", home)
	t.Setenv(appdir.EnvironmentVariable, root)
	if err := appdir.InitializeFromEnvironment(); err != nil {
		t.Fatal(err)
	}

	if err := auth.Save(auth.Credentials{AccessToken: "isolated-token", UserID: "isolated-user"}); err != nil {
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

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer isolated-token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		for _, value := range []string{request.URL.String(), request.Header.Get("User-Agent"), request.Header.Get("Authorization")} {
			if strings.Contains(value, root) {
				t.Fatal("application root leaked to remote request")
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"inputs": map[string]bool{"repos": true}})
	}))
	defer server.Close()
	if _, err := api.NewClient(server.URL).Capabilities(time.Minute); err != nil {
		t.Fatal(err)
	}

	profiles, err := config.LoadProfiles()
	if err != nil || profiles.Default != "isolated" {
		t.Fatalf("profiles = %#v, %v", profiles, err)
	}
	credentials, err := auth.Load()
	if err != nil || credentials.AccessToken != "isolated-token" {
		t.Fatalf("credentials = %#v, %v", credentials, err)
	}

	for _, relative := range []string{
		"credentials.json",
		"config.yaml",
		"active_group",
		"profiles.yaml",
		"core_runtime.yaml",
		"workspace.db",
		filepath.Join("cache", "capabilities.json"),
	} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("missing isolated state %s: %v", relative, err)
		}
	}
	if after := snapshotTree(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("HOME changed:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if !entry.IsDir() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + string(content)
		}
		snapshot[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
