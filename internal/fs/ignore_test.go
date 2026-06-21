package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompairIgnoreDirectoryPrefix(t *testing.T) {
	root := t.TempDir()
	err := os.WriteFile(
		filepath.Join(root, ".compairignore"),
		[]byte("internal/studio/sdk/docs/\n__snapshots__/\n*.pb.go\n"),
		0o644,
	)
	if err != nil {
		t.Fatal(err)
	}

	ig := LoadIgnore(root)
	if !ig.ShouldIgnore("internal/studio/sdk/docs/sdks/sdk/README.md", false) {
		t.Fatal("expected trailing slash pattern to ignore nested files")
	}
	if !ig.ShouldIgnore("proto/service.pb.go", false) {
		t.Fatal("expected basename glob to ignore generated protobuf files")
	}
	if !ig.ShouldIgnore("packages/app/src/__snapshots__/example.snap", false) {
		t.Fatal("expected unqualified directory pattern to ignore matching nested directories")
	}
	if ig.ShouldIgnore("internal/studio/sdk/sdk.go", false) {
		t.Fatal("did not expect sibling source file to be ignored")
	}
}

func TestDefaultDirectoryIgnoreMatchesTrackedFilePaths(t *testing.T) {
	ig := DefaultIgnore()
	if !ig.ShouldIgnore("dist/app.js", false) {
		t.Fatal("expected tracked file below default ignored directory to be ignored")
	}
	if !ig.ShouldIgnore("packages/web/node_modules/lib/index.js", false) {
		t.Fatal("expected nested default ignored directory to be ignored")
	}
	if ig.ShouldIgnore("src/app.js", false) {
		t.Fatal("did not expect normal source file to be ignored")
	}
}
