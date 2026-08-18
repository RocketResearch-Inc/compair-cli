package baseline

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadReplayedOpenStagingContinuesNormally(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	server := newUploadTestServer(t)
	server.forceReplay = true
	options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
	options.Wait = false

	execution, err := RunUpload(context.Background(), input.GroupID, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.State != "snapshot_committed" || !execution.Result.Replayed || execution.Result.Resumed {
		t.Fatalf("open replay result = %#v", execution.Result)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.requests["status"]) != 1 || len(server.requests["part"]) != execution.Result.PartTotal || len(server.requests["commit"]) != 1 {
		t.Fatalf("open replay requests = %#v", requestCounts(server.requests))
	}
}

func TestUploadReplayedOpenStagingAdoptsAcceptedPartPrefix(t *testing.T) {
	toy := newScannerToy(t)
	for index := 0; index < 6; index++ {
		writeScannerFile(t, filepath.Join(toy.siblingRoot, "replay-"+string(rune('a'+index))+".txt"), []byte(strings.Repeat(string(rune('a'+index)), 190_000)), 0o644)
	}
	gitTest(t, toy.siblingRoot, "add", "--", ".")
	gitTest(t, toy.siblingRoot, "commit", "-m", "multipart replay")
	input := toy.input()
	input.Siblings[0].RepositoryRevision = strings.TrimSpace(gitTest(t, toy.siblingRoot, "rev-parse", "HEAD"))
	server := newUploadTestServer(t)
	server.forceReplay = true
	server.receivedParts = 1
	options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
	options.Wait = false

	execution, err := RunUpload(context.Background(), input.GroupID, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.PartTotal < 2 || execution.Result.PartCompleted != execution.Result.PartTotal {
		t.Fatalf("partial replay result = %#v", execution.Result)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.requests["part"]) != execution.Result.PartTotal-1 {
		t.Fatalf("replayed part requests = %d, want %d", len(server.requests["part"]), execution.Result.PartTotal-1)
	}
	var first struct {
		Ordinal int `json:"part_ordinal"`
	}
	if err := json.Unmarshal(server.requests["part"][0], &first); err != nil || first.Ordinal != 2 {
		t.Fatalf("first replayed part = %#v, %v", first, err)
	}
}

func TestUploadSealedReplayRecoversQueuedRunningAndSucceededContinuation(t *testing.T) {
	for _, state := range []string{"queued", "running", "retryable_failed", "succeeded"} {
		t.Run(state, func(t *testing.T) {
			toy := newScannerToy(t)
			input := toy.input()
			server := newUploadTestServer(t)
			server.forceReplay = true
			server.stagingState = "sealed"
			server.continuations = []string{state}
			options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
			options.Wait = false

			execution, err := RunUpload(context.Background(), input.GroupID, input, options)
			if err != nil {
				t.Fatal(err)
			}
			wantState := "snapshot_committed"
			if state == "succeeded" {
				wantState = "succeeded"
			}
			if execution.Result.State != wantState || execution.Result.StagingJobID != uploadTestJobID || execution.Result.ContinuationJobID != uploadTestContinuation || !execution.Result.Replayed || execution.Result.Resumed {
				t.Fatalf("%s replay result = %#v", state, execution.Result)
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			if len(server.requests["part"]) != 0 || len(server.requests["status"]) != 1 || len(server.requests["commit"]) != 1 || len(server.requests["continuation"]) != 1 {
				t.Fatalf("%s replay requests = %#v", state, requestCounts(server.requests))
			}
		})
	}
}

func TestUploadSealedReplayTerminalContinuationFailsClosed(t *testing.T) {
	for _, state := range []string{"terminal_failed", "blocked", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			toy := newScannerToy(t)
			input := toy.input()
			server := newUploadTestServer(t)
			server.forceReplay = true
			server.stagingState = "sealed"
			server.continuations = []string{state}
			options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
			options.Wait = false

			execution, err := RunUpload(context.Background(), input.GroupID, input, options)
			if UploadFailure(err) != UploadFailureTerminal || SafeUploadReason(err) != "continuation_"+state || execution.Result.ReasonCode != "continuation_"+state {
				t.Fatalf("%s continuation result = %#v, %v", state, execution.Result, err)
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			if len(server.requests["part"]) != 0 || len(server.requests["continuation"]) != 1 {
				t.Fatalf("%s continuation requests = %#v", state, requestCounts(server.requests))
			}
		})
	}
}

func TestUploadCompletedExactReplayReturnsOriginalResultAndInvocationCounts(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	server := newUploadTestServer(t)
	server.continuations = []string{"succeeded"}
	stateDirectory := filepath.Join(t.TempDir(), "uploads")
	options := testUploadOptions(server, stateDirectory)

	first, err := RunUpload(context.Background(), input.GroupID, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Finalize(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(stateDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("completed local state remains: %v, %v", entries, err)
	}
	server.mu.Lock()
	before := requestOffsets(server.requests)
	partsBefore := len(server.requests["part"])
	server.mu.Unlock()

	second, err := RunUpload(context.Background(), input.GroupID, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Result.StagingJobID != first.Result.StagingJobID || second.Result.ContinuationJobID != first.Result.ContinuationJobID || second.Result.CorpusID != first.Result.CorpusID || second.Result.CorpusGenerationID != first.Result.CorpusGenerationID || second.Result.CorpusGenerationVersion != first.Result.CorpusGenerationVersion || !second.Result.Replayed || second.Result.Resumed {
		t.Fatalf("completed replay identities differ: first=%#v second=%#v", first.Result, second.Result)
	}
	server.mu.Lock()
	deltaCount, deltaBytes := requestDelta(server.requests, before)
	partsAfter := len(server.requests["part"])
	server.mu.Unlock()
	if partsAfter != partsBefore {
		t.Fatalf("sealed replay transmitted content parts: before=%d after=%d", partsBefore, partsAfter)
	}
	if second.Result.TransmittedRequestCount != deltaCount || second.Result.TransmittedRequestBytes != deltaBytes {
		t.Fatalf("invocation counters = (%d,%d), want (%d,%d)", second.Result.TransmittedRequestCount, second.Result.TransmittedRequestBytes, deltaCount, deltaBytes)
	}
	if err := second.Finalize(); err != nil {
		t.Fatal(err)
	}
	options.Resume = true
	beginBefore := requestCount(server, "begin")
	missing, missingErr := RunUpload(context.Background(), input.GroupID, input, options)
	if UploadFailure(missingErr) != UploadFailureContract || SafeUploadReason(missingErr) != "resume_state_not_found" || missing.Result.Resumed {
		t.Fatalf("missing completed resume state = %#v, %v", missing.Result, missingErr)
	}
	if requestCount(server, "begin") != beginBefore {
		t.Fatal("--resume without retained state submitted a new begin")
	}
}

func TestUploadOpenToSealedRaceRecoversOnce(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	server := newUploadTestServer(t)
	server.forceReplay = true
	server.raceSealOnPart = true
	server.continuations = []string{"succeeded"}
	options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
	options.Wait = false

	execution, err := RunUpload(context.Background(), input.GroupID, input, options)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.State != "succeeded" || !execution.Result.Replayed {
		t.Fatalf("race replay result = %#v", execution.Result)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.requests["status"]) != 2 || len(server.requests["part"]) != 1 || len(server.requests["commit"]) != 1 {
		t.Fatalf("race refresh requests = %#v", requestCounts(server.requests))
	}
}

func TestUploadOpenToSealedRaceMismatchFailsClosed(t *testing.T) {
	toy := newScannerToy(t)
	input := toy.input()
	server := newUploadTestServer(t)
	server.forceReplay = true
	server.raceSealOnPart = true
	server.raceMismatch = true
	options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
	options.Wait = false

	execution, err := RunUpload(context.Background(), input.GroupID, input, options)
	if UploadFailure(err) != UploadFailureTerminal || SafeUploadReason(err) != "staging_not_open" || execution.Result.ReasonCode != "staging_not_open" {
		t.Fatalf("mismatched race result = %#v, %v", execution.Result, err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.requests["status"]) != 2 || len(server.requests["part"]) != 1 || len(server.requests["commit"]) != 0 || len(server.requests["continuation"]) != 0 {
		t.Fatalf("mismatched race requests = %#v", requestCounts(server.requests))
	}
}

func TestUploadReplayTerminalUnauthorizedAndContradictoryStatesFailSafely(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*uploadTestServer)
		wantKind   UploadFailureKind
		wantReason string
	}{
		{"terminal", func(server *uploadTestServer) { server.stagingJobState = "terminal_failed" }, UploadFailureTerminal, "snapshot_terminal_failed"},
		{"cancelled", func(server *uploadTestServer) { server.stagingJobState = "cancelled" }, UploadFailureTerminal, "snapshot_cancelled"},
		{"expired", func(server *uploadTestServer) {
			server.stagingState = "expired"
			server.stagingJobState = "terminal_failed"
		}, UploadFailureTerminal, "staging_expired"},
		{"unauthorized", func(server *uploadTestServer) {
			server.stageErrors = map[string]uploadTestHTTPError{"status": {status: http.StatusUnauthorized, code: "authentication_required"}}
		}, UploadFailureAuthentication, "authentication_required"},
		{"contradictory", func(server *uploadTestServer) {
			server.stagingState = "sealed"
			server.stagingJobState = "queued"
			server.preserveJobState = true
		}, UploadFailureContract, "replay_status_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			toy := newScannerToy(t)
			input := toy.input()
			server := newUploadTestServer(t)
			server.forceReplay = true
			test.configure(server)
			options := testUploadOptions(server, filepath.Join(t.TempDir(), "uploads"))
			options.Wait = false
			execution, err := RunUpload(context.Background(), input.GroupID, input, options)
			if UploadFailure(err) != test.wantKind || SafeUploadReason(err) != test.wantReason || execution.Result.ReasonCode != test.wantReason {
				t.Fatalf("%s result = %#v, %v", test.name, execution.Result, err)
			}
			encoded, _ := json.Marshal(execution.Result)
			for _, forbidden := range []string{toy.changedRoot, toy.siblingRoot, "test-token", "idempotency_key", "content_utf8", "integrity_hmac", "install_secret"} {
				if strings.Contains(string(encoded)+err.Error(), forbidden) {
					t.Fatalf("%s failure leaked %q", test.name, forbidden)
				}
			}
		})
	}
}

func requestOffsets(requests map[string][][]byte) map[string]int {
	result := make(map[string]int, len(requests))
	for stage, values := range requests {
		result[stage] = len(values)
	}
	return result
}

func requestDelta(requests map[string][][]byte, offsets map[string]int) (int, int64) {
	count := 0
	var bytes int64
	for stage, values := range requests {
		start := offsets[stage]
		for _, body := range values[start:] {
			count++
			bytes += int64(len(body))
		}
	}
	return count, bytes
}

func requestCounts(requests map[string][][]byte) map[string]int {
	result := make(map[string]int, len(requests))
	for stage, values := range requests {
		result[stage] = len(values)
	}
	return result
}

func requestCount(server *uploadTestServer, stage string) int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return len(server.requests[stage])
}
