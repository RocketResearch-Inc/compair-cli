package compair

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSnapshotOptionsUseFullRepoDefaults(t *testing.T) {
	opts := defaultSnapshotOptions()
	if opts.MaxTreeEntries != 0 || opts.MaxFiles != 0 || opts.MaxTotalBytes != 0 || opts.MaxFileBytes != 0 || opts.MaxFileRead != 0 {
		t.Fatalf("expected full-repo defaults, got %+v", opts)
	}
}

func TestDescribeSnapshotLimitBytes(t *testing.T) {
	if got := describeSnapshotLimitBytes(0); got != "full repo (no cap)" {
		t.Fatalf("unexpected unlimited label: %q", got)
	}
	if got := describeSnapshotLimitBytes(1024); got != "1.0 KB" {
		t.Fatalf("unexpected sized label: %q", got)
	}
}

func TestFitChunkAllowsUnlimitedBudget(t *testing.T) {
	chunk := strings.Repeat("line\n", 500)
	payload, ok := fitChunk("### File: demo.go", chunk, "go", 160, 0)
	if !ok {
		t.Fatalf("expected chunk to fit when budget is unlimited")
	}
	if strings.Contains(payload, "[truncated]") {
		t.Fatalf("did not expect unlimited budget payload to be truncated")
	}
}

func TestSnapshotChunkProfileDefaultsAndAcceptsKnownValues(t *testing.T) {
	t.Setenv("COMPAIR_SNAPSHOT_CHUNK_PROFILE", "")
	if got := snapshotChunkProfile(); got != snapshotChunkProfileDefault {
		t.Fatalf("expected default profile, got %q", got)
	}

	t.Setenv("COMPAIR_SNAPSHOT_CHUNK_PROFILE", snapshotChunkProfileSemanticLite)
	if got := snapshotChunkProfile(); got != snapshotChunkProfileSemanticLite {
		t.Fatalf("expected semantic-lite profile, got %q", got)
	}

	t.Setenv("COMPAIR_SNAPSHOT_CHUNK_PROFILE", snapshotChunkProfileMarkdownStrict)
	if got := snapshotChunkProfile(); got != snapshotChunkProfileMarkdownStrict {
		t.Fatalf("expected markdown-strict profile, got %q", got)
	}

	t.Setenv("COMPAIR_SNAPSHOT_CHUNK_PROFILE", snapshotChunkProfileSignalStress)
	if got := snapshotChunkProfile(); got != snapshotChunkProfileSignalStress {
		t.Fatalf("expected signal-stress profile, got %q", got)
	}

	t.Setenv("COMPAIR_SNAPSHOT_CHUNK_PROFILE", "unknown-profile")
	if got := snapshotChunkProfile(); got != snapshotChunkProfileDefault {
		t.Fatalf("expected unknown profile to fall back to default, got %q", got)
	}
}

func TestWriteSnapshotManifestIfConfiguredWritesInspectableFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COMPAIR_SNAPSHOT_MANIFEST_DIR", dir)
	path := writeSnapshotManifestIfConfigured(snapshotChunkManifest{
		GeneratedAt:  "2026-04-15T00:00:00Z",
		Root:         "/tmp/example-repo",
		ChunkProfile: snapshotChunkProfileSignalStress,
		Stats:        snapshotStats{IncludedFiles: 2},
		Files: []snapshotChunkManifestFile{
			{Path: "docs/user-guide.md", ChunkCount: 5, ChunkLineCounts: []int{12, 8}, ChunkCharCounts: []int{120, 80}},
		},
	})
	if path == "" {
		t.Fatalf("expected manifest path")
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("expected manifest in %s, got %s", dir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var payload snapshotChunkManifest
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if payload.ChunkProfile != snapshotChunkProfileSignalStress {
		t.Fatalf("expected chunk profile %q, got %q", snapshotChunkProfileSignalStress, payload.ChunkProfile)
	}
	if len(payload.Files) != 1 || payload.Files[0].Path != "docs/user-guide.md" {
		t.Fatalf("unexpected manifest files: %#v", payload.Files)
	}
}

func TestChunkTextSemanticLiteSplitsPythonSymbolsWithoutDuplicatingPreamble(t *testing.T) {
	rule := chunkRule{MaxLines: 4, MinLines: 1, SplitPrefixes: []string{"def ", "class ", "async def "}}
	text := strings.Join([]string{
		"\"\"\"module doc\"\"\"",
		"",
		"import os",
		"",
		"# service comment",
		"class Service:",
		"    \"\"\"service docstring\"\"\"",
		"    pass",
		"",
		"def helper():",
		"    \"\"\"helper docstring\"\"\"",
		"    return os.getenv(\"HOME\")",
	}, "\n")

	chunks := chunkText(text, "demo.py", "python", rule, snapshotChunkProfileSemanticLite)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d: %#v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0], "\"\"\"module doc\"\"\"") || !strings.Contains(chunks[0], "import os") {
		t.Fatalf("expected preamble chunk to keep module doc and imports, got %q", chunks[0])
	}
	if strings.Contains(chunks[1], "import os") {
		t.Fatalf("semantic-lite should not duplicate preamble into first symbol chunk: %q", chunks[1])
	}
	if !strings.Contains(chunks[1], "# service comment") || !strings.Contains(chunks[1], "class Service:") || !strings.Contains(chunks[1], "\"\"\"service docstring\"\"\"") {
		t.Fatalf("expected class chunk to keep comment and docstring, got %q", chunks[1])
	}
	if !strings.Contains(chunks[2], "def helper():") || !strings.Contains(chunks[2], "\"\"\"helper docstring\"\"\"") {
		t.Fatalf("expected helper chunk to keep function body/docstring, got %q", chunks[2])
	}
}

func TestChunkTextSemanticContextDuplicatesPreambleIntoPythonSymbols(t *testing.T) {
	rule := chunkRule{MaxLines: 4, MinLines: 1, SplitPrefixes: []string{"def ", "class ", "async def "}}
	text := strings.Join([]string{
		"\"\"\"module doc\"\"\"",
		"",
		"import os",
		"",
		"class Service:",
		"    pass",
		"",
		"def helper():",
		"    return os.getenv(\"HOME\")",
	}, "\n")

	chunks := chunkText(text, "demo.py", "python", rule, snapshotChunkProfileSemanticContext)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 symbol chunks, got %d: %#v", len(chunks), chunks)
	}
	for idx, chunk := range chunks {
		if !strings.Contains(chunk, "import os") {
			t.Fatalf("expected semantic-context chunk %d to include shared preamble, got %q", idx, chunk)
		}
	}
}

func TestChunkTextSemanticLiteSplitsMarkdownByHeadingTree(t *testing.T) {
	rule := chunkRule{MaxLines: 4, MinLines: 1, SplitPrefixes: []string{"# "}}
	text := strings.Join([]string{
		"Intro line",
		"",
		"# Overview",
		"Overview body",
		"",
		"## Backend",
		"Backend details",
		"",
		"# Troubleshooting",
		"Fix steps",
	}, "\n")

	chunks := chunkText(text, "docs/user-guide.md", "markdown", rule, snapshotChunkProfileSemanticLite)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 markdown chunks, got %d: %#v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0], "Intro line") || !strings.Contains(chunks[0], "# Overview") {
		t.Fatalf("expected intro and first heading to stay together, got %q", chunks[0])
	}
	if !strings.Contains(chunks[1], "# Overview") || !strings.Contains(chunks[1], "## Backend") {
		t.Fatalf("expected subsection chunk to preserve heading tree, got %q", chunks[1])
	}
	if !strings.Contains(chunks[2], "# Troubleshooting") || !strings.Contains(chunks[2], "Fix steps") {
		t.Fatalf("expected final section chunk, got %q", chunks[2])
	}
}

func TestChunkTextMarkdownStrictKeepsSiblingSectionsSeparate(t *testing.T) {
	rule := chunkRule{MaxLines: 4, MinLines: 1, SplitPrefixes: []string{"# "}}
	text := strings.Join([]string{
		"# Guide",
		"Intro",
		"",
		"## Install",
		"Install intro",
		"",
		"### macOS",
		"brew install compair",
		"",
		"### Debian",
		"apt install compair",
		"",
		"## Review",
		"Run compair review",
	}, "\n")

	chunks := chunkText(text, "docs/user-guide.md", "markdown", rule, snapshotChunkProfileMarkdownStrict)
	if len(chunks) != 5 {
		t.Fatalf("expected 5 markdown-strict chunks, got %d: %#v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[2], "### macOS") || strings.Contains(chunks[2], "### Debian") {
		t.Fatalf("expected macOS subsection to stay isolated, got %q", chunks[2])
	}
	if !strings.Contains(chunks[3], "### Debian") || strings.Contains(chunks[3], "### macOS") {
		t.Fatalf("expected Debian subsection to stay isolated, got %q", chunks[3])
	}
}

func TestChunkTextMarkdownH2KeepsH2SubtreeTogether(t *testing.T) {
	rule := chunkRule{MaxLines: 20, MinLines: 1, SplitPrefixes: []string{"# "}}
	text := strings.Join([]string{
		"# Guide",
		"Intro",
		"",
		"## Install",
		"Install intro",
		"",
		"### macOS",
		"brew install compair",
		"",
		"### Debian",
		"apt install compair",
		"",
		"## Review",
		"Run compair review",
	}, "\n")

	chunks := chunkText(text, "docs/user-guide.md", "markdown", rule, snapshotChunkProfileMarkdownH2)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 markdown-h2 chunks, got %d: %#v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[1], "## Install") || !strings.Contains(chunks[1], "### macOS") || !strings.Contains(chunks[1], "### Debian") {
		t.Fatalf("expected Install subtree chunk, got %q", chunks[1])
	}
}

func TestChunkTextMarkdownH2WindowRepeatsSectionIntroWhenSplit(t *testing.T) {
	rule := chunkRule{MaxLines: 6, MinLines: 1, SplitPrefixes: []string{"# "}}
	text := strings.Join([]string{
		"# Guide",
		"Intro",
		"",
		"## Install",
		"Install intro",
		"",
		"### macOS",
		"brew install compair",
		"",
		"### Debian",
		"apt install compair",
	}, "\n")

	chunks := chunkText(text, "docs/user-guide.md", "markdown", rule, snapshotChunkProfileMarkdownH2Win)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 markdown-h2-window chunks, got %d: %#v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[1], "Install intro") || !strings.Contains(chunks[2], "Install intro") {
		t.Fatalf("expected split child chunks to repeat H2 intro, got %#v", chunks)
	}
}

func TestChunkTextSignalStressCreatesFocusedMarkdownWindows(t *testing.T) {
	rule := chunkRule{MaxLines: 8, MinLines: 1, SplitPrefixes: []string{"# "}}
	text := strings.Join([]string{
		"# User Guide",
		"General intro",
		"",
		"## Email delivery",
		"Configure the mailer backend carefully.",
		"",
		"```bash",
		"COMPAIR_EMAIL_BACKEND=console",
		"COMPAIR_SMTP_HOST=localhost",
		"```",
		"",
		"Implementation lives in the console mailer provider.",
	}, "\n")

	chunks := chunkText(text, "docs/user-guide.md", "markdown", rule, snapshotChunkProfileSignalStress)
	if len(chunks) < 2 {
		t.Fatalf("expected signal-stress to create multiple focused chunks, got %d: %#v", len(chunks), chunks)
	}
	foundSignal := false
	for _, chunk := range chunks {
		if strings.Contains(chunk, "COMPAIR_EMAIL_BACKEND=console") && strings.Contains(chunk, "console mailer provider") {
			foundSignal = true
			break
		}
	}
	if !foundSignal {
		t.Fatalf("expected a focused markdown chunk around mailer signals, got %#v", chunks)
	}
}

func TestChunkTextSemanticLiteSplitsStructuredConfigSections(t *testing.T) {
	rule := chunkRule{MaxLines: 4, MinLines: 1, SplitPrefixes: []string{"["}}
	text := strings.Join([]string{
		"title = \"demo\"",
		"",
		"[project]",
		"name = \"compair\"",
		"",
		"[tool.compair]",
		"mode = \"hybrid\"",
	}, "\n")

	chunks := chunkText(text, "pyproject.toml", "toml", rule, snapshotChunkProfileSemanticLite)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 structured chunks, got %d: %#v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[1], "[project]") || !strings.Contains(chunks[2], "[tool.compair]") {
		t.Fatalf("expected section chunks, got %#v", chunks)
	}
}

func TestChunkTextSignalStressCreatesFocusedStructuredWindows(t *testing.T) {
	rule := chunkRule{MaxLines: 8, MinLines: 1, SplitPrefixes: []string{"["}}
	text := strings.Join([]string{
		"[project]",
		"name = \"compair\"",
		"license = { text = \"MIT\" }",
		"classifiers = [",
		"  \"License :: OSI Approved :: MIT License\",",
		"]",
		"",
		"[tool.compair]",
		"mailer_backend = \"console\"",
	}, "\n")

	chunks := chunkText(text, "pyproject.toml", "toml", rule, snapshotChunkProfileSignalStress)
	if len(chunks) == 0 {
		t.Fatalf("expected signal-stress structured chunks, got none")
	}
	foundLicense := false
	for _, chunk := range chunks {
		if strings.Contains(chunk, "license = { text = \"MIT\" }") {
			foundLicense = true
			break
		}
	}
	if !foundLicense {
		t.Fatalf("expected a focused structured chunk around license lines, got %#v", chunks)
	}
}
