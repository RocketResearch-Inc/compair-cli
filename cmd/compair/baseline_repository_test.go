package compair

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/baseline"
	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

type commandRepositoryRecord struct {
	id         string
	descriptor map[string]any
	hash       string
	state      string
	source     any
}

func TestBaselineRepositoryAndPlanCommandsCompleteSupportedWorkflow(t *testing.T) {
	input, _, protected := commandScanFixture(t)
	input.GroupID = "00000000-0000-4000-8000-000000000201"
	installCommandCredential(t, "repository-command-token")
	var mutex sync.Mutex
	records := map[string]*commandRepositoryRecord{}
	requestBodies := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(payload)
		mutex.Lock()
		defer mutex.Unlock()
		requestBodies = append(requestBodies, string(encoded))
		requestID := payload["request_id"].(string)
		groupID := payload["group_id"].(string)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/baseline/control/admin/v1/repositories/register":
			descriptor := payload["identity_descriptor"].(map[string]any)
			uid := descriptor["repository_uid"].(string)
			record := records[uid]
			replayed := record != nil
			if record == nil {
				canonical, _ := jsoncanonicalizer.Transform(mustCommandJSON(t, descriptor))
				digest := sha256.Sum256(canonical)
				ids := []string{"00000000-0000-4000-8000-000000000211", "00000000-0000-4000-8000-000000000212"}
				record = &commandRepositoryRecord{id: ids[len(records)], descriptor: descriptor, hash: hex.EncodeToString(digest[:]), state: "active", source: payload["source_document_id"]}
				records[uid] = record
			}
			writeCommandRepositoryJSON(t, writer, map[string]any{"schema_version": baseline.RepositoryAdminSchemaVersion, "message_type": "repository_registration", "request_id": requestID, "group_id": groupID, "registration_id": record.id, "identity_descriptor_hash": record.hash, "state": record.state, "replayed": replayed})
		case "/baseline/control/admin/v1/repositories/list":
			items := make([]map[string]any, 0, len(records))
			for _, record := range records {
				items = append(items, commandRepositoryMetadata(groupID, record))
			}
			sort.Slice(items, func(left, right int) bool {
				return items[left]["registration_id"].(string) < items[right]["registration_id"].(string)
			})
			writeCommandRepositoryJSON(t, writer, map[string]any{"schema_version": baseline.RepositoryDiscoverySchemaVersion, "message_type": "repository_list", "request_id": requestID, "group_id": groupID, "repositories": items})
		case "/baseline/control/v1/repositories/inspect":
			record := commandRepositoryByID(records, payload["registration_id"].(string))
			writeCommandRepositoryJSON(t, writer, map[string]any{"schema_version": baseline.RepositoryDiscoverySchemaVersion, "message_type": "repository_inspection", "request_id": requestID, "group_id": groupID, "repository": commandRepositoryMetadata(groupID, record)})
		case "/baseline/control/admin/v1/repositories/state":
			record := commandRepositoryByID(records, payload["registration_id"].(string))
			record.state = map[bool]string{true: "active", false: "disabled"}[payload["active"].(bool)]
			writeCommandRepositoryJSON(t, writer, map[string]any{"schema_version": baseline.RepositoryAdminSchemaVersion, "message_type": "repository_registration", "request_id": requestID, "group_id": groupID, "registration_id": record.id, "identity_descriptor_hash": record.hash, "state": record.state, "replayed": false})
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()

	register := func(path, source string) baseline.RepositoryOperationResult {
		args := []string{"baseline", "repository", "register", "--group", input.GroupID, "--path", path, "--allow-loopback-http", "--json"}
		if source != "" {
			args = append(args, "--source-document-id", source)
		}
		stdout, stderr, err := executeBaselineForTest(t, server.URL, args...)
		if err != nil || stderr != "" {
			t.Fatalf("register = %q %q %v", stdout, stderr, err)
		}
		var result baseline.RepositoryOperationResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	changed := register(input.Changed.LocalPath, input.Changed.SourceDocumentID)
	sibling := register(input.Siblings[0].LocalPath, "")

	stdout, _, err := executeBaselineForTest(t, server.URL, "baseline", "repository", "list", "--group", input.GroupID, "--allow-loopback-http", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var listed baseline.RepositoryListResult
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil || len(listed.Repositories) != 2 {
		t.Fatalf("list = %#v, %v", listed, err)
	}
	stdout, _, err = executeBaselineForTest(t, server.URL, "baseline", "repository", "inspect", "--group", input.GroupID, "--registration-id", changed.RegistrationID, "--allow-loopback-http", "--json")
	if err != nil || !strings.Contains(stdout, changed.RegistrationID) {
		t.Fatalf("inspect = %q, %v", stdout, err)
	}

	planPath := filepath.Join(t.TempDir(), "supported-plan.json")
	planArgs := []string{"baseline", "plan", "create", "--group", input.GroupID, "--changed", input.Changed.LocalPath, "--base", input.BaseRevision, "--head", input.HeadRevision, "--sibling", input.Siblings[0].LocalPath, "--output", planPath, "--allow-loopback-http", "--json"}
	stdout, _, err = executeBaselineForTest(t, server.URL, planArgs...)
	if err != nil {
		t.Fatalf("plan create = %q, %v", stdout, err)
	}
	var planResult baseline.PlanCreateResult
	if err := json.Unmarshal([]byte(stdout), &planResult); err != nil || planResult.ChangedRepositoryRegistration != changed.RegistrationID || planResult.SiblingRepositoryRegistrations[0] != sibling.RegistrationID {
		t.Fatalf("plan result = %#v, %v", planResult, err)
	}
	stdout, stderr, err := executeBaselineForTest(t, server.URL, "baseline", "scan", "--group", input.GroupID, "--plan", planPath, "--dry-run")
	if err != nil || stderr != "" {
		t.Fatalf("scan = %q %q %v", stdout, stderr, err)
	}
	var scan baseline.DryRunReport
	if err := json.Unmarshal([]byte(stdout), &scan); err != nil || scan.Errors == nil || len(scan.Errors) != 0 {
		t.Fatalf("scan report = %#v, %v", scan, err)
	}

	_, _, err = executeBaselineForTest(t, server.URL, "baseline", "repository", "state", "--group", input.GroupID, "--registration-id", sibling.RegistrationID, "--active=false", "--allow-loopback-http", "--json")
	if err != nil {
		t.Fatal(err)
	}
	planArgs = append(planArgs, "--overwrite")
	if _, _, err := executeBaselineForTest(t, server.URL, planArgs...); err == nil || exitCodeForError(err) != baselineRepositoryAuthExitCode {
		t.Fatalf("disabled plan result = %v", err)
	}
	_, _, err = executeBaselineForTest(t, server.URL, "baseline", "repository", "state", "--group", input.GroupID, "--registration-id", sibling.RegistrationID, "--active=true", "--allow-loopback-http", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeBaselineForTest(t, server.URL, planArgs...); err != nil {
		t.Fatalf("reactivated plan = %v", err)
	}

	mutex.Lock()
	defer mutex.Unlock()
	for _, body := range requestBodies {
		for _, forbidden := range append(protected, "local_path", "remote_url") {
			if strings.Contains(body, forbidden) {
				t.Fatalf("Core request leaked %q: %s", forbidden, body)
			}
		}
	}
}

func commandRepositoryMetadata(groupID string, record *commandRepositoryRecord) map[string]any {
	return map[string]any{"registration_id": record.id, "group_id": groupID, "identity_descriptor_hash": record.hash, "identity_descriptor": record.descriptor, "state": record.state, "source_document_id": record.source, "created_at": "2026-08-17T12:00:00Z", "updated_at": "2026-08-17T12:00:00Z"}
}

func commandRepositoryByID(records map[string]*commandRepositoryRecord, id string) *commandRepositoryRecord {
	for _, record := range records {
		if record.id == id {
			return record
		}
	}
	return nil
}

func mustCommandJSON(t *testing.T, value any) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := json.NewEncoder(&output).Encode(value); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(output.Bytes())
}

func writeCommandRepositoryJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatal(err)
	}
}
