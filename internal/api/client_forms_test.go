package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestCreateGroupReturnsCreatedGroupID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/create_group" {
			t.Fatalf("expected /create_group, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"group_id":"grp_demo_123","name":"demo-group","visibility":"public"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	client.http = server.Client()

	group, err := client.CreateGroup("demo-group", "", "", "public", "")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if got := group.GroupID; got != "grp_demo_123" {
		t.Fatalf("expected group_id grp_demo_123, got %q", got)
	}
	if got := group.Name; got != "demo-group" {
		t.Fatalf("expected name demo-group, got %q", got)
	}
}

func TestCreateGroupFallsBackWhenResponseBodyIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	client.http = server.Client()

	group, err := client.CreateGroup("demo-group", "", "", "private", "")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if got := group.Name; got != "demo-group" {
		t.Fatalf("expected fallback name demo-group, got %q", got)
	}
	if got := group.Visibility; got != "private" {
		t.Fatalf("expected fallback visibility private, got %q", got)
	}
}

func TestProcessDocWithOptionsIncludesReanalyzeExistingAndSkipIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/process_doc" {
			t.Fatalf("expected /process_doc, got %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := values.Get("generate_feedback"); got != "true" {
			t.Fatalf("expected generate_feedback=true, got %q", got)
		}
		if got := values.Get("chunk_mode"); got != "client" {
			t.Fatalf("expected chunk_mode=client, got %q", got)
		}
		if got := values.Get("reanalyze_existing"); got != "true" {
			t.Fatalf("expected reanalyze_existing=true, got %q", got)
		}
		if got := values.Get("skip_index"); got != "true" {
			t.Fatalf("expected skip_index=true, got %q", got)
		}
		if got := values["reference_doc_ids"]; len(got) != 2 || got[0] != "peer_doc_1" || got[1] != "peer_doc_2" {
			t.Fatalf("expected repeated reference_doc_ids values, got %#v", got)
		}
		if got := values.Get("doc_text"); got != "" {
			t.Fatalf("expected doc_text to be omitted, got %q", got)
		}
		if got := values.Get("doc_text_b64"); got == "" {
			t.Fatal("expected doc_text_b64 to be populated")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"task_id":"task_123","skipped_index":true}`)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	client.http = server.Client()

	resp, err := client.ProcessDocWithOptions("doc_123", "hello", true, ProcessDocOptions{
		ChunkMode:         "client",
		ReanalyzeExisting: true,
		ReferenceDocIDs:   []string{"peer_doc_1", "peer_doc_2"},
		SkipIndex:         true,
	})
	if err != nil {
		t.Fatalf("ProcessDocWithOptions returned error: %v", err)
	}
	if strings.TrimSpace(resp.TaskID) != "task_123" {
		t.Fatalf("expected task_id task_123, got %q", resp.TaskID)
	}
	if !resp.SkippedIndex {
		t.Fatal("expected skipped_index=true in response")
	}
}

func TestListGroupsFetchesMultiplePages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/load_groups" {
			t.Fatalf("expected /load_groups, got %s", r.URL.Path)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if pageSize != 100 {
			t.Fatalf("expected page_size=100, got %d", pageSize)
		}
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case 1:
			groups := make([]string, 0, 100)
			for i := 0; i < 100; i++ {
				groups = append(groups, `{"group_id":"grp_`+strconv.Itoa(i+1)+`","name":"group`+strconv.Itoa(i+1)+`"}`)
			}
			_, _ = io.WriteString(w, `{"groups":[`+strings.Join(groups, ",")+`],"total_count":101}`)
		case 2:
			_, _ = io.WriteString(w, `{"groups":[{"group_id":"grp_101","name":"group101"}],"total_count":101}`)
		default:
			_, _ = io.WriteString(w, `{"groups":[],"total_count":101}`)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	client.http = server.Client()

	groups, err := client.ListGroups(true)
	if err != nil {
		t.Fatalf("ListGroups returned error: %v", err)
	}
	if len(groups) != 101 {
		t.Fatalf("expected 101 groups, got %d", len(groups))
	}
	if groups[0].GroupID != "grp_1" || groups[len(groups)-1].GroupID != "grp_101" {
		t.Fatalf("unexpected groups: first=%#v last=%#v", groups[0], groups[len(groups)-1])
	}
}

func TestGetTaskStatusIncludesMeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/task_123" {
			t.Fatalf("expected /status/task_123, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"task_id":"task_123",
			"status":"PROGRESS",
			"result":"Task is running.",
			"message":"Indexing chunks",
			"meta":{
				"stage":"indexing",
				"indexed_chunks_done":12,
				"indexed_chunks_total":48
			}
		}`)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	client.http = server.Client()

	status, err := client.GetTaskStatus("task_123")
	if err != nil {
		t.Fatalf("GetTaskStatus returned error: %v", err)
	}
	if status.Status != "PROGRESS" {
		t.Fatalf("expected PROGRESS status, got %q", status.Status)
	}
	if status.Message != "Indexing chunks" {
		t.Fatalf("expected message to round-trip, got %q", status.Message)
	}
	if got := status.Meta["stage"]; got != "indexing" {
		t.Fatalf("expected meta.stage=indexing, got %#v", got)
	}
	if got := status.Meta["indexed_chunks_total"]; got != float64(48) {
		t.Fatalf("expected indexed_chunks_total to decode as 48, got %#v", got)
	}
}

func TestReviewNowPostsJSONPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/review_now" {
			t.Fatalf("expected /review_now, got %s", r.URL.Path)
		}
		var payload ReviewNowOptions
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if payload.GroupID != "grp_123" {
			t.Fatalf("expected group_id grp_123, got %q", payload.GroupID)
		}
		if len(payload.DocumentIDs) != 2 {
			t.Fatalf("expected 2 document ids, got %d", len(payload.DocumentIDs))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"group_id":"grp_123",
			"group_name":"demo",
			"document_ids":["doc_1","doc_2"],
			"markdown":"# demo",
			"findings":[{"intent":"potential_conflict","severity":"high","certainty":"high","title":"x","summary":"y","why_it_matters":"z","target_repos":[],"target_files":[],"peer_repos":[],"peer_files":[],"evidence_target":"a","evidence_peer":"b","follow_up":"c"}],
			"meta":{"model":"gpt-5.4"}
		}`)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	client.http = server.Client()

	resp, err := client.ReviewNow(ReviewNowOptions{
		GroupID:     "grp_123",
		DocumentIDs: []string{"doc_1", "doc_2"},
		MaxFindings: 8,
		Model:       "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("ReviewNow returned error: %v", err)
	}
	if resp.GroupName != "demo" {
		t.Fatalf("expected group name demo, got %q", resp.GroupName)
	}
	if got := resp.Meta["model"]; got != "gpt-5.4" {
		t.Fatalf("expected meta.model gpt-5.4, got %#v", got)
	}
}
