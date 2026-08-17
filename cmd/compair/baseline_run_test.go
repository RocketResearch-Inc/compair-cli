package compair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	commandRunGroup        = "81000000-0000-4000-8000-000000000001"
	commandRunRegistration = "81000000-0000-4000-8000-000000000002"
	commandRunJob          = "81000000-0000-4000-8000-000000000003"
	commandRunProcessing   = "81000000-0000-4000-8000-000000000004"
	commandRunPersisted    = "81000000-0000-4000-8000-000000000005"
)

func TestBaselineRunCommandEmitsOneSafeJSONAndUsesSeparatePreview(t *testing.T) {
	installCommandCredential(t, "run-command-token")
	input, planPath, protected := commandScanFixture(t)
	input.GroupID = commandRunGroup
	input.Changed.RepositoryID = commandRunRegistration
	input.Siblings[0].RepositoryID = "81000000-0000-4000-8000-000000000006"
	writeCommandScanPlan(t, planPath, input)
	scan, err := baseline.NewScanner().Scan(context.Background(), input.GroupID, input)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.ClearProtected()
	intentEncoded, err := json.Marshal(commandIndexIntent())
	if err != nil {
		t.Fatal(err)
	}
	intentDigest := sha256.Sum256(intentEncoded)
	intentFingerprint := hex.EncodeToString(intentDigest[:])
	documents := 2
	now := "2026-08-17T12:00:00Z"
	indexResult := baseline.IndexResult{
		SchemaVersion: baseline.IndexResultSchemaVersion, ProtocolVersion: baseline.IndexControlProtocolVersion,
		ProtocolSHA256: baseline.IndexControlProtocolSHA256, GroupID: commandRunGroup,
		ScanFingerprint: stringAddress(scan.Report.DeterministicFingerprint), CorpusGenerationID: stringAddress(commandIndexGeneration),
		CorpusManifestHash: stringAddress(commandIndexHashD), DispatchMode: "automatic", State: "succeeded", ExitClassification: "success",
		CompatiblePublicationID: stringAddress(commandIndexPublication), IndexFormatVersion: stringAddress("baseline-index.v1"),
		TokenizerVersion: stringAddress("baseline_v1_frozen_tokenizer.v1"), IndexIntentFingerprint: &intentFingerprint,
		RetrievalConfigFingerprint: stringAddress(commandIndexHashC), EmbeddingFingerprint: stringAddress(commandIndexHashE),
		IndexFingerprint: stringAddress(commandIndexHashF), IndexedDocumentCount: &documents, VectorCount: &documents,
		TransmittedRequestCount: 3, TransmittedRequestBytes: 512, UpdatedAt: now,
	}
	indexPath := filepath.Join(t.TempDir(), "index-result.json")
	indexFile, err := os.OpenFile(indexPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.EncodeIndexResult(indexFile, indexResult); err != nil {
		_ = indexFile.Close()
		t.Fatal(err)
	}
	if err := indexFile.Close(); err != nil {
		t.Fatal(err)
	}

	var queryText string
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "Bearer run-command-token" {
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
			capability := commandIndexCapabilities(requestID)
			capability["group_id"] = commandRunGroup
			operations := capability["operations"].(map[string]any)
			operations["index_build"] = map[string]any{"submission": "safe", "endpoint": "authenticated_post", "dispatch": "manual", "readiness": "not_ready", "reason_code": "worker_unavailable"}
			operations["baseline_run"] = map[string]any{"submission": "safe", "endpoint": "authenticated_post", "dispatch": "automatic", "readiness": "ready", "reason_code": nil}
			writeCommandUploadJSON(t, writer, capability)
		case "/baseline/control/v2/runs":
			query := payload["retrieval_query"].(map[string]any)
			queryText = query["text"].(string)
			writeCommandUploadJSON(t, writer, map[string]any{
				"protocol_version": baseline.IndexControlProtocolVersion, "protocol_sha256": baseline.IndexControlProtocolSHA256,
				"message_type": "job_accepted", "request_id": requestID, "group_id": commandRunGroup,
				"job_id": commandRunJob, "operation": "baseline_run", "state": "queued", "replayed": false,
				"processing_run_id": commandRunProcessing,
			})
		case "/baseline/control/v2/runs/status":
			statusCalls++
			queryBytes := []byte(queryText)
			queryDigest := sha256.Sum256(queryBytes)
			state := "references_persisted"
			terminal := false
			exit := "pending"
			effects := map[string]any{"evidence_count": 2, "reference_count": 2, "feedback_count": 0, "generation_invoked": false, "notification_outbox_count": 0, "persisted_run_id": commandRunPersisted}
			if statusCalls > 1 {
				state, terminal, exit = "feedback_persisted", true, "success"
				effects = map[string]any{"evidence_count": 2, "reference_count": 2, "feedback_count": 2, "generation_invoked": true, "notification_outbox_count": 1, "persisted_run_id": commandRunPersisted}
			}
			writeCommandUploadJSON(t, writer, map[string]any{
				"protocol_version": baseline.IndexControlProtocolVersion, "protocol_sha256": baseline.IndexControlProtocolSHA256,
				"message_type": "job_status", "request_id": requestID, "group_id": commandRunGroup,
				"job_id": commandRunJob, "operation": "baseline_run", "processing_run_id": commandRunProcessing,
				"source_document_id": input.Changed.SourceDocumentID, "changed_repository_registration_id": commandRunRegistration,
				"index_publication": map[string]any{
					"index_publication_id": commandIndexPublication, "corpus_generation_id": commandIndexGeneration,
					"corpus_manifest_hash": commandIndexHashD, "index_format_version": "baseline-index.v1",
					"tokenizer_version": "baseline_v1_frozen_tokenizer.v1", "retrieval_config_fingerprint": commandIndexHashC,
					"embedding_fingerprint": commandIndexHashE, "index_fingerprint": commandIndexHashF,
				},
				"state": state, "terminal": terminal, "exit_classification": exit, "attempt": statusCalls,
				"created_at": now, "updated_at": now, "retrieval_status": "ok",
				"query_provenance": map[string]any{"sha256": hex.EncodeToString(queryDigest[:]), "length": len([]rune(queryText)), "byte_size": len(queryBytes), "origin": "explicit"},
				"effects":          effects, "reason_code": nil, "failure_stage": nil, "replayed": false,
			})
		default:
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr, err := executeBaselineForTest(t, server.URL, "baseline", "run", "--group", commandRunGroup, "--plan", planPath, "--index-result", indexPath, "--wait", "--poll-interval", "1ms", "--allow-loopback-http", "--json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	var result baseline.RunResult
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout is not exactly one JSON value: %q", stdout)
	}
	if result.State != "feedback_persisted" || result.FeedbackCount != 2 || result.ReferenceCount != 2 || result.RunJobID == nil || *result.RunJobID != commandRunJob {
		t.Fatalf("result=%#v", result)
	}
	if !strings.Contains(stderr, "state=references_persisted") || !strings.Contains(stderr, "state=feedback_persisted") {
		t.Fatalf("progress=%q", stderr)
	}
	for _, forbidden := range append(protected, "run-command-token", server.URL, queryText, "idempotency_key", "lease_token") {
		if forbidden != "" && strings.Contains(stdout+stderr, forbidden) {
			t.Fatalf("command output leaked protected value")
		}
	}

	preview, _, err := executeBaselineForTest(t, "http://127.0.0.1:1", "baseline", "preview", "--group", commandRunGroup, "--job-id", commandRunJob)
	if err == nil || preview != "" {
		t.Fatal("run command unexpectedly duplicated preview behavior")
	}
}

func TestBaselineRunCommandRejectsRawQueryFlagsAndDocumentsExitClasses(t *testing.T) {
	for _, flag := range []string{"--raw-query", "--retrieval-query", "--diff-text", "--prompt", "--evidence"} {
		stdout, _, err := executeBaselineForTest(t, "https://core.example.test", "baseline", "run", "--group", commandRunGroup, "--plan", "plan.json", "--index-result", "index.json", flag, "private")
		if err == nil || exitCodeForError(err) != baselineRunUsageExitCode || stdout != "" || strings.Contains(err.Error(), "private") {
			t.Fatalf("flag %s result stdout=%q err=%v", flag, stdout, err)
		}
	}
	if baselineRunSuccessExitCode != 0 || baselineRunAuthExitCode != 3 || baselineRunCapabilityExitCode != 4 || baselineRunConflictExitCode != 5 || baselineRunRetryableExitCode != 6 || baselineRunTerminalExitCode != 7 || baselineRunContractExitCode != 8 || baselineRunInternalExitCode != 9 {
		t.Fatal("baseline run exit classes changed")
	}
}

func stringAddress(value string) *string { return &value }
