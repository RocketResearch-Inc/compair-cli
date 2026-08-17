package baseline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type scannerToy struct {
	changedRoot string
	siblingRoot string
	base        string
	head        string
	sibling     string
	diff        []byte
}

func TestProductionScannerUsesImmutableObjectsAndFrozenClassification(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	before := gitTest(t, toy.siblingRoot, "status", "--porcelain=v2", "--untracked-files=all")
	first, err := NewScanner().Scan(context.Background(), input.GroupID, input)
	if err != nil {
		t.Fatal(err)
	}
	defer first.ClearProtected()
	after := gitTest(t, toy.siblingRoot, "status", "--porcelain=v2", "--untracked-files=all")
	if before != after {
		t.Fatalf("scan mutated sibling worktree: before %q after %q", before, after)
	}

	if first.Plan.RawDiff.ByteSize != len(toy.diff) || first.Plan.RawDiff.SHA256 != testSHA256(toy.diff) || !bytes.Equal(first.RawDiff, toy.diff) {
		t.Fatalf("raw diff metadata/content mismatch: %#v", first.Plan.RawDiff)
	}
	if first.Plan.Snapshot.RepositoryCount != 1 || first.Plan.Snapshot.TotalFileCount != 11 || first.Plan.Snapshot.SupportedFileCount != 4 {
		t.Fatalf("snapshot counts = %#v", first.Plan.Snapshot)
	}
	manifestValue := struct {
		SchemaVersion       string              `json:"schema_version"`
		ChangedRepository   ChangedRepository   `json:"changed_repository"`
		SiblingRepositories []SiblingRepository `json:"sibling_repositories"`
		Files               []FileRecord        `json:"files"`
	}{first.Plan.Snapshot.SchemaVersion, first.Plan.Snapshot.ChangedRepository, first.Plan.Snapshot.SiblingRepositories, first.Plan.Snapshot.Files}
	manifestJCS, err := canonicalJSONBytes(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	if first.Plan.Snapshot.CanonicalManifestHash != testSHA256(manifestJCS) || first.Plan.Snapshot.SnapshotID != "bsnap_"+testSHA256(manifestJCS) {
		t.Fatal("snapshot identity is not the exact metadata-only JCS hash")
	}
	planJCS, err := canonicalJSONBytes(first.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if first.Report.ScanPlanJCSSHA256 != testSHA256(planJCS) {
		t.Fatal("scan plan JCS hash mismatch")
	}
	descriptors := make([]PartDescriptor, len(first.Parts))
	for index := range first.Parts {
		descriptors[index] = first.Parts[index].Descriptor
	}
	descriptorJCS, err := canonicalJSONBytes(descriptors)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentManifestHash != testSHA256(descriptorJCS) || first.Report.ContentManifestHash != first.ContentManifestHash {
		t.Fatal("content manifest JCS hash mismatch")
	}

	byPath := make(map[string]FileRecord)
	for index, record := range first.Plan.Snapshot.Files {
		if record.Ordinal != index+1 {
			t.Fatalf("ordinal %d = %d", index, record.Ordinal)
		}
		if record.RepositoryID == input.Changed.RepositoryID {
			t.Fatal("changed repository entered sibling file corpus")
		}
		byPath[record.RelativePath] = record
	}
	assertFileState(t, byPath, "regular.txt", "supported", "", "100644", []byte("alpha\n"))
	assertFileState(t, byPath, "empty.txt", "supported", "", "100644", []byte{})
	assertFileState(t, byPath, "script.sh", "supported", "", "100755", []byte("#!/bin/sh\nexit 0\n"))
	assertFileState(t, byPath, "unicode/é.txt", "supported", "", "100644", []byte("café\n"))
	assertFileState(t, byPath, "bad.bin", "unsupported_utf8", "non_utf8", "100644", []byte{0xff, 0xfe})
	assertFileState(t, byPath, "large.bin", "oversized", "oversized", "100644", bytes.Repeat([]byte("L"), MaxFileBytes+1))
	assertFileState(t, byPath, ".compair/private.txt", "excluded", "excluded_directory", "100644", []byte("private marker\n"))
	assertFileState(t, byPath, "node_modules/dependency.txt", "excluded", "excluded_directory", "100644", []byte("dependency marker\n"))
	assertFileState(t, byPath, "inside-link", "symlink_rejected", "symlink", "120000", []byte("regular.txt"))
	assertFileState(t, byPath, "escape-link", "symlink_rejected", "symlink", "120000", []byte("../../outside-secret"))
	gitlink := byPath["vendor/module"]
	if gitlink.FileState != "excluded" || dereference(gitlink.SkipReason) != "unsupported_file_type" || gitlink.GitMode != "160000" || gitlink.ContentSHA256 != nil {
		t.Fatalf("gitlink = %#v", gitlink)
	}

	encodedFirst := encodeReportForTest(t, first.Report)
	for _, forbidden := range []string{toy.changedRoot, toy.siblingRoot, "alpha\n", "private marker", "outside-secret", string(toy.diff)} {
		if bytes.Contains(encodedFirst, []byte(forbidden)) {
			t.Fatalf("dry-run output leaked protected value %q", forbidden)
		}
	}

	// Mutate tracked and untracked working-tree state, including a symlink.
	writeScannerFile(t, filepath.Join(toy.siblingRoot, "regular.txt"), []byte("dirty worktree\n"), 0o644)
	writeScannerFile(t, filepath.Join(toy.siblingRoot, "untracked.txt"), []byte("untracked\n"), 0o644)
	writeScannerFile(t, filepath.Join(toy.changedRoot, ".gitattributes"), []byte("changed.txt binary\n"), 0o644)
	writeScannerFile(t, filepath.Join(toy.changedRoot, ".git", "info", "attributes"), []byte("changed.txt binary\n"), 0o600)
	if runtime.GOOS != "windows" {
		if err := os.Remove(filepath.Join(toy.siblingRoot, "escape-link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("different-target", filepath.Join(toy.siblingRoot, "escape-link")); err != nil {
			t.Fatal(err)
		}
	}
	dirtyBefore := gitTest(t, toy.siblingRoot, "status", "--porcelain=v2", "--untracked-files=all")
	changedDirtyBefore := gitTest(t, toy.changedRoot, "status", "--porcelain=v2", "--untracked-files=all")
	second, err := NewScanner().Scan(context.Background(), input.GroupID, input)
	if err != nil {
		t.Fatal(err)
	}
	defer second.ClearProtected()
	dirtyAfter := gitTest(t, toy.siblingRoot, "status", "--porcelain=v2", "--untracked-files=all")
	changedDirtyAfter := gitTest(t, toy.changedRoot, "status", "--porcelain=v2", "--untracked-files=all")
	if dirtyBefore != dirtyAfter {
		t.Fatalf("scan mutated dirty worktree: before %q after %q", dirtyBefore, dirtyAfter)
	}
	if changedDirtyBefore != changedDirtyAfter {
		t.Fatalf("scan mutated changed worktree: before %q after %q", changedDirtyBefore, changedDirtyAfter)
	}
	if !bytes.Equal(encodedFirst, encodeReportForTest(t, second.Report)) {
		t.Fatal("clean and dirty immutable scans were not byte-identical")
	}
	if first.Report.DeterministicFingerprint == "" || first.Report.DeterministicFingerprint != second.Report.DeterministicFingerprint {
		t.Fatal("deterministic scan fingerprint mismatch")
	}
}

func TestProductionScannerSiblingOrderAndProcessStateAreDeterministic(t *testing.T) {
	toy := newScannerToy(t)
	secondRoot := initScannerRepo(t)
	writeScannerFile(t, filepath.Join(secondRoot, "z.txt"), []byte("zeta\n"), 0o644)
	gitTest(t, secondRoot, "add", "--", "z.txt")
	gitTest(t, secondRoot, "commit", "-m", "second sibling")
	secondRevision := strings.TrimSpace(gitTest(t, secondRoot, "rev-parse", "HEAD"))

	inputA := toy.input()
	inputA.Siblings = append(inputA.Siblings, SiblingRepositoryInput{
		SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: secondRoot,
		RepositoryID: "repo-a-second", RepositoryName: "aardvark", RepositoryRevision: secondRevision,
	})
	inputB := inputA
	inputB.Siblings = []SiblingRepositoryInput{inputA.Siblings[1], inputA.Siblings[0]}

	oldLocale, hadLocale := os.LookupEnv("LC_ALL")
	oldTZ, hadTZ := os.LookupEnv("TZ")
	t.Setenv("LC_ALL", "tr_TR.UTF-8")
	t.Setenv("TZ", "Pacific/Kiritimati")
	first, err := NewScanner().Scan(context.Background(), inputA.GroupID, inputA)
	if err != nil {
		t.Fatal(err)
	}
	defer first.ClearProtected()
	t.Setenv("LC_ALL", "C")
	t.Setenv("TZ", "UTC")
	second, err := NewScanner().Scan(context.Background(), inputB.GroupID, inputB)
	if err != nil {
		t.Fatal(err)
	}
	defer second.ClearProtected()
	if !bytes.Equal(encodeReportForTest(t, first.Report), encodeReportForTest(t, second.Report)) {
		t.Fatal("sibling input order, locale, or timezone changed output")
	}
	if first.Plan.Snapshot.SiblingRepositories[0].RepositoryName != "aardvark" {
		t.Fatalf("repository order = %#v", first.Plan.Snapshot.SiblingRepositories)
	}
	if hadLocale {
		_ = os.Setenv("LC_ALL", oldLocale)
	}
	if hadTZ {
		_ = os.Setenv("TZ", oldTZ)
	}
}

func TestProductionScannerNeverContactsConfiguredRemote(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	toy := newScannerToy(t)
	gitTest(t, toy.changedRoot, "remote", "add", "origin", server.URL+"/changed.git")
	gitTest(t, toy.siblingRoot, "remote", "add", "origin", server.URL+"/sibling.git")
	temporaryRoot := t.TempDir()
	scanner := NewScanner()
	scanner.temporaryRoot = temporaryRoot
	result, err := scanner.Scan(context.Background(), "toy-group", toy.input())
	if err != nil {
		t.Fatal(err)
	}
	result.ClearProtected()
	if requests != 0 {
		t.Fatalf("scanner made %d network requests", requests)
	}
	remaining, err := os.ReadDir(temporaryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("scanner retained temporary material: %#v", remaining)
	}
}

func TestProductionScannerSupportsFrozenSHA256GitIdentities(t *testing.T) {
	changed := initScannerRepoWithObjectFormat(t, "sha256")
	writeScannerFile(t, filepath.Join(changed, "change.txt"), []byte("before\n"), 0o644)
	gitTest(t, changed, "add", "--", "change.txt")
	gitTest(t, changed, "commit", "-m", "base")
	base := strings.TrimSpace(gitTest(t, changed, "rev-parse", "HEAD"))
	writeScannerFile(t, filepath.Join(changed, "change.txt"), []byte("after\n"), 0o644)
	gitTest(t, changed, "add", "--", "change.txt")
	gitTest(t, changed, "commit", "-m", "head")
	head := strings.TrimSpace(gitTest(t, changed, "rev-parse", "HEAD"))
	sibling := initScannerRepoWithObjectFormat(t, "sha256")
	writeScannerFile(t, filepath.Join(sibling, "file.txt"), []byte("content\n"), 0o644)
	gitTest(t, sibling, "add", "--", "file.txt")
	gitTest(t, sibling, "commit", "-m", "sibling")
	siblingRevision := strings.TrimSpace(gitTest(t, sibling, "rev-parse", "HEAD"))
	if len(base) != 64 || len(head) != 64 || len(siblingRevision) != 64 {
		t.Fatalf("SHA-256 Git identities = %q %q %q", base, head, siblingRevision)
	}
	input := ScanInput{
		Changed: ChangedRepositoryInput{
			SchemaVersion: "baseline-changed-repository-input.v1", LocalPath: changed,
			RepositoryID: "sha256-changed", RepositoryName: "sha256-changed", RepositoryRevision: head,
			SourceDocumentID: "33333333-3333-4333-8333-333333333333",
		},
		Siblings: []SiblingRepositoryInput{{
			SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: sibling,
			RepositoryID: "sha256-sibling", RepositoryName: "sha256-sibling", RepositoryRevision: siblingRevision,
		}},
		BaseRevision: base, HeadRevision: head, GroupID: "sha256-group", DryRun: true, JSON: true,
	}
	result, err := NewScanner().Scan(context.Background(), input.GroupID, input)
	if err != nil {
		t.Fatal(err)
	}
	defer result.ClearProtected()
	if len(result.Plan.Snapshot.Files) != 1 || len(result.Plan.Snapshot.Files[0].GitObjectID) != 64 || result.Report.RawDiff.ByteSize == 0 {
		t.Fatalf("SHA-256 scan result = %#v", result.Plan)
	}
}

func TestProductionScannerRejectsStrictJSONIdentityAndPathAliases(t *testing.T) {
	valid := `{
      "changed":{"schema_version":"baseline-changed-repository-input.v1","local_path":"/tmp/c","repository_id":"changed","repository_name":"changed","repository_revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","source_document_id":"11111111-1111-4111-8111-111111111111"},
      "siblings":[{"schema_version":"baseline-sibling-repository-input.v1","local_path":"/tmp/s","repository_id":"sibling","repository_name":"sibling","repository_revision":"cccccccccccccccccccccccccccccccccccccccc"}],
      "base_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","group_id":"toy-group","dry_run":true,"json":true}`
	if _, err := DecodeScanInput([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(valid, `"group_id":"toy-group"`, `"group_id":"toy-group","group_id":"other"`, 1),
		strings.Replace(valid, `"repository_id":"sibling"`, `"repository_id":"sibling","repository_id":"duplicate"`, 1),
		strings.Replace(valid, `"dry_run":true`, `"dry_run":true,"unknown":1`, 1),
		strings.Replace(valid, `"dry_run":true`, `"dry_run":NaN`, 1),
		valid + `{}`,
		string([]byte{0xff}),
	} {
		if _, err := DecodeScanInput([]byte(invalid)); err == nil || ScanFailure(err) != ScanFailureContract {
			t.Fatalf("invalid strict input accepted: %q", invalid)
		}
	}
	for _, invalidPath := range []string{"", "/absolute", "../escape", "a/../b", "a//b", `a\b`, "C:/drive", "e\u0301.txt", "a/"} {
		if err := validatePOSIXPath(invalidPath); err == nil {
			t.Fatalf("invalid path accepted: %q", invalidPath)
		}
	}
	for _, validPath := range []string{"a", "dir/file.go", "unicode/é.txt"} {
		if err := validatePOSIXPath(validPath); err != nil {
			t.Fatalf("valid path rejected: %q: %v", validPath, err)
		}
	}
}

func TestProductionScannerRejectsDuplicateRepositoriesAndNonCanonicalGitPath(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	duplicateID := input
	duplicateID.Siblings = append(duplicateID.Siblings, input.Siblings[0])
	if _, err := NewScanner().Scan(context.Background(), input.GroupID, duplicateID); err == nil || ScanFailure(err) != ScanFailureContract {
		t.Fatal("duplicate repository registration was accepted")
	}
	duplicateRoot := input
	duplicateRoot.Siblings = append(duplicateRoot.Siblings, SiblingRepositoryInput{
		SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: input.Siblings[0].LocalPath,
		RepositoryID: "different-registration", RepositoryName: "different", RepositoryRevision: input.Siblings[0].RepositoryRevision,
	})
	if _, err := NewScanner().Scan(context.Background(), input.GroupID, duplicateRoot); err == nil || ScanFailure(err) != ScanFailureContract {
		t.Fatal("duplicate physical repository root was accepted")
	}
	changedAsSibling := input
	changedAsSibling.Siblings = append(changedAsSibling.Siblings, SiblingRepositoryInput{
		SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: input.Changed.LocalPath,
		RepositoryID: "changed-alias", RepositoryName: "changed-alias", RepositoryRevision: input.HeadRevision,
	})
	if _, err := NewScanner().Scan(context.Background(), input.GroupID, changedAsSibling); err == nil || ScanFailure(err) != ScanFailureContract {
		t.Fatal("changed repository physical root entered siblings")
	}

	unicodeRoot := initScannerRepo(t)
	gitTest(t, unicodeRoot, "config", "core.precomposeunicode", "false")
	decomposed := "e\u0301.txt"
	writeScannerFile(t, filepath.Join(unicodeRoot, decomposed), []byte("alias\n"), 0o644)
	gitTest(t, unicodeRoot, "add", "--", decomposed)
	gitTest(t, unicodeRoot, "commit", "-m", "decomposed path")
	unicodeRevision := strings.TrimSpace(gitTest(t, unicodeRoot, "rev-parse", "HEAD"))
	input.Siblings[0] = SiblingRepositoryInput{
		SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: unicodeRoot,
		RepositoryID: "unicode-alias", RepositoryName: "unicode-alias", RepositoryRevision: unicodeRevision,
	}
	if _, err := NewScanner().Scan(context.Background(), input.GroupID, input); err == nil || ScanFailure(err) != ScanFailureContract {
		t.Fatal("non-NFC Git path was accepted")
	}
}

func TestProductionScannerRejectsInvalidAndDriftingRevisions(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	missing := input
	missing.Siblings[0].RepositoryRevision = strings.Repeat("f", 40)
	if _, err := NewScanner().Scan(context.Background(), input.GroupID, missing); err == nil || ScanFailure(err) != ScanFailureRepository {
		t.Fatal("missing revision was accepted")
	}
	symbolic := input
	symbolic.HeadRevision = "HEAD"
	if _, err := NewScanner().Scan(context.Background(), input.GroupID, symbolic); err == nil || ScanFailure(err) != ScanFailureContract {
		t.Fatal("symbolic revision was accepted")
	}
	blobID := strings.TrimSpace(gitTest(t, toy.siblingRoot, "rev-parse", toy.sibling+":regular.txt"))
	nonCommit := input
	nonCommit.Siblings[0].RepositoryRevision = blobID
	if _, err := NewScanner().Scan(context.Background(), input.GroupID, nonCommit); err == nil || ScanFailure(err) != ScanFailureRepository {
		t.Fatal("blob revision was accepted as a commit")
	}

	driftingToy := newScannerToy(t)
	driftingInput := driftingToy.input()
	objectPath := filepath.Join(driftingToy.siblingRoot, ".git", "objects", driftingToy.sibling[:2], driftingToy.sibling[2:])
	scanner := NewScanner()
	scanner.beforeFinalVerify = func() { _ = os.Remove(objectPath) }
	if _, err := scanner.Scan(context.Background(), driftingInput.GroupID, driftingInput); err == nil || ScanFailure(err) != ScanFailureRepository || !strings.Contains(err.Error(), "changed or became unavailable") {
		t.Fatalf("revision drift was not detected: %v", err)
	}
}

func TestProductionScannerFrozenJCSAndLimitBoundaries(t *testing.T) {
	canonical, err := canonicalJSONBytes(map[string]any{"z": 1, "é": "line\n", "a": []any{true, nil, 1e-7}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(canonical), `{"a":[true,null,1e-7],"z":1,"é":"line\n"}`; got != want {
		t.Fatalf("RFC 8785 bytes = %q, want %q", got, want)
	}

	records := make([]FileRecord, 6)
	blobs := make(map[string][]byte)
	for index := range records {
		blob := bytes.Repeat([]byte{byte('a' + index)}, MaxFileBytes)
		hash := testSHA256(blob)
		path := string(rune('a'+index)) + ".txt"
		records[index] = FileRecord{Ordinal: index + 1, RepositoryID: "repo", RelativePath: path, ByteSize: MaxFileBytes, ContentSHA256: &hash, ContentRequired: true}
		blobs["repo\x00"+path] = blob
	}
	parts, descriptors, err := buildContentParts(records, blobs, "group", "bsnap_"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || len(parts[0].Items) != 5 || parts[0].DecodedContentByte != MaxPartBytes || len(parts[1].Items) != 1 || len(descriptors) != 2 {
		t.Fatalf("greedy boundary parts = %#v", parts)
	}
	canonicalItems, _ := canonicalJSONBytes(parts[0].Items)
	if descriptors[0].PartSHA256 != testSHA256(canonicalItems) {
		t.Fatal("part hash is not exact RFC 8785 content_items hash")
	}
	emptyRecords := make([]FileRecord, MaxPartItems+1)
	emptyBlobs := make(map[string][]byte, len(emptyRecords))
	emptyHash := testSHA256(nil)
	for index := range emptyRecords {
		path := "empty-" + strconvItoa(index)
		emptyRecords[index] = FileRecord{Ordinal: index + 1, RepositoryID: "repo", RelativePath: path, ContentSHA256: &emptyHash, ContentRequired: true}
		emptyBlobs["repo\x00"+path] = []byte{}
	}
	emptyParts, _, err := buildContentParts(emptyRecords, emptyBlobs, "group", "bsnap_"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyParts) != 2 || len(emptyParts[0].Items) != MaxPartItems || len(emptyParts[1].Items) != 1 {
		t.Fatalf("item-count boundary parts = %#v", emptyParts)
	}

	totalLimitRecords := make([]FileRecord, MaxSupportedBytes/MaxFileBytes+1)
	for index := range totalLimitRecords {
		totalLimitRecords[index] = FileRecord{ByteSize: MaxFileBytes, ContentRequired: true}
	}
	input := ScanInput{GroupID: "group"}
	if _, err := buildScanResult(input, nil, totalLimitRecords, nil, []byte("diff")); err == nil || ScanFailure(err) != ScanFailureContract {
		t.Fatal("supported-content total limit was not enforced before content access")
	}
	if _, _, err := validateSnapshotAggregate(make([]FileRecord, MaxFileRecords+1)); err == nil || ScanFailure(err) != ScanFailureContract {
		t.Fatal("file-record count limit was not enforced")
	}
	tooManyRepositories := newScannerToy(t).input()
	tooManyRepositories.Siblings = make([]SiblingRepositoryInput, MaxSiblingRepositories+1)
	for index := range tooManyRepositories.Siblings {
		tooManyRepositories.Siblings[index] = SiblingRepositoryInput{
			SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: "/unused",
			RepositoryID: "repo-" + strconvItoa(index), RepositoryName: "repo-" + strconvItoa(index), RepositoryRevision: strings.Repeat("a", 40),
		}
	}
	if err := validateScanInput("toy-group", tooManyRepositories); err == nil || ScanFailure(err) != ScanFailureContract {
		t.Fatal("sibling repository limit was not enforced")
	}
}

func TestProductionScannerV1ArtifactsMatchCorePins(t *testing.T) {
	artifacts := []struct {
		relative string
		digest   string
	}{
		{relative: "baseline-control-plane.v1.md", digest: pinnedSpecSHA256},
		{relative: "baseline-control-plane.v1.schema.json", digest: pinnedSchemaSHA256},
		{relative: "fixtures/baseline-control-plane.v1.valid.json", digest: pinnedFixtureSHA256},
		{relative: "fixtures/baseline-scanner-inputs.v1.valid.json", digest: pinnedScannerFixtureSHA256},
		{relative: "baseline-scan-dry-run.v1.md", digest: "080633b7af37a7dfed4998527a1e7d1877bee364385e55c9027a53cd81e66ca4"},
		{relative: "baseline-scan-dry-run.v1.schema.json", digest: "9dc19feca68ee5aa655a397b7001c1d675592d6f146049c7469ebe6befe636fd"},
		{relative: "fixtures/baseline-scan-dry-run.v1.valid.json", digest: "35ef126001808d4b6e9ebb1072dd6e9b12772775bb35f867441876221b7719f4"},
		{relative: "fixtures/baseline-scan-dry-run.v1.invalid.json", digest: "cf1e52d90d552f0b91d737ea38556ab439962733166476c31600888d497ce683"},
	}
	for _, artifact := range artifacts {
		cliPath := filepath.Join("..", "..", "protocol", filepath.FromSlash(artifact.relative))
		cliBytes, err := os.ReadFile(cliPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := testSHA256(cliBytes); got != artifact.digest {
			t.Fatalf("%s SHA-256 = %s, want %s", artifact.relative, got, artifact.digest)
		}
	}
}

func TestRawGitDiffV1MatchesVendoredComparatorBytes(t *testing.T) {
	for _, objectFormat := range []string{"sha1", "sha256"} {
		t.Run(objectFormat, func(t *testing.T) {
			repository := initScannerRepoWithObjectFormat(t, objectFormat)
			writeScannerFile(t, filepath.Join(repository, "ordinary.txt"), []byte("before\n"), 0o644)
			writeScannerFile(t, filepath.Join(repository, "deleted.txt"), []byte("deleted\n"), 0o644)
			writeScannerFile(t, filepath.Join(repository, "renamed.txt"), []byte("rename candidate\n"), 0o644)
			writeScannerFile(t, filepath.Join(repository, "copied.txt"), []byte("copy candidate\n"), 0o644)
			writeScannerFile(t, filepath.Join(repository, "mode.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o644)
			writeScannerFile(t, filepath.Join(repository, "binary.bin"), []byte{0, 1, 2, 3, 0}, 0o644)
			gitTest(t, repository, "add", "--all")
			gitTest(t, repository, "commit", "-m", "base")
			base := strings.TrimSpace(gitTest(t, repository, "rev-parse", "HEAD"))

			writeScannerFile(t, filepath.Join(repository, "ordinary.txt"), []byte("after\n"), 0o644)
			if err := os.Remove(filepath.Join(repository, "deleted.txt")); err != nil {
				t.Fatal(err)
			}
			writeScannerFile(t, filepath.Join(repository, "added.txt"), []byte("added\n"), 0o644)
			gitTest(t, repository, "mv", "renamed.txt", "renamed-new.txt")
			copyValue, err := os.ReadFile(filepath.Join(repository, "copied.txt"))
			if err != nil {
				t.Fatal(err)
			}
			writeScannerFile(t, filepath.Join(repository, "copied-new.txt"), copyValue, 0o644)
			if err := os.Chmod(filepath.Join(repository, "mode.sh"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeScannerFile(t, filepath.Join(repository, "binary.bin"), []byte{0, 4, 5, 6, 0}, 0o644)
			gitTest(t, repository, "add", "--all")
			gitTest(t, repository, "commit", "-m", "head")
			head := strings.TrimSpace(gitTest(t, repository, "rev-parse", "HEAD"))

			vendor := []byte(gitTestWithEnv(t, repository, []string{"LC_ALL=C", "LANG=C"}, "diff", "HEAD^", "HEAD", "--no-ext-diff"))
			actual, err := NewScanner().rawDiff(context.Background(), repository, base, head)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, vendor) {
				t.Fatalf("raw_git_diff_v1 does not match vendor bytes: vendor=%s actual=%s", testSHA256(vendor), testSHA256(actual))
			}
			for _, expected := range []string{"ordinary.txt", "deleted.txt", "added.txt", "renamed-new.txt", "copied-new.txt", "old mode 100644", "new mode 100755", "Binary files"} {
				if !bytes.Contains(actual, []byte(expected)) {
					t.Fatalf("parity fixture did not exercise %q", expected)
				}
			}
		})
	}
}

func TestRawGitDiffV1IgnoresDirtyWorktreeAndMutableGitConfiguration(t *testing.T) {
	repository := initScannerRepo(t)
	writeScannerFile(t, filepath.Join(repository, "source.txt"), []byte("before\n"), 0o644)
	gitTest(t, repository, "add", "source.txt")
	gitTest(t, repository, "commit", "-m", "base")
	base := strings.TrimSpace(gitTest(t, repository, "rev-parse", "HEAD"))
	gitTest(t, repository, "mv", "source.txt", "moved.txt")
	gitTest(t, repository, "commit", "-m", "head")
	head := strings.TrimSpace(gitTest(t, repository, "rev-parse", "HEAD"))

	vendor := []byte(gitTestWithEnv(t, repository, []string{"LC_ALL=C", "LANG=C"}, "diff", "HEAD^", "HEAD", "--no-ext-diff"))
	gitTest(t, repository, "config", "diff.renames", "false")
	gitTest(t, repository, "config", "core.abbrev", "12")
	gitTest(t, repository, "config", "diff.noprefix", "true")
	writeScannerFile(t, filepath.Join(repository, "moved.txt"), []byte("dirty worktree must not enter query\n"), 0o644)

	actual, err := NewScanner().rawDiff(context.Background(), repository, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, vendor) {
		t.Fatalf("hardened immutable query drifted: vendor=%s actual=%s", testSHA256(vendor), testSHA256(actual))
	}
	if bytes.Contains(actual, []byte("dirty worktree")) {
		t.Fatal("dirty worktree content entered retrieval query")
	}
}

func newScannerToy(t *testing.T) scannerToy {
	t.Helper()
	changed := initScannerRepo(t)
	writeScannerFile(t, filepath.Join(changed, "changed.txt"), []byte("before\n"), 0o644)
	gitTest(t, changed, "add", "--", "changed.txt")
	gitTest(t, changed, "commit", "-m", "base")
	base := strings.TrimSpace(gitTest(t, changed, "rev-parse", "HEAD"))
	writeScannerFile(t, filepath.Join(changed, "changed.txt"), []byte("after\n"), 0o644)
	writeScannerFile(t, filepath.Join(changed, "new.txt"), []byte("new\n"), 0o644)
	gitTest(t, changed, "add", "--", "changed.txt", "new.txt")
	gitTest(t, changed, "commit", "-m", "head")
	head := strings.TrimSpace(gitTest(t, changed, "rev-parse", "HEAD"))
	diff := []byte(gitTestWithEnv(t, changed, []string{"LC_ALL=C"}, "diff", base, head, "--no-ext-diff"))

	sibling := initScannerRepo(t)
	writeScannerFile(t, filepath.Join(sibling, "regular.txt"), []byte("alpha\n"), 0o644)
	writeScannerFile(t, filepath.Join(sibling, "empty.txt"), []byte{}, 0o644)
	writeScannerFile(t, filepath.Join(sibling, "script.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	writeScannerFile(t, filepath.Join(sibling, "unicode", "é.txt"), []byte("café\n"), 0o644)
	writeScannerFile(t, filepath.Join(sibling, "bad.bin"), []byte{0xff, 0xfe}, 0o644)
	writeScannerFile(t, filepath.Join(sibling, "large.bin"), bytes.Repeat([]byte("L"), MaxFileBytes+1), 0o644)
	writeScannerFile(t, filepath.Join(sibling, ".compair", "private.txt"), []byte("private marker\n"), 0o644)
	writeScannerFile(t, filepath.Join(sibling, "node_modules", "dependency.txt"), []byte("dependency marker\n"), 0o644)
	if runtime.GOOS == "windows" {
		t.Skip("Git symlink object integration requires symlink support")
	}
	if err := os.Symlink("regular.txt", filepath.Join(sibling, "inside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside-secret", filepath.Join(sibling, "escape-link")); err != nil {
		t.Fatal(err)
	}
	gitTest(t, sibling, "add", "--all")
	gitTest(t, sibling, "update-index", "--chmod=+x", "script.sh")
	gitTest(t, sibling, "commit", "-m", "files")
	parent := strings.TrimSpace(gitTest(t, sibling, "rev-parse", "HEAD"))
	gitTest(t, sibling, "update-index", "--add", "--cacheinfo", "160000,"+parent+",vendor/module")
	gitTest(t, sibling, "commit", "-m", "gitlink")
	siblingRevision := strings.TrimSpace(gitTest(t, sibling, "rev-parse", "HEAD"))
	return scannerToy{changedRoot: changed, siblingRoot: sibling, base: base, head: head, sibling: siblingRevision, diff: diff}
}

func (toy scannerToy) input() ScanInput {
	return ScanInput{
		Changed: ChangedRepositoryInput{
			SchemaVersion: "baseline-changed-repository-input.v1", LocalPath: toy.changedRoot,
			RepositoryID: "repo-changed-toy", RepositoryName: "changed-toy", RepositoryRevision: toy.head,
			SourceDocumentID: "11111111-1111-4111-8111-111111111111",
		},
		Siblings: []SiblingRepositoryInput{{
			SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: toy.siblingRoot,
			RepositoryID: "repo-sibling-toy", RepositoryName: "sibling-toy", RepositoryRevision: toy.sibling,
		}},
		BaseRevision: toy.base, HeadRevision: toy.head, GroupID: "toy-group", DryRun: true, JSON: true,
	}
}

func initScannerRepo(t *testing.T) string {
	t.Helper()
	return initScannerRepoWithObjectFormat(t, "sha1")
}

func initScannerRepoWithObjectFormat(t *testing.T, objectFormat string) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init", "--quiet", "--object-format="+objectFormat)
	if output, err := cmd.CombinedOutput(); err != nil {
		if objectFormat != "sha1" {
			t.Skipf("Git object format %s unavailable: %v", objectFormat, err)
		}
		t.Fatalf("toy Git init failed: %v: %s", err, output)
	}
	gitTest(t, root, "config", "user.email", "scanner@example.invalid")
	gitTest(t, root, "config", "user.name", "Scanner Test")
	gitTest(t, root, "config", "core.autocrlf", "false")
	gitTest(t, root, "config", "core.symlinks", "true")
	return root
}

func writeScannerFile(t *testing.T, filename string, value []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, value, mode); err != nil {
		t.Fatal(err)
	}
}

func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	return gitTestWithEnv(t, root, nil, args...)
}

func gitTestWithEnv(t *testing.T, root string, environment []string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Env = append(os.Environ(), environment...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("toy Git command failed: %v: %s", err, output)
	}
	return string(output)
}

func assertFileState(t *testing.T, records map[string]FileRecord, relativePath, state, reason, mode string, raw []byte) {
	t.Helper()
	record, ok := records[relativePath]
	if !ok {
		t.Fatalf("missing file record %q", relativePath)
	}
	if record.FileState != state || dereference(record.SkipReason) != reason || record.GitMode != mode || record.ByteSize != int64(len(raw)) || record.ContentSHA256 == nil || *record.ContentSHA256 != testSHA256(raw) {
		t.Fatalf("record %q = %#v", relativePath, record)
	}
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func encodeReportForTest(t *testing.T, report DryRunReport) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := EncodeDryRunReport(&output, report); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("report contains multiple JSON values: %q", output.Bytes())
	}
	return output.Bytes()
}

func testSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func strconvItoa(value int) string {
	// The scanner contracts need only non-negative decimal indices here.
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
