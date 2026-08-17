package baseline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	uploadTestJobID        = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	uploadTestContinuation = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	uploadTestCorpusID     = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	uploadTestGenerationID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
)

type uploadTestServer struct {
	t              *testing.T
	server         *httptest.Server
	mu             sync.Mutex
	requests       map[string][][]byte
	failFirst      map[string]bool
	failParts      bool
	receivedParts  int
	expectedParts  int
	continuations  []string
	continuationAt int
	wrongProtocol  bool
	authFailure    bool
	stageErrors    map[string]uploadTestHTTPError
}

type uploadTestHTTPError struct {
	status    int
	code      string
	retryable bool
}

func newUploadTestServer(t *testing.T) *uploadTestServer {
	t.Helper()
	fixture := &uploadTestServer{t: t, requests: make(map[string][][]byte), failFirst: make(map[string]bool), continuations: []string{"queued", "succeeded"}}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.handle))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *uploadTestServer) handle(writer http.ResponseWriter, request *http.Request) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if request.Method != http.MethodPost {
		fixture.t.Errorf("method = %s", request.Method)
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if request.Header.Get("Authorization") != "Bearer test-token" || request.Header.Get("auth-token") != "test-token" {
		fixture.t.Errorf("authentication headers missing")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		fixture.t.Error(err)
		return
	}
	stage := fixture.stage(request.URL.Path)
	fixture.requests[stage] = append(fixture.requests[stage], append([]byte(nil), body...))
	requestID := jsonString(body, "request_id")
	groupID := jsonString(body, "group_id")
	if fixture.authFailure {
		fixture.writeError(writer, requestID, http.StatusUnauthorized, "authentication_required", false)
		return
	}
	if configured, ok := fixture.stageErrors[stage]; ok {
		fixture.writeError(writer, requestID, configured.status, configured.code, configured.retryable)
		return
	}
	if fixture.failFirst[stage] && len(fixture.requests[stage]) == 1 {
		fixture.writeError(writer, requestID, http.StatusServiceUnavailable, "temporary_unavailable", true)
		return
	}
	if stage == "part" && fixture.failParts {
		fixture.writeError(writer, requestID, http.StatusServiceUnavailable, "temporary_unavailable", true)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	switch stage {
	case "capabilities":
		protocol := ControlProtocolVersion
		if fixture.wrongProtocol {
			protocol = "baseline-control-plane.v0"
		}
		fixture.writeJSON(writer, map[string]any{
			"protocol_version": protocol, "protocol_sha256": ControlProtocolSHA256, "message_type": "capabilities", "request_id": requestID, "group_id": groupID,
			"operations":           map[string]any{"snapshot_staging": "safe", "corpus_ingestion": "unavailable", "index_build": "unavailable", "baseline_run": "unavailable"},
			"transport":            map[string]any{"status": "local_override", "reason": "explicit_loopback_http_override", "encrypted": false, "local_override_enabled": true},
			"request_body_logging": false, "staging_is_corpus_eligible": false, "staging_is_index_eligible": false,
			"limits": map[string]any{"sibling_repositories": MaxSiblingRepositories, "file_records": MaxFileRecords, "file_bytes": MaxFileBytes, "supported_content_bytes": MaxSupportedBytes, "manifest_request_bytes": MaxManifestRequest, "content_part_request_bytes": MaxPartRequest, "content_part_bytes": MaxPartBytes, "content_part_items": MaxPartItems, "content_parts": MaxContentParts, "control_request_bytes": MaxControlRequest, "staging_lifetime_seconds": 86400},
		})
	case "begin":
		var value struct {
			Snapshot struct {
				Files []struct {
					Required bool  `json:"content_required"`
					ByteSize int64 `json:"byte_size"`
				} `json:"files"`
			} `json:"snapshot"`
		}
		_ = json.Unmarshal(body, &value)
		fixture.expectedParts = countContentPartsFromRequests(value.Snapshot.Files)
		fixture.writeJSON(writer, map[string]any{"protocol_version": ControlProtocolVersion, "protocol_sha256": ControlProtocolSHA256, "message_type": "job_accepted", "request_id": requestID, "group_id": groupID, "job_id": uploadTestJobID, "operation": "snapshot_ingest", "state": "queued", "replayed": len(fixture.requests[stage]) > 1})
	case "part":
		fixture.receivedParts++
		fixture.writeJSON(writer, fixture.jobStatus(requestID, groupID, "queued", nil, len(fixture.requests[stage]) > 1))
	case "commit":
		result := map[string]any{"snapshot_id": jsonString(body, "snapshot_id"), "staging_state": "sealed", "corpus_eligible": false, "index_eligible": false}
		fixture.writeJSON(writer, fixture.jobStatus(requestID, groupID, "succeeded", result, len(fixture.requests[stage]) > 1))
	case "continuation":
		state := fixture.continuations[min(fixture.continuationAt, len(fixture.continuations)-1)]
		fixture.continuationAt++
		fixture.writeJSON(writer, fixture.continuationStatus(requestID, groupID, state))
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (fixture *uploadTestServer) stage(path string) string {
	switch {
	case path == "/baseline/control/v1/capabilities":
		return "capabilities"
	case path == "/baseline/control/v1/snapshots":
		return "begin"
	case strings.HasSuffix(path, "/parts"):
		return "part"
	case strings.HasSuffix(path, "/commit"):
		return "commit"
	case path == "/baseline/control/v1/continuations/status":
		return "continuation"
	default:
		return "unknown"
	}
}

func (fixture *uploadTestServer) jobStatus(requestID, groupID, state string, result any, replayed bool) map[string]any {
	now := "2026-01-02T03:04:05Z"
	return map[string]any{
		"protocol_version": ControlProtocolVersion, "protocol_sha256": ControlProtocolSHA256, "message_type": "job_status", "request_id": requestID, "group_id": groupID,
		"job_id": uploadTestJobID, "operation": "snapshot_ingest", "state": state, "attempt": 0, "created_at": now, "updated_at": now,
		"progress": map[string]any{"completed": fixture.receivedParts, "total": fixture.expectedParts}, "result": result, "error_code": nil,
		"staging":  map[string]any{"state": map[bool]string{true: "sealed", false: "open"}[state == "succeeded"], "received_parts": fixture.receivedParts, "expected_parts": fixture.expectedParts, "expires_at": "2026-01-03T03:04:05Z", "corpus_eligible": false, "index_eligible": false},
		"replayed": replayed,
	}
}

func (fixture *uploadTestServer) continuationStatus(requestID, groupID, state string) map[string]any {
	now := "2026-01-02T03:04:05Z"
	succeeded := state == "succeeded"
	result := map[string]any{"snapshot_id": fixture.snapshotID(), "staging_state": "sealed", "corpus_ingestion_complete": succeeded, "corpus_eligible": succeeded, "index_eligible": false, "baseline_eligible": false, "index_state": map[bool]string{true: "incomplete", false: "unavailable"}[succeeded]}
	if succeeded {
		result["corpus_id"] = uploadTestCorpusID
		result["corpus_generation_id"] = uploadTestGenerationID
		result["corpus_generation_version"] = "corpus-generation.v1"
		result["corpus_manifest_hash"] = strings.Repeat("1", 64)
		result["corpus_provenance_fingerprint"] = strings.Repeat("2", 64)
		result["worker_contract_version"] = "baseline-snapshot-continuation.v1"
	}
	return map[string]any{
		"schema_version": "baseline-snapshot-continuation.v1", "message_type": "continuation_job_status", "request_id": requestID, "group_id": groupID,
		"staging_job_id": uploadTestJobID, "job_id": uploadTestContinuation, "operation": "sealed_snapshot_continue", "state": state, "attempt": 1, "created_at": now, "updated_at": now,
		"progress": map[string]any{"completed": map[bool]int{true: 1, false: 0}[succeeded], "total": 1}, "result": result, "error_code": nil,
		"staging":      map[string]any{"state": "sealed", "received_parts": fixture.receivedParts, "expected_parts": fixture.expectedParts, "expires_at": "2026-01-03T03:04:05Z", "corpus_eligible": false, "index_eligible": false},
		"continuation": map[string]any{"job_id": uploadTestContinuation, "operation": "sealed_snapshot_continue", "state": state, "attempt": 1, "created_at": now, "updated_at": now, "error_code": nil, "corpus_ingestion_complete": succeeded, "corpus_eligible": succeeded, "index_eligible": false, "baseline_eligible": false},
		"replayed":     false,
	}
}

func (fixture *uploadTestServer) snapshotID() string {
	requests := fixture.requests["begin"]
	if len(requests) == 0 {
		return ""
	}
	var value struct {
		Snapshot struct {
			SnapshotID string `json:"snapshot_id"`
		} `json:"snapshot"`
	}
	_ = json.Unmarshal(requests[len(requests)-1], &value)
	return value.Snapshot.SnapshotID
}

func (fixture *uploadTestServer) writeError(writer http.ResponseWriter, requestID string, status int, code string, retryable bool) {
	writer.WriteHeader(status)
	fixture.writeJSON(writer, map[string]any{"protocol_version": ControlProtocolVersion, "protocol_sha256": ControlProtocolSHA256, "message_type": "error", "request_id": requestID, "http_status": status, "stage": "snapshot", "retryable": retryable, "code": code})
}

func (fixture *uploadTestServer) writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		fixture.t.Error(err)
	}
}

func jsonString(body []byte, field string) string {
	var value map[string]any
	_ = json.Unmarshal(body, &value)
	result, _ := value[field].(string)
	return result
}

func countContentPartsFromRequests(files []struct {
	Required bool  `json:"content_required"`
	ByteSize int64 `json:"byte_size"`
}) int {
	parts, items, bytesInPart := 0, 0, int64(0)
	for _, file := range files {
		if !file.Required {
			continue
		}
		if parts == 0 || items >= MaxPartItems || bytesInPart+file.ByteSize > MaxPartBytes {
			parts++
			items = 0
			bytesInPart = 0
		}
		items++
		bytesInPart += file.ByteSize
	}
	return parts
}

func testUploadOptions(server *uploadTestServer, stateDirectory string) UploadOptions {
	return UploadOptions{
		BaseURL: server.server.URL, Token: "test-token", AllowLoopbackHTTP: true,
		Wait: true, Timeout: 5 * time.Second, PollInterval: time.Nanosecond,
		StateDirectory: stateDirectory, RetryAttempts: 3, RetryBaseDelay: time.Nanosecond,
		sleep: func(context.Context, time.Duration) error { return nil },
	}
}

func TestUploadBeginPartsCommitWaitAndLostResponseReplay(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	scan, err := NewScanner().Scan(context.Background(), input.GroupID, input)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.ClearProtected()
	server := newUploadTestServer(t)
	server.failFirst = map[string]bool{"begin": true, "part": true, "commit": true}
	stateDirectory := filepath.Join(t.TempDir(), "uploads")
	execution, err := RunUpload(context.Background(), input.GroupID, input, testUploadOptions(server, stateDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.State != "succeeded" || execution.Result.CorpusID != uploadTestCorpusID || execution.Result.CorpusGenerationID != uploadTestGenerationID || execution.Result.PartCompleted != len(scan.Parts) || !execution.Result.Replayed {
		t.Fatalf("unexpected result: %#v", execution.Result)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	var exactBytes int64
	for _, requests := range server.requests {
		for _, body := range requests {
			exactBytes += int64(len(body))
		}
	}
	if execution.Result.TransmittedRequestBytes != exactBytes {
		t.Fatalf("transmitted bytes = %d, want %d", execution.Result.TransmittedRequestBytes, exactBytes)
	}
	for _, stage := range []string{"begin", "part", "commit"} {
		if len(server.requests[stage]) < 2 || !bytes.Equal(server.requests[stage][0], server.requests[stage][1]) {
			t.Fatalf("%s replay did not preserve canonical request bytes", stage)
		}
	}
	if len(server.requests["begin"][0]) > scan.Report.ManifestRequestBytes || len(server.requests["commit"][0]) > scan.Report.CommitRequestBytes || len(server.requests["part"][0]) > scan.Report.Parts[0].RequestBytes {
		t.Fatal("a transmitted request exceeded its planned bound")
	}
	resultJSON, _ := json.Marshal(execution.Result)
	if bytes.Contains(resultJSON, []byte(toy.changedRoot)) || bytes.Contains(resultJSON, []byte(toy.siblingRoot)) || bytes.Contains(resultJSON, []byte("idempotency")) || bytes.Contains(resultJSON, []byte("content_utf8")) {
		t.Fatal("safe result leaked protected upload material")
	}
	entries, err := os.ReadDir(stateDirectory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("resume entries before finalize = %d, err = %v", len(entries), err)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("resume state mode = %o", info.Mode().Perm())
	}
	stateBytes, _ := os.ReadFile(filepath.Join(stateDirectory, entries[0].Name()))
	for _, forbidden := range []string{toy.changedRoot, toy.siblingRoot, "content_utf8", "idempotency_key", "test-token"} {
		if strings.Contains(string(stateBytes), forbidden) {
			t.Fatalf("resume state leaked %q", forbidden)
		}
	}
	server.mu.Unlock()
	if err := execution.Finalize(); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	entries, _ = os.ReadDir(stateDirectory)
	if len(entries) != 0 {
		t.Fatalf("successful resume state was not removed: %v", entries)
	}
}

func TestUploadInterruptedResumeUsesSameOpaqueIdentities(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	server := newUploadTestServer(t)
	server.failParts = true
	stateDirectory := filepath.Join(t.TempDir(), "uploads")
	options := testUploadOptions(server, stateDirectory)
	options.Wait = false
	options.RetryAttempts = 1
	first, firstErr := RunUpload(context.Background(), input.GroupID, input, options)
	if firstErr == nil || UploadFailure(firstErr) != UploadFailureRetryable || first.Result.State != "retryable_incomplete" {
		t.Fatalf("first interruption = %#v, %v", first.Result, firstErr)
	}
	server.mu.Lock()
	firstBegin := append([]byte(nil), server.requests["begin"][0]...)
	firstPart := append([]byte(nil), server.requests["part"][0]...)
	server.failParts = false
	server.mu.Unlock()
	options.Resume = true
	second, err := RunUpload(context.Background(), input.GroupID, input, options)
	if err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	if !bytes.Equal(firstBegin, server.requests["begin"][1]) || !bytes.Equal(firstPart, server.requests["part"][1]) {
		t.Fatal("resume did not recover the same begin/part opaque request identities")
	}
	server.mu.Unlock()
	if !second.Result.Resumed || !second.Result.Replayed || second.Result.State != "snapshot_committed" {
		t.Fatalf("resume result = %#v", second.Result)
	}
	if err := second.Finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadMultiPartOrderAndPlannedBounds(t *testing.T) {
	toy := newScannerToy(t)
	for index := 0; index < 6; index++ {
		filename := filepath.Join(toy.siblingRoot, fmt.Sprintf("bulk-%02d.txt", index))
		writeScannerFile(t, filename, bytes.Repeat([]byte{byte('a' + index)}, 190_000), 0o644)
	}
	gitTest(t, toy.siblingRoot, "add", "--", ".")
	gitTest(t, toy.siblingRoot, "commit", "-m", "multipart")
	input := toy.input()
	input.Siblings[0].RepositoryRevision = strings.TrimSpace(gitTest(t, toy.siblingRoot, "rev-parse", "HEAD"))
	scan, err := NewScanner().Scan(context.Background(), input.GroupID, input)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.ClearProtected()
	if len(scan.Parts) < 2 {
		t.Fatalf("toy corpus did not produce multiple parts: %d", len(scan.Parts))
	}
	server := newUploadTestServer(t)
	options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
	options.Wait = false
	execution, err := RunUpload(context.Background(), input.GroupID, input, options)
	if err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	if len(server.requests["part"]) != len(scan.Parts) {
		t.Fatalf("uploaded parts = %d, want %d", len(server.requests["part"]), len(scan.Parts))
	}
	for index, request := range server.requests["part"] {
		var payload struct {
			Ordinal int `json:"part_ordinal"`
		}
		if err := json.Unmarshal(request, &payload); err != nil || payload.Ordinal != index+1 {
			t.Fatalf("part %d order invalid: %#v, %v", index, payload, err)
		}
		if len(request) > scan.Report.Parts[index].RequestBytes {
			t.Fatalf("part %d exceeded planned request bytes", index+1)
		}
	}
	server.mu.Unlock()
	if execution.Result.PartCompleted != len(scan.Parts) || execution.Result.PartTotal != len(scan.Parts) {
		t.Fatalf("multipart result = %#v", execution.Result)
	}
	if err := execution.Finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadCapabilityTransportAuthenticationAndTerminalStates(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	stateRoot := filepath.Join(t.TempDir(), "uploads")
	server := newUploadTestServer(t)
	server.wrongProtocol = true
	_, err := RunUpload(context.Background(), input.GroupID, input, testUploadOptions(server, stateRoot))
	if UploadFailure(err) != UploadFailureContract || SafeUploadReason(err) != "capability_protocol_mismatch" {
		t.Fatalf("wrong protocol error = %v", err)
	}
	if len(server.requests["begin"]) != 0 {
		t.Fatal("capability rejection wrote a snapshot")
	}
	if _, err := NewControlClient(server.server.URL, "test-token", false); UploadFailure(err) != UploadFailureContract {
		t.Fatalf("implicit loopback HTTP was accepted: %v", err)
	}
	if _, err := NewControlClient("http://example.test", "test-token", true); UploadFailure(err) != UploadFailureContract {
		t.Fatalf("remote plaintext HTTP was accepted: %v", err)
	}
	if _, err := NewControlClient(server.server.URL, "", true); UploadFailure(err) != UploadFailureAuthentication {
		t.Fatalf("missing authentication was accepted: %v", err)
	}

	terminalServer := newUploadTestServer(t)
	terminalServer.continuations = []string{"blocked"}
	execution, terminalErr := RunUpload(context.Background(), input.GroupID, input, testUploadOptions(terminalServer, filepath.Join(t.TempDir(), "uploads")))
	if UploadFailure(terminalErr) != UploadFailureTerminal || execution.Result.State != "terminal_failed" || execution.Result.ReasonCode != "continuation_blocked" {
		t.Fatalf("blocked result = %#v, err = %v", execution.Result, terminalErr)
	}
	if err := execution.Finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadStateCorruptionAndSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	stateDirectory := filepath.Join(root, "uploads")
	store, err := newUploadStateStore(stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	secretInfo, err := os.Stat(filepath.Join(filepath.Dir(stateDirectory), "baseline-upload-install-secret.v1"))
	if err != nil || secretInfo.Mode().Perm() != 0o600 || secretInfo.Size() != installSecretBytes {
		t.Fatalf("install secret metadata = %#v, %v", secretInfo, err)
	}
	state := &uploadState{SchemaVersion: uploadStateSchemaVersion, GroupID: "group", IntegrityHMACSHA256: ""}
	if err := store.save(strings.Repeat("a", 64), state); err != nil {
		t.Fatal(err)
	}
	filename := store.path(strings.Repeat("a", 64))
	value, _ := os.ReadFile(filename)
	value[len(value)/2] ^= 1
	if err := os.WriteFile(filename, value, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.load(strings.Repeat("a", 64)); UploadFailure(err) != UploadFailureContract {
		t.Fatalf("corrupt state was accepted: %v", err)
	}

	symlinkRoot := t.TempDir()
	target := filepath.Join(symlinkRoot, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(symlinkRoot, "uploads")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := newUploadStateStore(link); UploadFailure(err) != UploadFailureContract {
		t.Fatalf("symlink state root was accepted: %v", err)
	}
}

func TestUploadRescanMismatchMissingObjectConflictExpiryAndTimeout(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	stateDirectory := filepath.Join(t.TempDir(), "uploads")
	server := newUploadTestServer(t)
	server.failParts = true
	options := testUploadOptions(server, stateDirectory)
	options.Wait = false
	options.RetryAttempts = 1
	if _, err := RunUpload(context.Background(), input.GroupID, input, options); UploadFailure(err) != UploadFailureRetryable {
		t.Fatalf("interruption = %v", err)
	}
	store, err := newUploadStateStore(stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(stateDirectory)
	identity := strings.TrimSuffix(entries[0].Name(), ".json")
	state, err := store.load(identity)
	if err != nil {
		t.Fatal(err)
	}
	state.ScanFingerprint = strings.Repeat("f", 64)
	if err := store.save(identity, state); err != nil {
		t.Fatal(err)
	}
	server.failParts = false
	options.Resume = true
	if _, err := RunUpload(context.Background(), input.GroupID, input, options); UploadFailure(err) != UploadFailureRepository || SafeUploadReason(err) != "resume_rescan_mismatch" {
		t.Fatalf("rescan mismatch = %v", err)
	}

	missing := input
	missing.Siblings = append([]SiblingRepositoryInput(nil), input.Siblings...)
	missing.Siblings[0].RepositoryRevision = strings.Repeat("f", 40)
	missingOptions := testUploadOptions(newUploadTestServer(t), filepath.Join(t.TempDir(), "uploads"))
	missingOptions.Wait = false
	if _, err := RunUpload(context.Background(), missing.GroupID, missing, missingOptions); UploadFailure(err) != UploadFailureRepository {
		t.Fatalf("missing immutable object = %v", err)
	}

	for _, test := range []struct {
		name       string
		stage      string
		serverCode string
		wantKind   UploadFailureKind
	}{
		{name: "idempotency conflict", stage: "begin", serverCode: "idempotency_conflict", wantKind: UploadFailureContract},
		{name: "part hash conflict", stage: "part", serverCode: "content_hash_mismatch", wantKind: UploadFailureContract},
		{name: "commit conflict", stage: "commit", serverCode: "commit_conflict", wantKind: UploadFailureContract},
		{name: "expired staging", stage: "commit", serverCode: "staging_expired", wantKind: UploadFailureTerminal},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUploadTestServer(t)
			fixture.stageErrors = map[string]uploadTestHTTPError{test.stage: {status: http.StatusConflict, code: test.serverCode}}
			options := testUploadOptions(fixture, filepath.Join(t.TempDir(), "uploads"))
			options.Wait = false
			execution, err := RunUpload(context.Background(), input.GroupID, input, options)
			if UploadFailure(err) != test.wantKind || execution.Result.ReasonCode != test.serverCode {
				t.Fatalf("result = %#v, error = %v", execution.Result, err)
			}
			if err := execution.Finalize(); err != nil {
				t.Fatal(err)
			}
		})
	}

	timeoutServer := newUploadTestServer(t)
	timeoutServer.continuations = []string{"queued"}
	timeoutOptions := testUploadOptions(timeoutServer, filepath.Join(t.TempDir(), "uploads"))
	timeoutOptions.Timeout = 2 * time.Second
	timeoutOptions.PollInterval = 10 * time.Second
	timeoutOptions.sleep = sleepContext
	execution, err := RunUpload(context.Background(), input.GroupID, input, timeoutOptions)
	if UploadFailure(err) != UploadFailureRetryable || execution.Result.State != "retryable_incomplete" || execution.Result.ReasonCode != "wait_timeout" {
		t.Fatalf("timeout result = %#v, error = %v", execution.Result, err)
	}
}

func TestUploadDirtyWorktreeDoesNotChangeImmutableUpload(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	writeScannerFile(t, filepath.Join(toy.siblingRoot, "supported.txt"), []byte("dirty-private-change\n"), 0o644)
	writeScannerFile(t, filepath.Join(toy.siblingRoot, "untracked-private.txt"), []byte("untracked-private\n"), 0o644)
	server := newUploadTestServer(t)
	options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
	options.Wait = false
	execution, err := RunUpload(context.Background(), input.GroupID, input, options)
	if err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	requests := append([][]byte(nil), server.requests["part"]...)
	server.mu.Unlock()
	for _, body := range requests {
		if bytes.Contains(body, []byte("dirty-private-change")) || bytes.Contains(body, []byte("untracked-private")) {
			t.Fatal("dirty worktree bytes entered immutable upload")
		}
	}
	if err := execution.Finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadResumeStatePartOrderingIsDeterministic(t *testing.T) {
	parts := []uploadStatePart{{Ordinal: 3}, {Ordinal: 1}, {Ordinal: 2}}
	ordered := sortedParts(parts)
	for index := range ordered {
		if ordered[index].Ordinal != index+1 {
			t.Fatalf("ordered parts = %#v", ordered)
		}
	}
	fields := StableUploadJSONFields()
	if len(fields) != 23 || fields[0] != "schema_version" || fields[len(fields)-1] != "reason_code" {
		t.Fatalf("unexpected upload output contract: %v", fields)
	}
	for _, field := range fields {
		if containsSensitiveUploadField(field) {
			t.Fatalf("sensitive output field %s", field)
		}
	}
}
