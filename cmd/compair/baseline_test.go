package compair

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	commandPreviewGroupID  = "00000000-0000-4000-8000-000000000002"
	commandPreviewJobID    = "00000000-0000-4000-8000-000000000010"
	commandPreviewDigestID = "00000000-0000-4000-8000-000000000022"
)

func commandPreviewFixture(requestID string, zero bool) string {
	feedback := `[
    {"ordinal":1,"feedback_id":"00000000-0000-4000-8000-000000000023","feedback":"second-looking finding"},
    {"ordinal":2,"feedback_id":"00000000-0000-4000-8000-000000000024","feedback":"first-looking finding"}
  ]`
	digest := `{"digest_id":"` + commandPreviewDigestID + `","state":"suppressed","channel":"in_app","finding_count":2,"finding_manifest_sha256":"3333333333333333333333333333333333333333333333333333333333333333"}`
	feedbackCount := 2
	outboxCount := 1
	if zero {
		feedback = "[]"
		digest = "null"
		feedbackCount = 0
		outboxCount = 0
	}
	return fmt.Sprintf(`{
  "schema_version":"baseline-preview.v1","request_id":%q,
  "control_job":{"job_id":"%s","state":"feedback_persisted","completed_at":"2026-08-16T12:00:00Z","generation_invoked":true,"feedback_count":%d,"notification_outbox_count":%d},
  "retrieval":{"persisted_run_id":"00000000-0000-4000-8000-000000000011","status":"ok","evidence_count":2,"reference_count":2},
  "source":{"group_id":"%s","document_id":"00000000-0000-4000-8000-000000000004","source_scope":"control_document","chunk_id":null},
  "feedback":%s,"digest":%s,
  "provenance":{
    "retrieval":{"engine":"baseline_v1","version":"baseline_v1.persistent.v1","result_schema_version":"retrieval-result.v2"},
    "query":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","length":17,"origin":"explicit"},
    "corpus":{"generation_id":"00000000-0000-4000-8000-000000000040","generation_version":"corpus-generation.v1","manifest_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
    "index":{"publication_id":"00000000-0000-4000-8000-000000000041","publication_fingerprint":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","index_id":"00000000-0000-4000-8000-000000000041","version":"baseline-index.v1","schema_version":"baseline-index.v1","fingerprint":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","config_fingerprint":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},
    "embedding":{"provider":"fastembed-http","model":"BAAI/bge-small-en-v1.5","revision":"snapshot-1","dimension":384,"fingerprint":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
    "generation":{"provider":"local","model":"structured-reviewer","version":"snapshot-1","input_fingerprint":"1111111111111111111111111111111111111111111111111111111111111111","output_fingerprint":"2222222222222222222222222222222222222222222222222222222222222222"}
  }
}`, requestID, commandPreviewJobID, feedbackCount, outboxCount, commandPreviewGroupID, feedback, digest)
}

func executeBaselineForTest(t *testing.T, baseURL string, args ...string) (string, string, error) {
	t.Helper()
	previousBase := viper.GetString("api.base")
	viper.Set("api.base", baseURL)
	t.Cleanup(func() { viper.Set("api.base", previousBase) })

	cmd := &cobra.Command{Use: "compair", SilenceErrors: true, SilenceUsage: true}
	cmd.PersistentFlags().String("group", "", "Active group")
	cmd.AddCommand(newBaselineCommand())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestBaselinePreviewEmitsExactlyOneOrderedSuppressedJSONValue(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/baseline/preview/v1" ||
			r.URL.RawQuery != "" {
			t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		var request struct {
			SchemaVersion string `json:"schema_version"`
			RequestID     string `json:"request_id"`
			GroupID       string `json:"group_id"`
			JobID         string `json:"job_id"`
			DigestID      string `json:"digest_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.SchemaVersion != "baseline-preview.v1" ||
			request.GroupID != commandPreviewGroupID || request.JobID != commandPreviewJobID ||
			request.DigestID != "" || request.RequestID == "" {
			t.Fatalf("request = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(commandPreviewFixture(request.RequestID, false)))
	}))
	defer server.Close()

	stdout, stderr, err := executeBaselineForTest(
		t,
		server.URL,
		"baseline",
		"preview",
		"--group",
		commandPreviewGroupID,
		"--job-id",
		commandPreviewJobID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(stdout))
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		ControlJob    struct {
			State string `json:"state"`
		} `json:"control_job"`
		Digest *struct {
			State string `json:"state"`
		} `json:"digest"`
		Feedback []struct {
			Ordinal int `json:"ordinal"`
		} `json:"feedback"`
	}
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout contains more than one JSON value: %q", stdout)
	}
	if payload.SchemaVersion != "baseline-preview.v1" ||
		payload.ControlJob.State != "feedback_persisted" || payload.Digest == nil ||
		payload.Digest.State != "suppressed" {
		t.Fatalf("states = %#v", payload)
	}
	if len(payload.Feedback) != 2 || payload.Feedback[0].Ordinal != 1 ||
		payload.Feedback[1].Ordinal != 2 {
		t.Fatalf("feedback = %#v", payload.Feedback)
	}
}

func TestBaselinePreviewZeroFindingsIsSuccessfulOneValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			RequestID string `json:"request_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(commandPreviewFixture(request.RequestID, true)))
	}))
	defer server.Close()
	stdout, _, err := executeBaselineForTest(
		t, server.URL, "baseline", "preview", "--group", commandPreviewGroupID,
		"--job-id", commandPreviewJobID,
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Feedback []any `json:"feedback"`
		Digest   any   `json:"digest"`
	}
	decoder := json.NewDecoder(bytes.NewBufferString(stdout))
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Feedback) != 0 || payload.Digest != nil {
		t.Fatalf("payload = %#v", payload)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout contains more than one JSON value: %q", stdout)
	}
}

func TestBaselinePreviewRequiresExplicitGroupAndExactlyOneSelector(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing group", args: []string{"baseline", "preview", "--job-id", commandPreviewJobID}},
		{name: "missing selector", args: []string{"--group", commandPreviewGroupID, "baseline", "preview"}},
		{name: "both selectors", args: []string{"--group", commandPreviewGroupID, "baseline", "preview", "--job-id", commandPreviewJobID, "--digest-id", commandPreviewDigestID}},
		{name: "obsolete run id", args: []string{"--group", commandPreviewGroupID, "baseline", "preview", "--run-id", "obsolete"}},
		{name: "positional input", args: []string{"--group", commandPreviewGroupID, "baseline", "preview", "extra", "--job-id", commandPreviewJobID}},
		{name: "raw query flag rejected", args: []string{"--group", commandPreviewGroupID, "baseline", "preview", "--job-id", commandPreviewJobID, "--retrieval-query", "private source"}},
		{name: "evidence flag rejected", args: []string{"--group", commandPreviewGroupID, "baseline", "preview", "--job-id", commandPreviewJobID, "--evidence", "private source"}},
		{name: "prompt flag rejected", args: []string{"--group", commandPreviewGroupID, "baseline", "preview", "--job-id", commandPreviewJobID, "--prompt", "private prompt"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := executeBaselineForTest(t, "http://127.0.0.1:1", test.args...)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if stdout != "" {
				t.Fatalf("stdout = %q", stdout)
			}
			if strings.Contains(strings.ToLower(stderr+err.Error()), "private") {
				t.Fatalf("diagnostic leaked argument value: %q %v", stderr, err)
			}
			if code := exitCodeForError(err); code != baselinePreviewUsageExitCode {
				t.Fatalf("exit code = %d", code)
			}
		})
	}
}

func TestBaselinePreviewDocumentedExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantCode   int
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, wantCode: baselinePreviewAuthExitCode},
		{name: "forbidden", statusCode: http.StatusForbidden, wantCode: baselinePreviewAuthExitCode},
		{name: "not found", statusCode: http.StatusNotFound, wantCode: baselinePreviewNotFoundExitCode},
		{name: "server failure", statusCode: http.StatusServiceUnavailable, wantCode: baselinePreviewTransportExitCode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{}`, test.statusCode)
			}))
			defer server.Close()
			stdout, _, err := executeBaselineForTest(
				t, server.URL, "--group", commandPreviewGroupID, "baseline", "preview",
				"--digest-id", commandPreviewDigestID,
			)
			if err == nil {
				t.Fatal("expected command error")
			}
			if stdout != "" {
				t.Fatalf("stdout = %q", stdout)
			}
			if code := exitCodeForError(err); code != test.wantCode {
				t.Fatalf("exit code = %d, want %d", code, test.wantCode)
			}
		})
	}
}

func TestBaselinePreviewTransportFailureAndLegacyNotificationTree(t *testing.T) {
	stdout, _, err := executeBaselineForTest(
		t, "http://127.0.0.1:1", "--group", commandPreviewGroupID,
		"baseline", "preview", "--job-id", commandPreviewJobID,
	)
	if err == nil || exitCodeForError(err) != baselinePreviewTransportExitCode {
		t.Fatalf("error = %v, code = %d", err, exitCodeForError(err))
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	for _, path := range [][]string{
		{"notifications"},
		{"notifications", "ack"},
		{"notifications", "dismiss"},
		{"notifications", "share"},
	} {
		if command, _, findErr := rootCmd.Find(path); findErr != nil || command == nil {
			t.Fatalf("legacy command %v missing: %v", path, findErr)
		}
	}
}
