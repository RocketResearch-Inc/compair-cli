package compair

import (
	"strings"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/spf13/viper"
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
	prevVerbose := viper.GetBool("verbose")
	viper.Set("verbose", false)
	defer viper.Set("verbose", prevVerbose)

	md := renderNotificationEventsMarkdown([]api.NotificationEvent{
		{
			EventID:        "evt_123",
			Severity:       "high",
			Intent:         "potential_conflict",
			TargetDocID:    "doc_target",
			PeerDocIDs:     []string{"doc_peer"},
			ParseMode:      "json",
			Model:          "gpt-5",
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
	for _, unwanted := range []string{
		"**Event ID:**",
		"**Target Doc:**",
		"**Scoring Parse Mode:**",
		"**Peer Docs:**",
	} {
		if strings.Contains(md, unwanted) {
			t.Fatalf("did not expect markdown to contain %q without verbose mode, got:\n%s", unwanted, md)
		}
	}
}

func TestRenderNotificationEventsMarkdownIncludesDebugMetadataWhenVerbose(t *testing.T) {
	prevVerbose := viper.GetBool("verbose")
	viper.Set("verbose", true)
	defer viper.Set("verbose", prevVerbose)

	md := renderNotificationEventsMarkdown([]api.NotificationEvent{
		{
			EventID:     "evt_123",
			Severity:    "high",
			Intent:      "potential_conflict",
			TargetDocID: "doc_target",
			PeerDocIDs:  []string{"doc_peer"},
			ParseMode:   "json",
			Model:       "gpt-5",
			CreatedAt:   "2026-03-26T03:21:10Z",
		},
	}, false)

	for _, want := range []string{
		"**Event ID:** `evt_123`",
		"**Target Doc:** `doc_target`",
		"**Scoring Parse Mode:** json",
		"**Peer Docs:** `doc_peer`",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected verbose markdown to contain %q, got:\n%s", want, md)
		}
	}
	if strings.Contains(md, "gpt-5") {
		t.Fatalf("did not expect verbose markdown to expose backend model names, got:\n%s", md)
	}
}

func TestRenderNotificationEventsMarkdownNormalizesBulletRationale(t *testing.T) {
	prevVerbose := viper.GetBool("verbose")
	viper.Set("verbose", false)
	defer viper.Set("verbose", prevVerbose)

	md := renderNotificationEventsMarkdown([]api.NotificationEvent{
		{
			Severity:  "medium",
			Intent:    "relevant_update",
			CreatedAt: "2026-05-05T03:21:10Z",
			Rationale: []string{
				"•",
				"• Both excerpts discuss release automation coverage.",
				"- The peer reinforces parts of the target rather than disagreeing.",
				"Generated feedback describes a mismatch/drift, so this is not treated as benign overlap.",
			},
		},
	}, false)

	if strings.Contains(md, "\n- •") || strings.Contains(md, "\n- -") {
		t.Fatalf("expected normalized rationale bullets, got:\n%s", md)
	}
	for _, want := range []string{
		"- Both excerpts discuss release automation coverage.",
		"- The peer reinforces parts of the target rather than disagreeing.",
		"- Generated feedback describes a mismatch/drift, so this is not treated as benign overlap.",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected markdown to contain %q, got:\n%s", want, md)
		}
	}
}

func TestVisibleNotificationEventsHidesDropByDefault(t *testing.T) {
	events := []api.NotificationEvent{
		{EventID: "evt_keep", DeliveryAction: "digest"},
		{EventID: "evt_drop", DeliveryAction: "drop"},
	}

	filtered := visibleNotificationEvents(events, false)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 visible event, got %d", len(filtered))
	}
	if filtered[0].EventID != "evt_keep" {
		t.Fatalf("expected kept event to be evt_keep, got %#v", filtered[0])
	}

	unfiltered := visibleNotificationEvents(events, true)
	if len(unfiltered) != 2 {
		t.Fatalf("expected include-drop to keep both events, got %d", len(unfiltered))
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
