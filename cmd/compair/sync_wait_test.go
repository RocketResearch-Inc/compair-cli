package compair

import (
	"errors"
	"os"
	"testing"
	"time"
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
