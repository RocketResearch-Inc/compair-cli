package baseline

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LocalRepositoryAuthority           = "compair-local-repository.v1"
	RepositoryIdentityVersion          = "repository-identity.v1"
	RepositoryAdminSchemaVersion       = "baseline-repository-registration-admin.v1"
	RepositoryDiscoverySchemaVersion   = "baseline-repository-discovery.v1"
	LocalRepositoryResultSchemaVersion = "baseline-local-repository-result.v1"
)

var repositoryUIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,255}$`)

type RepositoryFailureKind string

const (
	RepositoryFailureUsage          RepositoryFailureKind = "usage"
	RepositoryFailureAuthentication RepositoryFailureKind = "authentication_authorization"
	RepositoryFailureContract       RepositoryFailureKind = "contract_conflict"
	RepositoryFailureRepository     RepositoryFailureKind = "local_repository"
	RepositoryFailureRetryable      RepositoryFailureKind = "retryable_transport"
	RepositoryFailureInternal       RepositoryFailureKind = "internal_failure"
)

type RepositoryError struct {
	Kind   RepositoryFailureKind
	Reason string
}

func (err *RepositoryError) Error() string {
	if err == nil || err.Reason == "" {
		return "baseline repository operation failed"
	}
	return "baseline repository operation failed: " + err.Reason
}

func repositoryError(kind RepositoryFailureKind, reason string) error {
	return &RepositoryError{Kind: kind, Reason: reason}
}

func RepositoryFailure(err error) RepositoryFailureKind {
	var target *RepositoryError
	if errors.As(err, &target) {
		return target.Kind
	}
	var control *ControlHTTPError
	if errors.As(err, &control) {
		if control.StatusCode == 401 || control.StatusCode == 403 || control.StatusCode == 404 {
			return RepositoryFailureAuthentication
		}
		if control.Retryable || control.StatusCode >= 500 {
			return RepositoryFailureRetryable
		}
		return RepositoryFailureContract
	}
	return RepositoryFailureInternal
}

func SafeRepositoryReason(err error) string {
	var target *RepositoryError
	if errors.As(err, &target) && target.Reason != "" {
		return target.Reason
	}
	var control *ControlHTTPError
	if errors.As(err, &control) && control.Code != "" {
		return control.Code
	}
	return "internal_failure"
}

type RepositoryOptions struct {
	BaseURL           string
	Token             string
	AllowLoopbackHTTP bool
	StateDirectory    string
}

type RepositoryIdentityDescriptor struct {
	Version       string `json:"version"`
	Authority     string `json:"authority"`
	RepositoryUID string `json:"repository_uid"`
}

type RepositoryMetadata struct {
	RegistrationID           string                       `json:"registration_id"`
	GroupID                  string                       `json:"group_id"`
	IdentityDescriptorSHA256 string                       `json:"identity_descriptor_hash"`
	IdentityDescriptor       RepositoryIdentityDescriptor `json:"identity_descriptor"`
	State                    string                       `json:"state"`
	SourceDocumentID         *string                      `json:"source_document_id"`
	CreatedAt                string                       `json:"created_at"`
	UpdatedAt                string                       `json:"updated_at"`
}

type RepositoryListResult struct {
	SchemaVersion string               `json:"schema_version"`
	MessageType   string               `json:"message_type"`
	RequestID     string               `json:"request_id"`
	GroupID       string               `json:"group_id"`
	Repositories  []RepositoryMetadata `json:"repositories"`
}

type RepositoryInspectionResult struct {
	SchemaVersion string             `json:"schema_version"`
	MessageType   string             `json:"message_type"`
	RequestID     string             `json:"request_id"`
	GroupID       string             `json:"group_id"`
	Repository    RepositoryMetadata `json:"repository"`
}

type RepositoryOperationResult struct {
	SchemaVersion            string  `json:"schema_version"`
	Operation                string  `json:"operation"`
	GroupID                  string  `json:"group_id"`
	RegistrationID           string  `json:"registration_id"`
	State                    string  `json:"state"`
	SourceDocumentID         *string `json:"source_document_id"`
	IdentityDescriptorSHA256 string  `json:"identity_descriptor_sha256"`
	BindingID                string  `json:"binding_id,omitempty"`
	PathSHA256               string  `json:"path_sha256,omitempty"`
	GitSanitySHA256          string  `json:"git_sanity_sha256,omitempty"`
	Replayed                 bool    `json:"replayed"`
}

type repositoryAdminResponse struct {
	SchemaVersion            string `json:"schema_version"`
	MessageType              string `json:"message_type"`
	RequestID                string `json:"request_id"`
	GroupID                  string `json:"group_id"`
	RegistrationID           string `json:"registration_id"`
	IdentityDescriptorSHA256 string `json:"identity_descriptor_hash"`
	State                    string `json:"state"`
	Replayed                 bool   `json:"replayed"`
}

type localRepositoryInfo struct {
	CanonicalPath   string
	PathSHA256      string
	SanitySHA256    string
	GitObjectFormat string
}

func RegisterLocalRepository(ctx context.Context, groupID, localPath, sourceDocumentID, displayName string, options RepositoryOptions) (RepositoryOperationResult, error) {
	if !validGroupID(groupID) || strings.TrimSpace(localPath) == "" || (sourceDocumentID != "" && !validUUID(sourceDocumentID)) || !validDisplayName(displayName) {
		return RepositoryOperationResult{}, repositoryError(RepositoryFailureUsage, "invalid_registration_input")
	}
	repository, err := inspectLocalRepository(ctx, localPath)
	if err != nil {
		return RepositoryOperationResult{}, err
	}
	store, err := newRepositoryBindingStore(options.StateDirectory)
	if err != nil {
		return RepositoryOperationResult{}, err
	}
	var result RepositoryOperationResult
	err = store.withLock(ctx, func() error {
		bindings, loadErr := store.loadAll()
		if loadErr != nil {
			return loadErr
		}
		var existing *RepositoryBinding
		for index := range bindings {
			binding := &bindings[index]
			if binding.GroupID == groupID && binding.CanonicalPath == repository.CanonicalPath {
				if existing != nil {
					return repositoryError(RepositoryFailureContract, "duplicate_local_binding")
				}
				existing = binding
			}
		}
		repositoryUID := ""
		if existing != nil {
			repositoryUID = existing.RepositoryUID
			if stringPointerValue(existing.SourceDocumentID) != sourceDocumentID {
				return repositoryError(RepositoryFailureContract, "source_document_conflict")
			}
		} else {
			uid, uidErr := newRepositoryUID()
			if uidErr != nil {
				return repositoryError(RepositoryFailureInternal, "repository_uid_unavailable")
			}
			repositoryUID = uid
		}
		descriptor := RepositoryIdentityDescriptor{Version: RepositoryIdentityVersion, Authority: LocalRepositoryAuthority, RepositoryUID: repositoryUID}
		descriptorHash, hashErr := repositoryDescriptorHash(descriptor)
		if hashErr != nil {
			return repositoryError(RepositoryFailureInternal, "identity_hash_failed")
		}
		client, clientErr := NewControlClient(options.BaseURL, options.Token, options.AllowLoopbackHTTP)
		if clientErr != nil {
			return repositoryError(RepositoryFailureAuthentication, SafeUploadReason(clientErr))
		}
		requestID, requestErr := newUUID()
		if requestErr != nil {
			return repositoryError(RepositoryFailureInternal, "request_identity_unavailable")
		}
		var source any
		if sourceDocumentID != "" {
			source = sourceDocumentID
		}
		request := struct {
			SchemaVersion      string                       `json:"schema_version"`
			MessageType        string                       `json:"message_type"`
			RequestID          string                       `json:"request_id"`
			GroupID            string                       `json:"group_id"`
			IdentityDescriptor RepositoryIdentityDescriptor `json:"identity_descriptor"`
			SourceDocumentID   any                          `json:"source_document_id"`
		}{RepositoryAdminSchemaVersion, "repository_registration_create", requestID, groupID, descriptor, source}
		body, marshalErr := canonicalJSONBytes(request)
		if marshalErr != nil {
			return repositoryError(RepositoryFailureInternal, "request_encoding_failed")
		}
		response, postErr := client.post(ctx, "/baseline/control/admin/v1/repositories/register", body)
		if postErr != nil {
			return postErr
		}
		var accepted repositoryAdminResponse
		if decodeErr := decodeStrictResponseJSON(response.Body, &accepted); decodeErr != nil || !validAdminResponse(accepted, requestID, groupID) || accepted.IdentityDescriptorSHA256 != descriptorHash || accepted.State != "active" {
			return repositoryError(RepositoryFailureContract, "invalid_registration_response")
		}
		if existing != nil && existing.RegistrationID != accepted.RegistrationID {
			return repositoryError(RepositoryFailureContract, "registration_identity_conflict")
		}
		now := timestamp(time.Now())
		binding := RepositoryBinding{
			SchemaVersion: repositoryBindingSchemaVersion, GroupID: groupID,
			RegistrationID: accepted.RegistrationID, RepositoryUID: repositoryUID,
			IdentityDescriptorSHA256: descriptorHash, DisplayName: strings.TrimSpace(displayName),
			CanonicalPath: repository.CanonicalPath, PathSHA256: repository.PathSHA256,
			GitSanitySHA256: repository.SanitySHA256, GitObjectFormat: repository.GitObjectFormat,
			SourceDocumentID: nullableString(sourceDocumentID), UpdatedAt: now,
		}
		if existing != nil {
			binding.BindingID = existing.BindingID
			binding.CreatedAt = existing.CreatedAt
			if binding.DisplayName == "" {
				binding.DisplayName = existing.DisplayName
			}
		} else {
			binding.BindingID, requestErr = newUUID()
			if requestErr != nil {
				return repositoryError(RepositoryFailureInternal, "binding_identity_unavailable")
			}
			binding.CreatedAt = now
		}
		if saveErr := store.save(&binding); saveErr != nil {
			return saveErr
		}
		result = RepositoryOperationResult{SchemaVersion: LocalRepositoryResultSchemaVersion, Operation: "registered", GroupID: groupID, RegistrationID: accepted.RegistrationID, State: accepted.State, SourceDocumentID: binding.SourceDocumentID, IdentityDescriptorSHA256: descriptorHash, BindingID: binding.BindingID, PathSHA256: binding.PathSHA256, GitSanitySHA256: binding.GitSanitySHA256, Replayed: accepted.Replayed}
		return nil
	})
	return result, err
}

func ListRepositoryRegistrations(ctx context.Context, groupID string, options RepositoryOptions) (RepositoryListResult, error) {
	if !validGroupID(groupID) {
		return RepositoryListResult{}, repositoryError(RepositoryFailureUsage, "invalid_group")
	}
	requestID, err := newUUID()
	if err != nil {
		return RepositoryListResult{}, repositoryError(RepositoryFailureInternal, "request_identity_unavailable")
	}
	request := struct {
		SchemaVersion string `json:"schema_version"`
		MessageType   string `json:"message_type"`
		RequestID     string `json:"request_id"`
		GroupID       string `json:"group_id"`
	}{RepositoryDiscoverySchemaVersion, "repository_list_request", requestID, groupID}
	var result RepositoryListResult
	if err := repositoryPost(ctx, options, "/baseline/control/admin/v1/repositories/list", request, &result); err != nil {
		return RepositoryListResult{}, err
	}
	if result.SchemaVersion != RepositoryDiscoverySchemaVersion || result.MessageType != "repository_list" || result.RequestID != requestID || result.GroupID != groupID {
		return RepositoryListResult{}, repositoryError(RepositoryFailureContract, "invalid_repository_list_response")
	}
	previous := ""
	for _, repository := range result.Repositories {
		if err := validateRepositoryMetadata(repository, groupID); err != nil || (previous != "" && repository.RegistrationID <= previous) {
			return RepositoryListResult{}, repositoryError(RepositoryFailureContract, "invalid_repository_list_response")
		}
		previous = repository.RegistrationID
	}
	return result, nil
}

func InspectRepositoryRegistration(ctx context.Context, groupID, registrationID string, options RepositoryOptions) (RepositoryInspectionResult, error) {
	if !validGroupID(groupID) || !validUUID(registrationID) {
		return RepositoryInspectionResult{}, repositoryError(RepositoryFailureUsage, "invalid_repository_selector")
	}
	requestID, err := newUUID()
	if err != nil {
		return RepositoryInspectionResult{}, repositoryError(RepositoryFailureInternal, "request_identity_unavailable")
	}
	request := struct {
		SchemaVersion  string `json:"schema_version"`
		MessageType    string `json:"message_type"`
		RequestID      string `json:"request_id"`
		GroupID        string `json:"group_id"`
		RegistrationID string `json:"registration_id"`
	}{RepositoryDiscoverySchemaVersion, "repository_inspect_request", requestID, groupID, registrationID}
	var result RepositoryInspectionResult
	if err := repositoryPost(ctx, options, "/baseline/control/v1/repositories/inspect", request, &result); err != nil {
		return RepositoryInspectionResult{}, err
	}
	if result.SchemaVersion != RepositoryDiscoverySchemaVersion || result.MessageType != "repository_inspection" || result.RequestID != requestID || result.GroupID != groupID || result.Repository.RegistrationID != registrationID || validateRepositoryMetadata(result.Repository, groupID) != nil {
		return RepositoryInspectionResult{}, repositoryError(RepositoryFailureContract, "invalid_repository_inspection_response")
	}
	return result, nil
}

func SetRepositoryRegistrationState(ctx context.Context, groupID, registrationID string, active bool, options RepositoryOptions) (RepositoryOperationResult, error) {
	if !validGroupID(groupID) || !validUUID(registrationID) {
		return RepositoryOperationResult{}, repositoryError(RepositoryFailureUsage, "invalid_repository_selector")
	}
	requestID, err := newUUID()
	if err != nil {
		return RepositoryOperationResult{}, repositoryError(RepositoryFailureInternal, "request_identity_unavailable")
	}
	request := struct {
		SchemaVersion  string `json:"schema_version"`
		MessageType    string `json:"message_type"`
		RequestID      string `json:"request_id"`
		GroupID        string `json:"group_id"`
		RegistrationID string `json:"registration_id"`
		Active         bool   `json:"active"`
	}{RepositoryAdminSchemaVersion, "repository_registration_state", requestID, groupID, registrationID, active}
	var response repositoryAdminResponse
	if err := repositoryPost(ctx, options, "/baseline/control/admin/v1/repositories/state", request, &response); err != nil {
		return RepositoryOperationResult{}, err
	}
	if !validAdminResponse(response, requestID, groupID) || response.RegistrationID != registrationID || response.State != map[bool]string{true: "active", false: "disabled"}[active] {
		return RepositoryOperationResult{}, repositoryError(RepositoryFailureContract, "invalid_repository_state_response")
	}
	return RepositoryOperationResult{SchemaVersion: LocalRepositoryResultSchemaVersion, Operation: "state_changed", GroupID: groupID, RegistrationID: registrationID, State: response.State, IdentityDescriptorSHA256: response.IdentityDescriptorSHA256, Replayed: response.Replayed}, nil
}

func BindLocalRepository(ctx context.Context, groupID, registrationID, localPath, displayName string, options RepositoryOptions) (RepositoryOperationResult, error) {
	if !validGroupID(groupID) || !validUUID(registrationID) || strings.TrimSpace(localPath) == "" || !validDisplayName(displayName) {
		return RepositoryOperationResult{}, repositoryError(RepositoryFailureUsage, "invalid_binding_input")
	}
	repository, err := inspectLocalRepository(ctx, localPath)
	if err != nil {
		return RepositoryOperationResult{}, err
	}
	inspection, err := InspectRepositoryRegistration(ctx, groupID, registrationID, options)
	if err != nil {
		return RepositoryOperationResult{}, err
	}
	metadata := inspection.Repository
	if metadata.State != "active" || metadata.IdentityDescriptor.Version != RepositoryIdentityVersion || metadata.IdentityDescriptor.Authority != LocalRepositoryAuthority {
		return RepositoryOperationResult{}, repositoryError(RepositoryFailureAuthentication, "repository_not_active_or_local")
	}
	store, err := newRepositoryBindingStore(options.StateDirectory)
	if err != nil {
		return RepositoryOperationResult{}, err
	}
	var result RepositoryOperationResult
	err = store.withLock(ctx, func() error {
		bindings, loadErr := store.loadAll()
		if loadErr != nil {
			return loadErr
		}
		var existing *RepositoryBinding
		for index := range bindings {
			binding := &bindings[index]
			if binding.GroupID == groupID && binding.CanonicalPath == repository.CanonicalPath {
				if binding.RegistrationID != registrationID {
					return repositoryError(RepositoryFailureContract, "path_already_bound")
				}
				existing = binding
			}
		}
		now := timestamp(time.Now())
		binding := RepositoryBinding{SchemaVersion: repositoryBindingSchemaVersion, GroupID: groupID, RegistrationID: registrationID, RepositoryUID: metadata.IdentityDescriptor.RepositoryUID, IdentityDescriptorSHA256: metadata.IdentityDescriptorSHA256, DisplayName: strings.TrimSpace(displayName), CanonicalPath: repository.CanonicalPath, PathSHA256: repository.PathSHA256, GitSanitySHA256: repository.SanitySHA256, GitObjectFormat: repository.GitObjectFormat, SourceDocumentID: metadata.SourceDocumentID, UpdatedAt: now}
		replayed := existing != nil && existing.GitSanitySHA256 == binding.GitSanitySHA256
		if existing != nil {
			binding.BindingID, binding.CreatedAt = existing.BindingID, existing.CreatedAt
			if binding.DisplayName == "" {
				binding.DisplayName = existing.DisplayName
			}
		} else {
			binding.BindingID, err = newUUID()
			if err != nil {
				return repositoryError(RepositoryFailureInternal, "binding_identity_unavailable")
			}
			binding.CreatedAt = now
		}
		if saveErr := store.save(&binding); saveErr != nil {
			return saveErr
		}
		result = RepositoryOperationResult{SchemaVersion: LocalRepositoryResultSchemaVersion, Operation: "bound", GroupID: groupID, RegistrationID: registrationID, State: metadata.State, SourceDocumentID: metadata.SourceDocumentID, IdentityDescriptorSHA256: metadata.IdentityDescriptorSHA256, BindingID: binding.BindingID, PathSHA256: binding.PathSHA256, GitSanitySHA256: binding.GitSanitySHA256, Replayed: replayed}
		return nil
	})
	return result, err
}

func repositoryPost(ctx context.Context, options RepositoryOptions, endpoint string, request any, output any) error {
	client, err := NewControlClient(options.BaseURL, options.Token, options.AllowLoopbackHTTP)
	if err != nil {
		return repositoryError(RepositoryFailureAuthentication, SafeUploadReason(err))
	}
	body, err := canonicalJSONBytes(request)
	if err != nil {
		return repositoryError(RepositoryFailureInternal, "request_encoding_failed")
	}
	response, err := client.post(ctx, endpoint, body)
	if err != nil {
		return err
	}
	if err := decodeStrictResponseJSON(response.Body, output); err != nil {
		return repositoryError(RepositoryFailureContract, "invalid_repository_response")
	}
	return nil
}

func validAdminResponse(response repositoryAdminResponse, requestID, groupID string) bool {
	return response.SchemaVersion == RepositoryAdminSchemaVersion && response.MessageType == "repository_registration" && response.RequestID == requestID && response.GroupID == groupID && validUUID(response.RegistrationID) && validSHA256(response.IdentityDescriptorSHA256) && (response.State == "active" || response.State == "disabled")
}

func validateRepositoryMetadata(metadata RepositoryMetadata, groupID string) error {
	if !validUUID(metadata.RegistrationID) || metadata.GroupID != groupID || !validSHA256(metadata.IdentityDescriptorSHA256) || metadata.IdentityDescriptor.Version != RepositoryIdentityVersion || !repositoryUIDPattern.MatchString(metadata.IdentityDescriptor.RepositoryUID) || metadata.IdentityDescriptor.Authority == "" || (metadata.State != "active" && metadata.State != "disabled") || (metadata.SourceDocumentID != nil && !validUUID(*metadata.SourceDocumentID)) || metadata.CreatedAt == "" || metadata.UpdatedAt == "" {
		return repositoryError(RepositoryFailureContract, "invalid_repository_metadata")
	}
	hash, err := repositoryDescriptorHash(metadata.IdentityDescriptor)
	if err != nil || hash != metadata.IdentityDescriptorSHA256 {
		return repositoryError(RepositoryFailureContract, "repository_descriptor_mismatch")
	}
	return nil
}

func inspectLocalRepository(ctx context.Context, localPath string) (localRepositoryInfo, error) {
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureRepository, "repository_path_unavailable")
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureRepository, "repository_path_unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureContract, "symlink_repository_rejected")
	}
	if !info.IsDir() {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureRepository, "repository_path_unavailable")
	}
	scanner := NewScanner()
	canonical, err := scanner.canonicalRepositoryRoot(ctx, abs)
	if err != nil {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureRepository, "repository_root_invalid")
	}
	formatBytes, err := scanner.gitOutput(ctx, canonical, maximumGitScalarBytes, "rev-parse", "--show-object-format")
	if err != nil {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureRepository, "repository_object_format_unavailable")
	}
	objectFormat := strings.TrimSpace(string(formatBytes))
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureContract, "repository_object_format_unsupported")
	}
	gitDirectoryBytes, err := scanner.gitOutput(ctx, canonical, maximumGitScalarBytes, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureRepository, "repository_git_directory_unavailable")
	}
	gitDirectory := strings.TrimSpace(string(gitDirectoryBytes))
	if !filepath.IsAbs(gitDirectory) {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureContract, "repository_git_directory_invalid")
	}
	instanceIdentity, err := repositoryInstanceIdentity(gitDirectory)
	if err != nil {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureContract, "repository_git_directory_unsafe")
	}
	rootBytes, err := scanner.gitOutput(ctx, canonical, 1_000_000, "rev-list", "--max-parents=0", "--all")
	if err != nil {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureRepository, "repository_history_unavailable")
	}
	roots := strings.Fields(string(rootBytes))
	if len(roots) == 0 {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureRepository, "repository_has_no_commits")
	}
	sort.Strings(roots)
	wantLength := 40
	if objectFormat == "sha256" {
		wantLength = 64
	}
	for _, root := range roots {
		if len(root) != wantLength || !validGitRevision(root) {
			return localRepositoryInfo{}, repositoryError(RepositoryFailureContract, "repository_history_invalid")
		}
	}
	sanityValue := struct {
		GitDirectoryInstanceSHA256 string   `json:"git_directory_instance_sha256"`
		ObjectFormat               string   `json:"object_format"`
		RootCommits                []string `json:"root_commits"`
	}{sha256String(instanceIdentity), objectFormat, roots}
	canonicalSanity, err := canonicalJSONBytes(sanityValue)
	if err != nil {
		return localRepositoryInfo{}, repositoryError(RepositoryFailureInternal, "repository_sanity_failed")
	}
	return localRepositoryInfo{CanonicalPath: canonical, PathSHA256: sha256String(canonical), SanitySHA256: sha256Bytes(canonicalSanity), GitObjectFormat: objectFormat}, nil
}

func repositoryDescriptorHash(descriptor RepositoryIdentityDescriptor) (string, error) {
	canonical, err := canonicalJSONBytes(descriptor)
	if err != nil {
		return "", err
	}
	return sha256Bytes(canonical), nil
}

func sha256Bytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func newRepositoryUID() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "local_" + hex.EncodeToString(value), nil
}

func validDisplayName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || len([]byte(value)) > 128 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func EncodeRepositoryJSON(output any) ([]byte, error) {
	return json.Marshal(output)
}
