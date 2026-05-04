package compair

import (
	"testing"
	"time"
)

func TestNotificationGateWaitBudgetRequiresNewFetchedUploadWork(t *testing.T) {
	t.Parallel()

	prevFeedbackWait := feedbackWaitSec
	prevSeverities := syncFailOnSeverity
	prevTypes := syncFailOnType
	feedbackWaitSec = 45
	syncFailOnSeverity = []string{"high"}
	syncFailOnType = []string{"potential_conflict"}
	t.Cleanup(func() {
		feedbackWaitSec = prevFeedbackWait
		syncFailOnSeverity = prevSeverities
		syncFailOnType = prevTypes
	})

	if got := notificationGateWaitBudget(false, true, 1); got != 0 {
		t.Fatalf("expected no wait budget without upload, got %s", got)
	}
	if got := notificationGateWaitBudget(true, false, 1); got != 0 {
		t.Fatalf("expected no wait budget without fetch, got %s", got)
	}
	if got := notificationGateWaitBudget(true, true, 0); got != 0 {
		t.Fatalf("expected no wait budget without updated docs, got %s", got)
	}
	if got := notificationGateWaitBudget(true, true, 2); got != 60*time.Second {
		t.Fatalf("expected 60s wait budget for fresh uploaded work, got %s", got)
	}
}
