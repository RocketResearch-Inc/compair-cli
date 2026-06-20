package compair

import "testing"
import "github.com/RocketResearch-Inc/compair-cli/internal/api"

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
