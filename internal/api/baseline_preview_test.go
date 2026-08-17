package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const (
	previewGroupID  = "00000000-0000-4000-8000-000000000002"
	previewJobID    = "00000000-0000-4000-8000-000000000010"
	previewDigestID = "00000000-0000-4000-8000-000000000022"
)

func previewFixture(requestID string, zero bool) string {
	feedback := `[
    {"ordinal":1,"feedback_id":"00000000-0000-4000-8000-000000000023","feedback":"second-looking finding"},
    {"ordinal":2,"feedback_id":"00000000-0000-4000-8000-000000000024","feedback":"first-looking finding"}
  ]`
	digest := `{"digest_id":"` + previewDigestID + `","state":"suppressed","channel":"in_app","finding_count":2,"finding_manifest_sha256":"3333333333333333333333333333333333333333333333333333333333333333"}`
	feedbackCount := 2
	outboxCount := 1
	if zero {
		feedback = "[]"
		digest = "null"
		feedbackCount = 0
		outboxCount = 0
	}
	return fmt.Sprintf(`{
  "schema_version":"baseline-preview.v1",
  "request_id":%q,
  "control_job":{"job_id":"%s","state":"feedback_persisted","completed_at":"2026-08-16T12:00:00Z","generation_invoked":true,"feedback_count":%d,"notification_outbox_count":%d},
  "retrieval":{"persisted_run_id":"00000000-0000-4000-8000-000000000011","status":"ok","evidence_count":2,"reference_count":2},
  "source":{"group_id":"%s","document_id":"00000000-0000-4000-8000-000000000004","source_scope":"control_document","chunk_id":null},
  "feedback":%s,
  "digest":%s,
  "provenance":{
    "retrieval":{"engine":"baseline_v1","version":"baseline_v1.persistent.v1","result_schema_version":"retrieval-result.v2"},
    "query":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","length":17,"origin":"explicit"},
    "corpus":{"generation_id":"00000000-0000-4000-8000-000000000040","generation_version":"corpus-generation.v1","manifest_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
    "index":{"publication_id":"00000000-0000-4000-8000-000000000041","publication_fingerprint":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","index_id":"00000000-0000-4000-8000-000000000041","version":"baseline-index.v1","schema_version":"baseline-index.v1","fingerprint":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","config_fingerprint":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},
    "embedding":{"provider":"fastembed-http","model":"BAAI/bge-small-en-v1.5","revision":"snapshot-1","dimension":384,"fingerprint":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
    "generation":{"provider":"local","model":"structured-reviewer","version":"snapshot-1","input_fingerprint":"1111111111111111111111111111111111111111111111111111111111111111","output_fingerprint":"2222222222222222222222222222222222222222222222222222222222222222"}
  }
}`, requestID, previewJobID, feedbackCount, outboxCount, previewGroupID, feedback, digest)
}

func writePreviewCredentials(t *testing.T, token string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".compair")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "credentials.json"),
		[]byte(`{"access_token":"`+token+`"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func decodePreviewRequest(t *testing.T, request *http.Request) BaselinePreviewRequest {
	t.Helper()
	if request.Method != http.MethodPost || request.URL.Path != baselinePreviewPath {
		t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
	}
	if request.URL.RawQuery != "" {
		t.Fatalf("preview IDs leaked to query: %s", request.URL.RawQuery)
	}
	if request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q", request.Header.Get("Content-Type"))
	}
	var body BaselinePreviewRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestPostBaselinePreviewContractHeadersBodyOrderingAndZero(t *testing.T) {
	writePreviewCredentials(t, "preview-token")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		request := decodePreviewRequest(t, r)
		if request.SchemaVersion != baselinePreviewSchemaVersion ||
			request.GroupID != previewGroupID || request.JobID != previewJobID ||
			request.DigestID != "" || request.RequestID == "" {
			t.Fatalf("request = %#v", request)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer preview-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("auth-token"); got != "preview-token" {
			t.Fatalf("auth-token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(previewFixture(request.RequestID, requests == 2)))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	positive, err := client.PostBaselinePreview(previewGroupID, previewJobID, "")
	if err != nil {
		t.Fatal(err)
	}
	if positive.ControlJob.State != "feedback_persisted" || positive.Digest == nil ||
		positive.Digest.State != "suppressed" {
		t.Fatalf("positive = %#v", positive)
	}
	if len(positive.Feedback) != 2 || positive.Feedback[0].Ordinal != 1 ||
		positive.Feedback[1].Ordinal != 2 {
		t.Fatalf("feedback order = %#v", positive.Feedback)
	}
	zero, err := client.PostBaselinePreview(previewGroupID, previewJobID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(zero.Feedback) != 0 || zero.Digest != nil ||
		zero.ControlJob.NotificationOutboxCount != 0 {
		t.Fatalf("zero = %#v", zero)
	}
}

func TestPostBaselinePreviewDigestSelectorAndTypedStatus(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		request := decodePreviewRequest(t, r)
		if request.JobID != "" || request.DigestID != previewDigestID {
			t.Fatalf("selector = %#v", request)
		}
		http.Error(w, `{}`, http.StatusNotFound)
	}))
	defer server.Close()

	_, err := NewClient(server.URL).PostBaselinePreview(
		previewGroupID, "", previewDigestID,
	)
	var statusError *BaselinePreviewHTTPError
	if !errors.As(err, &statusError) {
		t.Fatalf("error = %T %v", err, err)
	}
	if statusError.StatusCode != http.StatusNotFound || requests != 1 {
		t.Fatalf("status = %d requests = %d", statusError.StatusCode, requests)
	}
}

func TestPostBaselinePreviewRejectsMalformedOrContradictoryResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodePreviewRequest(t, r)
		value := previewFixture(request.RequestID, true)
		value = value[:len(value)-1] + `,"retrieval_query":"private"}`
		_, _ = w.Write([]byte(value))
	}))
	defer server.Close()

	if _, err := NewClient(server.URL).PostBaselinePreview(
		previewGroupID, previewJobID, "",
	); err == nil || err.Error() != "invalid baseline preview response" {
		t.Fatalf("error = %v", err)
	}
}
