package compair

import "github.com/RocketResearch-Inc/compair-cli/internal/api"
import "strings"
import "testing"

func TestExtractChunkTaskIDs(t *testing.T) {
	result := map[string]any{
		"chunk_task_ids": []any{"abc", "  ", "def"},
	}

	got := extractChunkTaskIDs(result)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunk task ids, got %d (%#v)", len(got), got)
	}
	if got[0] != "abc" || got[1] != "def" {
		t.Fatalf("unexpected chunk task ids: %#v", got)
	}
}

func TestExtractChunkTaskIDsHandlesMissingPayload(t *testing.T) {
	if got := extractChunkTaskIDs(nil); len(got) != 0 {
		t.Fatalf("expected no ids for nil payload, got %#v", got)
	}
	if got := extractChunkTaskIDs(map[string]any{"detail": "ok"}); len(got) != 0 {
		t.Fatalf("expected no ids for payload without chunk_task_ids, got %#v", got)
	}
}

func TestExtractChunkTaskIDsFromStatusFallsBackToMeta(t *testing.T) {
	st := api.TaskStatus{
		Status: "PROGRESS",
		Meta: map[string]any{
			"chunk_task_ids": []any{"abc", "def"},
		},
	}
	got := extractChunkTaskIDsFromStatus(st)
	if len(got) != 2 {
		t.Fatalf("expected 2 ids from task meta, got %d (%#v)", len(got), got)
	}
	if got[0] != "abc" || got[1] != "def" {
		t.Fatalf("unexpected ids from task meta: %#v", got)
	}
}

func TestExtractChunkTaskIDsFromStatusPrefersTopLevelChildTaskIDs(t *testing.T) {
	st := api.TaskStatus{
		Status:       "PROGRESS",
		ChildTaskIDs: []string{" child-a ", "", "child-b"},
		Result: map[string]any{
			"chunk_task_ids": []any{"legacy-a"},
		},
		Meta: map[string]any{
			"chunk_task_ids": []any{"legacy-b"},
		},
	}
	got := extractChunkTaskIDsFromStatus(st)
	if len(got) != 2 {
		t.Fatalf("expected 2 ids from top-level child_task_ids, got %d (%#v)", len(got), got)
	}
	if got[0] != "child-a" || got[1] != "child-b" {
		t.Fatalf("unexpected ids from top-level child_task_ids: %#v", got)
	}
}

func TestTaskLifecycleTerminalAllowsServerTerminalProgress(t *testing.T) {
	st := api.TaskStatus{
		Status:    "PROGRESS",
		Lifecycle: "failed_terminal",
		Terminal:  true,
	}
	if !isTaskLifecycleTerminal(st) {
		t.Fatalf("expected failed_terminal lifecycle to be terminal")
	}
	if got := displayTaskStatus(st, nil); got != "PROGRESS/failed_terminal" {
		t.Fatalf("unexpected display status: %q", got)
	}
}

func TestServerStaleTaskStatusIsDetected(t *testing.T) {
	st := api.TaskStatus{
		Status:             "PROGRESS",
		Health:             "stale",
		RecommendedAction:  "inspect_worker",
		LastProgressAgeSec: 901,
	}
	if !isServerStaleTask(st) {
		t.Fatal("expected server-stale task to be detected")
	}
	err := serverStaleTaskError("12345678-aaaa-bbbb-cccc-123456789abc", st)
	msg := err.Error()
	for _, want := range []string{"12345678", "health=stale", "recommended_action=inspect_worker", "last_progress=15m1s ago"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected stale task error to include %q, got %q", want, msg)
		}
	}
}

func TestServerPrechunkStaleTaskStatusIncludesSpecificGuidance(t *testing.T) {
	st := api.TaskStatus{
		Status:             "PROGRESS",
		Health:             "stale_prechunk",
		RecommendedAction:  "resubmit_smaller_or_inspect_worker",
		LastProgressAgeSec: 902,
		Message:            "Preparing document for indexing",
		Meta: map[string]any{
			"stage": "preparing",
		},
	}
	if !isServerStaleTask(st) {
		t.Fatal("expected pre-chunk stale task to be detected")
	}
	err := serverStaleTaskError("abcdef12-aaaa-bbbb-cccc-123456789abc", st)
	msg := err.Error()
	for _, want := range []string{
		"abcdef12",
		"health=stale_prechunk",
		"recommended_action=resubmit_smaller_or_inspect_worker",
		"parent task stalled before chunk tasks were queued",
		"smaller snapshot limits",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected pre-chunk stale task error to include %q, got %q", want, msg)
		}
	}
}

func TestServerStaleTaskIgnoresTerminalLifecycle(t *testing.T) {
	st := api.TaskStatus{
		Status:    "PROGRESS",
		Lifecycle: "failed_terminal",
		Terminal:  true,
		Health:    "stale",
	}
	if isServerStaleTask(st) {
		t.Fatal("did not expect terminal task to be treated as server-stale")
	}
}
