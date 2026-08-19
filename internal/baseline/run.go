package baseline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	RunResultSchemaVersion  = "baseline-run-result.v1"
	maximumIndexResultBytes = 256_000
	maximumRunRequestBytes  = 8_100_000
)

type RunFailureKind string

const (
	RunFailureInput      RunFailureKind = "usage_input_contract"
	RunFailureAuth       RunFailureKind = "authentication_authorization"
	RunFailureCapability RunFailureKind = "capability_not_ready"
	RunFailureConflict   RunFailureKind = "conflict_stale_identity"
	RunFailureRetryable  RunFailureKind = "timeout_retryable_incomplete"
	RunFailureTerminal   RunFailureKind = "terminal_blocked_cancelled"
	RunFailureContract   RunFailureKind = "transport_server_contract"
	RunFailureInternal   RunFailureKind = "internal_failure"
)

type RunError struct {
	Kind   RunFailureKind
	Reason string
}

func (err *RunError) Error() string {
	if err == nil || err.Reason == "" {
		return "baseline run operation failed"
	}
	return "baseline run operation failed: " + err.Reason
}

func runError(kind RunFailureKind, reason string) error {
	return &RunError{Kind: kind, Reason: reason}
}

func RunFailure(err error) RunFailureKind {
	var target *RunError
	if errors.As(err, &target) {
		return target.Kind
	}
	return RunFailureInternal
}

func SafeRunReason(err error) string {
	var target *RunError
	if errors.As(err, &target) && validSafeReasonCode(target.Reason) {
		return target.Reason
	}
	return "internal_failure"
}

type RunOptions struct {
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

type RunResult struct {
	SchemaVersion                   string  `json:"schema_version"`
	ProtocolVersion                 string  `json:"protocol_version"`
	ProtocolSHA256                  string  `json:"protocol_sha256"`
	GroupID                         string  `json:"group_id"`
	RunJobID                        *string `json:"run_job_id"`
	ProcessingRunID                 *string `json:"processing_run_id"`
	SourceDocumentID                *string `json:"source_document_id"`
	ChangedRepositoryRegistrationID *string `json:"changed_repository_registration_id"`
	DispatchMode                    string  `json:"dispatch_mode"`
	State                           string  `json:"state"`
	ExitClassification              string  `json:"exit_classification"`
	QuerySHA256                     *string `json:"query_sha256"`
	QueryLength                     *int    `json:"query_length"`
	QueryByteSize                   *int    `json:"query_byte_size"`
	QueryOrigin                     *string `json:"query_origin"`
	ScanFingerprint                 *string `json:"scan_fingerprint"`
	CorpusGenerationID              *string `json:"corpus_generation_id"`
	CorpusManifestHash              *string `json:"corpus_manifest_hash"`
	IndexPublicationID              *string `json:"index_publication_id"`
	IndexFormatVersion              *string `json:"index_format_version"`
	TokenizerVersion                *string `json:"tokenizer_version"`
	RetrievalConfigFingerprint      *string `json:"retrieval_config_fingerprint"`
	EmbeddingFingerprint            *string `json:"embedding_fingerprint"`
	IndexFingerprint                *string `json:"index_fingerprint"`
	PersistedRetrievalRunID         *string `json:"persisted_retrieval_run_id"`
	EvidenceCount                   int     `json:"evidence_count"`
	ReferenceCount                  int     `json:"reference_count"`
	FeedbackCount                   int     `json:"feedback_count"`
	NotificationOutboxCount         int     `json:"notification_outbox_count"`
	GenerationInvoked               bool    `json:"generation_invoked"`
	GenerationProvider              *string `json:"generation_provider"`
	GenerationModel                 *string `json:"generation_model"`
	GenerationVersion               *string `json:"generation_version"`
	GenerationInputFingerprint      *string `json:"generation_input_fingerprint"`
	GenerationOutputFingerprint     *string `json:"generation_output_fingerprint"`
	Resumed                         bool    `json:"resumed"`
	Replayed                        bool    `json:"replayed"`
	TransmittedRequestCount         int     `json:"transmitted_request_count"`
	TransmittedRequestBytes         int64   `json:"transmitted_request_bytes"`
	CreatedAt                       *string `json:"created_at"`
	UpdatedAt                       string  `json:"updated_at"`
	ReasonCode                      *string `json:"reason_code"`
	FailureStage                    *string `json:"failure_stage"`
}

type RunExecution struct {
	Result       RunResult
	store        *runStateStore
	stateID      string
	cleanupState bool
}

func (execution *RunExecution) Finalize() error {
	if execution == nil || !execution.cleanupState || execution.store == nil || execution.stateID == "" {
		return nil
	}
	return execution.store.delete(execution.stateID)
}

type v2RunPublication struct {
	IndexPublicationID         string `json:"index_publication_id"`
	CorpusGenerationID         string `json:"corpus_generation_id"`
	CorpusManifestHash         string `json:"corpus_manifest_hash"`
	IndexFormatVersion         string `json:"index_format_version"`
	TokenizerVersion           string `json:"tokenizer_version"`
	RetrievalConfigFingerprint string `json:"retrieval_config_fingerprint"`
	EmbeddingFingerprint       string `json:"embedding_fingerprint"`
	IndexFingerprint           string `json:"index_fingerprint"`
}

type v2RunQuery struct {
	Representation string `json:"representation"`
	Origin         string `json:"origin"`
	Encoding       string `json:"encoding"`
	BaseRevision   string `json:"base_revision"`
	HeadRevision   string `json:"head_revision"`
	ByteSize       int    `json:"byte_size"`
	SHA256         string `json:"sha256"`
	Text           string `json:"text"`
}

type v2RunAccepted struct {
	ProtocolVersion string `json:"protocol_version"`
	ProtocolSHA256  string `json:"protocol_sha256"`
	MessageType     string `json:"message_type"`
	RequestID       string `json:"request_id"`
	GroupID         string `json:"group_id"`
	JobID           string `json:"job_id"`
	Operation       string `json:"operation"`
	State           string `json:"state"`
	Replayed        bool   `json:"replayed"`
	ProcessingRunID string `json:"processing_run_id"`
}

type v2RunEffects struct {
	EvidenceCount           int     `json:"evidence_count"`
	ReferenceCount          int     `json:"reference_count"`
	FeedbackCount           int     `json:"feedback_count"`
	GenerationInvoked       bool    `json:"generation_invoked"`
	NotificationOutboxCount int     `json:"notification_outbox_count"`
	PersistedRunID          *string `json:"persisted_run_id"`
}

type v2RunStatus struct {
	ProtocolVersion                 string           `json:"protocol_version"`
	ProtocolSHA256                  string           `json:"protocol_sha256"`
	MessageType                     string           `json:"message_type"`
	RequestID                       string           `json:"request_id"`
	GroupID                         string           `json:"group_id"`
	JobID                           string           `json:"job_id"`
	Operation                       string           `json:"operation"`
	ProcessingRunID                 string           `json:"processing_run_id"`
	SourceDocumentID                string           `json:"source_document_id"`
	ChangedRepositoryRegistrationID string           `json:"changed_repository_registration_id"`
	IndexPublication                v2RunPublication `json:"index_publication"`
	State                           string           `json:"state"`
	Terminal                        bool             `json:"terminal"`
	ExitClassification              string           `json:"exit_classification"`
	Attempt                         int              `json:"attempt"`
	CreatedAt                       string           `json:"created_at"`
	UpdatedAt                       string           `json:"updated_at"`
	RetrievalStatus                 string           `json:"retrieval_status"`
	QueryProvenance                 struct {
		SHA256   string `json:"sha256"`
		Length   int    `json:"length"`
		ByteSize int    `json:"byte_size"`
		Origin   string `json:"origin"`
	} `json:"query_provenance"`
	Effects      v2RunEffects `json:"effects"`
	ReasonCode   *string      `json:"reason_code"`
	FailureStage *string      `json:"failure_stage"`
	Replayed     bool         `json:"replayed"`
}

type runRuntime struct {
	client  *ControlClient
	options RunOptions
	counter requestCounter
	result  *RunResult
	store   *runStateStore
	state   *runState
	stateID string
}

func LoadSuccessfulIndexResult(filename, groupID string) (IndexResult, error) {
	var result IndexResult
	info, err := os.Lstat(filename)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumIndexResultBytes {
		return result, runError(RunFailureInput, "invalid_index_result_file")
	}
	value, err := os.ReadFile(filename)
	if err != nil || decodeStrictResponseJSON(value, &result) != nil {
		return IndexResult{}, runError(RunFailureInput, "malformed_index_result")
	}
	if err := validateIndexResult(result); err != nil || result.State != "succeeded" || result.GroupID != strings.TrimSpace(groupID) {
		return IndexResult{}, runError(RunFailureInput, "index_result_not_succeeded")
	}
	if result.ScanFingerprint == nil || result.CorpusGenerationID == nil || result.CorpusManifestHash == nil || result.CompatiblePublicationID == nil || result.IndexFormatVersion == nil || result.TokenizerVersion == nil || result.RetrievalConfigFingerprint == nil || result.EmbeddingFingerprint == nil || result.IndexFingerprint == nil || !validSHA256(*result.ScanFingerprint) || !validUUID(*result.CorpusGenerationID) || !validSHA256(*result.CorpusManifestHash) || !validUUID(*result.CompatiblePublicationID) || !validSHA256(*result.RetrievalConfigFingerprint) || !validSHA256(*result.EmbeddingFingerprint) || !validSHA256(*result.IndexFingerprint) {
		return IndexResult{}, runError(RunFailureInput, "index_result_identity_invalid")
	}
	return result, nil
}

func RunBaselineRun(ctx context.Context, groupID string, input ScanInput, indexResultPath string, options RunOptions) (*RunExecution, error) {
	configureRunOptions(&options)
	groupID = strings.TrimSpace(groupID)
	result := newRunResult(groupID, options.now().UTC())
	indexResult, err := LoadSuccessfulIndexResult(indexResultPath, groupID)
	if err != nil {
		setRunFailure(result, err, options.now())
		return &RunExecution{Result: *result}, err
	}
	scan, err := NewScanner().Scan(ctx, groupID, input)
	if err != nil {
		err = translateScanRunError(err)
		setRunFailure(result, err, options.now())
		return &RunExecution{Result: *result}, err
	}
	defer scan.ClearProtected()
	if err := validateRunLineage(input, scan, indexResult); err != nil {
		setRunFailure(result, err, options.now())
		return &RunExecution{Result: *result}, err
	}
	queryHash := sha256.Sum256(scan.RawDiff)
	querySHA := hex.EncodeToString(queryHash[:])
	queryLength := utf8.RuneCount(scan.RawDiff)
	setRunInputResult(result, input, scan, indexResult, querySHA, queryLength)

	client, err := NewControlClient(options.BaseURL, options.Token, options.AllowLoopbackHTTP)
	if err != nil {
		err = translateRunClientError(err)
		setRunFailure(result, err, options.now())
		return &RunExecution{Result: *result}, err
	}
	runtime := &runRuntime{client: client, options: options, result: result}
	operationContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	capabilities, err := runtime.capabilities(operationContext, groupID)
	if err != nil {
		return runtime.failure(err)
	}
	result.DispatchMode = capabilities.Operations.BaselineRun.Dispatch
	if err := validateRunCapabilityIntent(capabilities, indexResult); err != nil {
		return runtime.failure(err)
	}

	store, err := newRunStateStore(options.StateDirectory)
	if err != nil {
		return runtime.failure(err)
	}
	runtime.store = store
	publication := publicationFromIndexResult(indexResult)
	intentIdentity, err := runIntentIdentity(client.serverIdentitySHA256(), input, scan.Report.DeterministicFingerprint, querySHA, queryLength, len(scan.RawDiff), publication)
	if err != nil {
		return runtime.failure(runError(RunFailureInternal, "run_intent_fingerprint_failed"))
	}
	runtime.stateID = intentIdentity
	state, err := store.load(intentIdentity)
	if err != nil {
		return runtime.failure(err)
	}
	if options.Resume && state == nil {
		return runtime.failure(runError(RunFailureInput, "resume_state_not_found"))
	}
	if !options.Resume && state != nil {
		return runtime.failure(runError(RunFailureInput, "resume_required"))
	}
	if state == nil {
		if err := requireBaselineRunReady(capabilities.Operations.BaselineRun); err != nil {
			return runtime.failure(err)
		}
		state = newRunState(client.serverIdentitySHA256(), input, scan, publication, querySHA, queryLength, intentIdentity, options.now().UTC())
		if err := store.save(intentIdentity, state); err != nil {
			return runtime.failure(err)
		}
	} else if err := validateRunStateIdentity(state, client.serverIdentitySHA256(), input, scan, publication, querySHA, queryLength, intentIdentity); err != nil {
		return runtime.failure(err)
	}
	runtime.state = state
	result.Resumed = options.Resume
	if state.RunJobID != "" {
		result.RunJobID = stringPointer(state.RunJobID)
	}
	if state.ProcessingRunID != "" {
		result.ProcessingRunID = stringPointer(state.ProcessingRunID)
	}
	if state.PersistedRetrievalRunID != "" {
		result.PersistedRetrievalRunID = stringPointer(state.PersistedRetrievalRunID)
	}
	if state.SafeState == "terminal_failed" || state.SafeState == "blocked" || state.SafeState == "cancelled" || state.SafeState == "conflict" {
		return runtime.failure(runError(RunFailureConflict, "resume_state_terminal"))
	}

	if state.RunJobID == "" {
		// An exact resume may replay an already committed response even when new
		// admission has become not-ready; Core does not extend payload expiry.
		if !options.Resume {
			if err := requireBaselineRunReady(capabilities.Operations.BaselineRun); err != nil {
				return runtime.failure(err)
			}
		}
		accepted, err := runtime.submit(operationContext, input, scan.RawDiff, querySHA, publication)
		if err != nil {
			return runtime.failure(err)
		}
		state.RunJobID = accepted.JobID
		state.ProcessingRunID = accepted.ProcessingRunID
		state.SafeState = accepted.State
		state.UpdatedAt = timestamp(options.now().UTC())
		if err := store.save(intentIdentity, state); err != nil {
			return runtime.failure(err)
		}
		result.RunJobID = stringPointer(accepted.JobID)
		result.ProcessingRunID = stringPointer(accepted.ProcessingRunID)
		result.Replayed = accepted.Replayed
		result.State = accepted.State
		result.ExitClassification = "pending"
	}

	if !options.Wait {
		if options.Resume || state.SafeState != "queued" {
			status, err := runtime.status(operationContext, groupID, state.RunJobID, &publication)
			if err != nil {
				return runtime.failure(err)
			}
			if err := runtime.applyStatus(status); err != nil {
				return runtime.failure(err)
			}
		}
		runtime.refresh()
		return &RunExecution{Result: *result, store: store, stateID: intentIdentity, cleanupState: result.State == "feedback_persisted"}, terminalRunResult(result)
	}
	if err := runtime.wait(operationContext, groupID, state.RunJobID, publication); err != nil {
		return runtime.failure(err)
	}
	runtime.refresh()
	return &RunExecution{Result: *result, store: store, stateID: intentIdentity, cleanupState: true}, nil
}

func RunBaselineRunStatus(ctx context.Context, groupID, jobID string, options RunOptions) (*RunExecution, error) {
	configureRunOptions(&options)
	groupID, jobID = strings.TrimSpace(groupID), strings.TrimSpace(jobID)
	result := newRunResult(groupID, options.now().UTC())
	if groupID == "" || !validUUID(jobID) {
		err := runError(RunFailureInput, "status_identity_invalid")
		setRunFailure(result, err, options.now())
		return &RunExecution{Result: *result}, err
	}
	client, err := NewControlClient(options.BaseURL, options.Token, options.AllowLoopbackHTTP)
	if err != nil {
		err = translateRunClientError(err)
		setRunFailure(result, err, options.now())
		return &RunExecution{Result: *result}, err
	}
	runtime := &runRuntime{client: client, options: options, result: result}
	operationContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	capabilities, err := runtime.capabilities(operationContext, groupID)
	if err != nil {
		return runtime.failure(err)
	}
	result.DispatchMode = capabilities.Operations.BaselineRun.Dispatch
	status, err := runtime.status(operationContext, groupID, jobID, nil)
	if err != nil {
		return runtime.failure(err)
	}
	if err := runtime.applyStatus(status); err != nil {
		return runtime.failure(err)
	}
	runtime.refresh()
	return &RunExecution{Result: *result}, terminalRunResult(result)
}

func configureRunOptions(options *RunOptions) {
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

func newRunResult(groupID string, now time.Time) *RunResult {
	return &RunResult{
		SchemaVersion: RunResultSchemaVersion, ProtocolVersion: IndexControlProtocolVersion,
		ProtocolSHA256: IndexControlProtocolSHA256, GroupID: groupID, DispatchMode: "unavailable",
		State: "preflight", ExitClassification: "failed", UpdatedAt: timestamp(now),
	}
}

func validateRunLineage(input ScanInput, scan *ScanResult, index IndexResult) error {
	if scan == nil || scan.Report.GroupID != index.GroupID || scan.Report.DeterministicFingerprint != *index.ScanFingerprint || scan.Plan.Snapshot.ChangedRepository.SourceDocumentID != input.Changed.SourceDocumentID || scan.Plan.Snapshot.ChangedRepository.RepositoryID != input.Changed.RepositoryID || scan.Plan.RawDiff.BaseRevision != input.BaseRevision || scan.Plan.RawDiff.HeadRevision != input.HeadRevision {
		return runError(RunFailureConflict, "scan_index_lineage_mismatch")
	}
	if !validUUID(input.Changed.SourceDocumentID) || !validUUID(input.Changed.RepositoryID) {
		return runError(RunFailureInput, "run_authority_identity_invalid")
	}
	return nil
}

func validateRunCapabilityIntent(capabilities *v2CapabilitiesResponse, index IndexResult) error {
	if capabilities == nil {
		return runError(RunFailureContract, "capability_contract_mismatch")
	}
	intent := capabilities.RequiredIndexIdentity
	fingerprint, err := fingerprintValue(intent)
	if err != nil || index.IndexIntentFingerprint == nil || fingerprint != *index.IndexIntentFingerprint || intent.IndexFormatVersion != *index.IndexFormatVersion || intent.TokenizerVersion != *index.TokenizerVersion || intent.RetrievalConfigFingerprint != *index.RetrievalConfigFingerprint || intent.Embedding.Fingerprint != *index.EmbeddingFingerprint {
		return runError(RunFailureConflict, "index_identity_mismatch")
	}
	return nil
}

func publicationFromIndexResult(index IndexResult) v2RunPublication {
	return v2RunPublication{
		IndexPublicationID: *index.CompatiblePublicationID, CorpusGenerationID: *index.CorpusGenerationID,
		CorpusManifestHash: *index.CorpusManifestHash, IndexFormatVersion: *index.IndexFormatVersion,
		TokenizerVersion: *index.TokenizerVersion, RetrievalConfigFingerprint: *index.RetrievalConfigFingerprint,
		EmbeddingFingerprint: *index.EmbeddingFingerprint, IndexFingerprint: *index.IndexFingerprint,
	}
}

func setRunInputResult(result *RunResult, input ScanInput, scan *ScanResult, index IndexResult, querySHA string, queryLength int) {
	result.SourceDocumentID = stringPointer(input.Changed.SourceDocumentID)
	result.ChangedRepositoryRegistrationID = stringPointer(input.Changed.RepositoryID)
	result.QuerySHA256, result.QueryLength = stringPointer(querySHA), intPointer(queryLength)
	result.QueryByteSize, result.QueryOrigin = intPointer(len(scan.RawDiff)), stringPointer("explicit")
	result.ScanFingerprint = stringPointer(scan.Report.DeterministicFingerprint)
	result.CorpusGenerationID, result.CorpusManifestHash = index.CorpusGenerationID, index.CorpusManifestHash
	result.IndexPublicationID = index.CompatiblePublicationID
	result.IndexFormatVersion, result.TokenizerVersion = index.IndexFormatVersion, index.TokenizerVersion
	result.RetrievalConfigFingerprint, result.EmbeddingFingerprint = index.RetrievalConfigFingerprint, index.EmbeddingFingerprint
	result.IndexFingerprint = index.IndexFingerprint
}

func (runtime *runRuntime) capabilities(ctx context.Context, groupID string) (*v2CapabilitiesResponse, error) {
	requestID, err := newUUID()
	if err != nil {
		return nil, runError(RunFailureInternal, "request_identity_failed")
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
		return nil, runError(RunFailureInternal, "capability_request_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v2/capabilities", body)
	clearBytes(body)
	if err != nil {
		return nil, err
	}
	var capabilities v2CapabilitiesResponse
	if err := decodeStrictResponseJSON(response.Body, &capabilities); err != nil || validateV2Capabilities(capabilities, requestID, groupID, false) != nil {
		return nil, runError(RunFailureContract, "capability_contract_mismatch")
	}
	return &capabilities, nil
}

func requireBaselineRunReady(capability v2OperationCapability) error {
	if capability.Submission != "safe" || capability.Endpoint != "authenticated_post" || (capability.Dispatch != "automatic" && capability.Dispatch != "manual") || capability.Readiness != "ready" || capability.ReasonCode != nil {
		return runError(RunFailureCapability, capabilityReason(capability))
	}
	return nil
}

func (runtime *runRuntime) submit(ctx context.Context, input ScanInput, rawDiff []byte, querySHA string, publication v2RunPublication) (*v2RunAccepted, error) {
	requestID := runtime.store.deriveUUID("baseline-run-submit-request.v1", runtime.stateID)
	payload := struct {
		ProtocolVersion                 string           `json:"protocol_version"`
		ProtocolSHA256                  string           `json:"protocol_sha256"`
		MessageType                     string           `json:"message_type"`
		RequestID                       string           `json:"request_id"`
		GroupID                         string           `json:"group_id"`
		IdempotencyKey                  string           `json:"idempotency_key"`
		SourceDocumentID                string           `json:"source_document_id"`
		ChangedRepositoryRegistrationID string           `json:"changed_repository_registration_id"`
		IndexPublication                v2RunPublication `json:"index_publication"`
		RetrievalQuery                  v2RunQuery       `json:"retrieval_query"`
	}{
		ProtocolVersion: IndexControlProtocolVersion, ProtocolSHA256: IndexControlProtocolSHA256,
		MessageType: "run_submit", RequestID: requestID, GroupID: input.GroupID,
		IdempotencyKey:   runtime.store.deriveHex("baseline-run-submit-idempotency.v1", runtime.stateID),
		SourceDocumentID: input.Changed.SourceDocumentID, ChangedRepositoryRegistrationID: input.Changed.RepositoryID,
		IndexPublication: publication,
		RetrievalQuery:   v2RunQuery{Representation: "raw_git_diff_v1", Origin: "explicit", Encoding: "utf-8", BaseRevision: input.BaseRevision, HeadRevision: input.HeadRevision, ByteSize: len(rawDiff), SHA256: querySHA, Text: string(rawDiff)},
	}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > maximumRunRequestBytes {
		clearBytes(body)
		return nil, runError(RunFailureInput, "run_request_too_large")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v2/runs", body)
	clearBytes(body)
	if err != nil {
		return nil, err
	}
	var accepted v2RunAccepted
	if err := decodeStrictResponseJSON(response.Body, &accepted); err != nil || accepted.ProtocolVersion != IndexControlProtocolVersion || accepted.ProtocolSHA256 != IndexControlProtocolSHA256 || accepted.MessageType != "job_accepted" || accepted.RequestID != requestID || accepted.GroupID != input.GroupID || !validUUID(accepted.JobID) || accepted.Operation != "baseline_run" || accepted.State != "queued" || !validUUID(accepted.ProcessingRunID) {
		return nil, runError(RunFailureContract, "run_acceptance_mismatch")
	}
	return &accepted, nil
}

func (runtime *runRuntime) status(ctx context.Context, groupID, jobID string, expected *v2RunPublication) (*v2RunStatus, error) {
	requestID, err := newUUID()
	if err != nil {
		return nil, runError(RunFailureInternal, "request_identity_failed")
	}
	payload := struct {
		ProtocolVersion string `json:"protocol_version"`
		ProtocolSHA256  string `json:"protocol_sha256"`
		MessageType     string `json:"message_type"`
		RequestID       string `json:"request_id"`
		GroupID         string `json:"group_id"`
		JobID           string `json:"job_id"`
		Operation       string `json:"operation"`
	}{IndexControlProtocolVersion, IndexControlProtocolSHA256, "job_status_request", requestID, groupID, jobID, "baseline_run"}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > maximumV2ControlRequest {
		return nil, runError(RunFailureInternal, "run_status_encoding_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v2/runs/status", body)
	clearBytes(body)
	if err != nil {
		return nil, err
	}
	var status v2RunStatus
	if err := decodeStrictResponseJSON(response.Body, &status); err != nil || validateV2RunStatus(status, requestID, groupID, jobID, expected) != nil {
		return nil, runError(RunFailureContract, "run_status_contract_mismatch")
	}
	return &status, nil
}

func validateV2RunStatus(status v2RunStatus, requestID, groupID, jobID string, expected *v2RunPublication) error {
	if status.ProtocolVersion != IndexControlProtocolVersion || status.ProtocolSHA256 != IndexControlProtocolSHA256 || status.MessageType != "job_status" || status.RequestID != requestID || status.GroupID != groupID || status.JobID != jobID || status.Operation != "baseline_run" || !validUUID(status.ProcessingRunID) || !validUUID(status.SourceDocumentID) || !validUUID(status.ChangedRepositoryRegistrationID) || status.Attempt < 0 || !validTimestamp(status.CreatedAt) || !validTimestamp(status.UpdatedAt) || status.QueryProvenance.Origin != "explicit" || !validSHA256(status.QueryProvenance.SHA256) || status.QueryProvenance.Length < 1 || status.QueryProvenance.Length > MaxRawDiffBytes || status.QueryProvenance.ByteSize < 1 || status.QueryProvenance.ByteSize > MaxRawDiffBytes || !validRunPublication(status.IndexPublication) {
		return runError(RunFailureContract, "run_status_contract_mismatch")
	}
	if expected != nil && !reflect.DeepEqual(status.IndexPublication, *expected) {
		return runError(RunFailureConflict, "index_publication_mismatch")
	}
	effects := status.Effects
	if effects.EvidenceCount < 0 || effects.EvidenceCount > 4 || effects.ReferenceCount < 0 || effects.ReferenceCount > 4 || effects.FeedbackCount < 0 || effects.FeedbackCount > 4 || effects.NotificationOutboxCount < 0 || effects.NotificationOutboxCount > 1024 || (status.ReasonCode != nil && !validSafeReasonCode(*status.ReasonCode)) || (status.FailureStage != nil && !validRunFailureStage(*status.FailureStage)) {
		return runError(RunFailureContract, "run_status_contract_mismatch")
	}
	zero := effects.EvidenceCount == 0 && effects.ReferenceCount == 0 && effects.FeedbackCount == 0 && !effects.GenerationInvoked && effects.NotificationOutboxCount == 0 && effects.PersistedRunID == nil
	positiveReferences := effects.EvidenceCount >= 1 && effects.EvidenceCount == effects.ReferenceCount && effects.PersistedRunID != nil && validUUID(*effects.PersistedRunID)
	switch status.State {
	case "queued", "running":
		if status.Terminal || status.ExitClassification != "pending" || status.RetrievalStatus != "pending" || !zero || status.ReasonCode != nil || status.FailureStage != nil {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
	case "references_persisted":
		if status.Terminal || status.ExitClassification != "pending" || status.RetrievalStatus != "ok" || !positiveReferences || effects.FeedbackCount != 0 || effects.GenerationInvoked || effects.NotificationOutboxCount != 0 || status.ReasonCode != nil || status.FailureStage != nil {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
	case "feedback_persisted":
		if !status.Terminal || status.ExitClassification != "success" || status.RetrievalStatus != "ok" || !positiveReferences || !effects.GenerationInvoked || (effects.FeedbackCount == 0 && effects.NotificationOutboxCount != 0) || status.ReasonCode != nil || status.FailureStage != nil {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
	case "insufficient":
		if !status.Terminal || status.ExitClassification != "insufficient" || status.RetrievalStatus != "insufficient" || !zero || status.ReasonCode == nil || *status.ReasonCode != "retrieval_insufficient" || status.FailureStage == nil || *status.FailureStage != "retrieval" {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
	case "retryable_failed":
		if status.Terminal || status.ExitClassification != "pending" || status.ReasonCode == nil || status.FailureStage == nil {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
	case "terminal_failed":
		if !status.Terminal || status.ExitClassification != "failed" || status.ReasonCode == nil || status.FailureStage == nil {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
	case "blocked":
		if !status.Terminal || status.ExitClassification != "blocked" || status.ReasonCode == nil || status.FailureStage == nil {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
	case "cancelled":
		if !status.Terminal || status.ExitClassification != "cancelled" || status.ReasonCode == nil || *status.ReasonCode != "job_cancelled" || status.FailureStage == nil || *status.FailureStage != "dispatch" {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
	default:
		return runError(RunFailureContract, "run_status_contract_mismatch")
	}
	return nil
}

func validRunPublication(value v2RunPublication) bool {
	return validUUID(value.IndexPublicationID) && validUUID(value.CorpusGenerationID) && validSHA256(value.CorpusManifestHash) && validSafeIdentity(value.IndexFormatVersion) && validSafeIdentity(value.TokenizerVersion) && validSHA256(value.RetrievalConfigFingerprint) && validSHA256(value.EmbeddingFingerprint) && validSHA256(value.IndexFingerprint)
}

func validRunFailureStage(value string) bool {
	return value == "dispatch" || value == "retrieval" || value == "persistence" || value == "generation"
}

func (runtime *runRuntime) applyStatus(status *v2RunStatus) error {
	if status == nil {
		return runError(RunFailureContract, "run_status_contract_mismatch")
	}
	result := runtime.result
	result.RunJobID, result.ProcessingRunID = stringPointer(status.JobID), stringPointer(status.ProcessingRunID)
	result.SourceDocumentID, result.ChangedRepositoryRegistrationID = stringPointer(status.SourceDocumentID), stringPointer(status.ChangedRepositoryRegistrationID)
	result.State, result.ExitClassification = status.State, status.ExitClassification
	result.QuerySHA256, result.QueryLength = stringPointer(status.QueryProvenance.SHA256), intPointer(status.QueryProvenance.Length)
	result.QueryByteSize, result.QueryOrigin = intPointer(status.QueryProvenance.ByteSize), stringPointer(status.QueryProvenance.Origin)
	result.CorpusGenerationID, result.CorpusManifestHash = stringPointer(status.IndexPublication.CorpusGenerationID), stringPointer(status.IndexPublication.CorpusManifestHash)
	result.IndexPublicationID, result.IndexFormatVersion = stringPointer(status.IndexPublication.IndexPublicationID), stringPointer(status.IndexPublication.IndexFormatVersion)
	result.TokenizerVersion, result.RetrievalConfigFingerprint = stringPointer(status.IndexPublication.TokenizerVersion), stringPointer(status.IndexPublication.RetrievalConfigFingerprint)
	result.EmbeddingFingerprint, result.IndexFingerprint = stringPointer(status.IndexPublication.EmbeddingFingerprint), stringPointer(status.IndexPublication.IndexFingerprint)
	result.PersistedRetrievalRunID = status.Effects.PersistedRunID
	result.EvidenceCount, result.ReferenceCount, result.FeedbackCount = status.Effects.EvidenceCount, status.Effects.ReferenceCount, status.Effects.FeedbackCount
	result.NotificationOutboxCount, result.GenerationInvoked = status.Effects.NotificationOutboxCount, status.Effects.GenerationInvoked
	result.CreatedAt, result.UpdatedAt = stringPointer(status.CreatedAt), status.UpdatedAt
	// Replayed describes this invocation's authoritative submission receipt.
	// Status polling cannot infer or replace that receipt (Core status snapshots
	// deliberately report replayed=false).
	result.ReasonCode, result.FailureStage = status.ReasonCode, status.FailureStage
	if runtime.state != nil {
		if runtime.state.ProcessingRunID != "" && runtime.state.ProcessingRunID != status.ProcessingRunID {
			return runError(RunFailureConflict, "processing_run_identity_mismatch")
		}
		if runtime.state.QuerySHA256 != status.QueryProvenance.SHA256 || runtime.state.QueryLength != status.QueryProvenance.Length || runtime.state.QueryByteSize != status.QueryProvenance.ByteSize || runtime.state.SourceDocumentID != status.SourceDocumentID || runtime.state.ChangedRepositoryRegistrationID != status.ChangedRepositoryRegistrationID {
			return runError(RunFailureConflict, "run_status_identity_mismatch")
		}
		runtime.state.ProcessingRunID = status.ProcessingRunID
		runtime.state.SafeState = status.State
		runtime.state.UpdatedAt = status.UpdatedAt
		if status.Effects.PersistedRunID != nil {
			runtime.state.PersistedRetrievalRunID = *status.Effects.PersistedRunID
		}
		if status.Terminal {
			completed := status.UpdatedAt
			runtime.state.CompletedAt = &completed
		}
		if err := runtime.store.save(runtime.stateID, runtime.state); err != nil {
			return err
		}
	}
	return nil
}

func (runtime *runRuntime) wait(ctx context.Context, groupID, jobID string, publication v2RunPublication) error {
	delay := runtime.options.PollInterval
	for attempt := 1; ; attempt++ {
		status, err := runtime.status(ctx, groupID, jobID, &publication)
		if err != nil {
			return err
		}
		if err := runtime.applyStatus(status); err != nil {
			return err
		}
		if runtime.options.Progress != nil {
			runtime.options.Progress(status.State, status.Attempt)
		}
		if status.Terminal {
			return terminalRunResult(runtime.result)
		}
		if status.State != "queued" && status.State != "running" && status.State != "references_persisted" && status.State != "retryable_failed" {
			return runError(RunFailureContract, "run_status_contract_mismatch")
		}
		if err := runtime.options.sleep(ctx, jitter(delay)); err != nil {
			return runError(RunFailureRetryable, "wait_timeout")
		}
		if delay < 10*time.Second {
			delay *= 2
			if delay > 10*time.Second {
				delay = 10 * time.Second
			}
		}
	}
}

func (runtime *runRuntime) postWithRetry(ctx context.Context, endpoint string, body []byte) (controlResponse, error) {
	var last error
	for attempt := 0; attempt < runtime.options.RetryAttempts; attempt++ {
		response, err := runtime.client.postProtocol(ctx, endpoint, body, IndexControlProtocolVersion, IndexControlProtocolSHA256)
		runtime.counter.bytes += response.BodyBytes
		runtime.counter.count++
		if err == nil {
			return response, nil
		}
		last = classifyRunControlError(err)
		if RunFailure(last) != RunFailureRetryable || attempt+1 >= runtime.options.RetryAttempts {
			break
		}
		delay := runtime.options.RetryBaseDelay << attempt
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		if err := runtime.options.sleep(ctx, jitter(delay)); err != nil {
			last = runError(RunFailureRetryable, "wait_timeout")
			break
		}
	}
	if last == nil {
		last = runError(RunFailureRetryable, "retry_exhausted")
	}
	return controlResponse{}, last
}

func classifyRunControlError(err error) error {
	if err == nil {
		return nil
	}
	var run *RunError
	if errors.As(err, &run) {
		return run
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return runError(RunFailureRetryable, "wait_timeout")
	}
	var control *ControlHTTPError
	if errors.As(err, &control) {
		if control.StatusCode == http.StatusUnauthorized || control.StatusCode == http.StatusForbidden || control.StatusCode == http.StatusNotFound || control.Code == "not_found_or_forbidden" || control.Code == "repository_not_authorized" || control.Code == "source_not_authorized" {
			return runError(RunFailureAuth, control.Code)
		}
		switch control.Code {
		case "capability_unavailable", "embedding_unavailable", "worker_unavailable":
			return runError(RunFailureCapability, control.Code)
		case "idempotency_conflict", "stale_generation", "corpus_not_active", "repository_approval_revoked", "embedding_identity_mismatch", "index_identity_mismatch", "index_state_incompatible", "index_publication_stale":
			return runError(RunFailureConflict, control.Code)
		}
		if control.Retryable || control.StatusCode == http.StatusRequestTimeout || control.StatusCode == http.StatusTooManyRequests || control.StatusCode >= 500 {
			return runError(RunFailureRetryable, control.Code)
		}
		return runError(RunFailureContract, control.Code)
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, io.ErrUnexpectedEOF) {
		return runError(RunFailureRetryable, "transport_retry_exhausted")
	}
	return runError(RunFailureRetryable, "transport_retry_exhausted")
}

func translateRunClientError(err error) error {
	var upload *UploadError
	if errors.As(err, &upload) {
		if upload.Kind == UploadFailureAuthentication {
			return runError(RunFailureAuth, upload.Reason)
		}
		return runError(RunFailureInput, upload.Reason)
	}
	return runError(RunFailureInternal, "control_client_unavailable")
}

func translateScanRunError(err error) error {
	switch ScanFailure(err) {
	case ScanFailureRepository:
		return runError(RunFailureConflict, scannerReason(err))
	case ScanFailureContract:
		return runError(RunFailureInput, scannerReason(err))
	default:
		return runError(RunFailureInternal, "scan_internal_failure")
	}
}

func (runtime *runRuntime) failure(err error) (*RunExecution, error) {
	setRunFailure(runtime.result, err, runtime.options.now())
	if runtime.state != nil && runtime.store != nil && runtime.stateID != "" {
		switch RunFailure(err) {
		case RunFailureRetryable:
			runtime.state.SafeState = "retryable_incomplete"
		case RunFailureConflict:
			runtime.state.SafeState = "conflict"
		case RunFailureTerminal:
			runtime.state.SafeState = runtime.result.State
		}
		runtime.state.UpdatedAt = runtime.result.UpdatedAt
		if saveErr := runtime.store.save(runtime.stateID, runtime.state); saveErr != nil {
			err = saveErr
			setRunFailure(runtime.result, err, runtime.options.now())
		}
	}
	runtime.refresh()
	return &RunExecution{Result: *runtime.result, store: runtime.store, stateID: runtime.stateID}, err
}

func setRunFailure(result *RunResult, err error, now time.Time) {
	reason := SafeRunReason(err)
	result.ReasonCode = &reason
	result.UpdatedAt = timestamp(now.UTC())
	switch RunFailure(err) {
	case RunFailureRetryable:
		result.State, result.ExitClassification = "retryable_incomplete", "retryable"
	case RunFailureTerminal:
		if result.State != "insufficient" && result.State != "terminal_failed" && result.State != "blocked" && result.State != "cancelled" {
			result.State = "terminal_failed"
		}
		if result.State == "insufficient" {
			result.ExitClassification = "insufficient"
		} else if result.State == "blocked" {
			result.ExitClassification = "blocked"
		} else if result.State == "cancelled" {
			result.ExitClassification = "cancelled"
		} else {
			result.ExitClassification = "failed"
		}
	default:
		result.State, result.ExitClassification = "failed", "failed"
	}
}

func terminalRunResult(result *RunResult) error {
	if result == nil {
		return runError(RunFailureInternal, "internal_failure")
	}
	switch result.State {
	case "feedback_persisted":
		return nil
	case "insufficient", "terminal_failed", "blocked", "cancelled":
		reason := result.State
		if result.ReasonCode != nil {
			reason = *result.ReasonCode
		}
		return runError(RunFailureTerminal, reason)
	default:
		return nil
	}
}

func (runtime *runRuntime) refresh() {
	if runtime == nil || runtime.result == nil {
		return
	}
	runtime.result.TransmittedRequestBytes = runtime.counter.bytes
	runtime.result.TransmittedRequestCount = runtime.counter.count
	if runtime.result.UpdatedAt == "" {
		runtime.result.UpdatedAt = timestamp(runtime.options.now().UTC())
	}
}

func newRunState(serverIdentity string, input ScanInput, scan *ScanResult, publication v2RunPublication, querySHA string, queryLength int, intentFingerprint string, now time.Time) *runState {
	return &runState{
		SchemaVersion: runStateSchemaVersion, GroupID: input.GroupID, ProtocolVersion: IndexControlProtocolVersion,
		ProtocolSHA256: IndexControlProtocolSHA256, ServerIdentitySHA256: serverIdentity,
		ScanFingerprint: scan.Report.DeterministicFingerprint, SourceDocumentID: input.Changed.SourceDocumentID,
		ChangedRepositoryRegistrationID: input.Changed.RepositoryID, BaseRevision: input.BaseRevision, HeadRevision: input.HeadRevision,
		QuerySHA256: querySHA, QueryLength: queryLength, QueryByteSize: len(scan.RawDiff),
		CorpusGenerationID: publication.CorpusGenerationID, CorpusManifestHash: publication.CorpusManifestHash,
		IndexPublicationID: publication.IndexPublicationID, IndexFormatVersion: publication.IndexFormatVersion,
		TokenizerVersion: publication.TokenizerVersion, RetrievalConfigFingerprint: publication.RetrievalConfigFingerprint,
		EmbeddingFingerprint: publication.EmbeddingFingerprint, IndexFingerprint: publication.IndexFingerprint,
		RunIntentFingerprint: intentFingerprint, SafeState: "prepared", CreatedAt: timestamp(now), UpdatedAt: timestamp(now),
	}
}

func validateRunStateIdentity(state *runState, serverIdentity string, input ScanInput, scan *ScanResult, publication v2RunPublication, querySHA string, queryLength int, intentFingerprint string) error {
	if state.SchemaVersion != runStateSchemaVersion || state.GroupID != input.GroupID || state.ProtocolVersion != IndexControlProtocolVersion || state.ProtocolSHA256 != IndexControlProtocolSHA256 || state.ServerIdentitySHA256 != serverIdentity || state.ScanFingerprint != scan.Report.DeterministicFingerprint || state.SourceDocumentID != input.Changed.SourceDocumentID || state.ChangedRepositoryRegistrationID != input.Changed.RepositoryID || state.BaseRevision != input.BaseRevision || state.HeadRevision != input.HeadRevision || state.QuerySHA256 != querySHA || state.QueryLength != queryLength || state.QueryByteSize != len(scan.RawDiff) || state.CorpusGenerationID != publication.CorpusGenerationID || state.CorpusManifestHash != publication.CorpusManifestHash || state.IndexPublicationID != publication.IndexPublicationID || state.IndexFormatVersion != publication.IndexFormatVersion || state.TokenizerVersion != publication.TokenizerVersion || state.RetrievalConfigFingerprint != publication.RetrievalConfigFingerprint || state.EmbeddingFingerprint != publication.EmbeddingFingerprint || state.IndexFingerprint != publication.IndexFingerprint || state.RunIntentFingerprint != intentFingerprint || !validTimestamp(state.CreatedAt) || !validTimestamp(state.UpdatedAt) || (state.RunJobID != "" && !validUUID(state.RunJobID)) || (state.ProcessingRunID != "" && !validUUID(state.ProcessingRunID)) || (state.PersistedRetrievalRunID != "" && !validUUID(state.PersistedRetrievalRunID)) || !validSafeReasonCode(state.SafeState) {
		return runError(RunFailureConflict, "resume_identity_mismatch")
	}
	return nil
}

func runIntentIdentity(serverIdentity string, input ScanInput, scanFingerprint, querySHA string, queryLength, queryByteSize int, publication v2RunPublication) (string, error) {
	value := struct {
		SchemaVersion                   string           `json:"schema_version"`
		ServerIdentitySHA256            string           `json:"server_identity_sha256"`
		GroupID                         string           `json:"group_id"`
		ScanFingerprint                 string           `json:"scan_fingerprint"`
		SourceDocumentID                string           `json:"source_document_id"`
		ChangedRepositoryRegistrationID string           `json:"changed_repository_registration_id"`
		BaseRevision                    string           `json:"base_revision"`
		HeadRevision                    string           `json:"head_revision"`
		QuerySHA256                     string           `json:"query_sha256"`
		QueryLength                     int              `json:"query_length"`
		QueryByteSize                   int              `json:"query_byte_size"`
		IndexPublication                v2RunPublication `json:"index_publication"`
	}{runStateSchemaVersion, serverIdentity, input.GroupID, scanFingerprint, input.Changed.SourceDocumentID, input.Changed.RepositoryID, input.BaseRevision, input.HeadRevision, querySHA, queryLength, queryByteSize, publication}
	return fingerprintValue(value)
}

func intPointer(value int) *int { return &value }

func EncodeRunResult(writer io.Writer, result RunResult) error {
	if err := validateRunResult(result); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}

func validateRunResult(result RunResult) error {
	if result.SchemaVersion != RunResultSchemaVersion || result.ProtocolVersion != IndexControlProtocolVersion || result.ProtocolSHA256 != IndexControlProtocolSHA256 || strings.TrimSpace(result.GroupID) == "" || !validTimestamp(result.UpdatedAt) || result.EvidenceCount < 0 || result.EvidenceCount > 4 || result.ReferenceCount < 0 || result.ReferenceCount > 4 || result.FeedbackCount < 0 || result.FeedbackCount > 4 || result.NotificationOutboxCount < 0 || result.TransmittedRequestCount < 0 || result.TransmittedRequestBytes < 0 || (result.ReasonCode != nil && !validSafeReasonCode(*result.ReasonCode)) || (result.FailureStage != nil && !validRunFailureStage(*result.FailureStage)) {
		return runError(RunFailureInternal, "result_contract_mismatch")
	}
	switch result.State {
	case "feedback_persisted":
		if result.ExitClassification != "success" || result.PersistedRetrievalRunID == nil || result.EvidenceCount < 1 || result.EvidenceCount != result.ReferenceCount || !result.GenerationInvoked || (result.FeedbackCount == 0 && result.NotificationOutboxCount != 0) || result.ReasonCode != nil {
			return runError(RunFailureInternal, "result_contract_mismatch")
		}
	case "references_persisted":
		if result.ExitClassification != "pending" || result.PersistedRetrievalRunID == nil || result.EvidenceCount < 1 || result.EvidenceCount != result.ReferenceCount || result.FeedbackCount != 0 || result.GenerationInvoked || result.ReasonCode != nil {
			return runError(RunFailureInternal, "result_contract_mismatch")
		}
	case "queued", "running":
		if result.ExitClassification != "pending" || result.EvidenceCount != 0 || result.ReferenceCount != 0 || result.FeedbackCount != 0 || result.GenerationInvoked || result.ReasonCode != nil {
			return runError(RunFailureInternal, "result_contract_mismatch")
		}
	case "retryable_failed":
		if result.ExitClassification != "pending" || result.ReasonCode == nil {
			return runError(RunFailureInternal, "result_contract_mismatch")
		}
	case "retryable_incomplete":
		if result.ExitClassification != "retryable" || result.ReasonCode == nil {
			return runError(RunFailureInternal, "result_contract_mismatch")
		}
	case "insufficient":
		if result.ExitClassification != "insufficient" || result.EvidenceCount != 0 || result.ReferenceCount != 0 || result.FeedbackCount != 0 || result.GenerationInvoked || result.ReasonCode == nil {
			return runError(RunFailureInternal, "result_contract_mismatch")
		}
	case "terminal_failed", "blocked", "cancelled", "failed":
		if result.ReasonCode == nil {
			return runError(RunFailureInternal, "result_contract_mismatch")
		}
	default:
		return runError(RunFailureInternal, "result_contract_mismatch")
	}
	return nil
}

func RunResultForbiddenFields(value []byte) bool {
	text := strings.ToLower(string(value))
	for _, forbidden := range []string{"retrieval_query", "raw_diff", "idempotency_key", "lease_token", "ciphertext", "nonce", "key_id", "parent_processing", "provider_input", "provider_output", "evidence_content", "feedback_text", "credentials", "endpoint_url"} {
		if strings.Contains(text, forbidden) {
			return true
		}
	}
	return false
}
