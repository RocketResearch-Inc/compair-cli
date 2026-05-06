package compair

import (
	"testing"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
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

func TestCollectNotificationGateResultSkipsDropEvents(t *testing.T) {
	t.Parallel()

	prevSeverities := syncFailOnSeverity
	prevTypes := syncFailOnType
	syncFailOnSeverity = []string{"high"}
	syncFailOnType = []string{"potential_conflict"}
	t.Cleanup(func() {
		syncFailOnSeverity = prevSeverities
		syncFailOnType = prevTypes
	})

	now := time.Now()
	result := collectNotificationGateResult([]api.NotificationEvent{
		{
			EventID:        "evt_drop",
			TargetDocID:    "doc_1",
			Severity:       "high",
			Intent:         "potential_conflict",
			DeliveryAction: "drop",
			CreatedAt:      now.Format(time.RFC3339),
		},
		{
			EventID:        "evt_keep",
			TargetDocID:    "doc_1",
			Severity:       "high",
			Intent:         "potential_conflict",
			DeliveryAction: "push",
			CreatedAt:      now.Format(time.RFC3339),
		},
	}, notificationGateResult{Enabled: true}, map[string]struct{}{"doc_1": {}}, now.Add(-time.Minute))

	if result.ConsideredCount != 1 {
		t.Fatalf("expected only non-drop events to be considered, got %d", result.ConsideredCount)
	}
	if result.MatchedCount != 1 {
		t.Fatalf("expected only non-drop events to match, got %d", result.MatchedCount)
	}
}
