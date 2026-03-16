package compair

import (
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
)

func TestNotificationStatus(t *testing.T) {
	event := api.NotificationEvent{}
	if got := notificationStatus(event); got != "new" {
		t.Fatalf("expected new status, got %q", got)
	}

	event.AcknowledgedAt = "2026-03-13T12:00:00Z"
	if got := notificationStatus(event); got != "acknowledged" {
		t.Fatalf("expected acknowledged status, got %q", got)
	}

	event.DismissedAt = "2026-03-13T12:05:00Z"
	if got := notificationStatus(event); got != "dismissed" {
		t.Fatalf("expected dismissed status, got %q", got)
	}
}

func TestNonEmptyLines(t *testing.T) {
	lines := nonEmptyLines([]string{" first ", "", "   ", "second"})
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "first" || lines[1] != "second" {
		t.Fatalf("unexpected filtered lines: %#v", lines)
	}
}
