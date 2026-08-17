package baseline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"
)

const (
	IndexControlProtocolVersion = "baseline-control-plane.v2"
	IndexControlProtocolSHA256  = "b278abe007779f05e92509db068f555701c03cba5cf236151e8df231a9b44091"
	IndexResultSchemaVersion    = "baseline-index-result.v1"
	maximumUploadResultBytes    = 256_000
	maximumV2ControlRequest     = 64_000
)

type IndexFailureKind string

const (
	IndexFailureInput      IndexFailureKind = "usage_input_contract"
	IndexFailureAuth       IndexFailureKind = "authentication_authorization"
	IndexFailureCapability IndexFailureKind = "capability_not_ready"
	IndexFailureConflict   IndexFailureKind = "conflict_stale_identity"
	IndexFailureRetryable  IndexFailureKind = "timeout_retryable_incomplete"
	IndexFailureTerminal   IndexFailureKind = "terminal_blocked_cancelled"
	IndexFailureContract   IndexFailureKind = "transport_server_contract"
	IndexFailureInternal   IndexFailureKind = "internal_failure"
)

type IndexError struct {
	Kind   IndexFailureKind
	Reason string
}

func (err *IndexError) Error() string {
	if err == nil || err.Reason == "" {
		return "baseline index operation failed"
	}
	return "baseline index operation failed: " + err.Reason
}

func indexError(kind IndexFailureKind, reason string) error {
	return &IndexError{Kind: kind, Reason: reason}
}

func IndexFailure(err error) IndexFailureKind {
	var target *IndexError
	if errors.As(err, &target) {
		return target.Kind
	}
	return IndexFailureInternal
}

func SafeIndexReason(err error) string {
	var target *IndexError
	if errors.As(err, &target) && validSafeReasonCode(target.Reason) {
		return target.Reason
	}
	return "internal_failure"
}

type IndexOptions struct {
	BaseURL           string
	Token             string
	AllowLoopbackHTTP bool
	Resume            bool
	Wait              bool
	Timeout           time.Duration
	PollInterval      time.Duration
	StateDirectory    string
	Progress          func(state string, attempt int)
	RetryAttempts     int
	RetryBaseDelay    time.Duration
	sleep             func(context.Context, time.Duration) error
	now               func() time.Time
}

type IndexResult struct {
	SchemaVersion                  string  `json:"schema_version"`
	ProtocolVersion                string  `json:"protocol_version"`
	ProtocolSHA256                 string  `json:"protocol_sha256"`
	GroupID                        string  `json:"group_id"`
	ScanFingerprint                *string `json:"scan_fingerprint"`
	IngestionContinuationID        *string `json:"ingestion_continuation_id"`
	CorpusID                       *string `json:"corpus_id"`
	CorpusGenerationID             *string `json:"corpus_generation_id"`
	CorpusGenerationVersion        *string `json:"corpus_generation_version"`
	CorpusManifestHash             *string `json:"corpus_manifest_hash"`
	IngestionProvenanceFingerprint *string `json:"ingestion_provenance_fingerprint"`
	IndexJobID                     *string `json:"index_job_id"`
	DispatchMode                   string  `json:"dispatch_mode"`
	State                          string  `json:"state"`
	ExitClassification             string  `json:"exit_classification"`
	CompatiblePublicationID        *string `json:"compatible_publication_id"`
	IndexFormatVersion             *string `json:"index_format_version"`
	TokenizerVersion               *string `json:"tokenizer_version"`
	IndexIntentFingerprint         *string `json:"index_intent_fingerprint"`
	RetrievalConfigFingerprint     *string `json:"retrieval_config_fingerprint"`
	EmbeddingFingerprint           *string `json:"embedding_fingerprint"`
	IndexFingerprint               *string `json:"index_fingerprint"`
	IndexedDocumentCount           *int    `json:"indexed_document_count"`
	VectorCount                    *int    `json:"vector_count"`
	Resumed                        bool    `json:"resumed"`
	Replayed                       bool    `json:"replayed"`
	TransmittedRequestCount        int     `json:"transmitted_request_count"`
	TransmittedRequestBytes        int64   `json:"transmitted_request_bytes"`
	CreatedAt                      *string `json:"created_at"`
	UpdatedAt                      string  `json:"updated_at"`
	ReasonCode                     *string `json:"reason_code"`
}

type IndexExecution struct {
	Result       IndexResult
	store        *indexStateStore
	stateID      string
	cleanupState bool
}

func (execution *IndexExecution) Finalize() error {
	if execution == nil || !execution.cleanupState || execution.store == nil || execution.stateID == "" {
		return nil
	}
	return execution.store.delete(execution.stateID)
}

type v2EmbeddingIdentity struct {
	ContractVersion string `json:"contract_version"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	Revision        string `json:"revision"`
	Dimension       int    `json:"dimension"`
	DType           string `json:"dtype"`
	Fingerprint     string `json:"fingerprint"`
}

type v2IndexIntent struct {
	IndexFormatVersion         string              `json:"index_format_version"`
	TokenizerVersion           string              `json:"tokenizer_version"`
	RetrievalConfigFingerprint string              `json:"retrieval_config_fingerprint"`
	Embedding                  v2EmbeddingIdentity `json:"embedding"`
}

type v2OperationCapability struct {
	Submission string  `json:"submission"`
	Endpoint   string  `json:"endpoint"`
	Dispatch   string  `json:"dispatch"`
	Readiness  string  `json:"readiness"`
	ReasonCode *string `json:"reason_code"`
}

type v2CapabilitiesResponse struct {
	ProtocolVersion string `json:"protocol_version"`
	ProtocolSHA256  string `json:"protocol_sha256"`
	MessageType     string `json:"message_type"`
	RequestID       string `json:"request_id"`
	GroupID         string `json:"group_id"`
	Supported       []struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
		Role    string `json:"role"`
	} `json:"supported_protocols"`
	Operations struct {
		IndexBuild  v2OperationCapability `json:"index_build"`
		BaselineRun v2OperationCapability `json:"baseline_run"`
	} `json:"operations"`
	Limits struct {
		ControlRequestBytes        int `json:"control_request_bytes"`
		RunRequestBytes            int `json:"run_request_bytes"`
		RawQueryBytes              int `json:"raw_query_bytes"`
		IdempotencyKeyMin          int `json:"idempotency_key_min_characters"`
		IdempotencyKeyMax          int `json:"idempotency_key_max_characters"`
		SelectedEvidenceItems      int `json:"selected_evidence_items"`
		SelectedEvidenceCharacters int `json:"selected_evidence_characters"`
		FeedbackItems              int `json:"feedback_items"`
		TerminalRetentionDays      int `json:"terminal_status_retention_days"`
	} `json:"limits"`
	RequiredIndexIdentity v2IndexIntent `json:"required_index_identity"`
	Transport             struct {
		Remote       string `json:"remote"`
		LoopbackHTTP string `json:"loopback_http"`
		JSONMedia    string `json:"json_media_type"`
		Encoding     string `json:"encoding"`
	} `json:"transport"`
}

type v2IndexAccepted struct {
	ProtocolVersion string  `json:"protocol_version"`
	ProtocolSHA256  string  `json:"protocol_sha256"`
	MessageType     string  `json:"message_type"`
	RequestID       string  `json:"request_id"`
	GroupID         string  `json:"group_id"`
	JobID           string  `json:"job_id"`
	Operation       string  `json:"operation"`
	State           string  `json:"state"`
	Replayed        bool    `json:"replayed"`
	ProcessingRunID *string `json:"processing_run_id"`
}

type v2IndexProgress struct {
	DocumentCount int `json:"document_count"`
	VectorCount   int `json:"vector_count"`
}

type v2IndexJobResult struct {
	IndexPublicationID         string `json:"index_publication_id"`
	CorpusGenerationID         string `json:"corpus_generation_id"`
	CorpusManifestHash         string `json:"corpus_manifest_hash"`
	IndexFingerprint           string `json:"index_fingerprint"`
	RetrievalConfigFingerprint string `json:"retrieval_config_fingerprint"`
	EmbeddingFingerprint       string `json:"embedding_fingerprint"`
	DocumentCount              int    `json:"document_count"`
	VectorCount                int    `json:"vector_count"`
}

type v2IndexStatus struct {
	ProtocolVersion    string            `json:"protocol_version"`
	ProtocolSHA256     string            `json:"protocol_sha256"`
	MessageType        string            `json:"message_type"`
	RequestID          string            `json:"request_id"`
	GroupID            string            `json:"group_id"`
	JobID              string            `json:"job_id"`
	Operation          string            `json:"operation"`
	State              string            `json:"state"`
	Terminal           bool              `json:"terminal"`
	ExitClassification string            `json:"exit_classification"`
	Attempt            int               `json:"attempt"`
	CreatedAt          string            `json:"created_at"`
	UpdatedAt          string            `json:"updated_at"`
	ContinuationID     string            `json:"ingestion_continuation_id"`
	CorpusGenerationID string            `json:"corpus_generation_id"`
	CorpusManifestHash string            `json:"corpus_manifest_hash"`
	IndexIntent        v2IndexIntent     `json:"index_intent"`
	Progress           v2IndexProgress   `json:"progress"`
	Result             *v2IndexJobResult `json:"result"`
	ReasonCode         *string           `json:"reason_code"`
	Replayed           bool              `json:"replayed"`
}

type continuationIdentity struct {
	CorpusID              string
	GenerationID          string
	GenerationVersion     string
	ManifestHash          string
	ProvenanceFingerprint string
	WorkerContractVersion string
}

type indexRuntime struct {
	client  *ControlClient
	options IndexOptions
	counter requestCounter
	result  *IndexResult
	store   *indexStateStore
	state   *indexState
	stateID string
}

func LoadSuccessfulUploadResult(filename, groupID string) (UploadResult, error) {
	var result UploadResult
	info, err := os.Lstat(filename)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumUploadResultBytes {
		return result, indexError(IndexFailureInput, "invalid_upload_result_file")
	}
	value, err := os.ReadFile(filename)
	if err != nil {
		return result, indexError(IndexFailureInput, "invalid_upload_result_file")
	}
	if err := decodeStrictResponseJSON(value, &result); err != nil {
		return UploadResult{}, indexError(IndexFailureInput, "malformed_upload_result")
	}
	if err := validateSuccessfulUploadResult(result, groupID); err != nil {
		return UploadResult{}, err
	}
	return result, nil
}

func validateSuccessfulUploadResult(result UploadResult, groupID string) error {
	if result.SchemaVersion != UploadResultSchemaVersion || result.ProtocolVersion != ControlProtocolVersion || result.ProtocolSHA256 != ControlProtocolSHA256 {
		return indexError(IndexFailureInput, "upload_result_protocol_mismatch")
	}
	if strings.TrimSpace(groupID) == "" || result.GroupID != strings.TrimSpace(groupID) {
		return indexError(IndexFailureInput, "upload_result_group_mismatch")
	}
	if result.State != "succeeded" || result.ReasonCode != "" {
		return indexError(IndexFailureInput, "upload_result_not_succeeded")
	}
	if !validSnapshotID(result.SnapshotID) || !validUUID(result.StagingJobID) || !validUUID(result.ContinuationJobID) || !validUUID(result.CorpusID) || !validUUID(result.CorpusGenerationID) || !validSafeIdentity(result.CorpusGenerationVersion) {
		return indexError(IndexFailureInput, "upload_result_identity_invalid")
	}
	if !validSHA256(result.ScanFingerprint) || !validSHA256(result.CanonicalManifestHash) || !validSHA256(result.ContentManifestHash) {
		return indexError(IndexFailureInput, "upload_result_hash_invalid")
	}
	if result.PartTotal < 0 || result.PartCompleted != result.PartTotal || result.TransmittedRequestCount < 1 || result.TransmittedRequestBytes < 1 {
		return indexError(IndexFailureInput, "upload_result_counts_inconsistent")
	}
	if !validTimestamp(result.StartedAt) || !validTimestamp(result.UpdatedAt) {
		return indexError(IndexFailureInput, "upload_result_timestamp_invalid")
	}
	return nil
}

func RunIndex(ctx context.Context, groupID, uploadResultPath string, options IndexOptions) (*IndexExecution, error) {
	configureIndexOptions(&options)
	started := options.now().UTC()
	result := newIndexResult(strings.TrimSpace(groupID), started)
	uploadResult, err := LoadSuccessfulUploadResult(uploadResultPath, groupID)
	if err != nil {
		setIndexFailure(result, err, options.now())
		return &IndexExecution{Result: *result}, err
	}
	copyUploadIdentity(result, uploadResult)
	client, err := NewControlClient(options.BaseURL, options.Token, options.AllowLoopbackHTTP)
	if err != nil {
		err = translateUploadClientError(err)
		setIndexFailure(result, err, options.now())
		return &IndexExecution{Result: *result}, err
	}
	runtime := &indexRuntime{client: client, options: options, result: result}
	operationContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	capabilities, err := runtime.capabilities(operationContext, groupID, false)
	if err != nil {
		return runtime.failure(err)
	}
	result.DispatchMode = capabilities.Operations.IndexBuild.Dispatch
	identity, err := runtime.resolveContinuation(operationContext, uploadResult)
	if err != nil {
		return runtime.failure(err)
	}
	setContinuationIdentity(result, identity)
	intent := capabilities.RequiredIndexIdentity
	intentFingerprint, err := fingerprintValue(intent)
	if err != nil {
		return runtime.failure(indexError(IndexFailureInternal, "index_intent_fingerprint_failed"))
	}
	result.IndexIntentFingerprint = stringPointer(intentFingerprint)
	store, err := newIndexStateStore(options.StateDirectory)
	if err != nil {
		return runtime.failure(err)
	}
	runtime.store = store
	stateID, err := indexOperationIdentity(client.serverIdentitySHA256(), uploadResult, identity)
	if err != nil {
		return runtime.failure(indexError(IndexFailureInternal, "index_operation_identity_failed"))
	}
	runtime.stateID = stateID
	state, err := store.load(stateID)
	if err != nil {
		return runtime.failure(err)
	}
	if options.Resume && state == nil {
		return runtime.failure(indexError(IndexFailureInput, "resume_state_not_found"))
	}
	if !options.Resume && state != nil {
		return runtime.failure(indexError(IndexFailureInput, "resume_required"))
	}
	if state == nil {
		if err := requireIndexBuildReady(capabilities.Operations.IndexBuild); err != nil {
			return runtime.failure(err)
		}
		state = newIndexState(uploadResult, identity, intentFingerprint, intent.RetrievalConfigFingerprint, started)
		if err := store.save(stateID, state); err != nil {
			return runtime.failure(err)
		}
	} else {
		if err := validateIndexStateIdentity(state, uploadResult, identity); err != nil {
			return runtime.failure(err)
		}
		if state.IndexJobID == "" && (state.IndexIntentFingerprint != intentFingerprint || state.RetrievalConfigFingerprint != intent.RetrievalConfigFingerprint) {
			return runtime.failure(indexError(IndexFailureConflict, "resume_identity_mismatch"))
		}
	}
	runtime.state = state
	result.Resumed = options.Resume
	if state.SafeState == "terminal_failed" || state.SafeState == "cancelled" || state.SafeState == "conflict" {
		return runtime.failure(indexError(IndexFailureConflict, "resume_state_terminal"))
	}
	if state.IndexJobID != "" {
		result.IndexJobID = stringPointer(state.IndexJobID)
	}
	if state.IndexPublicationID != "" {
		result.CompatiblePublicationID = stringPointer(state.IndexPublicationID)
	}
	setIntentResult(result, intent)

	if state.IndexJobID == "" {
		if err := requireIndexBuildReady(capabilities.Operations.IndexBuild); err != nil {
			return runtime.failure(err)
		}
		accepted, err := runtime.submit(operationContext, uploadResult, identity, intent)
		if err != nil {
			return runtime.failure(err)
		}
		state.IndexJobID = accepted.JobID
		state.SafeState = accepted.State
		state.UpdatedAt = timestamp(options.now().UTC())
		if err := store.save(stateID, state); err != nil {
			return runtime.failure(err)
		}
		result.IndexJobID = stringPointer(accepted.JobID)
		result.Replayed = accepted.Replayed
		result.State = accepted.State
		result.ExitClassification = "pending"
	}
	if !options.Wait {
		if options.Resume || state.SafeState != "queued" {
			status, err := runtime.status(operationContext, groupID, state.IndexJobID, &intent)
			if err != nil {
				return runtime.failure(err)
			}
			if err := runtime.applyStatus(status); err != nil {
				return runtime.failure(err)
			}
		}
		runtime.refresh()
		return &IndexExecution{Result: *result, store: store, stateID: stateID, cleanupState: result.State == "succeeded"}, terminalIndexResult(result)
	}
	if err := runtime.wait(operationContext, groupID, state.IndexJobID); err != nil {
		return runtime.failure(err)
	}
	runtime.refresh()
	return &IndexExecution{Result: *result, store: store, stateID: stateID, cleanupState: true}, nil
}

func RunIndexStatus(ctx context.Context, groupID, jobID string, options IndexOptions) (*IndexExecution, error) {
	configureIndexOptions(&options)
	result := newIndexResult(strings.TrimSpace(groupID), options.now().UTC())
	if strings.TrimSpace(groupID) == "" || !validUUID(strings.TrimSpace(jobID)) {
		err := indexError(IndexFailureInput, "status_identity_invalid")
		setIndexFailure(result, err, options.now())
		return &IndexExecution{Result: *result}, err
	}
	client, err := NewControlClient(options.BaseURL, options.Token, options.AllowLoopbackHTTP)
	if err != nil {
		err = translateUploadClientError(err)
		setIndexFailure(result, err, options.now())
		return &IndexExecution{Result: *result}, err
	}
	runtime := &indexRuntime{client: client, options: options, result: result}
	operationContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	capabilities, err := runtime.capabilities(operationContext, groupID, false)
	if err != nil {
		return runtime.failure(err)
	}
	result.DispatchMode = capabilities.Operations.IndexBuild.Dispatch
	status, err := runtime.status(operationContext, groupID, jobID, nil)
	if err != nil {
		return runtime.failure(err)
	}
	if err := runtime.applyStatus(status); err != nil {
		return runtime.failure(err)
	}
	runtime.refresh()
	return &IndexExecution{Result: *result}, terminalIndexResult(result)
}

func configureIndexOptions(options *IndexOptions) {
	if options.Timeout <= 0 {
		options.Timeout = 20 * time.Minute
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 2 * time.Second
	}
	if options.RetryAttempts <= 0 {
		options.RetryAttempts = 4
	}
	if options.RetryBaseDelay <= 0 {
		options.RetryBaseDelay = 100 * time.Millisecond
	}
	if options.sleep == nil {
		options.sleep = sleepContext
	}
	if options.now == nil {
		options.now = time.Now
	}
}

func newIndexResult(groupID string, now time.Time) *IndexResult {
	return &IndexResult{
		SchemaVersion: IndexResultSchemaVersion, ProtocolVersion: IndexControlProtocolVersion,
		ProtocolSHA256: IndexControlProtocolSHA256, GroupID: groupID, DispatchMode: "unavailable",
		State: "preflight", ExitClassification: "failed", UpdatedAt: timestamp(now),
	}
}

func copyUploadIdentity(result *IndexResult, upload UploadResult) {
	result.ScanFingerprint = stringPointer(upload.ScanFingerprint)
	result.IngestionContinuationID = stringPointer(upload.ContinuationJobID)
	result.CorpusID = stringPointer(upload.CorpusID)
	result.CorpusGenerationID = stringPointer(upload.CorpusGenerationID)
	result.CorpusGenerationVersion = stringPointer(upload.CorpusGenerationVersion)
}

func setContinuationIdentity(result *IndexResult, identity continuationIdentity) {
	result.CorpusManifestHash = stringPointer(identity.ManifestHash)
	result.IngestionProvenanceFingerprint = stringPointer(identity.ProvenanceFingerprint)
}

func setIntentResult(result *IndexResult, intent v2IndexIntent) {
	result.IndexFormatVersion = stringPointer(intent.IndexFormatVersion)
	result.TokenizerVersion = stringPointer(intent.TokenizerVersion)
	result.RetrievalConfigFingerprint = stringPointer(intent.RetrievalConfigFingerprint)
	result.EmbeddingFingerprint = stringPointer(intent.Embedding.Fingerprint)
}

func (runtime *indexRuntime) capabilities(ctx context.Context, groupID string, requireReady bool) (*v2CapabilitiesResponse, error) {
	requestID, err := newUUID()
	if err != nil {
		return nil, indexError(IndexFailureInternal, "request_identity_failed")
	}
	payload := struct {
		ProtocolVersion string `json:"protocol_version"`
		ProtocolSHA256  string `json:"protocol_sha256"`
		MessageType     string `json:"message_type"`
		RequestID       string `json:"request_id"`
		GroupID         string `json:"group_id"`
	}{IndexControlProtocolVersion, IndexControlProtocolSHA256, "capabilities_request", requestID, groupID}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > maximumV2ControlRequest {
		return nil, indexError(IndexFailureInternal, "capability_request_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v2/capabilities", body, IndexControlProtocolVersion, IndexControlProtocolSHA256)
	clearBytes(body)
	if err != nil {
		return nil, err
	}
	var capabilities v2CapabilitiesResponse
	if err := decodeStrictResponseJSON(response.Body, &capabilities); err != nil {
		return nil, indexError(IndexFailureContract, "capability_contract_mismatch")
	}
	if err := validateV2Capabilities(capabilities, requestID, groupID, requireReady); err != nil {
		return nil, err
	}
	return &capabilities, nil
}

func validateV2Capabilities(capability v2CapabilitiesResponse, requestID, groupID string, requireReady bool) error {
	if capability.ProtocolVersion != IndexControlProtocolVersion || capability.ProtocolSHA256 != IndexControlProtocolSHA256 || capability.MessageType != "capabilities" || capability.RequestID != requestID || capability.GroupID != groupID {
		return indexError(IndexFailureContract, "capability_protocol_mismatch")
	}
	if len(capability.Supported) != 2 || capability.Supported[0].Version != ControlProtocolVersion || capability.Supported[0].SHA256 != ControlProtocolSHA256 || capability.Supported[0].Role != "staging_only" || capability.Supported[1].Version != IndexControlProtocolVersion || capability.Supported[1].SHA256 != IndexControlProtocolSHA256 || capability.Supported[1].Role != "index_and_run_submission" {
		return indexError(IndexFailureContract, "capability_protocol_mismatch")
	}
	if err := validateOperationCapability(capability.Operations.IndexBuild); err != nil {
		return err
	}
	if err := validateOperationCapability(capability.Operations.BaselineRun); err != nil {
		return err
	}
	indexCapability := capability.Operations.IndexBuild
	if requireReady && (indexCapability.Submission != "safe" || indexCapability.Endpoint != "authenticated_post" || (indexCapability.Dispatch != "automatic" && indexCapability.Dispatch != "manual") || indexCapability.Readiness != "ready" || indexCapability.ReasonCode != nil) {
		return indexError(IndexFailureCapability, capabilityReason(indexCapability))
	}
	if capability.Limits.ControlRequestBytes < maximumV2ControlRequest || capability.Limits.IdempotencyKeyMin > 64 || capability.Limits.IdempotencyKeyMax < 64 || capability.Limits.SelectedEvidenceItems != 4 || capability.Limits.SelectedEvidenceCharacters != 16000 || capability.Limits.FeedbackItems != 4 {
		return indexError(IndexFailureContract, "capability_limits_incompatible")
	}
	if capability.Transport.Remote != "verified_https_required" || capability.Transport.LoopbackHTTP != "explicit_actual_peer_exception" || capability.Transport.JSONMedia != "application/json" || capability.Transport.Encoding != "utf-8" {
		return indexError(IndexFailureContract, "capability_transport_mismatch")
	}
	if err := validateIndexIntent(capability.RequiredIndexIdentity); err != nil {
		return err
	}
	return nil
}

func validateOperationCapability(capability v2OperationCapability) error {
	validReason := capability.ReasonCode == nil || validSafeReasonCode(*capability.ReasonCode)
	if !validReason {
		return indexError(IndexFailureContract, "capability_contract_mismatch")
	}
	safe := capability.Submission == "safe" && capability.Endpoint == "authenticated_post" && (capability.Dispatch == "automatic" || capability.Dispatch == "manual") && (capability.Readiness == "ready" || capability.Readiness == "not_ready")
	unavailable := capability.Submission == "unavailable" && capability.Endpoint == "unavailable" && capability.Dispatch == "unavailable" && capability.Readiness == "unavailable" && capability.ReasonCode != nil && *capability.ReasonCode == "capability_unavailable"
	if !safe && !unavailable {
		return indexError(IndexFailureContract, "capability_contract_mismatch")
	}
	if safe && capability.Readiness == "ready" && capability.ReasonCode != nil {
		return indexError(IndexFailureContract, "capability_contract_mismatch")
	}
	if safe && capability.Readiness == "not_ready" && capability.ReasonCode == nil {
		return indexError(IndexFailureContract, "capability_contract_mismatch")
	}
	return nil
}

func capabilityReason(capability v2OperationCapability) string {
	if capability.ReasonCode != nil && validSafeReasonCode(*capability.ReasonCode) {
		return *capability.ReasonCode
	}
	return "index_build_not_ready"
}

func requireIndexBuildReady(capability v2OperationCapability) error {
	if capability.Submission != "safe" || capability.Endpoint != "authenticated_post" || (capability.Dispatch != "automatic" && capability.Dispatch != "manual") || capability.Readiness != "ready" || capability.ReasonCode != nil {
		return indexError(IndexFailureCapability, capabilityReason(capability))
	}
	return nil
}

func validateIndexIntent(intent v2IndexIntent) error {
	identity := intent.Embedding
	if intent.IndexFormatVersion != "baseline-index.v1" || intent.TokenizerVersion != "baseline_v1_frozen_tokenizer.v1" || !validSHA256(intent.RetrievalConfigFingerprint) || identity.ContractVersion != "baseline-embedding-http.v1" || identity.Provider != "baseline_http_v1" || identity.Model != "BAAI/bge-small-en-v1.5" || strings.TrimSpace(identity.Revision) == "" || identity.Revision != strings.TrimSpace(identity.Revision) || len(identity.Revision) > 128 || identity.Dimension != 384 || identity.DType != "float32" || !validSHA256(identity.Fingerprint) {
		return indexError(IndexFailureContract, "index_identity_incompatible")
	}
	return nil
}

func (runtime *indexRuntime) resolveContinuation(ctx context.Context, upload UploadResult) (continuationIdentity, error) {
	requestID, err := newUUID()
	if err != nil {
		return continuationIdentity{}, indexError(IndexFailureInternal, "request_identity_failed")
	}
	payload := struct {
		SchemaVersion     string  `json:"schema_version"`
		MessageType       string  `json:"message_type"`
		RequestID         string  `json:"request_id"`
		GroupID           string  `json:"group_id"`
		StagingJobID      *string `json:"staging_job_id"`
		ContinuationJobID *string `json:"continuation_job_id"`
	}{"baseline-snapshot-continuation.v1", "continuation_job_status_request", requestID, upload.GroupID, nil, stringPointer(upload.ContinuationJobID)}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > maximumV2ControlRequest {
		return continuationIdentity{}, indexError(IndexFailureInternal, "continuation_status_request_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v1/continuations/status", body, ControlProtocolVersion, ControlProtocolSHA256)
	clearBytes(body)
	if err != nil {
		return continuationIdentity{}, err
	}
	var status continuationStatusResponse
	if err := decodeStrictResponseJSON(response.Body, &status); err != nil {
		return continuationIdentity{}, indexError(IndexFailureContract, "continuation_response_mismatch")
	}
	if status.SchemaVersion != "baseline-snapshot-continuation.v1" || status.MessageType != "continuation_job_status" || status.RequestID != requestID || status.GroupID != upload.GroupID || status.StagingJobID != upload.StagingJobID || status.JobID != upload.ContinuationJobID || status.Operation != "sealed_snapshot_continue" || status.State != "succeeded" || status.Result.SnapshotID != upload.SnapshotID || !status.Result.CorpusIngestionComplete || !status.Result.CorpusEligible || status.Result.IndexEligible || status.Result.BaselineEligible || status.Result.IndexState != "incomplete" || status.Staging.State != "sealed" || status.Staging.ReceivedParts != upload.PartTotal || status.Staging.ExpectedParts != upload.PartTotal {
		return continuationIdentity{}, indexError(IndexFailureConflict, "continuation_identity_mismatch")
	}
	if status.Result.CorpusID != upload.CorpusID || status.Result.CorpusGenerationID != upload.CorpusGenerationID || status.Result.CorpusGenerationVersion != upload.CorpusGenerationVersion || !validSHA256(status.Result.CorpusManifestHash) || !validSHA256(status.Result.CorpusProvenanceFingerprint) || status.Result.WorkerContractVersion != "baseline-continuation-worker.v1" {
		return continuationIdentity{}, indexError(IndexFailureConflict, "continuation_identity_mismatch")
	}
	return continuationIdentity{
		CorpusID: status.Result.CorpusID, GenerationID: status.Result.CorpusGenerationID,
		GenerationVersion: status.Result.CorpusGenerationVersion, ManifestHash: status.Result.CorpusManifestHash,
		ProvenanceFingerprint: status.Result.CorpusProvenanceFingerprint, WorkerContractVersion: status.Result.WorkerContractVersion,
	}, nil
}

func (runtime *indexRuntime) submit(ctx context.Context, upload UploadResult, identity continuationIdentity, intent v2IndexIntent) (*v2IndexAccepted, error) {
	requestID := runtime.store.deriveUUID("baseline-index-submit-request.v1", runtime.stateID)
	payload := struct {
		ProtocolVersion                string        `json:"protocol_version"`
		ProtocolSHA256                 string        `json:"protocol_sha256"`
		MessageType                    string        `json:"message_type"`
		RequestID                      string        `json:"request_id"`
		GroupID                        string        `json:"group_id"`
		IdempotencyKey                 string        `json:"idempotency_key"`
		IngestionContinuationID        string        `json:"ingestion_continuation_id"`
		CorpusGenerationID             string        `json:"corpus_generation_id"`
		CorpusManifestHash             string        `json:"corpus_manifest_hash"`
		IngestionProvenanceFingerprint string        `json:"ingestion_provenance_fingerprint"`
		IndexIntent                    v2IndexIntent `json:"index_intent"`
	}{
		ProtocolVersion: IndexControlProtocolVersion, ProtocolSHA256: IndexControlProtocolSHA256,
		MessageType: "index_build_submit", RequestID: requestID, GroupID: upload.GroupID,
		IdempotencyKey:          runtime.store.deriveHex("baseline-index-submit-idempotency.v1", runtime.stateID),
		IngestionContinuationID: upload.ContinuationJobID, CorpusGenerationID: identity.GenerationID,
		CorpusManifestHash: identity.ManifestHash, IngestionProvenanceFingerprint: identity.ProvenanceFingerprint,
		IndexIntent: intent,
	}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > maximumV2ControlRequest {
		return nil, indexError(IndexFailureInternal, "index_submission_encoding_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v2/index-builds", body, IndexControlProtocolVersion, IndexControlProtocolSHA256)
	clearBytes(body)
	if err != nil {
		return nil, err
	}
	var accepted v2IndexAccepted
	if err := decodeStrictResponseJSON(response.Body, &accepted); err != nil || accepted.ProtocolVersion != IndexControlProtocolVersion || accepted.ProtocolSHA256 != IndexControlProtocolSHA256 || accepted.MessageType != "job_accepted" || accepted.RequestID != requestID || accepted.GroupID != upload.GroupID || !validUUID(accepted.JobID) || accepted.Operation != "index_build" || accepted.State != "queued" || accepted.ProcessingRunID != nil {
		return nil, indexError(IndexFailureContract, "index_acceptance_mismatch")
	}
	return &accepted, nil
}

func (runtime *indexRuntime) status(ctx context.Context, groupID, jobID string, expectedIntent *v2IndexIntent) (*v2IndexStatus, error) {
	requestID, err := newUUID()
	if err != nil {
		return nil, indexError(IndexFailureInternal, "request_identity_failed")
	}
	payload := struct {
		ProtocolVersion string `json:"protocol_version"`
		ProtocolSHA256  string `json:"protocol_sha256"`
		MessageType     string `json:"message_type"`
		RequestID       string `json:"request_id"`
		GroupID         string `json:"group_id"`
		JobID           string `json:"job_id"`
		Operation       string `json:"operation"`
	}{IndexControlProtocolVersion, IndexControlProtocolSHA256, "job_status_request", requestID, groupID, jobID, "index_build"}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > maximumV2ControlRequest {
		return nil, indexError(IndexFailureInternal, "index_status_encoding_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v2/index-builds/status", body, IndexControlProtocolVersion, IndexControlProtocolSHA256)
	clearBytes(body)
	if err != nil {
		return nil, err
	}
	var status v2IndexStatus
	if err := decodeStrictResponseJSON(response.Body, &status); err != nil {
		return nil, indexError(IndexFailureContract, "index_status_contract_mismatch")
	}
	if err := validateV2IndexStatus(status, requestID, groupID, jobID, expectedIntent); err != nil {
		return nil, err
	}
	if runtime.state != nil && (status.ContinuationID != runtime.state.IngestionContinuationID || status.CorpusGenerationID != runtime.state.CorpusGenerationID || status.CorpusManifestHash != runtime.state.CorpusManifestHash) {
		return nil, indexError(IndexFailureConflict, "index_job_identity_mismatch")
	}
	if runtime.state != nil {
		fingerprint, err := fingerprintValue(status.IndexIntent)
		if err != nil || fingerprint != runtime.state.IndexIntentFingerprint || status.IndexIntent.RetrievalConfigFingerprint != runtime.state.RetrievalConfigFingerprint {
			return nil, indexError(IndexFailureConflict, "index_identity_mismatch")
		}
	}
	return &status, nil
}

func validateV2IndexStatus(status v2IndexStatus, requestID, groupID, jobID string, expectedIntent *v2IndexIntent) error {
	if status.ProtocolVersion != IndexControlProtocolVersion || status.ProtocolSHA256 != IndexControlProtocolSHA256 || status.MessageType != "job_status" || status.RequestID != requestID || status.GroupID != groupID || status.JobID != jobID || status.Operation != "index_build" || !validUUID(status.ContinuationID) || !validUUID(status.CorpusGenerationID) || !validSHA256(status.CorpusManifestHash) || status.Attempt < 0 || !validTimestamp(status.CreatedAt) || !validTimestamp(status.UpdatedAt) || status.Progress.DocumentCount < 0 || status.Progress.DocumentCount > MaxFileRecords || status.Progress.VectorCount < 0 || status.Progress.VectorCount > MaxFileRecords {
		return indexError(IndexFailureContract, "index_status_contract_mismatch")
	}
	if err := validateIndexIntent(status.IndexIntent); err != nil {
		return err
	}
	if expectedIntent != nil && !reflect.DeepEqual(status.IndexIntent, *expectedIntent) {
		return indexError(IndexFailureConflict, "index_identity_mismatch")
	}
	if status.ReasonCode != nil && !validSafeReasonCode(*status.ReasonCode) {
		return indexError(IndexFailureContract, "index_status_contract_mismatch")
	}
	switch status.State {
	case "queued", "running":
		if status.Terminal || status.ExitClassification != "pending" || status.Result != nil || status.ReasonCode != nil {
			return indexError(IndexFailureContract, "index_status_contract_mismatch")
		}
	case "retryable_failed":
		if status.Terminal || status.ExitClassification != "pending" || status.Result != nil || status.ReasonCode == nil {
			return indexError(IndexFailureContract, "index_status_contract_mismatch")
		}
	case "terminal_failed":
		if !status.Terminal || status.ExitClassification != "failed" || status.Result != nil || status.ReasonCode == nil {
			return indexError(IndexFailureContract, "index_status_contract_mismatch")
		}
	case "cancelled":
		if !status.Terminal || status.ExitClassification != "cancelled" || status.Result != nil || status.ReasonCode == nil || *status.ReasonCode != "job_cancelled" {
			return indexError(IndexFailureContract, "index_status_contract_mismatch")
		}
	case "succeeded":
		if !status.Terminal || status.ExitClassification != "success" || status.Result == nil || status.ReasonCode != nil {
			return indexError(IndexFailureContract, "index_status_contract_mismatch")
		}
		result := status.Result
		if !validUUID(result.IndexPublicationID) || result.CorpusGenerationID != status.CorpusGenerationID || result.CorpusManifestHash != status.CorpusManifestHash || !validSHA256(result.IndexFingerprint) || result.RetrievalConfigFingerprint != status.IndexIntent.RetrievalConfigFingerprint || result.EmbeddingFingerprint != status.IndexIntent.Embedding.Fingerprint || result.DocumentCount < 0 || result.DocumentCount > MaxFileRecords || result.VectorCount != result.DocumentCount || status.Progress.DocumentCount != result.DocumentCount || status.Progress.VectorCount != result.VectorCount {
			return indexError(IndexFailureContract, "index_success_inconsistent")
		}
	default:
		// The frozen index status schema has no blocked state. Accepting one here
		// would silently expand the wire contract; a server must use its frozen
		// safe error or terminal_failed projection instead.
		return indexError(IndexFailureContract, "index_status_contract_mismatch")
	}
	return nil
}

func (runtime *indexRuntime) wait(ctx context.Context, groupID, jobID string) error {
	delay := runtime.options.PollInterval
	lastState := ""
	for {
		status, err := runtime.status(ctx, groupID, jobID, nil)
		if err != nil {
			return err
		}
		if err := runtime.applyStatus(status); err != nil {
			return err
		}
		if runtime.options.Progress != nil {
			runtime.options.Progress(status.State, status.Attempt)
		}
		if status.State == "succeeded" {
			return nil
		}
		if status.State == "terminal_failed" || status.State == "cancelled" {
			return terminalIndexResult(runtime.result)
		}
		if lastState != status.State {
			delay = runtime.options.PollInterval
		}
		lastState = status.State
		if err := runtime.options.sleep(ctx, jitter(delay)); err != nil {
			return indexError(IndexFailureRetryable, "wait_timeout")
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

func (runtime *indexRuntime) applyStatus(status *v2IndexStatus) error {
	result := runtime.result
	result.IndexJobID = stringPointer(status.JobID)
	result.IngestionContinuationID = stringPointer(status.ContinuationID)
	result.CorpusGenerationID = stringPointer(status.CorpusGenerationID)
	result.CorpusManifestHash = stringPointer(status.CorpusManifestHash)
	result.State = status.State
	result.ExitClassification = status.ExitClassification
	result.CreatedAt = stringPointer(status.CreatedAt)
	result.UpdatedAt = status.UpdatedAt
	result.ReasonCode = status.ReasonCode
	result.Replayed = result.Replayed || status.Replayed
	setIntentResult(result, status.IndexIntent)
	if fingerprint, err := fingerprintValue(status.IndexIntent); err == nil {
		result.IndexIntentFingerprint = stringPointer(fingerprint)
	} else {
		return indexError(IndexFailureInternal, "index_intent_fingerprint_failed")
	}
	documents, vectors := status.Progress.DocumentCount, status.Progress.VectorCount
	result.IndexedDocumentCount, result.VectorCount = &documents, &vectors
	if status.Result != nil {
		result.CompatiblePublicationID = stringPointer(status.Result.IndexPublicationID)
		result.IndexFingerprint = stringPointer(status.Result.IndexFingerprint)
		documents, vectors = status.Result.DocumentCount, status.Result.VectorCount
		result.IndexedDocumentCount, result.VectorCount = &documents, &vectors
	}
	if runtime.state != nil {
		runtime.state.SafeState = status.State
		runtime.state.UpdatedAt = status.UpdatedAt
		if status.Result != nil {
			runtime.state.IndexPublicationID = status.Result.IndexPublicationID
		}
		if err := runtime.store.save(runtime.stateID, runtime.state); err != nil {
			return err
		}
	}
	return nil
}

func (runtime *indexRuntime) postWithRetry(ctx context.Context, endpoint string, body []byte, protocolVersion, protocolSHA string) (controlResponse, error) {
	var last error
	for attempt := 0; attempt < runtime.options.RetryAttempts; attempt++ {
		response, err := runtime.client.postProtocol(ctx, endpoint, body, protocolVersion, protocolSHA)
		runtime.counter.bytes += response.BodyBytes
		runtime.counter.count++
		if err == nil {
			return response, nil
		}
		last = classifyIndexControlError(err)
		if IndexFailure(last) != IndexFailureRetryable || attempt+1 >= runtime.options.RetryAttempts {
			break
		}
		delay := runtime.options.RetryBaseDelay << attempt
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		if err := runtime.options.sleep(ctx, jitter(delay)); err != nil {
			last = indexError(IndexFailureRetryable, "wait_timeout")
			break
		}
	}
	if last == nil {
		last = indexError(IndexFailureRetryable, "retry_exhausted")
	}
	return controlResponse{}, last
}

func classifyIndexControlError(err error) error {
	if err == nil {
		return nil
	}
	var index *IndexError
	if errors.As(err, &index) {
		return index
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return indexError(IndexFailureRetryable, "wait_timeout")
	}
	var control *ControlHTTPError
	if errors.As(err, &control) {
		if control.StatusCode == http.StatusUnauthorized || control.StatusCode == http.StatusForbidden || control.StatusCode == http.StatusNotFound || control.Code == "not_found_or_forbidden" || control.Code == "repository_not_authorized" || control.Code == "source_not_authorized" {
			return indexError(IndexFailureAuth, control.Code)
		}
		switch control.Code {
		case "capability_unavailable", "embedding_unavailable", "worker_unavailable":
			return indexError(IndexFailureCapability, control.Code)
		case "idempotency_conflict", "stale_generation", "corpus_not_active", "repository_approval_revoked", "embedding_identity_mismatch", "index_identity_mismatch", "index_state_incompatible":
			return indexError(IndexFailureConflict, control.Code)
		}
		if control.Retryable || control.StatusCode == http.StatusRequestTimeout || control.StatusCode == http.StatusTooManyRequests || control.StatusCode >= 500 {
			return indexError(IndexFailureRetryable, control.Code)
		}
		return indexError(IndexFailureContract, control.Code)
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, io.ErrUnexpectedEOF) {
		return indexError(IndexFailureRetryable, "transport_retry_exhausted")
	}
	return indexError(IndexFailureRetryable, "transport_retry_exhausted")
}

func translateUploadClientError(err error) error {
	var upload *UploadError
	if errors.As(err, &upload) {
		if upload.Kind == UploadFailureAuthentication {
			return indexError(IndexFailureAuth, upload.Reason)
		}
		return indexError(IndexFailureInput, upload.Reason)
	}
	return indexError(IndexFailureInternal, "control_client_unavailable")
}

func terminalIndexResult(result *IndexResult) error {
	if result == nil {
		return indexError(IndexFailureInternal, "internal_failure")
	}
	switch result.State {
	case "terminal_failed", "blocked", "cancelled":
		reason := "index_" + result.State
		if result.ReasonCode != nil {
			reason = *result.ReasonCode
		}
		return indexError(IndexFailureTerminal, reason)
	default:
		return nil
	}
}

func (runtime *indexRuntime) failure(err error) (*IndexExecution, error) {
	setIndexFailure(runtime.result, err, runtime.options.now())
	if runtime.state != nil && runtime.store != nil && runtime.stateID != "" {
		switch IndexFailure(err) {
		case IndexFailureRetryable:
			runtime.state.SafeState = "retryable_incomplete"
		case IndexFailureConflict:
			runtime.state.SafeState = "conflict"
		case IndexFailureTerminal:
			if runtime.result.State == "cancelled" {
				runtime.state.SafeState = "cancelled"
			} else {
				runtime.state.SafeState = "terminal_failed"
			}
		}
		runtime.state.UpdatedAt = runtime.result.UpdatedAt
		if saveErr := runtime.store.save(runtime.stateID, runtime.state); saveErr != nil {
			err = saveErr
			setIndexFailure(runtime.result, err, runtime.options.now())
		}
	}
	runtime.refresh()
	return &IndexExecution{Result: *runtime.result, store: runtime.store, stateID: runtime.stateID}, err
}

func setIndexFailure(result *IndexResult, err error, now time.Time) {
	reason := SafeIndexReason(err)
	result.ReasonCode = &reason
	result.UpdatedAt = timestamp(now.UTC())
	switch IndexFailure(err) {
	case IndexFailureRetryable:
		result.State = "retryable_incomplete"
		result.ExitClassification = "retryable"
	case IndexFailureTerminal:
		if result.State != "terminal_failed" && result.State != "cancelled" && result.State != "blocked" {
			result.State = "terminal_failed"
		}
		if result.State == "cancelled" {
			result.ExitClassification = "cancelled"
		} else {
			result.ExitClassification = "failed"
		}
	default:
		result.State = "failed"
		result.ExitClassification = "failed"
	}
}

func (runtime *indexRuntime) refresh() {
	if runtime == nil || runtime.result == nil {
		return
	}
	runtime.result.TransmittedRequestBytes = runtime.counter.bytes
	runtime.result.TransmittedRequestCount = runtime.counter.count
	if runtime.result.UpdatedAt == "" {
		runtime.result.UpdatedAt = timestamp(runtime.options.now().UTC())
	}
}

func newIndexState(upload UploadResult, identity continuationIdentity, intentFingerprint, configFingerprint string, now time.Time) *indexState {
	return &indexState{
		SchemaVersion: indexStateSchemaVersion, GroupID: upload.GroupID, ProtocolVersion: IndexControlProtocolVersion,
		ProtocolSHA256: IndexControlProtocolSHA256, ScanFingerprint: upload.ScanFingerprint, CorpusID: upload.CorpusID,
		CorpusGenerationID: upload.CorpusGenerationID, CorpusGenerationVersion: upload.CorpusGenerationVersion,
		IngestionContinuationID: upload.ContinuationJobID, CanonicalManifestHash: upload.CanonicalManifestHash,
		ContentManifestHash: upload.ContentManifestHash, CorpusManifestHash: identity.ManifestHash,
		IngestionProvenanceFingerprint: identity.ProvenanceFingerprint, IndexIntentFingerprint: intentFingerprint,
		RetrievalConfigFingerprint: configFingerprint, SafeState: "prepared", CreatedAt: timestamp(now), UpdatedAt: timestamp(now),
	}
}

func validateIndexStateIdentity(state *indexState, upload UploadResult, identity continuationIdentity) error {
	if state.SchemaVersion != indexStateSchemaVersion || state.GroupID != upload.GroupID || state.ProtocolVersion != IndexControlProtocolVersion || state.ProtocolSHA256 != IndexControlProtocolSHA256 || state.ScanFingerprint != upload.ScanFingerprint || state.CorpusID != upload.CorpusID || state.CorpusGenerationID != upload.CorpusGenerationID || state.CorpusGenerationVersion != upload.CorpusGenerationVersion || state.IngestionContinuationID != upload.ContinuationJobID || state.CanonicalManifestHash != upload.CanonicalManifestHash || state.ContentManifestHash != upload.ContentManifestHash || state.CorpusManifestHash != identity.ManifestHash || state.IngestionProvenanceFingerprint != identity.ProvenanceFingerprint || !validSHA256(state.IndexIntentFingerprint) || !validSHA256(state.RetrievalConfigFingerprint) || !validTimestamp(state.CreatedAt) || !validTimestamp(state.UpdatedAt) || (state.IndexJobID != "" && !validUUID(state.IndexJobID)) || (state.IndexPublicationID != "" && !validUUID(state.IndexPublicationID)) || !validSafeReasonCode(state.SafeState) {
		return indexError(IndexFailureConflict, "resume_identity_mismatch")
	}
	return nil
}

func indexOperationIdentity(serverIdentity string, upload UploadResult, identity continuationIdentity) (string, error) {
	value := struct {
		SchemaVersion         string `json:"schema_version"`
		ServerIdentitySHA256  string `json:"server_identity_sha256"`
		GroupID               string `json:"group_id"`
		ScanFingerprint       string `json:"scan_fingerprint"`
		ContinuationID        string `json:"ingestion_continuation_id"`
		CorpusID              string `json:"corpus_id"`
		GenerationID          string `json:"corpus_generation_id"`
		GenerationVersion     string `json:"corpus_generation_version"`
		CorpusManifestHash    string `json:"corpus_manifest_hash"`
		ProvenanceFingerprint string `json:"ingestion_provenance_fingerprint"`
	}{indexStateSchemaVersion, serverIdentity, upload.GroupID, upload.ScanFingerprint, upload.ContinuationJobID, upload.CorpusID, upload.CorpusGenerationID, upload.CorpusGenerationVersion, identity.ManifestHash, identity.ProvenanceFingerprint}
	return fingerprintValue(value)
}

func fingerprintValue(value any) (string, error) {
	canonical, err := canonicalJSONBytes(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("%x", digest[:]), nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !parsed.IsZero()
}

func stringPointer(value string) *string { return &value }

func EncodeIndexResult(writer io.Writer, result IndexResult) error {
	if err := validateIndexResult(result); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}

func validateIndexResult(result IndexResult) error {
	if result.SchemaVersion != IndexResultSchemaVersion || result.ProtocolVersion != IndexControlProtocolVersion || result.ProtocolSHA256 != IndexControlProtocolSHA256 || strings.TrimSpace(result.GroupID) == "" || !validTimestamp(result.UpdatedAt) {
		return indexError(IndexFailureInternal, "result_contract_mismatch")
	}
	if result.ReasonCode != nil && !validSafeReasonCode(*result.ReasonCode) {
		return indexError(IndexFailureInternal, "result_contract_mismatch")
	}
	switch result.State {
	case "succeeded":
		if result.ExitClassification != "success" || result.CompatiblePublicationID == nil || !validUUID(*result.CompatiblePublicationID) || result.IndexFingerprint == nil || !validSHA256(*result.IndexFingerprint) || result.IndexIntentFingerprint == nil || !validSHA256(*result.IndexIntentFingerprint) || result.IndexedDocumentCount == nil || result.VectorCount == nil || *result.IndexedDocumentCount != *result.VectorCount || result.ReasonCode != nil {
			return indexError(IndexFailureInternal, "result_contract_mismatch")
		}
	case "queued", "running":
		if result.ExitClassification != "pending" || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil || result.ReasonCode != nil {
			return indexError(IndexFailureInternal, "result_contract_mismatch")
		}
	case "retryable_failed":
		if result.ExitClassification != "pending" || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil || result.ReasonCode == nil {
			return indexError(IndexFailureInternal, "result_contract_mismatch")
		}
	case "retryable_incomplete":
		if result.ExitClassification != "retryable" || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil || result.ReasonCode == nil {
			return indexError(IndexFailureInternal, "result_contract_mismatch")
		}
	case "terminal_failed", "blocked", "failed":
		if result.ExitClassification != "failed" || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil || result.ReasonCode == nil {
			return indexError(IndexFailureInternal, "result_contract_mismatch")
		}
	case "cancelled":
		if result.ExitClassification != "cancelled" || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil || result.ReasonCode == nil {
			return indexError(IndexFailureInternal, "result_contract_mismatch")
		}
	default:
		return indexError(IndexFailureInternal, "result_contract_mismatch")
	}
	return nil
}

func IndexResultForbiddenFields(value []byte) bool {
	text := strings.ToLower(string(value))
	for _, forbidden := range []string{"idempotency_key", "lease_token", "endpoint_url", "raw_diff", "file_content", "embedding_vector", "credentials"} {
		if strings.Contains(text, forbidden) {
			return true
		}
	}
	return false
}
