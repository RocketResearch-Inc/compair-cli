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

const (
	commandIndexGroup        = "group-command-index"
	commandIndexSnapshot     = "bsnap_1111111111111111111111111111111111111111111111111111111111111111"
	commandIndexStaging      = "20000000-0000-4000-8000-000000000001"
	commandIndexContinuation = "30000000-0000-4000-8000-000000000001"
	commandIndexCorpus       = "40000000-0000-4000-8000-000000000001"
	commandIndexGeneration   = "50000000-0000-4000-8000-000000000001"
	commandIndexJob          = "60000000-0000-4000-8000-000000000001"
	commandIndexPublication  = "70000000-0000-4000-8000-000000000001"
	commandIndexHashA        = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commandIndexHashB        = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	commandIndexHashC        = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	commandIndexHashD        = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	commandIndexHashE        = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	commandIndexHashF        = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

func commandIndexIntent() map[string]any {
	return map[string]any{
		"index_format_version": "baseline-index.v1", "tokenizer_version": "baseline_v1_frozen_tokenizer.v1",
		"retrieval_config_fingerprint": commandIndexHashC,
		"embedding": map[string]any{
			"contract_version": "baseline-embedding-http.v1", "provider": "baseline_http_v1",
			"model": "BAAI/bge-small-en-v1.5", "revision": "52398278842ec682c6f32300af41344b1c0b0bb2",
			"dimension": 384, "dtype": "float32", "fingerprint": commandIndexHashE,
		},
	}
}

func commandIndexCapabilities(requestID string) map[string]any {
	return map[string]any{
		"protocol_version": baseline.IndexControlProtocolVersion, "protocol_sha256": baseline.IndexControlProtocolSHA256,
		"message_type": "capabilities", "request_id": requestID, "group_id": commandIndexGroup,
		"supported_protocols": []any{
			map[string]any{"version": baseline.ControlProtocolVersion, "sha256": baseline.ControlProtocolSHA256, "role": "staging_only"},
			map[string]any{"version": baseline.IndexControlProtocolVersion, "sha256": baseline.IndexControlProtocolSHA256, "role": "index_and_run_submission"},
		},
		"operations": map[string]any{
			"index_build":  map[string]any{"submission": "safe", "endpoint": "authenticated_post", "dispatch": "automatic", "readiness": "ready", "reason_code": nil},
			"baseline_run": map[string]any{"submission": "unavailable", "endpoint": "unavailable", "dispatch": "unavailable", "readiness": "unavailable", "reason_code": "capability_unavailable"},
		},
		"limits": map[string]any{
			"control_request_bytes": 64000, "run_request_bytes": 8100000, "raw_query_bytes": 8000000,
			"idempotency_key_min_characters": 32, "idempotency_key_max_characters": 128,
			"selected_evidence_items": 4, "selected_evidence_characters": 16000,
			"feedback_items": 4, "terminal_status_retention_days": 30,
		},
		"required_index_identity": commandIndexIntent(),
		"transport":               map[string]any{"remote": "verified_https_required", "loopback_http": "explicit_actual_peer_exception", "json_media_type": "application/json", "encoding": "utf-8"},
	}
}

func commandContinuationStatus(requestID string) map[string]any {
	now := "2026-08-17T12:00:00Z"
	return map[string]any{
		"schema_version": "baseline-snapshot-continuation.v1", "message_type": "continuation_job_status",
		"request_id": requestID, "group_id": commandIndexGroup, "staging_job_id": commandIndexStaging,
		"job_id": commandIndexContinuation, "operation": "sealed_snapshot_continue", "state": "succeeded",
		"attempt": 1, "created_at": now, "updated_at": now, "progress": map[string]any{"completed": 1, "total": 1},
		"result": map[string]any{
			"snapshot_id": commandIndexSnapshot, "staging_state": "sealed", "corpus_ingestion_complete": true,
			"corpus_eligible": true, "index_eligible": false, "baseline_eligible": false, "index_state": "incomplete",
			"corpus_id": commandIndexCorpus, "corpus_generation_id": commandIndexGeneration,
			"corpus_generation_version": "generation-v1", "corpus_manifest_hash": commandIndexHashD,
			"corpus_provenance_fingerprint": commandIndexHashB, "worker_contract_version": "baseline-continuation-worker.v1",
		},
		"error_code": nil,
		"staging":    map[string]any{"state": "sealed", "received_parts": 1, "expected_parts": 1, "expires_at": now, "corpus_eligible": false, "index_eligible": false},
		"continuation": map[string]any{
			"job_id": commandIndexContinuation, "operation": "sealed_snapshot_continue", "state": "succeeded", "attempt": 1,
			"created_at": now, "updated_at": now, "error_code": nil, "corpus_ingestion_complete": true,
			"corpus_eligible": true, "index_eligible": false, "baseline_eligible": false,
		},
		"replayed": false,
	}
}

func commandIndexStatus(requestID string) map[string]any {
	now := "2026-08-17T12:01:00Z"
	return map[string]any{
		"protocol_version": baseline.IndexControlProtocolVersion, "protocol_sha256": baseline.IndexControlProtocolSHA256,
		"message_type": "job_status", "request_id": requestID, "group_id": commandIndexGroup,
		"job_id": commandIndexJob, "operation": "index_build", "state": "succeeded", "terminal": true,
		"exit_classification": "success", "attempt": 1, "created_at": now, "updated_at": now,
		"ingestion_continuation_id": commandIndexContinuation, "corpus_generation_id": commandIndexGeneration,
		"corpus_manifest_hash": commandIndexHashD, "index_intent": commandIndexIntent(),
		"progress": map[string]any{"document_count": 2, "vector_count": 2},
		"result": map[string]any{
			"index_publication_id": commandIndexPublication, "corpus_generation_id": commandIndexGeneration,
			"corpus_manifest_hash": commandIndexHashD, "index_fingerprint": commandIndexHashF,
			"retrieval_config_fingerprint": commandIndexHashC, "embedding_fingerprint": commandIndexHashE,
			"document_count": 2, "vector_count": 2,
		},
		"reason_code": nil, "replayed": false,
	}
}

func writeCommandIndexUpload(t *testing.T, directory string) string {
	t.Helper()
	result := baseline.UploadResult{
		SchemaVersion: baseline.UploadResultSchemaVersion, ProtocolVersion: baseline.ControlProtocolVersion,
		ProtocolSHA256: baseline.ControlProtocolSHA256, GroupID: commandIndexGroup, ScanFingerprint: commandIndexHashA,
		SnapshotID: commandIndexSnapshot, StagingJobID: commandIndexStaging, ContinuationJobID: commandIndexContinuation,
		CanonicalManifestHash: commandIndexHashB, ContentManifestHash: commandIndexHashC,
		PartTotal: 1, PartCompleted: 1, TransmittedRequestBytes: 100, TransmittedRequestCount: 4,
		State: "succeeded", CorpusID: commandIndexCorpus, CorpusGenerationID: commandIndexGeneration,
		CorpusGenerationVersion: "generation-v1", StartedAt: "2026-08-17T11:00:00Z", UpdatedAt: "2026-08-17T12:00:00Z",
	}
	filename := filepath.Join(directory, "upload-result.json")
	value, _ := json.Marshal(result)
	if err := os.WriteFile(filename, value, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestBaselineIndexCommandEmitsExactlyOneSafeJSONValue(t *testing.T) {
	installCommandCredential(t, "index-command-token")
	uploadResult := writeCommandIndexUpload(t, t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "Bearer index-command-token" {
			t.Fatalf("unsafe or unauthenticated request: %s %s", request.Method, request.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		requestID := payload["request_id"].(string)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/baseline/control/v2/capabilities":
			writeCommandUploadJSON(t, writer, commandIndexCapabilities(requestID))
		case "/baseline/control/v1/continuations/status":
			writeCommandUploadJSON(t, writer, commandContinuationStatus(requestID))
		case "/baseline/control/v2/index-builds":
			writeCommandUploadJSON(t, writer, map[string]any{
				"protocol_version": baseline.IndexControlProtocolVersion, "protocol_sha256": baseline.IndexControlProtocolSHA256,
				"message_type": "job_accepted", "request_id": requestID, "group_id": commandIndexGroup,
				"job_id": commandIndexJob, "operation": "index_build", "state": "queued", "replayed": false, "processing_run_id": nil,
			})
		case "/baseline/control/v2/index-builds/status":
			writeCommandUploadJSON(t, writer, commandIndexStatus(requestID))
		default:
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr, err := executeBaselineForTest(t, server.URL, "baseline", "index", "--group", commandIndexGroup, "--upload-result", uploadResult, "--wait", "--poll-interval", "1ms", "--allow-loopback-http", "--json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	var result baseline.IndexResult
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout is not one JSON value: %q", stdout)
	}
	if result.State != "succeeded" || result.CompatiblePublicationID == nil || *result.CompatiblePublicationID != commandIndexPublication || result.DispatchMode != "automatic" {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(stderr, "baseline index: state=succeeded") {
		t.Fatalf("safe progress missing: %q", stderr)
	}
	for _, forbidden := range []string{"index-command-token", server.URL, "idempotency_key", "lease_token", "raw_diff", "repository_path"} {
		if strings.Contains(stdout+stderr, forbidden) {
			t.Fatalf("command output leaked %q", forbidden)
		}
	}
}

func TestBaselineIndexCommandUsageExitClass(t *testing.T) {
	stdout, _, err := executeBaselineForTest(t, "https://core.example.test", "baseline", "index", "--upload-result", "missing.json")
	if err == nil || exitCodeForError(err) != baselineIndexUsageExitCode || stdout != "" {
		t.Fatalf("usage result = %q, %v, %d", stdout, err, exitCodeForError(err))
	}
	if baselineIndexSuccessExitCode != 0 {
		t.Fatal("success exit code changed")
	}
}
