package compair

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/baseline"
	"github.com/spf13/cobra"
)

func TestBaselineScanCommandEmitsOneSanitizedDryRunValue(t *testing.T) {
	input, planPath, protected := commandScanFixture(t)
	stdout, stderr, err := executeBaselineForTest(
		t, "http://127.0.0.1:1", "baseline", "scan", "--dry-run",
		"--group", input.GroupID, "--plan", planPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	var report baseline.DryRunReport
	if err := decoder.Decode(&report); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout is not exactly one JSON value: %q", stdout)
	}
	if report.SchemaVersion != baseline.DryRunSchemaVersion || report.ProtocolVersion != baseline.ControlProtocolVersion ||
		report.ProtocolSHA256 != baseline.ControlProtocolSHA256 || report.GroupID != input.GroupID ||
		report.Counts.FileCount != 2 || report.Counts.SupportedFileCount != 2 || len(report.Parts) != 1 {
		t.Fatalf("report = %#v", report)
	}
	if report.DeterministicFingerprint == "" || report.RawDiff.SHA256 == "" || report.RawDiff.ByteSize == 0 {
		t.Fatalf("safe fingerprints missing: %#v", report)
	}
	for _, forbidden := range protected {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("stdout leaked protected scanner input/content %q", forbidden)
		}
	}
}

func TestBaselineScanCommandRequiresExplicitDryRunInputsAndSafeErrors(t *testing.T) {
	input, planPath, protected := commandScanFixture(t)
	t.Setenv("COMPAIR_ACTIVE_GROUP", input.GroupID)
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "missing dry run", args: []string{"baseline", "scan", "--group", input.GroupID, "--plan", planPath}, wantCode: baselineScanUsageExitCode},
		{name: "missing group ignores active", args: []string{"baseline", "scan", "--dry-run", "--plan", planPath}, wantCode: baselineScanUsageExitCode},
		{name: "missing plan", args: []string{"baseline", "scan", "--dry-run", "--group", input.GroupID}, wantCode: baselineScanUsageExitCode},
		{name: "positional", args: []string{"baseline", "scan", "private-positional", "--dry-run", "--group", input.GroupID, "--plan", planPath}, wantCode: baselineScanUsageExitCode},
		{name: "unknown flag", args: []string{"baseline", "scan", "--private-root", protected[0], "--dry-run", "--group", input.GroupID, "--plan", planPath}, wantCode: baselineScanUsageExitCode},
		{name: "group mismatch", args: []string{"baseline", "scan", "--dry-run", "--group", "other-group", "--plan", planPath}, wantCode: baselineScanContractExitCode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := executeBaselineForTest(t, "http://127.0.0.1:1", test.args...)
			if err == nil || exitCodeForError(err) != test.wantCode {
				t.Fatalf("error = %v, code = %d", err, exitCodeForError(err))
			}
			if stdout != "" {
				t.Fatalf("failure wrote stdout: %q", stdout)
			}
			for _, forbidden := range protected {
				if strings.Contains(stderr+err.Error(), forbidden) {
					t.Fatalf("diagnostic leaked protected scanner value %q: %q %v", forbidden, stderr, err)
				}
			}
		})
	}
}

func TestBaselineScanCommandExitClassesAndNoLegacyCommandRegression(t *testing.T) {
	input, planPath, _ := commandScanFixture(t)
	missing := input
	missing.Siblings[0].RepositoryRevision = strings.Repeat("f", 40)
	missingPath := filepath.Join(t.TempDir(), "missing.json")
	writeCommandScanPlan(t, missingPath, missing)
	stdout, _, err := executeBaselineForTest(
		t, "http://127.0.0.1:1", "baseline", "scan", "--dry-run",
		"--group", input.GroupID, "--plan", missingPath,
	)
	if err == nil || exitCodeForError(err) != baselineScanRepositoryExitCode || stdout != "" {
		t.Fatalf("repository failure = %v, code %d, stdout %q", err, exitCodeForError(err), stdout)
	}

	malformedPath := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformedPath, []byte(`{"group_id":"private","group_id":"duplicate"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = executeBaselineForTest(
		t, "http://127.0.0.1:1", "baseline", "scan", "--dry-run",
		"--group", input.GroupID, "--plan", malformedPath,
	)
	if err == nil || exitCodeForError(err) != baselineScanContractExitCode || stdout != "" {
		t.Fatalf("contract failure = %v, code %d, stdout %q", err, exitCodeForError(err), stdout)
	}
	oversizedPath := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(oversizedPath, bytes.Repeat([]byte{'x'}, baseline.MaxControlRequest+1), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = executeBaselineForTest(
		t, "http://127.0.0.1:1", "baseline", "scan", "--dry-run",
		"--group", input.GroupID, "--plan", oversizedPath,
	)
	if err == nil || exitCodeForError(err) != baselineScanContractExitCode || stdout != "" {
		t.Fatalf("oversized plan failure = %v, code %d, stdout %q", err, exitCodeForError(err), stdout)
	}

	commandRoot := &cobra.Command{Use: "compair"}
	commandRoot.AddCommand(newBaselineCommand())
	scanCommand, _, findErr := commandRoot.Find([]string{"baseline", "scan"})
	if findErr != nil || scanCommand == nil || !shouldSkipTelemetry(scanCommand) {
		t.Fatalf("baseline scan telemetry policy = command %#v error %v", scanCommand, findErr)
	}
	for _, path := range [][]string{{"baseline", "preview"}, {"notifications"}, {"notifications", "ack"}, {"notifications", "dismiss"}, {"notifications", "share"}} {
		if command, _, findErr := rootCmd.Find(path); findErr != nil || command == nil {
			t.Fatalf("existing command %v missing: %v", path, findErr)
		}
	}

	// The original valid plan remains usable after the failure checks.
	stdout, _, err = executeBaselineForTest(
		t, "http://127.0.0.1:1", "baseline", "scan", "--dry-run",
		"--group", input.GroupID, "--plan", planPath,
	)
	if err != nil || strings.TrimSpace(stdout) == "" {
		t.Fatalf("valid scan after failures = %v, stdout %q", err, stdout)
	}
}

func commandScanFixture(t *testing.T) (baseline.ScanInput, string, []string) {
	t.Helper()
	changed := initCommandScanRepo(t)
	writeCommandScanFile(t, filepath.Join(changed, "change.txt"), "before\n")
	commandScanGit(t, changed, "add", "--", "change.txt")
	commandScanGit(t, changed, "commit", "-m", "base")
	base := strings.TrimSpace(commandScanGit(t, changed, "rev-parse", "HEAD"))
	writeCommandScanFile(t, filepath.Join(changed, "change.txt"), "after\n")
	commandScanGit(t, changed, "add", "--", "change.txt")
	commandScanGit(t, changed, "commit", "-m", "head")
	head := strings.TrimSpace(commandScanGit(t, changed, "rev-parse", "HEAD"))

	sibling := initCommandScanRepo(t)
	writeCommandScanFile(t, filepath.Join(sibling, "alpha.txt"), "alpha-protected\n")
	writeCommandScanFile(t, filepath.Join(sibling, "empty.txt"), "")
	commandScanGit(t, sibling, "add", "--all")
	commandScanGit(t, sibling, "commit", "-m", "sibling")
	siblingRevision := strings.TrimSpace(commandScanGit(t, sibling, "rev-parse", "HEAD"))

	input := baseline.ScanInput{
		Changed: baseline.ChangedRepositoryInput{
			SchemaVersion: "baseline-changed-repository-input.v1", LocalPath: changed,
			RepositoryID: "repo-command-changed", RepositoryName: "command-changed", RepositoryRevision: head,
			SourceDocumentID: "22222222-2222-4222-8222-222222222222",
		},
		Siblings: []baseline.SiblingRepositoryInput{{
			SchemaVersion: "baseline-sibling-repository-input.v1", LocalPath: sibling,
			RepositoryID: "repo-command-sibling", RepositoryName: "command-sibling", RepositoryRevision: siblingRevision,
		}},
		BaseRevision: base, HeadRevision: head, GroupID: "group-command", DryRun: true, JSON: true,
	}
	planPath := filepath.Join(t.TempDir(), "scan-input.json")
	writeCommandScanPlan(t, planPath, input)
	return input, planPath, []string{changed, sibling, "alpha-protected", "before\\nafter", "private-positional"}
}

func initCommandScanRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	commandScanGit(t, root, "init", "--quiet")
	commandScanGit(t, root, "config", "user.email", "command@example.invalid")
	commandScanGit(t, root, "config", "user.name", "Command Test")
	commandScanGit(t, root, "config", "core.autocrlf", "false")
	return root
}

func writeCommandScanFile(t *testing.T, filename, value string) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCommandScanPlan(t *testing.T, filename string, input baseline.ScanInput) {
	t.Helper()
	value, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, value, 0o600); err != nil {
		t.Fatal(err)
	}
}

func commandScanGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", commandArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("toy Git command failed: %v: %s", err, output)
	}
	return string(output)
}
