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
	appendRepoServerResponse(
		&lines,
		"git@example.com:demo/repo.git",
		"abc123 Demo change\n file.txt | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n\n"+
			"diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new",
		map[string]any{"ok": true},
		false,
		reportRenderOptions{DetailLevel: reportDetailVerbose},
	)

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "## Repo: `git@example.com:demo/repo.git`") {
		t.Fatalf("expected repo heading, got:\n%s", out)
	}
	if !strings.Contains(out, "### Changes") || !strings.Contains(out, "~~~diff") {
		t.Fatalf("expected fenced diff block, got:\n%s", out)
	}
	if strings.Contains(out, "### Server Response") || strings.Contains(out, "~~~json") {
		t.Fatalf("did not expect server response without debug mode, got:\n%s", out)
	}
}

func TestAppendRepoServerResponseDetailedUsesSummaryBlock(t *testing.T) {
	lines := []string{}
	appendRepoServerResponse(
		&lines,
		"git@example.com:demo/repo.git",
		"abc123 Demo change\n file.txt | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n\n"+
			"diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new",
		map[string]any{"ok": true},
		false,
		reportRenderOptions{DetailLevel: reportDetailDetailed},
	)

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "### Changes") || !strings.Contains(out, "~~~text") {
		t.Fatalf("expected summarized text block, got:\n%s", out)
	}
	if strings.Contains(out, "diff --git a/file.txt b/file.txt") {
		t.Fatalf("did not expect raw diff in detailed mode, got:\n%s", out)
	}
	if !strings.Contains(out, "file.txt | 2 +-") {
		t.Fatalf("expected summary lines in detailed mode, got:\n%s", out)
	}
}

func TestAppendRepoServerResponseBriefUsesCondensedSummary(t *testing.T) {
	lines := []string{}
	appendRepoServerResponse(
		&lines,
		"git@example.com:demo/repo.git",
		"abc123 Demo change\n README.md | 2 +-\n src/client.ts | 4 ++--\n 2 files changed, 3 insertions(+), 3 deletions(-)\n\n"+
			"diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md",
		nil,
		false,
		reportRenderOptions{DetailLevel: reportDetailBrief},
	)

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "~~~text") {
		t.Fatalf("expected condensed text block, got:\n%s", out)
	}
	if strings.Contains(out, "diff --git a/README.md b/README.md") {
		t.Fatalf("did not expect raw diff in brief mode, got:\n%s", out)
	}
	if !strings.Contains(out, "2 files changed, 3 insertions(+), 3 deletions(-)") {
		t.Fatalf("expected condensed change summary, got:\n%s", out)
	}
}

func TestAppendRepoServerResponseIncludesServerResponseInDebugMode(t *testing.T) {
	lines := []string{}
	appendRepoServerResponse(
		&lines,
		"git@example.com:demo/repo.git",
		"",
		map[string]any{"chunk_task_ids": []string{"abc"}},
		false,
		reportRenderOptions{DetailLevel: reportDetailDetailed, IncludeDebug: true},
	)

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "### Server Response") || !strings.Contains(out, "~~~json") {
		t.Fatalf("expected debug server response block, got:\n%s", out)
	}
}

func TestAppendFeedbackContextHonorsVerbosity(t *testing.T) {
	brief := []string{}
	appendFeedbackContext(&brief, "line 1\nline 2", reportRenderOptions{DetailLevel: reportDetailBrief})
	if len(brief) != 0 {
		t.Fatalf("expected no context in brief mode, got %#v", brief)
	}

	detailed := []string{}
	appendFeedbackContext(&detailed, "line 1\nline 2", reportRenderOptions{DetailLevel: reportDetailDetailed})
	out := strings.Join(detailed, "\n")
	if !strings.Contains(out, "**Context**") || !strings.Contains(out, "~~~text") {
		t.Fatalf("expected context block in detailed mode, got:\n%s", out)
	}
}

func TestAppendFeedbackContextPreservesDiffPrefix(t *testing.T) {
	lines := []string{}
	ctx := "diff --git a/src/reviewClient.ts b/src/reviewClient.ts\n" +
		"index d5ffd84..b5dfb4a 100644\n" +
		"--- a/src/reviewClient.ts\n" +
		"+++ b/src/reviewClient.ts\n" +
		strings.Repeat("0123456789", 80)
	appendFeedbackContext(&lines, ctx, reportRenderOptions{DetailLevel: reportDetailDetailed})
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "diff --git a/src/reviewClient.ts b/src/reviewClient.ts") {
		t.Fatalf("expected diff header to be preserved, got:\n%s", out)
	}
	if strings.Contains(out, "\nff --git a/src/reviewClient.ts") {
		t.Fatalf("expected diff header not to be truncated mid-token, got:\n%s", out)
	}
}

func TestAppendFeedbackEvidenceIncludesGroundedSnippets(t *testing.T) {
	lines := []string{}
	appendFeedbackEvidence(&lines, &feedbackNotificationMeta{
		EvidenceTarget: `return (payload.items ?? []).map((item: any) => renderReviewCard(item));`,
		EvidencePeer:   `"reviews": [{"severity": "high", "category": "api-contract", "rationale": "..."}]`,
	}, reportRenderOptions{DetailLevel: reportDetailDetailed})

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "**Target Evidence**") || !strings.Contains(out, "payload.items") {
		t.Fatalf("expected target evidence block, got:\n%s", out)
	}
	if !strings.Contains(out, "**Peer Evidence**") || !strings.Contains(out, `"reviews"`) {
		t.Fatalf("expected peer evidence block, got:\n%s", out)
	}
}

func TestAppendFeedbackReferenceExcerptsIncludesReferenceContent(t *testing.T) {
	lines := []string{}
	appendFeedbackReferenceExcerpts(&lines, []api.FeedbackReference{
		{
			Title:   "demo-api",
			Content: `"reviews": [{"severity": "high", "category": "api-contract"}]`,
		},
	}, "demo-api still returns reviews[] with severity/category", "", nil, reportRenderOptions{DetailLevel: reportDetailDetailed})

	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "**Reference Excerpts**") {
		t.Fatalf("expected reference excerpts heading, got:\n%s", out)
	}
	if !strings.Contains(out, "Source: demo-api") || !strings.Contains(out, `"reviews"`) {
		t.Fatalf("expected reference excerpt content, got:\n%s", out)
	}
}

func TestAppendFeedbackReferenceExcerptsChoosesRelevantSnapshotSection(t *testing.T) {
	lines := []string{}
	refContent := strings.Join([]string{
		"# Compair baseline snapshot",
		"- Generated: 2026-03-26T23:26:01-04:00",
		snapshotChunkDelimiter,
		"### File: README.md",
		"The /reviews endpoint returns reviews[] objects.",
		"The API uses severity, category, and rationale.",
		snapshotChunkDelimiter,
		"### File: api/openapi.yaml",
		"paths:",
		"  /reviews:",
		"    get:",
		"      responses:",
		"        '200':",
		"          properties:",
		"            reviews:",
		"              items:",
		"                required: [severity, category, rationale]",
	}, "\n")
	appendFeedbackReferenceExcerpts(&lines, []api.FeedbackReference{
		{
			Title:   "demo.local:compair/demo-api",
			Content: refContent,
		},
	}, "demo-client now expects items[] and priority/type, while demo-api still defines /reviews returning reviews[] with severity/category/rationale in api/openapi.yaml", "", nil, reportRenderOptions{DetailLevel: reportDetailDetailed})

	out := strings.Join(lines, "\n")
	if strings.Contains(out, "# Compair baseline snapshot") {
		t.Fatalf("did not expect snapshot header excerpt, got:\n%s", out)
	}
	if !strings.Contains(out, "Source: demo.local:compair/demo-api (api/openapi.yaml)") {
		t.Fatalf("expected api/openapi.yaml section label, got:\n%s", out)
	}
	if !strings.Contains(out, "required: [severity, category, rationale]") {
		t.Fatalf("expected relevant OpenAPI contract line, got:\n%s", out)
	}
}

func TestAppendFeedbackComparedFilesIncludesNormalizedPaths(t *testing.T) {
	lines := []string{}
	item := feedbackRenderItem{
		Feedback: api.FeedbackEntry{
			ChunkContent: "diff --git a/internal/api/capabilities.go b/internal/api/capabilities.go\n" +
				"--- a/internal/api/capabilities.go\n+++ b/internal/api/capabilities.go\n",
			Feedback: "Update `docs/core_quickstart.md`, `cmd/compair/core.go`, `server/routers/capabilities.py`, `desktop/src/session.ts`, and `site/src/pages/core.astro` to match `internal/api/capabilities.go`.",
			References: []api.FeedbackReference{
				{Content: "### File: api/schema/openapi.yaml\n/components/schemas/Capabilities"},
			},
		},
		Meta: &feedbackNotificationMeta{
			EvidenceTarget: "`docs/core_quickstart.md` still references `internal/api/capabilities.go`.",
			EvidencePeer:   "`server/routers/capabilities.py` drives the capability response.",
		},
	}

	appendFeedbackComparedFiles(&lines, item, reportRenderOptions{DetailLevel: reportDetailBrief})
	out := strings.Join(lines, "\n")
	for _, want := range []string{
		"**Compared Files**",
		"- `api/schema/openapi.yaml`",
		"- `cmd/compair/core.go`",
		"- `desktop/src/session.ts`",
		"- `docs/core_quickstart.md`",
		"- `internal/api/capabilities.go`",
		"- ... +2 more",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected compared files output to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "site/src/pages/core.astro") {
		t.Fatalf("expected brief mode to cap compared files, got:\n%s", out)
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

func TestNotificationGateBlockedHeadlineUsesHumanSummary(t *testing.T) {
	result := notificationGateResult{
		MatchedCount: 1,
		Matches:      []string{"high/potential_conflict@doc_123"},
	}
	if got := notificationGateBlockedHeadline(result); got != "BLOCKED: high-severity potential conflict" {
		t.Fatalf("unexpected blocked headline: %s", got)
	}
}

func TestNotificationGateBlockedHeadlineFallsBackForMultipleMatches(t *testing.T) {
	result := notificationGateResult{
		MatchedCount: 2,
		Matches:      []string{"high/potential_conflict@doc_123", "medium/relevant_update@doc_456"},
	}
	if got := notificationGateBlockedHeadline(result); got != "BLOCKED: 2 events matched the notification gate" {
		t.Fatalf("unexpected fallback blocked headline: %s", got)
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
