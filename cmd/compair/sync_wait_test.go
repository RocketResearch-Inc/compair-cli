package compair

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
)

type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

func TestIsRetryableStatusPollError(t *testing.T) {
	if !isRetryableStatusPollError(timeoutNetErr{}) {
		t.Fatal("expected timeout net error to be retryable")
	}
	if !isRetryableStatusPollError(errors.New("read: operation timed out")) {
		t.Fatal("expected operation timed out error to be retryable")
	}
	if isRetryableStatusPollError(errors.New("unauthorized")) {
		t.Fatal("did not expect unauthorized error to be retryable")
	}
}

func TestIsPendingRepoTaskStale(t *testing.T) {
	oldEnv := os.Getenv("COMPAIR_PENDING_TASK_STALE_AFTER_SEC")
	t.Cleanup(func() {
		if oldEnv == "" {
			_ = os.Unsetenv("COMPAIR_PENDING_TASK_STALE_AFTER_SEC")
		} else {
			_ = os.Setenv("COMPAIR_PENDING_TASK_STALE_AFTER_SEC", oldEnv)
		}
	})
	_ = os.Setenv("COMPAIR_PENDING_TASK_STALE_AFTER_SEC", "60")

	stale, age, cutoff := isPendingRepoTaskStale(time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339))
	if !stale {
		t.Fatal("expected task to be stale")
	}
	if age < time.Minute || cutoff != time.Minute {
		t.Fatalf("unexpected age/cutoff: age=%s cutoff=%s", age, cutoff)
	}

	stale, _, _ = isPendingRepoTaskStale(time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339))
	if stale {
		t.Fatal("did not expect recent task to be stale")
	}
}

func TestTaskProgressRemaining(t *testing.T) {
	progress := taskProgressMeta{
		Stage:              "indexing",
		IndexedChunksDone:  25,
		IndexedChunksTotal: 100,
	}
	remaining := taskProgressRemaining(progress, 50*time.Second)
	if remaining < 149*time.Second || remaining > 151*time.Second {
		t.Fatalf("unexpected remaining estimate: %s", remaining)
	}
}

func TestFormatTaskProgressLineIncludesDetailAndETA(t *testing.T) {
	st := api.TaskStatus{
		Status: "PROGRESS",
		Meta: map[string]any{
			"stage":                "indexing",
			"indexed_chunks_done":  10,
			"indexed_chunks_total": 40,
		},
	}
	line := formatTaskProgressLine(1, 2, "Still processing", "example/repo", st, 20*time.Second)
	if !strings.Contains(line, "indexing 10/40 chunk(s) (25%)") {
		t.Fatalf("expected indexing detail in progress line, got %q", line)
	}
	if !strings.Contains(line, "est.") {
		t.Fatalf("expected ETA in progress line, got %q", line)
	}
}

func TestFormatTaskProgressLineLabelsBarePendingAsQueued(t *testing.T) {
	st := api.TaskStatus{Status: "PENDING"}
	line := formatTaskProgressLine(1, 1, "Still processing", "example/repo", st, 20*time.Second)
	if !strings.Contains(line, "queued on server; waiting for worker progress") {
		t.Fatalf("expected pending queue detail in progress line, got %q", line)
	}
}

func TestPendingStatusStaleAfterCanBeDisabled(t *testing.T) {
	oldEnv := os.Getenv("COMPAIR_PENDING_STATUS_STALE_AFTER_SEC")
	t.Cleanup(func() {
		if oldEnv == "" {
			_ = os.Unsetenv("COMPAIR_PENDING_STATUS_STALE_AFTER_SEC")
		} else {
			_ = os.Setenv("COMPAIR_PENDING_STATUS_STALE_AFTER_SEC", oldEnv)
		}
	})
	_ = os.Setenv("COMPAIR_PENDING_STATUS_STALE_AFTER_SEC", "0")
	if got := pendingStatusStaleAfter(); got != 0 {
		t.Fatalf("expected disabled pending cutoff, got %s", got)
	}
}

func TestIsTaskProgressStaleUsesLastProgressAt(t *testing.T) {
	oldEnv := os.Getenv("COMPAIR_PROCESS_PROGRESS_STALE_AFTER_SEC")
	t.Cleanup(func() {
		if oldEnv == "" {
			_ = os.Unsetenv("COMPAIR_PROCESS_PROGRESS_STALE_AFTER_SEC")
		} else {
			_ = os.Setenv("COMPAIR_PROCESS_PROGRESS_STALE_AFTER_SEC", oldEnv)
		}
	})
	_ = os.Setenv("COMPAIR_PROCESS_PROGRESS_STALE_AFTER_SEC", "60")

	progress := taskProgressMeta{
		StartedAt:      time.Now().Add(-10 * time.Minute),
		LastProgressAt: time.Now().Add(-2 * time.Minute),
	}
	stale, age, cutoff := isTaskProgressStale(progress, time.Now().Add(-10*time.Minute))
	if !stale {
		t.Fatal("expected progress to be stale")
	}
	if age < time.Minute || cutoff != time.Minute {
		t.Fatalf("unexpected age/cutoff: age=%s cutoff=%s", age, cutoff)
	}
}

func TestHasNewFeedbackDetectsReplacementIDsAtSameCount(t *testing.T) {
	baseline := feedbackSnapshot{
		Count: 1,
		IDs: map[string]struct{}{
			"old-feedback": {},
		},
		LatestTimestamp: time.Date(2026, time.April, 14, 12, 0, 0, 0, time.UTC),
	}
	fbs := []api.FeedbackEntry{
		{FeedbackID: "new-feedback", Timestamp: "2026-04-14T12:05:00Z"},
	}
	if !hasNewFeedback(fbs, baseline) {
		t.Fatal("expected replacement feedback id at same count to be detected as new")
	}
}

func TestHasNewFeedbackDetectsNewerTimestampAtSameCount(t *testing.T) {
	baseline := feedbackSnapshot{
		Count:           1,
		LatestTimestamp: time.Date(2026, time.April, 14, 12, 0, 0, 0, time.UTC),
	}
	fbs := []api.FeedbackEntry{
		{FeedbackID: "same-slot", Timestamp: "2026-04-14T12:05:00Z"},
	}
	if !hasNewFeedback(fbs, baseline) {
		t.Fatal("expected newer feedback timestamp at same count to be detected as new")
	}
}

func TestShouldWaitForPendingInitialSyncs(t *testing.T) {
	tests := []struct {
		name             string
		doUpload         bool
		generateFeedback bool
		mode             syncInvocationMode
		want             bool
	}{
		{
			name:             "normal feedback-generating upload waits",
			doUpload:         true,
			generateFeedback: true,
			want:             true,
		},
		{
			name:             "detached review still waits before submit",
			doUpload:         true,
			generateFeedback: true,
			mode:             syncInvocationMode{Detach: true},
			want:             true,
		},
		{
			name:             "no upload means no wait",
			doUpload:         false,
			generateFeedback: true,
			want:             false,
		},
		{
			name:             "review now push skips per-chunk feedback wait",
			doUpload:         true,
			generateFeedback: false,
			mode:             syncInvocationMode{PushOnly: true},
			want:             false,
		},
		{
			name:             "explicit skip disables wait",
			doUpload:         true,
			generateFeedback: true,
			mode:             syncInvocationMode{SkipInitialSyncWait: true},
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldWaitForPendingInitialSyncs(tt.doUpload, tt.generateFeedback, tt.mode); got != tt.want {
				t.Fatalf("shouldWaitForPendingInitialSyncs(%v, %v, %+v) = %v, want %v", tt.doUpload, tt.generateFeedback, tt.mode, got, tt.want)
			}
		})
	}
}
