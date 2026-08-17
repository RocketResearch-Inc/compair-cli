package baseline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	protocolVersion            = "baseline-control-plane.v1"
	maxFileBytes               = 200_000
	maxPartBytes               = 1_000_000
	maxPartItems               = 1_000
	pinnedSpecSHA256           = "3b45287a54d04cea751e9cc3209c5f0783192de13062e682eadcae40af322650"
	pinnedSchemaSHA256         = "4ea2bbd09c6362b0510cf6cc43dc16f0ec3458fda2525a2409a59d299e801200"
	pinnedFixtureSHA256        = "bd89803abcdeac97a57bf0c22b9460cf61be8e0b186b58db8fc0c5cfd3dd60c4"
	pinnedScannerFixtureSHA256 = "e483e017270aff1997aafce4225e4b4787e643084ffe716dfe36acb40c03c553"
)

type treeEntry struct {
	repositoryID   string
	repositoryName string
	revision       string
	relativePath   string
	mode           string
	objectID       string
	blob           []byte
}

type scannedFile struct {
	treeEntry
	ordinal         int
	state           string
	skipReason      any
	contentRequired bool
	contentHash     string
}

type contentPart struct {
	ordinal int
	files   []scannedFile
	bytes   int
}

type scanOptions struct {
	group        string
	changedSpec  string
	siblingSpecs []string
	baseRevision string
	headRevision string
	dryRun       bool
	jsonOutput   bool
}

// These test-only helpers are the pure scanner contract frozen by L.0. Phase
// L.2 may implement the scanner, but it must satisfy this behavior unchanged.
func canonicalContractPath(value string) error {
	if value == "" || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) {
		return errors.New("path must be nonempty UTF-8 NFC")
	}
	if path.IsAbs(value) || strings.Contains(value, "\\") ||
		strings.ContainsRune(value, '\x00') || strings.Contains(value, "//") ||
		strings.HasSuffix(value, "/") {
		return errors.New("path must be canonical POSIX relative")
	}
	if len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') ||
		(value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' {
		return errors.New("drive paths are forbidden")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("path aliases and traversal are forbidden")
		}
	}
	if path.Clean(value) != value {
		return errors.New("path aliases are forbidden")
	}
	return nil
}

func validIdentity(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			(index > 0 && strings.ContainsRune("._~-", character)) {
			continue
		}
		return false
	}
	return true
}

func validGroup(value string) bool {
	return len(value) <= 64 && validIdentity(value)
}

func validRepositoryName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			(index > 0 && strings.ContainsRune("._-", character)) {
			continue
		}
		return false
	}
	return true
}

func validRevision(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validateScanOptions(options scanOptions) error {
	if !validGroup(options.group) {
		return errors.New("explicit group is required")
	}
	if options.changedSpec == "" || len(options.siblingSpecs) == 0 {
		return errors.New("explicit changed and sibling specs are required")
	}
	if !validRevision(options.baseRevision) || !validRevision(options.headRevision) ||
		options.baseRevision == options.headRevision {
		return errors.New("distinct exact base/head revisions are required")
	}
	if !options.dryRun || !options.jsonOutput {
		return errors.New("L.0 scanner contract is dry-run JSON only")
	}
	return nil
}

func classify(entry treeEntry) (scannedFile, error) {
	if !validIdentity(entry.repositoryID) || !validRepositoryName(entry.repositoryName) {
		return scannedFile{}, errors.New("invalid repository identity")
	}
	if !validRevision(entry.revision) || !validRevision(entry.objectID) {
		return scannedFile{}, errors.New("immutable Git object identity is required")
	}
	if err := canonicalContractPath(entry.relativePath); err != nil {
		return scannedFile{}, err
	}
	result := scannedFile{
		treeEntry:   entry,
		contentHash: hashBytes(entry.blob),
		skipReason:  nil,
	}
	// Frozen precedence: object type, excluded directory, size, UTF-8.
	switch entry.mode {
	case "120000":
		result.state = "symlink_rejected"
		result.skipReason = "symlink"
		return result, nil
	case "160000":
		result.state = "excluded"
		result.skipReason = "unsupported_file_type"
		return result, nil
	case "100644", "100755":
	default:
		result.state = "excluded"
		result.skipReason = "unsupported_file_type"
		return result, nil
	}
	for _, component := range strings.Split(entry.relativePath, "/") {
		switch component {
		case ".git", ".compair", "build", "dist", "node_modules":
			result.state = "excluded"
			result.skipReason = "excluded_directory"
			return result, nil
		}
	}
	if len(entry.blob) > maxFileBytes {
		result.state = "oversized"
		result.skipReason = "oversized"
		return result, nil
	}
	if !utf8.Valid(entry.blob) {
		result.state = "unsupported_utf8"
		result.skipReason = "non_utf8"
		return result, nil
	}
	result.state = "supported"
	result.contentRequired = true
	return result, nil
}

func scan(entries []treeEntry) ([]scannedFile, error) {
	files := make([]scannedFile, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		file, err := classify(entry)
		if err != nil {
			return nil, err
		}
		key := entry.repositoryID + "\x00" + entry.relativePath
		if _, exists := seen[key]; exists {
			return nil, errors.New("duplicate repository/path")
		}
		seen[key] = struct{}{}
		files = append(files, file)
	}
	sort.Slice(files, func(left, right int) bool {
		a, b := files[left], files[right]
		if a.repositoryName != b.repositoryName {
			return a.repositoryName < b.repositoryName
		}
		if a.relativePath != b.relativePath {
			return a.relativePath < b.relativePath
		}
		return a.repositoryID < b.repositoryID
	})
	for index := range files {
		files[index].ordinal = index + 1
	}
	return files, nil
}

func pack(files []scannedFile) []contentPart {
	var parts []contentPart
	for _, file := range files {
		if !file.contentRequired {
			continue
		}
		needsPart := len(parts) == 0 || len(parts[len(parts)-1].files) == maxPartItems ||
			parts[len(parts)-1].bytes+len(file.blob) > maxPartBytes
		if needsPart {
			parts = append(parts, contentPart{ordinal: len(parts) + 1})
		}
		last := &parts[len(parts)-1]
		last.files = append(last.files, file)
		last.bytes += len(file.blob)
	}
	return parts
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func readProtocolFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	all := append([]string{"..", "..", "protocol"}, parts...)
	value, err := os.ReadFile(filepath.Join(all...))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func messageMap(t *testing.T) map[string]map[string]any {
	t.Helper()
	var fixtures []map[string]any
	if err := json.Unmarshal(
		readProtocolFile(t, "fixtures", "baseline-control-plane.v1.valid.json"),
		&fixtures,
	); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]map[string]any, len(fixtures))
	for _, fixture := range fixtures {
		messageType, ok := fixture["message_type"].(string)
		if !ok || messageType == "" {
			t.Fatal("fixture lacks message_type")
		}
		result[messageType] = fixture
	}
	return result
}

func canonicalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestProtocolArtifactsAndMessagesAreFrozen(t *testing.T) {
	if got := hashBytes(readProtocolFile(t, "baseline-control-plane.v1.md")); got != pinnedSpecSHA256 {
		t.Fatalf("spec SHA-256 = %s", got)
	}
	if got := hashBytes(readProtocolFile(t, "baseline-control-plane.v1.schema.json")); got != pinnedSchemaSHA256 {
		t.Fatalf("schema SHA-256 = %s", got)
	}
	if got := hashBytes(readProtocolFile(t, "fixtures", "baseline-control-plane.v1.valid.json")); got != pinnedFixtureSHA256 {
		t.Fatalf("fixture SHA-256 = %s", got)
	}
	if got := hashBytes(readProtocolFile(t, "fixtures", "baseline-scanner-inputs.v1.valid.json")); got != pinnedScannerFixtureSHA256 {
		t.Fatalf("scanner fixture SHA-256 = %s", got)
	}

	var schema map[string]any
	if err := json.Unmarshal(readProtocolFile(t, "baseline-control-plane.v1.schema.json"), &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("schema draft = %v", schema["$schema"])
	}
	messages := messageMap(t)
	wantTypes := []string{
		"scan_plan", "snapshot_begin", "snapshot_content_part", "snapshot_commit",
		"index_build_submit", "run_submit", "job_accepted", "job_status_request",
		"job_status", "error", "capabilities_request", "capabilities",
	}
	if len(messages) != len(wantTypes) {
		t.Fatalf("message count = %d", len(messages))
	}
	for _, messageType := range wantTypes {
		message, ok := messages[messageType]
		if !ok || message["protocol_version"] != protocolVersion {
			t.Fatalf("invalid %s fixture", messageType)
		}
	}
}

func TestLocalScannerInputFixtureRequiresExplicitImmutableInputs(t *testing.T) {
	var fixture struct {
		Changed struct {
			SchemaVersion      string `json:"schema_version"`
			LocalPath          string `json:"local_path"`
			RepositoryID       string `json:"repository_id"`
			RepositoryName     string `json:"repository_name"`
			RepositoryRevision string `json:"repository_revision"`
			SourceDocumentID   string `json:"source_document_id"`
		} `json:"changed"`
		Siblings []struct {
			SchemaVersion      string `json:"schema_version"`
			LocalPath          string `json:"local_path"`
			RepositoryID       string `json:"repository_id"`
			RepositoryName     string `json:"repository_name"`
			RepositoryRevision string `json:"repository_revision"`
		} `json:"siblings"`
		BaseRevision string `json:"base_revision"`
		HeadRevision string `json:"head_revision"`
		GroupID      string `json:"group_id"`
		DryRun       bool   `json:"dry_run"`
		JSON         bool   `json:"json"`
	}
	if err := json.Unmarshal(
		readProtocolFile(t, "fixtures", "baseline-scanner-inputs.v1.valid.json"),
		&fixture,
	); err != nil {
		t.Fatal(err)
	}
	options := scanOptions{
		group:        fixture.GroupID,
		changedSpec:  fixture.Changed.LocalPath,
		baseRevision: fixture.BaseRevision,
		headRevision: fixture.HeadRevision,
		dryRun:       fixture.DryRun,
		jsonOutput:   fixture.JSON,
	}
	for _, sibling := range fixture.Siblings {
		options.siblingSpecs = append(options.siblingSpecs, sibling.LocalPath)
	}
	if err := validateScanOptions(options); err != nil {
		t.Fatal(err)
	}
	if fixture.Changed.SchemaVersion != "baseline-changed-repository-input.v1" ||
		fixture.Changed.RepositoryRevision != fixture.HeadRevision ||
		fixture.Changed.SourceDocumentID == "" {
		t.Fatalf("changed input = %#v", fixture.Changed)
	}
	seen := map[string]struct{}{fixture.Changed.RepositoryID: {}}
	for _, sibling := range fixture.Siblings {
		if sibling.SchemaVersion != "baseline-sibling-repository-input.v1" ||
			!validIdentity(sibling.RepositoryID) || !validRepositoryName(sibling.RepositoryName) ||
			!validRevision(sibling.RepositoryRevision) {
			t.Fatalf("sibling input = %#v", sibling)
		}
		if _, duplicate := seen[sibling.RepositoryID]; duplicate {
			t.Fatalf("duplicate repository ID %q", sibling.RepositoryID)
		}
		seen[sibling.RepositoryID] = struct{}{}
	}
	plan := canonicalJSON(t, messageMap(t)["scan_plan"])
	if bytes.Contains(plan, []byte(fixture.Changed.LocalPath)) {
		t.Fatal("changed local path entered dry-run output")
	}
	for _, sibling := range fixture.Siblings {
		if bytes.Contains(plan, []byte(sibling.LocalPath)) {
			t.Fatal("sibling local path entered dry-run output")
		}
	}
}

func TestDryRunFixtureHasCanonicalManifestAndNoProtectedText(t *testing.T) {
	messages := messageMap(t)
	plan := messages["scan_plan"]
	snapshot := plan["snapshot"].(map[string]any)
	canonical := map[string]any{
		"schema_version":       snapshot["schema_version"],
		"changed_repository":   snapshot["changed_repository"],
		"sibling_repositories": snapshot["sibling_repositories"],
		"files":                snapshot["files"],
	}
	manifestHash := hashBytes(canonicalJSON(t, canonical))
	if snapshot["canonical_manifest_hash"] != manifestHash ||
		snapshot["snapshot_id"] != "bsnap_"+manifestHash {
		t.Fatalf("manifest identity mismatch: %v", snapshot["snapshot_id"])
	}

	serialized := canonicalJSON(t, plan)
	for _, forbidden := range []string{
		"content_utf8", "raw diff text", "local_path", "remote_url", "credential",
	} {
		if bytes.Contains(serialized, []byte(forbidden)) {
			t.Fatalf("dry-run contains forbidden field/value %q", forbidden)
		}
	}
	if rawDiff := plan["raw_diff"].(map[string]any); rawDiff["text"] != nil {
		t.Fatal("dry-run includes raw diff text")
	}
}

func TestCanonicalPOSIXPathPolicy(t *testing.T) {
	valid := []string{"a.txt", "src/a.txt", "unicodé/data.go"}
	for _, value := range valid {
		if err := canonicalContractPath(value); err != nil {
			t.Fatalf("valid path %q: %v", value, err)
		}
	}
	invalid := []string{
		"", "/src/a.txt", "../src/a.txt", "src/../a.txt", "src/./a.txt",
		"src//a.txt", "src/a.txt/", `src\a.txt`, "C:/src/a.txt", "src/\x00a.txt",
		"src/e\u0301.txt", string([]byte{'s', 'r', 'c', '/', 0xff}),
	}
	for _, value := range invalid {
		if err := canonicalContractPath(value); err == nil {
			t.Fatalf("invalid path accepted: %q", value)
		}
	}
}

func TestScannerRequiresEveryExplicitDryRunInput(t *testing.T) {
	valid := scanOptions{
		group:        "toy-group",
		changedSpec:  "changed.json",
		siblingSpecs: []string{"sibling.json"},
		baseRevision: strings.Repeat("a", 40),
		headRevision: strings.Repeat("b", 40),
		dryRun:       true,
		jsonOutput:   true,
	}
	if err := validateScanOptions(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*scanOptions)
	}{
		{name: "missing group", mutate: func(value *scanOptions) { value.group = "" }},
		{name: "missing changed repository", mutate: func(value *scanOptions) { value.changedSpec = "" }},
		{name: "missing siblings", mutate: func(value *scanOptions) { value.siblingSpecs = nil }},
		{name: "symbolic base", mutate: func(value *scanOptions) { value.baseRevision = "main~1" }},
		{name: "uppercase head", mutate: func(value *scanOptions) { value.headRevision = strings.Repeat("B", 40) }},
		{name: "same base and head", mutate: func(value *scanOptions) { value.headRevision = value.baseRevision }},
		{name: "not dry run", mutate: func(value *scanOptions) { value.dryRun = false }},
		{name: "not JSON", mutate: func(value *scanOptions) { value.jsonOutput = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := valid
			test.mutate(&options)
			if err := validateScanOptions(options); err == nil {
				t.Fatal("invalid scanner options accepted")
			}
		})
	}
}

func TestClassificationRejectsSymlinksAndFreezesSkipPrecedence(t *testing.T) {
	base := treeEntry{
		repositoryID: "repo-toy", repositoryName: "toy", revision: strings.Repeat("a", 40),
		relativePath: "src/a.txt", mode: "100644", objectID: strings.Repeat("b", 40), blob: []byte("alpha\n"),
	}
	tests := []struct {
		name        string
		mutate      func(*treeEntry)
		wantState   string
		wantReason  any
		wantContent bool
	}{
		{name: "supported", wantState: "supported", wantContent: true},
		{name: "inside symlink", mutate: func(item *treeEntry) { item.mode = "120000"; item.blob = []byte("a.txt") }, wantState: "symlink_rejected", wantReason: "symlink"},
		{name: "escaping symlink", mutate: func(item *treeEntry) { item.mode = "120000"; item.blob = []byte("../../outside") }, wantState: "symlink_rejected", wantReason: "symlink"},
		{name: "submodule", mutate: func(item *treeEntry) { item.mode = "160000" }, wantState: "excluded", wantReason: "unsupported_file_type"},
		{name: "excluded before size", mutate: func(item *treeEntry) {
			item.relativePath = "src/node_modules/a"
			item.blob = bytes.Repeat([]byte{'x'}, maxFileBytes+1)
		}, wantState: "excluded", wantReason: "excluded_directory"},
		{name: "oversized before UTF-8", mutate: func(item *treeEntry) { item.blob = append(bytes.Repeat([]byte{'x'}, maxFileBytes), 0xff) }, wantState: "oversized", wantReason: "oversized"},
		{name: "non UTF-8", mutate: func(item *treeEntry) { item.blob = []byte{0xff} }, wantState: "unsupported_utf8", wantReason: "non_utf8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := base
			if test.mutate != nil {
				test.mutate(&entry)
			}
			file, err := classify(entry)
			if err != nil {
				t.Fatal(err)
			}
			if file.state != test.wantState || file.skipReason != test.wantReason || file.contentRequired != test.wantContent {
				t.Fatalf("classification = state %q reason %v content %v", file.state, file.skipReason, file.contentRequired)
			}
			if entry.mode == "120000" && file.contentRequired {
				t.Fatal("symlink target became content")
			}
		})
	}
}

func TestManifestOrderingDuplicatesAndGreedyWholeFileParts(t *testing.T) {
	revision := strings.Repeat("a", 40)
	objectID := strings.Repeat("b", 40)
	entries := []treeEntry{
		{repositoryID: "repo-z", repositoryName: "zeta", revision: revision, relativePath: "a.txt", mode: "100644", objectID: objectID, blob: bytes.Repeat([]byte{'z'}, 100_001)},
		{repositoryID: "repo-a", repositoryName: "alpha", revision: revision, relativePath: "b.txt", mode: "100644", objectID: objectID, blob: bytes.Repeat([]byte{'b'}, 200_000)},
		{repositoryID: "repo-a", repositoryName: "alpha", revision: revision, relativePath: "a.txt", mode: "100644", objectID: objectID, blob: bytes.Repeat([]byte{'a'}, 200_000)},
	}
	files, err := scan(entries)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{files[0].repositoryName + "/" + files[0].relativePath, files[1].repositoryName + "/" + files[1].relativePath, files[2].repositoryName + "/" + files[2].relativePath}
	want := []string{"alpha/a.txt", "alpha/b.txt", "zeta/a.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v", got)
	}
	for index, file := range files {
		if file.ordinal != index+1 {
			t.Fatalf("ordinal %d = %d", index, file.ordinal)
		}
	}

	parts := pack(files)
	if len(parts) != 1 || parts[0].ordinal != 1 || parts[0].bytes != 500_001 || len(parts[0].files) != 3 {
		t.Fatalf("parts = %#v", parts)
	}
	large := make([]scannedFile, 6)
	for index := range large {
		large[index] = scannedFile{treeEntry: treeEntry{blob: bytes.Repeat([]byte{'x'}, 200_000)}, ordinal: index + 1, contentRequired: true}
	}
	parts = pack(large)
	if len(parts) != 2 || parts[0].bytes != 1_000_000 || len(parts[0].files) != 5 || parts[1].bytes != 200_000 || len(parts[1].files) != 1 {
		t.Fatalf("greedy parts = %#v", parts)
	}

	duplicate := append([]treeEntry(nil), entries...)
	duplicate = append(duplicate, entries[0])
	if _, err := scan(duplicate); err == nil {
		t.Fatal("duplicate repository/path accepted")
	}
}
