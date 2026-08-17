package baseline

import (
	"context"
	"encoding/json"
	"errors"
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
	indexTestGroup        = "group-index-toy"
	indexTestSnapshot     = "bsnap_1111111111111111111111111111111111111111111111111111111111111111"
	indexTestStaging      = "20000000-0000-4000-8000-000000000001"
	indexTestContinuation = "30000000-0000-4000-8000-000000000001"
	indexTestCorpus       = "40000000-0000-4000-8000-000000000001"
	indexTestGeneration   = "50000000-0000-4000-8000-000000000001"
	indexTestJob          = "60000000-0000-4000-8000-000000000001"
	indexTestPublication  = "70000000-0000-4000-8000-000000000001"
	indexTestHashA        = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	indexTestHashB        = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	indexTestHashC        = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	indexTestHashD        = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	indexTestHashE        = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	indexTestHashF        = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

type indexTestServer struct {
	t                   *testing.T
	server              *httptest.Server
	mu                  sync.Mutex
	requests            map[string][][]byte
	dispatch            string
	readiness           string
	capabilityReason    *string
	capabilityProtocol  string
	runCapabilityReady  bool
	statuses            []string
	statusAt            int
	publicationOnQueued bool
	inconsistentSuccess bool
	failSubmissionOnce  bool
	conflict            bool
}

func newIndexTestServer(t *testing.T) *indexTestServer {
	t.Helper()
	fixture := &indexTestServer{
		t: t, requests: map[string][][]byte{}, dispatch: "automatic", readiness: "ready",
		capabilityProtocol: IndexControlProtocolSHA256, statuses: []string{"queued", "running", "succeeded"},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *indexTestServer) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.RawQuery != "" {
		fixture.t.Errorf("unsafe method or query: %s %s", request.Method, request.URL.String())
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		fixture.t.Errorf("decode request: %v", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	stage := strings.TrimPrefix(request.URL.Path, "/baseline/control/")
	fixture.mu.Lock()
	encoded, _ := json.Marshal(body)
	fixture.requests[stage] = append(fixture.requests[stage], encoded)
	fixture.mu.Unlock()
	requestID, _ := body["request_id"].(string)
	groupID, _ := body["group_id"].(string)

	switch request.URL.Path {
	case "/baseline/control/v2/capabilities":
		fixture.writeJSON(writer, fixture.capabilities(requestID, groupID))
	case "/baseline/control/v1/continuations/status":
		fixture.writeJSON(writer, fixture.continuation(requestID, groupID))
	case "/baseline/control/v2/index-builds":
		if fixture.conflict {
			fixture.writeError(writer, requestID, http.StatusConflict, "index_submission", false, "idempotency_conflict")
			return
		}
		fixture.mu.Lock()
		requestCount := len(fixture.requests[stage])
		fixture.mu.Unlock()
		if fixture.failSubmissionOnce && requestCount == 1 {
			hijacker := writer.(http.Hijacker)
			connection, _, _ := hijacker.Hijack()
			_ = connection.Close()
			return
		}
		writer.WriteHeader(http.StatusAccepted)
		fixture.writeJSON(writer, map[string]any{
			"protocol_version": IndexControlProtocolVersion, "protocol_sha256": IndexControlProtocolSHA256,
			"message_type": "job_accepted", "request_id": requestID, "group_id": groupID,
			"job_id": indexTestJob, "operation": "index_build", "state": "queued",
			"replayed": requestCount > 1, "processing_run_id": nil,
		})
	case "/baseline/control/v2/index-builds/status":
		state := fixture.statuses[min(fixture.statusAt, len(fixture.statuses)-1)]
		fixture.statusAt++
		fixture.writeJSON(writer, fixture.status(requestID, groupID, state))
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (fixture *indexTestServer) writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		fixture.t.Errorf("encode response: %v", err)
	}
}

func (fixture *indexTestServer) writeError(writer http.ResponseWriter, requestID string, status int, stage string, retryable bool, code string) {
	writer.WriteHeader(status)
	fixture.writeJSON(writer, map[string]any{
		"protocol_version": IndexControlProtocolVersion, "protocol_sha256": IndexControlProtocolSHA256,
		"message_type": "error", "request_id": requestID, "http_status": status,
		"stage": stage, "retryable": retryable, "code": code,
	})
}

func indexTestIntent() map[string]any {
	return map[string]any{
		"index_format_version": "baseline-index.v1", "tokenizer_version": "baseline_v1_frozen_tokenizer.v1",
		"retrieval_config_fingerprint": indexTestHashC,
		"embedding": map[string]any{
			"contract_version": "baseline-embedding-http.v1", "provider": "baseline_http_v1",
			"model": "BAAI/bge-small-en-v1.5", "revision": "52398278842ec682c6f32300af41344b1c0b0bb2",
			"dimension": 384, "dtype": "float32", "fingerprint": indexTestHashE,
		},
	}
}

func (fixture *indexTestServer) capabilities(requestID, groupID string) map[string]any {
	reason := any(nil)
	if fixture.capabilityReason != nil {
		reason = *fixture.capabilityReason
	}
	indexCapability := map[string]any{
		"submission": "safe", "endpoint": "authenticated_post", "dispatch": fixture.dispatch,
		"readiness": fixture.readiness, "reason_code": reason,
	}
	runCapability := map[string]any{
		"submission": "unavailable", "endpoint": "unavailable", "dispatch": "unavailable",
		"readiness": "unavailable", "reason_code": "capability_unavailable",
	}
	if fixture.runCapabilityReady {
		runCapability = map[string]any{"submission": "safe", "endpoint": "authenticated_post", "dispatch": "automatic", "readiness": "ready", "reason_code": nil}
	}
	return map[string]any{
		"protocol_version": IndexControlProtocolVersion, "protocol_sha256": fixture.capabilityProtocol,
		"message_type": "capabilities", "request_id": requestID, "group_id": groupID,
		"supported_protocols": []any{
			map[string]any{"version": ControlProtocolVersion, "sha256": ControlProtocolSHA256, "role": "staging_only"},
			map[string]any{"version": IndexControlProtocolVersion, "sha256": IndexControlProtocolSHA256, "role": "index_and_run_submission"},
		},
		"operations": map[string]any{"index_build": indexCapability, "baseline_run": runCapability},
		"limits": map[string]any{
			"control_request_bytes": 64000, "run_request_bytes": 8100000, "raw_query_bytes": 8000000,
			"idempotency_key_min_characters": 32, "idempotency_key_max_characters": 128,
			"selected_evidence_items": 4, "selected_evidence_characters": 16000, "feedback_items": 4,
			"terminal_status_retention_days": 30,
		},
		"required_index_identity": indexTestIntent(),
		"transport": map[string]any{
			"remote": "verified_https_required", "loopback_http": "explicit_actual_peer_exception",
			"json_media_type": "application/json", "encoding": "utf-8",
		},
	}
}

func (fixture *indexTestServer) continuation(requestID, groupID string) map[string]any {
	now := "2026-08-17T12:00:00Z"
	return map[string]any{
		"schema_version": "baseline-snapshot-continuation.v1", "message_type": "continuation_job_status",
		"request_id": requestID, "group_id": groupID, "staging_job_id": indexTestStaging,
		"job_id": indexTestContinuation, "operation": "sealed_snapshot_continue", "state": "succeeded",
		"attempt": 1, "created_at": now, "updated_at": now,
		"progress": map[string]any{"completed": 1, "total": 1},
		"result": map[string]any{
			"snapshot_id": indexTestSnapshot, "staging_state": "sealed", "corpus_ingestion_complete": true,
			"corpus_eligible": true, "index_eligible": false, "baseline_eligible": false, "index_state": "incomplete",
			"corpus_id": indexTestCorpus, "corpus_generation_id": indexTestGeneration,
			"corpus_generation_version": "generation-v1", "corpus_manifest_hash": indexTestHashD,
			"corpus_provenance_fingerprint": indexTestHashB, "worker_contract_version": "baseline-continuation-worker.v1",
		},
		"error_code": nil,
		"staging": map[string]any{
			"state": "sealed", "received_parts": 2, "expected_parts": 2, "expires_at": "2026-08-18T12:00:00Z",
			"corpus_eligible": false, "index_eligible": false,
		},
		"continuation": map[string]any{
			"job_id": indexTestContinuation, "operation": "sealed_snapshot_continue", "state": "succeeded",
			"attempt": 1, "created_at": now, "updated_at": now, "error_code": nil,
			"corpus_ingestion_complete": true, "corpus_eligible": true, "index_eligible": false, "baseline_eligible": false,
		},
		"replayed": false,
	}
}

func (fixture *indexTestServer) status(requestID, groupID, state string) map[string]any {
	now := "2026-08-17T12:00:00Z"
	terminal := false
	exit := "pending"
	reason := any(nil)
	var result any
	documents := 0
	if state == "succeeded" {
		terminal, exit, documents = true, "success", 2
		result = map[string]any{
			"index_publication_id": indexTestPublication, "corpus_generation_id": indexTestGeneration,
			"corpus_manifest_hash": indexTestHashD, "index_fingerprint": indexTestHashF,
			"retrieval_config_fingerprint": indexTestHashC, "embedding_fingerprint": indexTestHashE,
			"document_count": 2, "vector_count": 2,
		}
		if fixture.inconsistentSuccess {
			result.(map[string]any)["corpus_generation_id"] = indexTestStaging
		}
	} else if state == "retryable_failed" {
		reason = "embedding_unavailable"
	} else if state == "terminal_failed" {
		terminal, exit, reason = true, "failed", "index_build_failed"
	} else if state == "cancelled" {
		terminal, exit, reason = true, "cancelled", "job_cancelled"
	}
	if fixture.publicationOnQueued && state == "queued" {
		result = map[string]any{
			"index_publication_id": indexTestPublication, "corpus_generation_id": indexTestGeneration,
			"corpus_manifest_hash": indexTestHashD, "index_fingerprint": indexTestHashF,
			"retrieval_config_fingerprint": indexTestHashC, "embedding_fingerprint": indexTestHashE,
			"document_count": 2, "vector_count": 2,
		}
	}
	return map[string]any{
		"protocol_version": IndexControlProtocolVersion, "protocol_sha256": IndexControlProtocolSHA256,
		"message_type": "job_status", "request_id": requestID, "group_id": groupID,
		"job_id": indexTestJob, "operation": "index_build", "state": state, "terminal": terminal,
		"exit_classification": exit, "attempt": 1, "created_at": now, "updated_at": now,
		"ingestion_continuation_id": indexTestContinuation, "corpus_generation_id": indexTestGeneration,
		"corpus_manifest_hash": indexTestHashD, "index_intent": indexTestIntent(),
		"progress": map[string]any{"document_count": documents, "vector_count": documents},
		"result":   result, "reason_code": reason, "replayed": false,
	}
}

func indexTestUpload() UploadResult {
	return UploadResult{
		SchemaVersion: UploadResultSchemaVersion, ProtocolVersion: ControlProtocolVersion,
		ProtocolSHA256: ControlProtocolSHA256, GroupID: indexTestGroup, ScanFingerprint: indexTestHashA,
		SnapshotID: indexTestSnapshot, StagingJobID: indexTestStaging, ContinuationJobID: indexTestContinuation,
		CanonicalManifestHash: indexTestHashB, ContentManifestHash: indexTestHashC,
		PartTotal: 2, PartCompleted: 2, TransmittedRequestBytes: 1024, TransmittedRequestCount: 5,
		State: "succeeded", CorpusID: indexTestCorpus, CorpusGenerationID: indexTestGeneration,
		CorpusGenerationVersion: "generation-v1", StartedAt: "2026-08-17T11:00:00Z", UpdatedAt: "2026-08-17T12:00:00Z",
	}
}

func writeIndexUploadResult(t *testing.T, directory string, value any) string {
	t.Helper()
	filename := filepath.Join(directory, "upload-result.json")
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func indexTestOptions(fixture *indexTestServer, directory string) IndexOptions {
	return IndexOptions{
		BaseURL: fixture.server.URL, Token: "test-token", AllowLoopbackHTTP: true,
		Wait: true, Timeout: time.Minute, PollInterval: time.Millisecond,
		StateDirectory: filepath.Join(directory, "baseline-indexes"), RetryBaseDelay: time.Millisecond,
		sleep: func(context.Context, time.Duration) error { return nil },
		now:   func() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) },
	}
}

func TestLoadSuccessfulUploadResultStrictlyRejectsPartialCrossGroupAndUnknown(t *testing.T) {
	base := indexTestUpload()
	tests := []struct {
		name  string
		value any
		group string
	}{
		{"cross_group", base, "another-group"},
		{"partial", func() UploadResult { value := base; value.State = "snapshot_committed"; return value }(), indexTestGroup},
		{"incomplete_parts", func() UploadResult { value := base; value.PartCompleted = 1; return value }(), indexTestGroup},
		{"missing_continuation", func() UploadResult { value := base; value.ContinuationJobID = ""; return value }(), indexTestGroup},
		{"bad_hash", func() UploadResult { value := base; value.ScanFingerprint = strings.Repeat("A", 64); return value }(), indexTestGroup},
		{"unknown", map[string]any{
			"schema_version": UploadResultSchemaVersion, "protocol_version": ControlProtocolVersion,
			"protocol_sha256": ControlProtocolSHA256, "group_id": indexTestGroup, "state": "succeeded",
			"repository_path": "/private/source",
		}, indexTestGroup},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			filename := writeIndexUploadResult(t, directory, test.value)
			if _, err := LoadSuccessfulUploadResult(filename, test.group); IndexFailure(err) != IndexFailureInput {
				t.Fatalf("failure = %v (%v)", IndexFailure(err), err)
			}
		})
	}
}

func TestLoadSuccessfulUploadResultRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := writeIndexUploadResult(t, directory, indexTestUpload())
	link := filepath.Join(directory, "upload-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := LoadSuccessfulUploadResult(link, indexTestGroup); IndexFailure(err) != IndexFailureInput {
		t.Fatalf("symlink failure = %v", err)
	}
}

func TestRunIndexPinsCapabilityContinuationIntentAndManualDispatch(t *testing.T) {
	fixture := newIndexTestServer(t)
	fixture.dispatch = "manual"
	fixture.runCapabilityReady = true // Index readiness is intentionally independent.
	directory := t.TempDir()
	filename := writeIndexUploadResult(t, directory, indexTestUpload())
	options := indexTestOptions(fixture, directory)
	execution, err := RunIndex(context.Background(), indexTestGroup, filename, options)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.State != "succeeded" || execution.Result.DispatchMode != "manual" || execution.Result.CompatiblePublicationID == nil || *execution.Result.CompatiblePublicationID != indexTestPublication {
		t.Fatalf("unexpected result: %#v", execution.Result)
	}
	fixture.mu.Lock()
	submissions := fixture.requests["v2/index-builds"]
	fixture.mu.Unlock()
	if len(submissions) != 1 {
		t.Fatalf("submission count = %d", len(submissions))
	}
	var submission map[string]any
	if err := json.Unmarshal(submissions[0], &submission); err != nil {
		t.Fatal(err)
	}
	wantIntent, _ := canonicalJSONBytes(indexTestIntent())
	gotIntent, _ := canonicalJSONBytes(submission["index_intent"])
	if submission["ingestion_continuation_id"] != indexTestContinuation || submission["corpus_generation_id"] != indexTestGeneration || submission["corpus_manifest_hash"] != indexTestHashD || submission["ingestion_provenance_fingerprint"] != indexTestHashB || string(gotIntent) != string(wantIntent) {
		t.Fatalf("submission is not pinned: %#v", submission)
	}
	encoded, _ := json.Marshal(execution.Result)
	if IndexResultForbiddenFields(encoded) || strings.Contains(string(encoded), fixture.server.URL) || strings.Contains(string(encoded), "test-token") {
		t.Fatalf("sensitive result: %s", encoded)
	}
	if err := execution.Finalize(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(options.StateDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("successful state was not cleaned after output handoff: entries=%d err=%v", len(entries), err)
	}
}

func TestRunIndexRejectsObsoleteCapabilityBeforeWrites(t *testing.T) {
	fixture := newIndexTestServer(t)
	fixture.capabilityProtocol = v2ObsoleteSpecSHA256
	directory := t.TempDir()
	filename := writeIndexUploadResult(t, directory, indexTestUpload())
	_, err := RunIndex(context.Background(), indexTestGroup, filename, indexTestOptions(fixture, directory))
	if IndexFailure(err) != IndexFailureContract {
		t.Fatalf("failure = %v (%v)", IndexFailure(err), err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests["v2/index-builds"]) != 0 || len(fixture.requests["v1/continuations/status"]) != 0 {
		t.Fatalf("capability rejection wrote or resolved continuation: %#v", fixture.requests)
	}
}

func TestRunIndexRejectsNotReadyBeforeSubmission(t *testing.T) {
	fixture := newIndexTestServer(t)
	reason := "embedding_unavailable"
	fixture.readiness, fixture.capabilityReason = "not_ready", &reason
	directory := t.TempDir()
	filename := writeIndexUploadResult(t, directory, indexTestUpload())
	_, err := RunIndex(context.Background(), indexTestGroup, filename, indexTestOptions(fixture, directory))
	if IndexFailure(err) != IndexFailureCapability || SafeIndexReason(err) != reason {
		t.Fatalf("failure = %v (%v)", IndexFailure(err), err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests["v2/index-builds"]) != 0 {
		t.Fatal("not-ready capability reached submission")
	}
}

func TestRunIndexLostResponseExactReplayAndResume(t *testing.T) {
	fixture := newIndexTestServer(t)
	fixture.failSubmissionOnce = true
	directory := t.TempDir()
	filename := writeIndexUploadResult(t, directory, indexTestUpload())
	options := indexTestOptions(fixture, directory)
	options.Wait = false
	first, err := RunIndex(context.Background(), indexTestGroup, filename, options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Result.Replayed || first.Result.State != "queued" {
		t.Fatalf("lost response was not replayed: %#v", first.Result)
	}
	fixture.mu.Lock()
	submissions := append([][]byte(nil), fixture.requests["v2/index-builds"]...)
	fixture.mu.Unlock()
	if len(submissions) != 2 || !reflect.DeepEqual(submissions[0], submissions[1]) {
		t.Fatalf("submission replay changed: %q != %q", submissions[0], submissions[1])
	}
	options.Resume, options.Wait = true, true
	resumed, err := RunIndex(context.Background(), indexTestGroup, filename, options)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Result.Resumed || resumed.Result.State != "succeeded" {
		t.Fatalf("resume result: %#v", resumed.Result)
	}
}

func TestRunIndexResumeCanPollAfterReadinessLoss(t *testing.T) {
	fixture := newIndexTestServer(t)
	directory := t.TempDir()
	filename := writeIndexUploadResult(t, directory, indexTestUpload())
	options := indexTestOptions(fixture, directory)
	options.Wait = false
	if _, err := RunIndex(context.Background(), indexTestGroup, filename, options); err != nil {
		t.Fatal(err)
	}
	reason := "worker_unavailable"
	fixture.readiness, fixture.capabilityReason = "not_ready", &reason
	fixture.statuses = []string{"succeeded"}
	options.Resume, options.Wait = true, true
	resumed, err := RunIndex(context.Background(), indexTestGroup, filename, options)
	if err != nil || resumed.Result.State != "succeeded" {
		t.Fatalf("result=%#v err=%v", resumed.Result, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests["v2/index-builds"]) != 1 {
		t.Fatalf("readiness-loss resume resubmitted: %d", len(fixture.requests["v2/index-builds"]))
	}
}

func TestRunIndexConflictAndInconsistentStatusFailClosed(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		fixture := newIndexTestServer(t)
		fixture.conflict = true
		directory := t.TempDir()
		filename := writeIndexUploadResult(t, directory, indexTestUpload())
		_, err := RunIndex(context.Background(), indexTestGroup, filename, indexTestOptions(fixture, directory))
		if IndexFailure(err) != IndexFailureConflict {
			t.Fatalf("failure = %v (%v)", IndexFailure(err), err)
		}
	})
	for _, test := range []struct {
		name string
		set  func(*indexTestServer)
	}{
		{"publication_before_success", func(value *indexTestServer) { value.publicationOnQueued = true; value.statuses = []string{"queued"} }},
		{"mismatched_success", func(value *indexTestServer) { value.inconsistentSuccess = true; value.statuses = []string{"succeeded"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIndexTestServer(t)
			test.set(fixture)
			directory := t.TempDir()
			filename := writeIndexUploadResult(t, directory, indexTestUpload())
			_, err := RunIndex(context.Background(), indexTestGroup, filename, indexTestOptions(fixture, directory))
			if IndexFailure(err) != IndexFailureContract {
				t.Fatalf("failure = %v (%v)", IndexFailure(err), err)
			}
		})
	}
}

func TestRunIndexRetryableTimeoutPreservesProtectedState(t *testing.T) {
	fixture := newIndexTestServer(t)
	fixture.statuses = []string{"retryable_failed"}
	directory := t.TempDir()
	filename := writeIndexUploadResult(t, directory, indexTestUpload())
	options := indexTestOptions(fixture, directory)
	options.sleep = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	execution, err := RunIndex(context.Background(), indexTestGroup, filename, options)
	if IndexFailure(err) != IndexFailureRetryable || execution.Result.State != "retryable_incomplete" {
		t.Fatalf("result=%#v err=%v", execution.Result, err)
	}
	entries, readErr := os.ReadDir(options.StateDirectory)
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("resume state not retained: entries=%d err=%v", len(entries), readErr)
	}
	info, _ := entries[0].Info()
	if !privateFilePermissions(info) {
		t.Fatalf("resume permissions = %v", info.Mode())
	}
}

func TestIndexStateRejectsCorruptionAndSymlink(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "baseline-indexes")
	store, err := newIndexStateStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	state := newIndexState(indexTestUpload(), continuationIdentity{ManifestHash: indexTestHashD, ProvenanceFingerprint: indexTestHashB}, indexTestHashE, indexTestHashC, time.Now().UTC())
	identity := indexTestHashA
	if err := store.save(identity, state); err != nil {
		t.Fatal(err)
	}
	value, _ := os.ReadFile(store.path(identity))
	value[len(value)/2] ^= 1
	if err := os.WriteFile(store.path(identity), value, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.load(identity); IndexFailure(err) != IndexFailureInput {
		t.Fatalf("corruption failure = %v", err)
	}
	if err := os.Remove(store.path(identity)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.path(identity)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.load(identity); IndexFailure(err) != IndexFailureInput {
		t.Fatalf("symlink failure = %v", err)
	}
}

func TestRunIndexStatusDoesNotRequireCurrentReadiness(t *testing.T) {
	fixture := newIndexTestServer(t)
	reason := "worker_unavailable"
	fixture.readiness, fixture.capabilityReason = "not_ready", &reason
	fixture.statuses = []string{"succeeded"}
	options := indexTestOptions(fixture, t.TempDir())
	execution, err := RunIndexStatus(context.Background(), indexTestGroup, indexTestJob, options)
	if err != nil || execution.Result.State != "succeeded" {
		t.Fatalf("result=%#v err=%v", execution.Result, err)
	}
}

func TestRunIndexStatusTerminalOutcomeIsSanitized(t *testing.T) {
	fixture := newIndexTestServer(t)
	fixture.statuses = []string{"terminal_failed"}
	options := indexTestOptions(fixture, t.TempDir())
	execution, err := RunIndexStatus(context.Background(), indexTestGroup, indexTestJob, options)
	if IndexFailure(err) != IndexFailureTerminal || execution.Result.State != "terminal_failed" || execution.Result.ReasonCode == nil || *execution.Result.ReasonCode != "index_build_failed" {
		t.Fatalf("result=%#v err=%v", execution.Result, err)
	}
	var output strings.Builder
	if err := EncodeIndexResult(&output, execution.Result); err != nil {
		t.Fatal(err)
	}
}

func TestIndexServerFailureClassification(t *testing.T) {
	for _, test := range []struct {
		code string
		kind IndexFailureKind
	}{
		{"stale_generation", IndexFailureConflict},
		{"embedding_identity_mismatch", IndexFailureConflict},
		{"repository_approval_revoked", IndexFailureConflict},
		{"worker_unavailable", IndexFailureCapability},
	} {
		err := classifyIndexControlError(&ControlHTTPError{StatusCode: http.StatusConflict, Code: test.code})
		if IndexFailure(err) != test.kind {
			t.Fatalf("%s classified as %s", test.code, IndexFailure(err))
		}
	}
}

func TestEncodeIndexResultEmitsOneSanitizedJSONValue(t *testing.T) {
	fixture := newIndexTestServer(t)
	fixture.statuses = []string{"succeeded"}
	directory := t.TempDir()
	filename := writeIndexUploadResult(t, directory, indexTestUpload())
	execution, err := RunIndex(context.Background(), indexTestGroup, filename, indexTestOptions(fixture, directory))
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := EncodeIndexResult(&output, execution.Result); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(output.String()))
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, os.ErrClosed) && err == nil {
		t.Fatal("more than one JSON value")
	}
	if IndexResultForbiddenFields([]byte(output.String())) || strings.Contains(output.String(), "test-token") {
		t.Fatalf("sensitive output: %s", output.String())
	}
}
