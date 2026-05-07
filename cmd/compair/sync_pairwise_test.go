package compair

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
)

func TestValidatePairwiseMode(t *testing.T) {
	originalPairwise := syncPairwise
	originalCrossRepoOnly := syncCrossRepoOnly
	t.Cleanup(func() {
		syncPairwise = originalPairwise
		syncCrossRepoOnly = originalCrossRepoOnly
	})

	syncPairwise = true

	if err := validatePairwiseMode(true, true, true, syncInvocationMode{}); err != nil {
		t.Fatalf("expected pairwise validation to pass, got %v", err)
	}
	if err := validatePairwiseMode(false, true, true, syncInvocationMode{}); err == nil {
		t.Fatal("expected fetch-only pairwise mode to fail")
	}
	if err := validatePairwiseMode(true, false, true, syncInvocationMode{}); err == nil {
		t.Fatal("expected non-waiting pairwise mode to fail")
	}
	if err := validatePairwiseMode(true, true, false, syncInvocationMode{}); err == nil {
		t.Fatal("expected no-feedback pairwise mode to fail")
	}
	if err := validatePairwiseMode(true, true, true, syncInvocationMode{Detach: true}); err == nil {
		t.Fatal("expected detached pairwise mode to fail")
	}

	syncPairwise = false
	syncCrossRepoOnly = true
	if err := validatePairwiseMode(true, true, true, syncInvocationMode{}); err == nil {
		t.Fatal("expected cross-repo-only without pairwise to fail")
	}
}

func TestResolvePairwiseRepoScopesPrefersCrossRepoPeersBeforeSelf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	groupID := "grp_pairwise"
	targetRoot := filepath.Join(home, "target-repo")
	peerRoot := filepath.Join(home, "peer-repo")
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		t.Fatalf("mkdir target root: %v", err)
	}
	if err := os.MkdirAll(peerRoot, 0o755); err != nil {
		t.Fatalf("mkdir peer root: %v", err)
	}

	writeRepoConfig := func(root string, remote string, docID string) {
		err := config.WriteProjectConfig(root, config.Project{
			Version:     1,
			ProjectName: filepath.Base(root),
			Group:       config.Group{ID: groupID, Name: "Pairwise"},
			Repos: []config.Repo{{
				RemoteURL:  remote,
				DocumentID: docID,
			}},
		})
		if err != nil {
			t.Fatalf("write project config for %s: %v", root, err)
		}
	}

	writeRepoConfig(targetRoot, "z-target", "doc_target")
	writeRepoConfig(peerRoot, "a-peer", "doc_peer")

	store, err := db.Open()
	if err != nil {
		t.Fatalf("open workspace db: %v", err)
	}
	defer store.Close()

	for _, root := range []string{targetRoot, peerRoot} {
		if err := store.UpsertItem(context.Background(), &db.TrackedItem{
			Path:     root,
			Kind:     "repo",
			GroupID:  groupID,
			RepoRoot: root,
		}); err != nil {
			t.Fatalf("upsert tracked repo %s: %v", root, err)
		}
	}

	scopes, err := resolvePairwiseRepoScopes(groupID, []string{targetRoot}, false)
	if err != nil {
		t.Fatalf("resolve pairwise scopes: %v", err)
	}
	peers := scopes[targetRoot]
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d (%#v)", len(peers), peers)
	}
	if peers[0].Root != peerRoot || peers[1].Root != targetRoot {
		t.Fatalf("expected cross-repo peer first and self last, got %#v", peers)
	}

	scopes, err = resolvePairwiseRepoScopes(groupID, []string{targetRoot}, true)
	if err != nil {
		t.Fatalf("resolve cross-repo-only scopes: %v", err)
	}
	peers = scopes[targetRoot]
	if len(peers) != 1 || peers[0].Root != peerRoot {
		t.Fatalf("expected only cross-repo peer when cross-repo-only is enabled, got %#v", peers)
	}
}
