package baseline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

const (
	testRepositoryGroup  = "00000000-0000-4000-8000-000000000101"
	testRepositorySource = "00000000-0000-4000-8000-000000000102"
)

type repositoryServerRecord struct {
	RegistrationID string
	Descriptor     RepositoryIdentityDescriptor
	DescriptorHash string
	State          string
	SourceDocument *string
}

type repositoryTestServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	records map[string]*repositoryServerRecord
	bodies  []string
	next    int
}

func newRepositoryTestServer(t *testing.T) *repositoryTestServer {
	t.Helper()
	fixture := &repositoryTestServer{records: make(map[string]*repositoryServerRecord)}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var payload map[string]any
		decoder := json.NewDecoder(request.Body)
		if err := decoder.Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		encoded, _ := json.Marshal(payload)
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.bodies = append(fixture.bodies, string(encoded))
		requestID, _ := payload["request_id"].(string)
		groupID, _ := payload["group_id"].(string)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/baseline/control/admin/v1/repositories/register":
			descriptorValue := payload["identity_descriptor"].(map[string]any)
			descriptor := RepositoryIdentityDescriptor{Version: descriptorValue["version"].(string), Authority: descriptorValue["authority"].(string), RepositoryUID: descriptorValue["repository_uid"].(string)}
			hash, _ := repositoryDescriptorHash(descriptor)
			record := fixture.records[descriptor.RepositoryUID]
			replayed := record != nil
			if record == nil {
				fixture.next++
				registrationID := []string{"00000000-0000-4000-8000-000000000111", "00000000-0000-4000-8000-000000000112", "00000000-0000-4000-8000-000000000113"}[fixture.next-1]
				var source *string
				if raw, ok := payload["source_document_id"].(string); ok {
					source = &raw
				}
				record = &repositoryServerRecord{RegistrationID: registrationID, Descriptor: descriptor, DescriptorHash: hash, State: "active", SourceDocument: source}
				fixture.records[descriptor.RepositoryUID] = record
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"schema_version": RepositoryAdminSchemaVersion, "message_type": "repository_registration", "request_id": requestID, "group_id": groupID, "registration_id": record.RegistrationID, "identity_descriptor_hash": record.DescriptorHash, "state": record.State, "replayed": replayed})
		case "/baseline/control/admin/v1/repositories/list":
			items := fixture.metadata(groupID)
			_ = json.NewEncoder(writer).Encode(map[string]any{"schema_version": RepositoryDiscoverySchemaVersion, "message_type": "repository_list", "request_id": requestID, "group_id": groupID, "repositories": items})
		case "/baseline/control/v1/repositories/inspect":
			registrationID := payload["registration_id"].(string)
			record := fixture.byRegistration(registrationID)
			if record == nil {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"schema_version": RepositoryDiscoverySchemaVersion, "message_type": "repository_inspection", "request_id": requestID, "group_id": groupID, "repository": fixture.metadataRecord(groupID, record)})
		case "/baseline/control/admin/v1/repositories/state":
			record := fixture.byRegistration(payload["registration_id"].(string))
			active := payload["active"].(bool)
			state := map[bool]string{true: "active", false: "disabled"}[active]
			replayed := record.State == state
			record.State = state
			_ = json.NewEncoder(writer).Encode(map[string]any{"schema_version": RepositoryAdminSchemaVersion, "message_type": "repository_registration", "request_id": requestID, "group_id": groupID, "registration_id": record.RegistrationID, "identity_descriptor_hash": record.DescriptorHash, "state": record.State, "replayed": replayed})
		default:
			t.Errorf("unexpected endpoint %s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *repositoryTestServer) byRegistration(registrationID string) *repositoryServerRecord {
	for _, record := range fixture.records {
		if record.RegistrationID == registrationID {
			return record
		}
	}
	return nil
}

func (fixture *repositoryTestServer) metadataRecord(groupID string, record *repositoryServerRecord) RepositoryMetadata {
	return RepositoryMetadata{RegistrationID: record.RegistrationID, GroupID: groupID, IdentityDescriptorSHA256: record.DescriptorHash, IdentityDescriptor: record.Descriptor, State: record.State, SourceDocumentID: record.SourceDocument, CreatedAt: "2026-08-17T12:00:00Z", UpdatedAt: "2026-08-17T12:00:00Z"}
}

func (fixture *repositoryTestServer) metadata(groupID string) []RepositoryMetadata {
	items := make([]RepositoryMetadata, 0, len(fixture.records))
	for _, record := range fixture.records {
		items = append(items, fixture.metadataRecord(groupID, record))
	}
	sort.Slice(items, func(left, right int) bool { return items[left].RegistrationID < items[right].RegistrationID })
	return items
}

func (fixture *repositoryTestServer) options(stateDirectory string) RepositoryOptions {
	return RepositoryOptions{BaseURL: fixture.server.URL, Token: "repository-test-token", AllowLoopbackHTTP: true, StateDirectory: stateDirectory}
}

func TestLocalRepositoryRegistrationBindingReplayAndNoDisclosure(t *testing.T) {
	fixture := newRepositoryTestServer(t)
	root := initScannerRepo(t)
	writeScannerFile(t, filepath.Join(root, "safe.txt"), []byte("safe\n"), 0o644)
	gitTest(t, root, "add", "--", "safe.txt")
	gitTest(t, root, "commit", "-m", "safe")
	gitTest(t, root, "remote", "add", "origin", "https://user:private-token@example.test/repository.git")
	state := t.TempDir()
	options := fixture.options(state)

	first, err := RegisterLocalRepository(context.Background(), testRepositoryGroup, root, testRepositorySource, "Repository café", options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RegisterLocalRepository(context.Background(), testRepositoryGroup, root, testRepositorySource, "Repository café", options)
	if err != nil {
		t.Fatal(err)
	}
	if first.RegistrationID != second.RegistrationID || first.BindingID != second.BindingID || !second.Replayed {
		t.Fatalf("registration replay = %#v %#v", first, second)
	}
	if len(fixture.records) != 1 {
		t.Fatalf("server registrations = %d", len(fixture.records))
	}
	for _, body := range fixture.bodies {
		for _, forbidden := range []string{root, "private-token", "repository.git", "safe\n"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("request body leaked %q: %s", forbidden, body)
			}
		}
	}
	store, err := newRepositoryBindingStore(state)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := store.loadAll()
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings = %#v, %v", bindings, err)
	}
	binding := bindings[0]
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	if binding.CanonicalPath != canonicalRoot || binding.RepositoryUID == "" || binding.GitSanitySHA256 == "" || binding.SourceDocumentID == nil || *binding.SourceDocumentID != testRepositorySource {
		t.Fatalf("binding = %#v", binding)
	}
	encoded, _ := json.Marshal(binding)
	for _, forbidden := range []string{"private-token", "repository.git", "safe\n"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("binding leaked %q", forbidden)
		}
	}
}

func TestBindingRequiresExplicitRebindAndRejectsCorruptionAndSymlinks(t *testing.T) {
	fixture := newRepositoryTestServer(t)
	root := initScannerRepo(t)
	writeScannerFile(t, filepath.Join(root, "file.txt"), []byte("one\n"), 0o644)
	gitTest(t, root, "add", "--", "file.txt")
	gitTest(t, root, "commit", "-m", "one")
	state := t.TempDir()
	registered, err := RegisterLocalRepository(context.Background(), testRepositoryGroup, root, testRepositorySource, "", fixture.options(state))
	if err != nil {
		t.Fatal(err)
	}
	moved := root + " moved café"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	store, _ := newRepositoryBindingStore(state)
	bindings, _ := store.loadAll()
	if _, err := resolvePlanRepository(context.Background(), testRepositoryGroup, moved, bindings, fixture.options(state)); err == nil || SafeRepositoryReason(err) != "repository_binding_missing" {
		t.Fatalf("moved repository resolved without rebind: %v", err)
	}
	bound, err := BindLocalRepository(context.Background(), testRepositoryGroup, registered.RegistrationID, moved, "", fixture.options(state))
	if err != nil || bound.BindingID == registered.BindingID {
		t.Fatalf("explicit rebind = %#v, %v", bound, err)
	}

	cloneParent := t.TempDir()
	replacement := filepath.Join(cloneParent, "replacement")
	gitTest(t, cloneParent, "clone", "--quiet", moved, replacement)
	displaced := moved + " displaced"
	if err := os.Rename(moved, displaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, moved); err != nil {
		t.Fatal(err)
	}
	bindings, _ = store.loadAll()
	if _, err := resolvePlanRepository(context.Background(), testRepositoryGroup, moved, bindings, fixture.options(state)); err == nil || SafeRepositoryReason(err) != "repository_binding_sanity_mismatch" {
		t.Fatalf("same-path reclone resolved without rebind: %v", err)
	}
	rebound, err := BindLocalRepository(context.Background(), testRepositoryGroup, registered.RegistrationID, moved, "", fixture.options(state))
	if err != nil || rebound.BindingID != bound.BindingID || rebound.Replayed {
		t.Fatalf("same-path explicit rebind = %#v, %v", rebound, err)
	}

	if err := os.WriteFile(filepath.Join(state, bound.BindingID+".json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.loadAll(); err == nil || SafeRepositoryReason(err) != "corrupt_binding_state" {
		t.Fatalf("corrupt state accepted: %v", err)
	}

	if err := os.Remove(filepath.Join(state, bound.BindingID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(state, registered.BindingID+".json"), filepath.Join(state, bound.BindingID+".json")); err == nil {
		if _, err := store.loadAll(); err == nil || SafeRepositoryReason(err) != "unsafe_or_corrupt_binding_state" {
			t.Fatalf("symlinked state accepted: %v", err)
		}
	}
	symlinkRoot := filepath.Join(t.TempDir(), "repository-link")
	if err := os.Symlink(moved, symlinkRoot); err == nil {
		if _, err := inspectLocalRepository(context.Background(), symlinkRoot); err == nil || SafeRepositoryReason(err) != "symlink_repository_rejected" {
			t.Fatalf("symlinked repository accepted: %v", err)
		}
	}
}

func TestConcurrentRegistrationUsesOneUIDAndBinding(t *testing.T) {
	fixture := newRepositoryTestServer(t)
	root := initScannerRepo(t)
	writeScannerFile(t, filepath.Join(root, "file.txt"), []byte("race\n"), 0o644)
	gitTest(t, root, "add", "--", "file.txt")
	gitTest(t, root, "commit", "-m", "race")
	state := t.TempDir()
	options := fixture.options(state)
	start := make(chan struct{})
	results := make(chan RepositoryOperationResult, 2)
	errors := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			result, err := RegisterLocalRepository(context.Background(), testRepositoryGroup, root, testRepositorySource, "", options)
			results <- result
			errors <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if first.RegistrationID != second.RegistrationID || first.BindingID != second.BindingID || len(fixture.records) != 1 {
		t.Fatalf("concurrent results = %#v %#v records=%d", first, second, len(fixture.records))
	}
	store, _ := newRepositoryBindingStore(state)
	bindings, err := store.loadAll()
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings = %#v, %v", bindings, err)
	}
	if info, err := os.Stat(filepath.Join(state, bindings[0].BindingID+".json")); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("binding permissions = %v, %v", info, err)
	}
}

func TestRepositorySanitySupportsSHA1AndSHA256(t *testing.T) {
	for _, objectFormat := range []string{"sha1", "sha256"} {
		t.Run(objectFormat, func(t *testing.T) {
			root := initScannerRepoWithObjectFormat(t, objectFormat)
			writeScannerFile(t, filepath.Join(root, "format.txt"), []byte(objectFormat+"\n"), 0o644)
			gitTest(t, root, "add", "--", "format.txt")
			gitTest(t, root, "commit", "-m", "object format")
			info, err := inspectLocalRepository(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if info.GitObjectFormat != objectFormat || !validSHA256(info.SanitySHA256) {
				t.Fatalf("repository info = %#v", info)
			}
		})
	}
}
