package compair

import (
	"strings"
	"testing"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
)

func TestSummarizeFeedbackReferencesDedupesRepeatedDocuments(t *testing.T) {
	direct := []api.FeedbackReference{
		{Title: "demo-api", Author: "Steven"},
		{Title: "demo-api", Author: "Steven"},
		{Title: "demo-client", Author: "Ava"},
	}

	got := summarizeFeedbackReferences(direct, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 collapsed references, got %d", len(got))
	}
	if got[0].Title != "demo-api" || got[0].Excerpts != 2 {
		t.Fatalf("unexpected first collapsed reference: %#v", got[0])
	}
}

func TestAppendRepoServerResponseUsesMarkdownSections(t *testing.T) {
	lines := []string{}
	appendRepoServerResponse(&lines, "git@example.com:demo/repo.git", "diff --git a/a b/a", map[string]any{"ok": true}, false)

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "## Repo: git@example.com:demo/repo.git") {
		t.Fatalf("expected repo heading, got:\n%s", out)
	}
	if !strings.Contains(out, "### Changes") || !strings.Contains(out, "~~~diff") {
		t.Fatalf("expected fenced diff block, got:\n%s", out)
	}
	if !strings.Contains(out, "### Server Response") || !strings.Contains(out, "~~~json") {
		t.Fatalf("expected fenced json block, got:\n%s", out)
	}
}

func TestFeedbackHeadingUsesIntentLabel(t *testing.T) {
	item := feedbackRenderItem{
		Meta: &feedbackNotificationMeta{Intent: "hidden_overlap"},
	}
	if got := feedbackHeading(item, 2); got != "### Hidden Overlap 2" {
		t.Fatalf("unexpected heading: %s", got)
	}
}

func TestCollapseDuplicateFeedbackItemsUsesSharedEvidence(t *testing.T) {
	now := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	items := []feedbackRenderItem{
		{
			Feedback: api.FeedbackEntry{
				FeedbackID: "fb-1",
				ChunkID:    "chunk-1",
				ChunkContent: "diff --git a/src/reviewClient.ts b/src/reviewClient.ts\n" +
					"@@ -1,3 +1,3 @@\n- severity\n+ priority\n",
				Feedback:   "The client now expects `priority`/`type` while the API still emits `severity`/`category`, so the review feed will drift or silently fall back to defaults.",
				References: []api.FeedbackReference{{Title: "demo-api", Author: "Steven"}},
				Timestamp:  now.Format(time.RFC3339),
			},
			Meta: &feedbackNotificationMeta{
				Intent:    "potential_conflict",
				Severity:  "high",
				CreatedAt: now.Format(time.RFC3339),
			},
		},
		{
			Feedback: api.FeedbackEntry{
				FeedbackID: "fb-2",
				ChunkID:    "chunk-2",
				ChunkContent: "diff --git a/src/reviewFeed.ts b/src/reviewFeed.ts\n" +
					"@@ -1,3 +1,3 @@\n- payload.reviews\n+ payload.items\n",
				Feedback:   "The client changed from `reviews` to `items` and from `severity`/`category` to `priority`/`type`, which breaks the current API contract and leaves the review feed empty or defaulted.",
				References: []api.FeedbackReference{{Title: "demo-api", Author: "Steven"}},
				Timestamp:  now.Add(2 * time.Minute).Format(time.RFC3339),
			},
			Meta: &feedbackNotificationMeta{
				Intent:    "potential_conflict",
				Severity:  "high",
				CreatedAt: now.Add(2 * time.Minute).Format(time.RFC3339),
			},
		},
	}

	got, collapsed := collapseDuplicateFeedbackItems(items)
	if collapsed != 1 {
		t.Fatalf("expected 1 collapsed duplicate, got %d", collapsed)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 remaining feedback item, got %d", len(got))
	}
}

func TestCollapseDuplicateFeedbackItemsKeepsDistinctEvidence(t *testing.T) {
	now := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	items := []feedbackRenderItem{
		{
			Feedback: api.FeedbackEntry{
				FeedbackID: "fb-1",
				ChunkID:    "chunk-1",
				ChunkContent: "diff --git a/src/reviewClient.ts b/src/reviewClient.ts\n" +
					"@@ -1,3 +1,3 @@\n- severity\n+ priority\n",
				Feedback:   "The client contract now expects `priority` and `type`, which diverges from the API serializer.",
				References: []api.FeedbackReference{{Title: "demo-api", Author: "Steven"}},
				Timestamp:  now.Format(time.RFC3339),
			},
			Meta: &feedbackNotificationMeta{
				Intent:    "potential_conflict",
				Severity:  "high",
				CreatedAt: now.Format(time.RFC3339),
			},
		},
		{
			Feedback: api.FeedbackEntry{
				FeedbackID: "fb-2",
				ChunkID:    "chunk-2",
				ChunkContent: "diff --git a/src/cache.ts b/src/cache.ts\n" +
					"@@ -1,3 +1,3 @@\n- ttl=30\n+ ttl=300\n",
				Feedback:   "The cache layer changed its TTL handling and may now lag behind the backend refresh cadence, which is a separate behavior difference to verify.",
				References: []api.FeedbackReference{{Title: "demo-ops", Author: "Ava"}},
				Timestamp:  now.Add(1 * time.Minute).Format(time.RFC3339),
			},
			Meta: &feedbackNotificationMeta{
				Intent:    "potential_conflict",
				Severity:  "high",
				CreatedAt: now.Add(1 * time.Minute).Format(time.RFC3339),
			},
		},
	}

	got, collapsed := collapseDuplicateFeedbackItems(items)
	if collapsed != 0 {
		t.Fatalf("expected no collapsed duplicates, got %d", collapsed)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 remaining feedback items, got %d", len(got))
	}
}
