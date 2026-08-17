package compair

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/baseline"
)

func TestBaselineUploadCommandEmitsExactlyOneSafeJSONValue(t *testing.T) {
	input, planPath, protected := commandScanFixture(t)
	installCommandCredential(t, "command-token")
	jobID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	receivedParts := 0
	expectedParts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.RawQuery != "" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer command-token" || request.Header.Get("auth-token") != "command-token" {
			t.Fatal("upload did not reuse CLI authentication")
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		requestID, _ := payload["request_id"].(string)
		groupID, _ := payload["group_id"].(string)
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/baseline/control/v1/capabilities":
			writeCommandUploadJSON(t, writer, map[string]any{
				"protocol_version": baseline.ControlProtocolVersion, "protocol_sha256": baseline.ControlProtocolSHA256, "message_type": "capabilities", "request_id": requestID, "group_id": groupID,
				"operations":           map[string]any{"snapshot_staging": "safe", "corpus_ingestion": "unavailable", "index_build": "unavailable", "baseline_run": "unavailable"},
				"transport":            map[string]any{"status": "local_override", "reason": "explicit_loopback_http_override", "encrypted": false, "local_override_enabled": true},
				"request_body_logging": false, "staging_is_corpus_eligible": false, "staging_is_index_eligible": false,
				"limits": map[string]any{"sibling_repositories": baseline.MaxSiblingRepositories, "file_records": baseline.MaxFileRecords, "file_bytes": baseline.MaxFileBytes, "supported_content_bytes": baseline.MaxSupportedBytes, "manifest_request_bytes": baseline.MaxManifestRequest, "content_part_request_bytes": baseline.MaxPartRequest, "content_part_bytes": baseline.MaxPartBytes, "content_part_items": baseline.MaxPartItems, "content_parts": baseline.MaxContentParts, "control_request_bytes": baseline.MaxControlRequest, "staging_lifetime_seconds": 86400},
			})
		case request.URL.Path == "/baseline/control/v1/snapshots":
			snapshot := payload["snapshot"].(map[string]any)
			for _, value := range snapshot["files"].([]any) {
				if value.(map[string]any)["content_required"].(bool) {
					expectedParts = 1
				}
			}
			writeCommandUploadJSON(t, writer, map[string]any{"protocol_version": baseline.ControlProtocolVersion, "protocol_sha256": baseline.ControlProtocolSHA256, "message_type": "job_accepted", "request_id": requestID, "group_id": groupID, "job_id": jobID, "operation": "snapshot_ingest", "state": "queued", "replayed": false})
		case strings.HasSuffix(request.URL.Path, "/parts"):
			receivedParts++
			writeCommandUploadJSON(t, writer, commandUploadJobStatus(requestID, groupID, jobID, "queued", receivedParts, expectedParts, nil))
		case strings.HasSuffix(request.URL.Path, "/commit"):
			result := map[string]any{"snapshot_id": payload["snapshot_id"], "staging_state": "sealed", "corpus_eligible": false, "index_eligible": false}
			writeCommandUploadJSON(t, writer, commandUploadJobStatus(requestID, groupID, jobID, "succeeded", receivedParts, expectedParts, result))
		default:
			t.Fatalf("unexpected upload path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr, err := executeBaselineForTest(
		t, server.URL, "baseline", "upload", "--group", input.GroupID, "--plan", planPath, "--allow-loopback-http",
	)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	var result baseline.UploadResult
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout is not exactly one JSON value: %q", stdout)
	}
	if result.SchemaVersion != baseline.UploadResultSchemaVersion || result.State != "snapshot_committed" || result.GroupID != input.GroupID || result.PartCompleted != result.PartTotal || result.TransmittedRequestBytes <= 0 {
		t.Fatalf("upload result = %#v", result)
	}
	if !strings.Contains(stderr, "baseline upload: preflight") || !strings.Contains(stderr, "baseline upload: commit") {
		t.Fatalf("safe progress missing: %q", stderr)
	}
	for _, forbidden := range append(protected, "command-token", "idempotency_key", "content_utf8") {
		if strings.Contains(stdout+stderr, forbidden) {
			t.Fatalf("command output leaked %q", forbidden)
		}
	}
}

func TestBaselineUploadCommandUsageAndAuthenticationExitClasses(t *testing.T) {
	input, planPath, _ := commandScanFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	stdout, _, err := executeBaselineForTest(t, "https://core.example.test", "baseline", "upload", "--plan", planPath)
	if err == nil || exitCodeForError(err) != baselineUploadUsageExitCode || stdout != "" {
		t.Fatalf("usage result = %q, %v, %d", stdout, err, exitCodeForError(err))
	}
	stdout, _, err = executeBaselineForTest(t, "https://core.example.test", "baseline", "upload", "--group", input.GroupID, "--plan", planPath)
	if err == nil || exitCodeForError(err) != baselineUploadAuthExitCode {
		t.Fatalf("authentication result = %q, %v, %d", stdout, err, exitCodeForError(err))
	}
	var safe baseline.UploadResult
	if decodeErr := json.Unmarshal([]byte(stdout), &safe); decodeErr != nil || safe.ReasonCode != "authentication_required" {
		t.Fatalf("safe auth output = %#v, %v", safe, decodeErr)
	}
	if baselineUploadSuccessExitCode != 0 {
		t.Fatal("success exit code changed")
	}
}

func installCommandCredential(t *testing.T, token string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	directory := filepath.Join(home, ".compair")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]string{"access_token": token})
	if err := os.WriteFile(filepath.Join(directory, "credentials.json"), value, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCommandUploadJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func commandUploadJobStatus(requestID, groupID, jobID, state string, completed, total int, result any) map[string]any {
	now := "2026-01-02T03:04:05Z"
	return map[string]any{
		"protocol_version": baseline.ControlProtocolVersion, "protocol_sha256": baseline.ControlProtocolSHA256, "message_type": "job_status", "request_id": requestID, "group_id": groupID,
		"job_id": jobID, "operation": "snapshot_ingest", "state": state, "attempt": 0, "created_at": now, "updated_at": now,
		"progress": map[string]any{"completed": completed, "total": total}, "result": result, "error_code": nil,
		"staging": map[string]any{"state": map[bool]string{true: "sealed", false: "open"}[state == "succeeded"], "received_parts": completed, "expected_parts": total, "expires_at": now, "corpus_eligible": false, "index_eligible": false}, "replayed": false,
	}
}
