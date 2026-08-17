package baseline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	runTestGroup        = "10000000-0000-4000-8000-000000000001"
	runTestSource       = "10000000-0000-4000-8000-000000000002"
	runTestRegistration = "10000000-0000-4000-8000-000000000003"
	runTestJob          = "10000000-0000-4000-8000-000000000004"
	runTestProcessing   = "10000000-0000-4000-8000-000000000005"
	runTestPersisted    = "10000000-0000-4000-8000-000000000006"
)

type runTestServer struct {
	t                  *testing.T
	server             *httptest.Server
	mu                 sync.Mutex
	requests           map[string][][]byte
	dispatch           string
	readiness          string
	capabilityReason   *string
	statuses           []string
	statusAt           int
	feedbackCount      int
	terminalReason     string
	failSubmissionOnce bool
	conflict           bool
	querySHA           string
	queryLength        int
	queryByteSize      int
	publication        v2RunPublication
}

func newRunTestServer(t *testing.T, publication v2RunPublication) *runTestServer {
	t.Helper()
	fixture := &runTestServer{
		t: t, requests: map[string][][]byte{}, dispatch: "automatic", readiness: "ready",
		statuses:      []string{"queued", "running", "references_persisted", "feedback_persisted"},
		feedbackCount: 2, publication: publication,
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *runTestServer) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		fixture.t.Errorf("read request: %v", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		fixture.t.Errorf("decode request: %v", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	stage := strings.TrimPrefix(request.URL.Path, "/baseline/control/")
	fixture.mu.Lock()
	fixture.requests[stage] = append(fixture.requests[stage], append([]byte(nil), raw...))
	requestCount := len(fixture.requests[stage])
	fixture.mu.Unlock()
	requestID, _ := body["request_id"].(string)
	groupID, _ := body["group_id"].(string)
	switch request.URL.Path {
	case "/baseline/control/v2/capabilities":
		fixture.writeJSON(writer, fixture.capabilities(requestID, groupID))
	case "/baseline/control/v2/runs":
		if fixture.conflict {
			fixture.writeError(writer, requestID, http.StatusConflict, "run_submission", false, "idempotency_conflict")
			return
		}
		query := body["retrieval_query"].(map[string]any)
		fixture.querySHA = query["sha256"].(string)
		fixture.queryByteSize = int(query["byte_size"].(float64))
		fixture.queryLength = utf8RuneCount(query["text"].(string))
		if fixture.failSubmissionOnce && requestCount == 1 {
			connection, _, _ := writer.(http.Hijacker).Hijack()
			_ = connection.Close()
			return
		}
		writer.WriteHeader(http.StatusAccepted)
		fixture.writeJSON(writer, map[string]any{
			"protocol_version": IndexControlProtocolVersion, "protocol_sha256": IndexControlProtocolSHA256,
			"message_type": "job_accepted", "request_id": requestID, "group_id": groupID,
			"job_id": runTestJob, "operation": "baseline_run", "state": "queued",
			"replayed": requestCount > 1, "processing_run_id": runTestProcessing,
		})
	case "/baseline/control/v2/runs/status":
		state := fixture.statuses[min(fixture.statusAt, len(fixture.statuses)-1)]
		fixture.statusAt++
		fixture.writeJSON(writer, fixture.status(requestID, groupID, state))
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (fixture *runTestServer) capabilities(requestID, groupID string) map[string]any {
	reason := any(nil)
	if fixture.capabilityReason != nil {
		reason = *fixture.capabilityReason
	}
	return map[string]any{
		"protocol_version": IndexControlProtocolVersion, "protocol_sha256": IndexControlProtocolSHA256,
		"message_type": "capabilities", "request_id": requestID, "group_id": groupID,
		"supported_protocols": []any{
			map[string]any{"version": ControlProtocolVersion, "sha256": ControlProtocolSHA256, "role": "staging_only"},
			map[string]any{"version": IndexControlProtocolVersion, "sha256": IndexControlProtocolSHA256, "role": "index_and_run_submission"},
		},
		"operations": map[string]any{
			"index_build":  map[string]any{"submission": "safe", "endpoint": "authenticated_post", "dispatch": "manual", "readiness": "not_ready", "reason_code": "worker_unavailable"},
			"baseline_run": map[string]any{"submission": "safe", "endpoint": "authenticated_post", "dispatch": fixture.dispatch, "readiness": fixture.readiness, "reason_code": reason},
		},
		"limits": map[string]any{
			"control_request_bytes": 64000, "run_request_bytes": 8100000, "raw_query_bytes": 8000000,
			"idempotency_key_min_characters": 32, "idempotency_key_max_characters": 128,
			"selected_evidence_items": 4, "selected_evidence_characters": 16000, "feedback_items": 4,
			"terminal_status_retention_days": 30,
		},
		"required_index_identity": indexTestIntent(),
		"transport":               map[string]any{"remote": "verified_https_required", "loopback_http": "explicit_actual_peer_exception", "json_media_type": "application/json", "encoding": "utf-8"},
	}
}

func (fixture *runTestServer) status(requestID, groupID, state string) map[string]any {
	terminal, exit, retrieval := false, "pending", "pending"
	reason, failureStage := any(nil), any(nil)
	effects := map[string]any{"evidence_count": 0, "reference_count": 0, "feedback_count": 0, "generation_invoked": false, "notification_outbox_count": 0, "persisted_run_id": nil}
	switch state {
	case "references_persisted":
		retrieval = "ok"
		effects = map[string]any{"evidence_count": 2, "reference_count": 2, "feedback_count": 0, "generation_invoked": false, "notification_outbox_count": 0, "persisted_run_id": runTestPersisted}
	case "feedback_persisted":
		terminal, exit, retrieval = true, "success", "ok"
		outbox := 0
		if fixture.feedbackCount > 0 {
			outbox = 1
		}
		effects = map[string]any{"evidence_count": 2, "reference_count": 2, "feedback_count": fixture.feedbackCount, "generation_invoked": true, "notification_outbox_count": outbox, "persisted_run_id": runTestPersisted}
	case "insufficient":
		terminal, exit, retrieval, reason, failureStage = true, "insufficient", "insufficient", "retrieval_insufficient", "retrieval"
	case "retryable_failed":
		retrieval, reason, failureStage = "error", "generation_provider_unavailable", "generation"
	case "terminal_failed":
		terminal, exit, retrieval, reason, failureStage = true, "failed", "error", "generation_provider_invalid", "generation"
	case "blocked":
		terminal, exit, retrieval, reason, failureStage = true, "blocked", "error", "worker_unavailable", "dispatch"
		if fixture.terminalReason != "" {
			reason = fixture.terminalReason
		}
	case "cancelled":
		terminal, exit, retrieval, reason, failureStage = true, "cancelled", "error", "job_cancelled", "dispatch"
	}
	return map[string]any{
		"protocol_version": IndexControlProtocolVersion, "protocol_sha256": IndexControlProtocolSHA256,
		"message_type": "job_status", "request_id": requestID, "group_id": groupID,
		"job_id": runTestJob, "operation": "baseline_run", "processing_run_id": runTestProcessing,
		"source_document_id": runTestSource, "changed_repository_registration_id": runTestRegistration,
		"index_publication": fixture.publication, "state": state, "terminal": terminal,
		"exit_classification": exit, "attempt": 1, "created_at": "2026-08-17T12:00:00Z", "updated_at": "2026-08-17T12:01:00Z",
		"retrieval_status": retrieval,
		"query_provenance": map[string]any{"sha256": fixture.querySHA, "length": fixture.queryLength, "byte_size": fixture.queryByteSize, "origin": "explicit"},
		"effects":          effects, "reason_code": reason, "failure_stage": failureStage, "replayed": false,
	}
}

func (fixture *runTestServer) writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		fixture.t.Errorf("encode response: %v", err)
	}
}

func (fixture *runTestServer) writeError(writer http.ResponseWriter, requestID string, status int, stage string, retryable bool, code string) {
	writer.WriteHeader(status)
	fixture.writeJSON(writer, map[string]any{
		"protocol_version": IndexControlProtocolVersion, "protocol_sha256": IndexControlProtocolSHA256,
		"message_type": "error", "request_id": requestID, "http_status": status,
		"stage": stage, "retryable": retryable, "code": code,
	})
}

func runTestInput(t *testing.T) ScanInput {
	t.Helper()
	toy := newScannerToy(t)
	input := toy.input()
	input.GroupID = runTestGroup
	input.Changed.RepositoryID = runTestRegistration
	input.Changed.SourceDocumentID = runTestSource
	input.Siblings[0].RepositoryID = "10000000-0000-4000-8000-000000000007"
	return input
}

func runTestIndexResult(t *testing.T, input ScanInput, directory string) (string, IndexResult) {
	t.Helper()
	scan, err := NewScanner().Scan(context.Background(), input.GroupID, input)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.ClearProtected()
	intent := v2IndexIntent{
		IndexFormatVersion: "baseline-index.v1", TokenizerVersion: "baseline_v1_frozen_tokenizer.v1",
		RetrievalConfigFingerprint: indexTestHashC,
		Embedding:                  v2EmbeddingIdentity{ContractVersion: "baseline-embedding-http.v1", Provider: "baseline_http_v1", Model: "BAAI/bge-small-en-v1.5", Revision: "52398278842ec682c6f32300af41344b1c0b0bb2", Dimension: 384, DType: "float32", Fingerprint: indexTestHashE},
	}
	intentFingerprint, err := fingerprintValue(intent)
	if err != nil {
		t.Fatal(err)
	}
	now := "2026-08-17T12:00:00Z"
	documents := 2
	result := IndexResult{
		SchemaVersion: IndexResultSchemaVersion, ProtocolVersion: IndexControlProtocolVersion, ProtocolSHA256: IndexControlProtocolSHA256,
		GroupID: input.GroupID, ScanFingerprint: stringPointer(scan.Report.DeterministicFingerprint), CorpusGenerationID: stringPointer(indexTestGeneration),
		CorpusManifestHash: stringPointer(indexTestHashD), DispatchMode: "automatic", State: "succeeded", ExitClassification: "success",
		CompatiblePublicationID: stringPointer(indexTestPublication), IndexFormatVersion: stringPointer(intent.IndexFormatVersion), TokenizerVersion: stringPointer(intent.TokenizerVersion),
		IndexIntentFingerprint: stringPointer(intentFingerprint), RetrievalConfigFingerprint: stringPointer(intent.RetrievalConfigFingerprint), EmbeddingFingerprint: stringPointer(intent.Embedding.Fingerprint), IndexFingerprint: stringPointer(indexTestHashF),
		IndexedDocumentCount: &documents, VectorCount: &documents, TransmittedRequestCount: 3, TransmittedRequestBytes: 1024, UpdatedAt: now,
	}
	filename := filepath.Join(directory, "index-result.json")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := EncodeIndexResult(file, result); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return filename, result
}

func runTestOptions(fixture *runTestServer, directory string) RunOptions {
	return RunOptions{
		BaseURL: fixture.server.URL, Token: "run-test-token", AllowLoopbackHTTP: true,
		Wait: true, Timeout: time.Minute, PollInterval: time.Millisecond,
		StateDirectory: filepath.Join(directory, "baseline-runs"), RetryBaseDelay: time.Millisecond,
		sleep: func(context.Context, time.Duration) error { return nil },
		now:   func() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) },
	}
}

func TestBaselineRunPositiveAndZeroFindingCompletion(t *testing.T) {
	for _, feedbackCount := range []int{2, 0} {
		t.Run(strconvItoa(feedbackCount), func(t *testing.T) {
			directory := t.TempDir()
			input := runTestInput(t)
			indexFile, index := runTestIndexResult(t, input, directory)
			fixture := newRunTestServer(t, publicationFromIndexResult(index))
			fixture.feedbackCount = feedbackCount
			if feedbackCount == 0 {
				fixture.dispatch = "manual"
			}
			execution, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, runTestOptions(fixture, directory))
			if err != nil {
				t.Fatal(err)
			}
			if execution.Result.State != "feedback_persisted" || execution.Result.FeedbackCount != feedbackCount || !execution.Result.GenerationInvoked || execution.Result.ReferenceCount != 2 || execution.Result.EvidenceCount != 2 || execution.Result.DispatchMode != fixture.dispatch {
				t.Fatalf("unexpected result: %#v", execution.Result)
			}
			if feedbackCount == 0 && execution.Result.NotificationOutboxCount != 0 {
				t.Fatal("zero findings created an outbox effect")
			}
			fixture.mu.Lock()
			submissions := fixture.requests["v2/runs"]
			fixture.mu.Unlock()
			if len(submissions) != 1 {
				t.Fatalf("document-level retrieval submission count = %d", len(submissions))
			}
			var submitted map[string]any
			if err := json.Unmarshal(submissions[0], &submitted); err != nil {
				t.Fatal(err)
			}
			query := submitted["retrieval_query"].(map[string]any)
			if query["text"] == "" || query["sha256"] != *execution.Result.QuerySHA256 || submitted["source_document_id"] != runTestSource || submitted["changed_repository_registration_id"] != runTestRegistration {
				t.Fatalf("protected query submission mismatch: %#v", submitted)
			}
			var output bytes.Buffer
			if err := EncodeRunResult(&output, execution.Result); err != nil {
				t.Fatal(err)
			}
			if RunResultForbiddenFields(output.Bytes()) || bytes.Contains(output.Bytes(), []byte(query["text"].(string))) || bytes.Contains(output.Bytes(), []byte("run-test-token")) {
				t.Fatalf("sensitive run output: %s", output.Bytes())
			}
		})
	}
}

func TestBaselineRunPayloadExpiryIsTerminalBlockedWithoutEffects(t *testing.T) {
	directory := t.TempDir()
	input := runTestInput(t)
	_, index := runTestIndexResult(t, input, directory)
	fixture := newRunTestServer(t, publicationFromIndexResult(index))
	fixture.querySHA, fixture.queryLength, fixture.queryByteSize = indexTestHashA, 1, 1
	fixture.statuses = []string{"blocked"}
	// Core intentionally projects the internal payload_expired reason as the
	// non-reflective public worker_unavailable code.
	fixture.terminalReason = "worker_unavailable"
	execution, err := RunBaselineRunStatus(context.Background(), input.GroupID, runTestJob, runTestOptions(fixture, directory))
	if RunFailure(err) != RunFailureTerminal || execution.Result.State != "blocked" || execution.Result.ReasonCode == nil || *execution.Result.ReasonCode != "worker_unavailable" {
		t.Fatalf("result=%#v err=%v", execution.Result, err)
	}
	if execution.Result.EvidenceCount != 0 || execution.Result.ReferenceCount != 0 || execution.Result.FeedbackCount != 0 || execution.Result.GenerationInvoked {
		t.Fatalf("expired payload produced effects: %#v", execution.Result)
	}
}

func TestBaselineRunInsufficientAndReferencesPersistedSemantics(t *testing.T) {
	for _, state := range []string{"references_persisted", "insufficient"} {
		t.Run(state, func(t *testing.T) {
			directory := t.TempDir()
			input := runTestInput(t)
			indexFile, index := runTestIndexResult(t, input, directory)
			fixture := newRunTestServer(t, publicationFromIndexResult(index))
			fixture.statuses = []string{state}
			options := runTestOptions(fixture, directory)
			options.Wait = false
			first, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, options)
			if err != nil || first.Result.State != "queued" {
				t.Fatalf("initial result=%#v err=%v", first.Result, err)
			}
			options.Resume = true
			resumed, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, options)
			if state == "insufficient" {
				if RunFailure(err) != RunFailureTerminal || resumed.Result.EvidenceCount != 0 || resumed.Result.ReferenceCount != 0 || resumed.Result.FeedbackCount != 0 || resumed.Result.GenerationInvoked {
					t.Fatalf("insufficient result=%#v err=%v", resumed.Result, err)
				}
			} else if err != nil || resumed.Result.State != "references_persisted" || resumed.Result.PersistedRetrievalRunID == nil {
				t.Fatalf("references result=%#v err=%v", resumed.Result, err)
			}
		})
	}
}

func TestBaselineRunLostResponseExactReplayAndResume(t *testing.T) {
	directory := t.TempDir()
	input := runTestInput(t)
	indexFile, index := runTestIndexResult(t, input, directory)
	fixture := newRunTestServer(t, publicationFromIndexResult(index))
	fixture.failSubmissionOnce = true
	options := runTestOptions(fixture, directory)
	options.Wait = false
	first, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, options)
	if err != nil || !first.Result.Replayed {
		t.Fatalf("result=%#v err=%v", first.Result, err)
	}
	fixture.mu.Lock()
	submissions := append([][]byte(nil), fixture.requests["v2/runs"]...)
	fixture.mu.Unlock()
	if len(submissions) != 2 || !reflect.DeepEqual(submissions[0], submissions[1]) {
		t.Fatalf("lost-response replay changed bytes")
	}
	options.Resume, options.Wait = true, true
	resumed, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, options)
	if err != nil || !resumed.Result.Resumed || resumed.Result.State != "feedback_persisted" {
		t.Fatalf("result=%#v err=%v", resumed.Result, err)
	}
}

func TestBaselineRunCapabilityStaleAndTerminalStates(t *testing.T) {
	states := []string{"retryable_failed", "terminal_failed", "blocked", "cancelled"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			directory := t.TempDir()
			input := runTestInput(t)
			_, index := runTestIndexResult(t, input, directory)
			fixture := newRunTestServer(t, publicationFromIndexResult(index))
			fixture.querySHA, fixture.queryLength, fixture.queryByteSize = indexTestHashA, 1, 1
			fixture.statuses = []string{state}
			execution, err := RunBaselineRunStatus(context.Background(), input.GroupID, runTestJob, runTestOptions(fixture, directory))
			if state == "retryable_failed" {
				if err != nil || execution.Result.State != state {
					t.Fatalf("result=%#v err=%v", execution.Result, err)
				}
			} else if RunFailure(err) != RunFailureTerminal || execution.Result.State != state {
				t.Fatalf("result=%#v err=%v", execution.Result, err)
			}
		})
	}

	t.Run("not_ready", func(t *testing.T) {
		directory := t.TempDir()
		input := runTestInput(t)
		indexFile, index := runTestIndexResult(t, input, directory)
		fixture := newRunTestServer(t, publicationFromIndexResult(index))
		reason := "worker_unavailable"
		fixture.readiness, fixture.capabilityReason = "not_ready", &reason
		_, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, runTestOptions(fixture, directory))
		if RunFailure(err) != RunFailureCapability {
			t.Fatalf("failure=%s err=%v", RunFailure(err), err)
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if len(fixture.requests["v2/runs"]) != 0 {
			t.Fatal("not-ready capability reached submission")
		}
	})

	t.Run("stale_index", func(t *testing.T) {
		directory := t.TempDir()
		input := runTestInput(t)
		indexFile, index := runTestIndexResult(t, input, directory)
		fixture := newRunTestServer(t, publicationFromIndexResult(index))
		fixture.conflict = true
		_, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, runTestOptions(fixture, directory))
		if RunFailure(err) != RunFailureConflict {
			t.Fatalf("failure=%s err=%v", RunFailure(err), err)
		}
	})
}

func TestBaselineRunResumeRejectsRevisionDriftAndUnsafeState(t *testing.T) {
	directory := t.TempDir()
	input := runTestInput(t)
	indexFile, index := runTestIndexResult(t, input, directory)
	fixture := newRunTestServer(t, publicationFromIndexResult(index))
	options := runTestOptions(fixture, directory)
	options.Wait = false
	if _, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, options); err != nil {
		t.Fatal(err)
	}
	writeScannerFile(t, filepath.Join(input.Changed.LocalPath, "changed.txt"), []byte("revision drift\n"), 0o644)
	gitTest(t, input.Changed.LocalPath, "add", "changed.txt")
	gitTest(t, input.Changed.LocalPath, "commit", "-m", "new immutable head")
	newHead := strings.TrimSpace(gitTest(t, input.Changed.LocalPath, "rev-parse", "HEAD"))
	input.HeadRevision = newHead
	input.Changed.RepositoryRevision = newHead
	options.Resume = true
	if _, err := RunBaselineRun(context.Background(), input.GroupID, input, indexFile, options); RunFailure(err) != RunFailureConflict {
		t.Fatalf("revision drift failure=%s err=%v", RunFailure(err), err)
	}

	entries, err := os.ReadDir(options.StateDirectory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("state entries=%d err=%v", len(entries), err)
	}
	statePath := filepath.Join(options.StateDirectory, entries[0].Name())
	value, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(value, []byte("diff --git")) || bytes.Contains(value, []byte("before")) || bytes.Contains(value, []byte("after")) || bytes.Contains(value, []byte("run-test-token")) {
		t.Fatalf("sensitive resume state: %s", value)
	}
	value[len(value)/2] ^= 1
	if err := os.WriteFile(statePath, value, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := newRunStateStore(options.StateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	identity := strings.TrimSuffix(entries[0].Name(), ".json")
	if _, err := store.load(identity); RunFailure(err) != RunFailureInput {
		t.Fatalf("corrupt state failure=%v", err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, statePath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.load(identity); RunFailure(err) != RunFailureInput {
		t.Fatalf("symlink state failure=%v", err)
	}
}

func utf8RuneCount(value string) int {
	return len([]rune(value))
}

func TestEncodeRunResultEmitsExactlyOneJSONValue(t *testing.T) {
	now := "2026-08-17T12:00:00Z"
	result := RunResult{
		SchemaVersion: RunResultSchemaVersion, ProtocolVersion: IndexControlProtocolVersion, ProtocolSHA256: IndexControlProtocolSHA256,
		GroupID: runTestGroup, RunJobID: stringPointer(runTestJob), ProcessingRunID: stringPointer(runTestProcessing),
		DispatchMode: "automatic", State: "feedback_persisted", ExitClassification: "success",
		PersistedRetrievalRunID: stringPointer(runTestPersisted), EvidenceCount: 1, ReferenceCount: 1,
		GenerationInvoked: true, UpdatedAt: now,
	}
	var output bytes.Buffer
	if err := EncodeRunResult(&output, result); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("run output contains more than one JSON value: %v", err)
	}
}
