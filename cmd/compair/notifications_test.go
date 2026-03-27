package compair

import (
	"strings"
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

func TestRenderNotificationEventsMarkdownIncludesEvidenceAndSummary(t *testing.T) {
	md := renderNotificationEventsMarkdown([]api.NotificationEvent{
		{
			EventID:        "evt_123",
			Severity:       "high",
			Intent:         "potential_conflict",
			TargetDocID:    "doc_target",
			PeerDocIDs:     []string{"doc_peer"},
			DeliveryAction: "digest",
			Channel:        "email_digest",
			Rationale:      []string{"Mismatch appears real."},
			EvidenceTarget: "COMPAIR_TELEMETRY_BASE_URL",
			EvidencePeer:   "COMPAIR_TELEMETRY_BASE",
			CreatedAt:      "2026-03-26T03:21:10Z",
		},
	}, false)

	for _, want := range []string{
		"## Summary",
		"## Notification 1",
		"**Target Evidence**",
		"COMPAIR_TELEMETRY_BASE_URL",
		"**Peer Evidence**",
		"COMPAIR_TELEMETRY_BASE",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected markdown to contain %q, got:\n%s", want, md)
		}
	}
}

func TestParseNotificationToggle(t *testing.T) {
	value, err := parseNotificationToggle("push", "on")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !value {
		t.Fatalf("expected toggle to parse as true")
	}
	if _, err := parseNotificationToggle("push", "maybe"); err == nil {
		t.Fatalf("expected invalid toggle value to fail")
	}
}

func TestRenderNotificationPreferencesMarkdownIncludesDeliveryNote(t *testing.T) {
	md := renderNotificationPreferencesMarkdown(api.NotificationPreferences{
		EmailDigestEnabled:       false,
		EmailDigestFrequency:     "daily",
		PushNotificationsEnabled: false,
		DigestBucketsEnabled:     []string{"conflicts", "updates"},
		QuietHoursStart:          "22:00",
		QuietHoursEnd:            "07:00",
		MaxDailyPushEmails:       1,
	}, false)

	for _, want := range []string{
		"Hosted instant email alerts: off.",
		"Email digests: off (daily).",
		"conflicts, updates",
		"22:00 -> 07:00",
		"hosted transactional delivery is not",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected markdown to contain %q, got:\n%s", want, md)
		}
	}
}
