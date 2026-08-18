package baseline

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

const UploadResultSchemaVersion = "baseline-snapshot-upload-result.v1"

type UploadFailureKind string

const (
	UploadFailureAuthentication UploadFailureKind = "authentication_authorization"
	UploadFailureRepository     UploadFailureKind = "repository_rescan_mismatch"
	UploadFailureContract       UploadFailureKind = "contract_limit_conflict"
	UploadFailureRetryable      UploadFailureKind = "timeout_retryable_incomplete"
	UploadFailureTerminal       UploadFailureKind = "terminal_server_failure"
	UploadFailureInternal       UploadFailureKind = "internal_failure"
)

type UploadError struct {
	Kind   UploadFailureKind
	Reason string
}

func (err *UploadError) Error() string {
	if err == nil || err.Reason == "" {
		return "baseline upload failed"
	}
	return "baseline upload failed: " + err.Reason
}

func uploadError(kind UploadFailureKind, reason string) error {
	return &UploadError{Kind: kind, Reason: reason}
}

func UploadFailure(err error) UploadFailureKind {
	var target *UploadError
	if errors.As(err, &target) {
		return target.Kind
	}
	return UploadFailureInternal
}

func SafeUploadReason(err error) string {
	var target *UploadError
	if errors.As(err, &target) && target.Reason != "" {
		return target.Reason
	}
	return "internal_failure"
}

type UploadOptions struct {
	BaseURL           string
	Token             string
	AllowLoopbackHTTP bool
	Resume            bool
	Wait              bool
	Timeout           time.Duration
	PollInterval      time.Duration
	StateDirectory    string
	Progress          func(stage string, completed, total int)
	RetryAttempts     int
	RetryBaseDelay    time.Duration
	sleep             func(context.Context, time.Duration) error
	now               func() time.Time
}

type UploadResult struct {
	SchemaVersion           string `json:"schema_version"`
	ProtocolVersion         string `json:"protocol_version"`
	ProtocolSHA256          string `json:"protocol_sha256"`
	GroupID                 string `json:"group_id"`
	ScanFingerprint         string `json:"scan_fingerprint,omitempty"`
	SnapshotID              string `json:"snapshot_id,omitempty"`
	StagingJobID            string `json:"staging_job_id,omitempty"`
	ContinuationJobID       string `json:"continuation_job_id,omitempty"`
	CanonicalManifestHash   string `json:"canonical_manifest_hash,omitempty"`
	ContentManifestHash     string `json:"content_manifest_hash,omitempty"`
	PartTotal               int    `json:"part_total"`
	PartCompleted           int    `json:"part_completed"`
	TransmittedRequestBytes int64  `json:"transmitted_request_bytes"`
	TransmittedRequestCount int    `json:"transmitted_request_count"`
	State                   string `json:"state"`
	CorpusID                string `json:"corpus_id,omitempty"`
	CorpusGenerationID      string `json:"corpus_generation_id,omitempty"`
	CorpusGenerationVersion string `json:"corpus_generation_version,omitempty"`
	Resumed                 bool   `json:"resumed"`
	Replayed                bool   `json:"replayed"`
	StartedAt               string `json:"started_at"`
	UpdatedAt               string `json:"updated_at"`
	ReasonCode              string `json:"reason_code,omitempty"`
}

type UploadExecution struct {
	Result       UploadResult
	store        *uploadStateStore
	stateID      string
	cleanupState bool
}

// Finalize removes only the safe resume record after the caller has durably
// emitted Result. It never removes the shared installation secret or user data.
func (execution *UploadExecution) Finalize() error {
	if execution == nil || !execution.cleanupState || execution.store == nil || execution.stateID == "" {
		return nil
	}
	return execution.store.delete(execution.stateID)
}

type capabilitiesResponse struct {
	ProtocolVersion string `json:"protocol_version"`
	ProtocolSHA256  string `json:"protocol_sha256"`
	MessageType     string `json:"message_type"`
	RequestID       string `json:"request_id"`
	GroupID         string `json:"group_id"`
	Operations      struct {
		SnapshotStaging string `json:"snapshot_staging"`
		CorpusIngestion string `json:"corpus_ingestion"`
		IndexBuild      string `json:"index_build"`
		BaselineRun     string `json:"baseline_run"`
	} `json:"operations"`
	Transport struct {
		Status               string `json:"status"`
		Reason               string `json:"reason"`
		Encrypted            bool   `json:"encrypted"`
		LocalOverrideEnabled bool   `json:"local_override_enabled"`
	} `json:"transport"`
	RequestBodyLogging    bool `json:"request_body_logging"`
	StagingCorpusEligible bool `json:"staging_is_corpus_eligible"`
	StagingIndexEligible  bool `json:"staging_is_index_eligible"`
	Limits                struct {
		SiblingRepositories   int `json:"sibling_repositories"`
		FileRecords           int `json:"file_records"`
		FileBytes             int `json:"file_bytes"`
		SupportedContentBytes int `json:"supported_content_bytes"`
		ManifestRequestBytes  int `json:"manifest_request_bytes"`
		PartRequestBytes      int `json:"content_part_request_bytes"`
		PartBytes             int `json:"content_part_bytes"`
		PartItems             int `json:"content_part_items"`
		Parts                 int `json:"content_parts"`
		ControlRequestBytes   int `json:"control_request_bytes"`
		StagingLifetime       int `json:"staging_lifetime_seconds"`
	} `json:"limits"`
}

type jobStatusResponse struct {
	ProtocolVersion string `json:"protocol_version"`
	ProtocolSHA256  string `json:"protocol_sha256"`
	MessageType     string `json:"message_type"`
	RequestID       string `json:"request_id"`
	GroupID         string `json:"group_id"`
	JobID           string `json:"job_id"`
	Operation       string `json:"operation"`
	State           string `json:"state"`
	Attempt         int    `json:"attempt"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	Progress        struct {
		Completed int `json:"completed"`
		Total     int `json:"total"`
	} `json:"progress"`
	Result *struct {
		SnapshotID     string `json:"snapshot_id"`
		StagingState   string `json:"staging_state"`
		CorpusEligible bool   `json:"corpus_eligible"`
		IndexEligible  bool   `json:"index_eligible"`
	} `json:"result"`
	ErrorCode *string `json:"error_code"`
	Staging   *struct {
		State          string `json:"state"`
		ReceivedParts  int    `json:"received_parts"`
		ExpectedParts  int    `json:"expected_parts"`
		ExpiresAt      string `json:"expires_at"`
		CorpusEligible bool   `json:"corpus_eligible"`
		IndexEligible  bool   `json:"index_eligible"`
	} `json:"staging"`
	Replayed bool `json:"replayed"`
}

type acceptedResponse struct {
	ProtocolVersion string `json:"protocol_version"`
	ProtocolSHA256  string `json:"protocol_sha256"`
	MessageType     string `json:"message_type"`
	RequestID       string `json:"request_id"`
	GroupID         string `json:"group_id"`
	JobID           string `json:"job_id"`
	Operation       string `json:"operation"`
	State           string `json:"state"`
	Replayed        bool   `json:"replayed"`
}

type continuationStatusResponse struct {
	SchemaVersion string `json:"schema_version"`
	MessageType   string `json:"message_type"`
	RequestID     string `json:"request_id"`
	GroupID       string `json:"group_id"`
	StagingJobID  string `json:"staging_job_id"`
	JobID         string `json:"job_id"`
	Operation     string `json:"operation"`
	State         string `json:"state"`
	Attempt       int    `json:"attempt"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	Progress      struct {
		Completed int `json:"completed"`
		Total     int `json:"total"`
	} `json:"progress"`
	Result struct {
		SnapshotID                  string `json:"snapshot_id"`
		StagingState                string `json:"staging_state"`
		CorpusIngestionComplete     bool   `json:"corpus_ingestion_complete"`
		CorpusEligible              bool   `json:"corpus_eligible"`
		IndexEligible               bool   `json:"index_eligible"`
		BaselineEligible            bool   `json:"baseline_eligible"`
		IndexState                  string `json:"index_state"`
		CorpusID                    string `json:"corpus_id,omitempty"`
		CorpusGenerationID          string `json:"corpus_generation_id,omitempty"`
		CorpusGenerationVersion     string `json:"corpus_generation_version,omitempty"`
		CorpusManifestHash          string `json:"corpus_manifest_hash,omitempty"`
		CorpusProvenanceFingerprint string `json:"corpus_provenance_fingerprint,omitempty"`
		WorkerContractVersion       string `json:"worker_contract_version,omitempty"`
	} `json:"result"`
	ErrorCode *string `json:"error_code"`
	Staging   struct {
		State          string `json:"state"`
		ReceivedParts  int    `json:"received_parts"`
		ExpectedParts  int    `json:"expected_parts"`
		ExpiresAt      string `json:"expires_at"`
		CorpusEligible bool   `json:"corpus_eligible"`
		IndexEligible  bool   `json:"index_eligible"`
	} `json:"staging"`
	Continuation struct {
		JobID                   string  `json:"job_id"`
		Operation               string  `json:"operation"`
		State                   string  `json:"state"`
		Attempt                 int     `json:"attempt"`
		CreatedAt               string  `json:"created_at"`
		UpdatedAt               string  `json:"updated_at"`
		ErrorCode               *string `json:"error_code"`
		CorpusIngestionComplete bool    `json:"corpus_ingestion_complete"`
		CorpusEligible          bool    `json:"corpus_eligible"`
		IndexEligible           bool    `json:"index_eligible"`
		BaselineEligible        bool    `json:"baseline_eligible"`
	} `json:"continuation"`
	Replayed bool `json:"replayed"`
}

type requestCounter struct {
	bytes int64
	count int
}

type uploadRuntime struct {
	client     *ControlClient
	options    UploadOptions
	counter    requestCounter
	store      *uploadStateStore
	state      *uploadState
	stateID    string
	result     *UploadResult
	replayed   bool
	mutationID string
	started    time.Time
}

func RunUpload(ctx context.Context, groupID string, input ScanInput, options UploadOptions) (*UploadExecution, error) {
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Minute
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
	started := options.now().UTC()
	result := &UploadResult{
		SchemaVersion: UploadResultSchemaVersion, ProtocolVersion: ControlProtocolVersion, ProtocolSHA256: ControlProtocolSHA256,
		GroupID: groupID, State: "preflight", StartedAt: timestamp(started), UpdatedAt: timestamp(started),
	}
	client, err := NewControlClient(options.BaseURL, options.Token, options.AllowLoopbackHTTP)
	if err != nil {
		result.State, result.ReasonCode = "failed", SafeUploadReason(err)
		return &UploadExecution{Result: *result}, err
	}
	runtime := &uploadRuntime{client: client, options: options, result: result, started: started}
	operationContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	if err := runtime.preflight(operationContext, groupID); err != nil {
		return runtime.failureExecution(err, false)
	}

	runtime.progress("scanning", 0, 1)
	scan, err := NewScanner().Scan(operationContext, groupID, input)
	if err != nil {
		kind := UploadFailureInternal
		if ScanFailure(err) == ScanFailureRepository {
			kind = UploadFailureRepository
		} else if ScanFailure(err) == ScanFailureContract {
			kind = UploadFailureContract
		}
		return runtime.failureExecution(uploadError(kind, scannerReason(err)), false)
	}
	defer scan.ClearProtected()
	runtime.progress("scanning", 1, 1)
	result.ScanFingerprint = scan.Report.DeterministicFingerprint
	result.SnapshotID = scan.Report.SnapshotID
	result.CanonicalManifestHash = scan.Report.CanonicalManifestHash
	result.ContentManifestHash = scan.Report.ContentManifestHash
	result.PartTotal = len(scan.Parts)

	store, err := newUploadStateStore(options.StateDirectory)
	if err != nil {
		return runtime.failureExecution(err, false)
	}
	runtime.store = store
	serverIdentity := client.serverIdentitySHA256()
	planIdentity, err := planIdentitySHA256(groupID, serverIdentity, input)
	if err != nil {
		return runtime.failureExecution(uploadError(UploadFailureInternal, "plan_identity_failed"), false)
	}
	runtime.stateID = planIdentity
	revisionHash, err := revisionSetSHA256(scan.Report)
	if err != nil {
		return runtime.failureExecution(uploadError(UploadFailureInternal, "revision_identity_failed"), false)
	}
	state, err := store.load(planIdentity)
	if err != nil {
		return runtime.failureExecution(err, false)
	}
	if options.Resume && state == nil {
		return runtime.failureExecution(uploadError(UploadFailureContract, "resume_state_not_found"), false)
	}
	if !options.Resume && state != nil {
		return runtime.failureExecution(uploadError(UploadFailureContract, "resume_required"), false)
	}
	if state == nil {
		state = newUploadState(groupID, serverIdentity, planIdentity, revisionHash, scan.Report, started)
		if err := store.save(planIdentity, state); err != nil {
			return runtime.failureExecution(err, false)
		}
	} else if err := validateUploadState(state, groupID, serverIdentity, planIdentity, revisionHash, scan.Report); err != nil {
		return runtime.failureExecution(err, false)
	}
	runtime.state = state
	result.Resumed = options.Resume
	result.StagingJobID = state.StagingJobID
	result.ContinuationJobID = state.ContinuationJobID
	result.PartCompleted = len(state.CompletedParts)
	runtime.mutationID = planIdentity + "\x00" + scan.Report.DeterministicFingerprint

	if err := runtime.begin(operationContext, scan); err != nil {
		return runtime.failureExecution(err, shouldDiscardState(err))
	}
	beginReplayed := runtime.replayed
	sealedReplay := false
	if beginReplayed {
		status, err := runtime.stagingStatus(operationContext)
		if err != nil {
			return runtime.failureExecution(err, shouldDiscardState(err))
		}
		sealedReplay, err = runtime.reconcileStagingStatus(status)
		if err != nil {
			return runtime.failureExecution(err, shouldDiscardState(err))
		}
	}
	if !sealedReplay {
		if err := runtime.parts(operationContext, scan); err != nil {
			if beginReplayed && SafeUploadReason(err) == "staging_not_open" {
				recovered, refreshErr := runtime.refreshSealedRace(operationContext)
				if refreshErr == nil && recovered {
					sealedReplay = true
				} else {
					return runtime.failureExecution(err, shouldDiscardState(err))
				}
			} else {
				return runtime.failureExecution(err, shouldDiscardState(err))
			}
		}
	}
	if err := runtime.commit(operationContext, scan); err != nil {
		return runtime.failureExecution(err, shouldDiscardState(err))
	}
	if options.Wait {
		if err := runtime.waitForContinuation(operationContext); err != nil {
			return runtime.failureExecution(err, shouldDiscardState(err))
		}
	} else if sealedReplay {
		if err := runtime.recoverContinuation(operationContext); err != nil {
			return runtime.failureExecution(err, shouldDiscardState(err))
		}
	} else {
		result.State = "snapshot_committed"
		result.ReasonCode = ""
	}
	runtime.refreshResult()
	return &UploadExecution{Result: *result, store: store, stateID: planIdentity, cleanupState: true}, nil
}

func (runtime *uploadRuntime) preflight(ctx context.Context, groupID string) error {
	requestID, err := newUUID()
	if err != nil {
		return uploadError(UploadFailureInternal, "request_identity_failed")
	}
	payload := struct {
		ProtocolVersion string `json:"protocol_version"`
		ProtocolSHA256  string `json:"protocol_sha256"`
		MessageType     string `json:"message_type"`
		RequestID       string `json:"request_id"`
		GroupID         string `json:"group_id"`
	}{ControlProtocolVersion, ControlProtocolSHA256, "capabilities_request", requestID, groupID}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > MaxControlRequest {
		return uploadError(UploadFailureInternal, "capability_request_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v1/capabilities", body)
	clearBytes(body)
	if err != nil {
		return err
	}
	var capabilities capabilitiesResponse
	if err := decodeStrictResponseJSON(response.Body, &capabilities); err != nil {
		return uploadError(UploadFailureContract, "capability_contract_mismatch")
	}
	if capabilities.ProtocolVersion != ControlProtocolVersion || capabilities.ProtocolSHA256 != ControlProtocolSHA256 || capabilities.MessageType != "capabilities" || capabilities.RequestID != requestID || capabilities.GroupID != groupID {
		return uploadError(UploadFailureContract, "capability_protocol_mismatch")
	}
	if capabilities.Operations.SnapshotStaging != "safe" || capabilities.RequestBodyLogging || capabilities.StagingCorpusEligible || capabilities.StagingIndexEligible {
		return uploadError(UploadFailureContract, "snapshot_staging_unavailable")
	}
	isHTTPS := runtime.client.baseURL.Scheme == "https"
	if isHTTPS {
		if capabilities.Transport.Status != "safe" || !capabilities.Transport.Encrypted {
			return uploadError(UploadFailureContract, "transport_attestation_mismatch")
		}
	} else if capabilities.Transport.Status != "local_override" || capabilities.Transport.Encrypted || !capabilities.Transport.LocalOverrideEnabled {
		return uploadError(UploadFailureContract, "loopback_transport_attestation_mismatch")
	}
	limits := capabilities.Limits
	if limits.SiblingRepositories < MaxSiblingRepositories || limits.FileRecords < MaxFileRecords || limits.FileBytes < MaxFileBytes || limits.SupportedContentBytes < MaxSupportedBytes || limits.ManifestRequestBytes < MaxManifestRequest || limits.PartRequestBytes < MaxPartRequest || limits.PartBytes < MaxPartBytes || limits.PartItems < MaxPartItems || limits.Parts < MaxContentParts || limits.ControlRequestBytes < MaxControlRequest || limits.StagingLifetime <= 0 {
		return uploadError(UploadFailureContract, "server_limits_incompatible")
	}
	runtime.progress("preflight", 1, 1)
	return nil
}

func newUploadState(groupID, serverIdentity, planIdentity, revisionHash string, report DryRunReport, now time.Time) *uploadState {
	parts := make([]uploadStatePart, len(report.Parts))
	for index, part := range report.Parts {
		parts[index] = uploadStatePart{Ordinal: part.Ordinal, SHA256: part.SHA256}
	}
	return &uploadState{
		SchemaVersion: uploadStateSchemaVersion, GroupID: groupID, ServerIdentitySHA256: serverIdentity, PlanIdentitySHA256: planIdentity,
		ProtocolVersion: ControlProtocolVersion, ProtocolSHA256: ControlProtocolSHA256, ScanFingerprint: report.DeterministicFingerprint,
		RevisionSetSHA256: revisionHash, SnapshotID: report.SnapshotID, CanonicalManifestHash: report.CanonicalManifestHash,
		ContentManifestHash: report.ContentManifestHash,
		Counts:              uploadStateCounts{RepositoryCount: report.Counts.RepositoryCount, FileCount: report.Counts.FileCount, SupportedFileCount: report.Counts.SupportedFileCount, PartCount: len(parts)},
		Parts:               parts, CompletedParts: []uploadStatePart{}, SafeState: "scanned", CreatedAt: timestamp(now), UpdatedAt: timestamp(now),
	}
}

func validateUploadState(state *uploadState, groupID, serverIdentity, planIdentity, revisionHash string, report DryRunReport) error {
	if state.SchemaVersion != uploadStateSchemaVersion || state.GroupID != groupID || state.ServerIdentitySHA256 != serverIdentity || state.PlanIdentitySHA256 != planIdentity || state.ProtocolVersion != ControlProtocolVersion || state.ProtocolSHA256 != ControlProtocolSHA256 || state.ScanFingerprint != report.DeterministicFingerprint || state.RevisionSetSHA256 != revisionHash || state.SnapshotID != report.SnapshotID || state.CanonicalManifestHash != report.CanonicalManifestHash || state.ContentManifestHash != report.ContentManifestHash {
		return uploadError(UploadFailureRepository, "resume_rescan_mismatch")
	}
	counts := state.Counts
	if counts.RepositoryCount != report.Counts.RepositoryCount || counts.FileCount != report.Counts.FileCount || counts.SupportedFileCount != report.Counts.SupportedFileCount || counts.PartCount != len(report.Parts) || len(state.Parts) != len(report.Parts) || len(state.CompletedParts) > len(report.Parts) {
		return uploadError(UploadFailureRepository, "resume_count_mismatch")
	}
	for index, part := range report.Parts {
		if state.Parts[index].Ordinal != part.Ordinal || state.Parts[index].SHA256 != part.SHA256 {
			return uploadError(UploadFailureRepository, "resume_part_mismatch")
		}
	}
	for index, completed := range state.CompletedParts {
		if completed != state.Parts[index] {
			return uploadError(UploadFailureContract, "corrupt_resume_state")
		}
	}
	return nil
}

func (runtime *uploadRuntime) begin(ctx context.Context, scan *ScanResult) error {
	requestID := runtime.store.deriveUUID("snapshot-begin-request", runtime.mutationID)
	idempotencyKey := "bi_" + runtime.store.deriveHex("snapshot-begin-idempotency", runtime.mutationID)
	payload := struct {
		ProtocolVersion string           `json:"protocol_version"`
		ProtocolSHA256  string           `json:"protocol_sha256"`
		MessageType     string           `json:"message_type"`
		RequestID       string           `json:"request_id"`
		GroupID         string           `json:"group_id"`
		IdempotencyKey  string           `json:"idempotency_key"`
		Snapshot        SnapshotManifest `json:"snapshot"`
	}{ControlProtocolVersion, ControlProtocolSHA256, "snapshot_begin", requestID, runtime.state.GroupID, idempotencyKey, scan.Plan.Snapshot}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > scan.Report.ManifestRequestBytes || len(body) > MaxManifestRequest {
		return uploadError(UploadFailureContract, "manifest_request_exceeds_plan")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v1/snapshots", body)
	clearBytes(body)
	if err != nil {
		return err
	}
	var accepted acceptedResponse
	if err := decodeStrictResponseJSON(response.Body, &accepted); err != nil || accepted.ProtocolVersion != ControlProtocolVersion || accepted.ProtocolSHA256 != ControlProtocolSHA256 || accepted.MessageType != "job_accepted" || accepted.RequestID != requestID || accepted.GroupID != runtime.state.GroupID || accepted.Operation != "snapshot_ingest" || accepted.State != "queued" || !validUUID(accepted.JobID) {
		return uploadError(UploadFailureContract, "begin_response_mismatch")
	}
	if runtime.state.StagingJobID != "" && runtime.state.StagingJobID != accepted.JobID {
		return uploadError(UploadFailureContract, "begin_replay_identity_mismatch")
	}
	runtime.state.StagingJobID = accepted.JobID
	runtime.state.SafeState = "staging"
	runtime.state.UpdatedAt = timestamp(runtime.options.now().UTC())
	runtime.replayed = runtime.replayed || accepted.Replayed
	runtime.result.StagingJobID = accepted.JobID
	if err := runtime.store.save(runtime.stateID, runtime.state); err != nil {
		return err
	}
	runtime.progress("begin", 1, 1)
	return nil
}

func (runtime *uploadRuntime) stagingStatus(ctx context.Context) (*jobStatusResponse, error) {
	requestID, err := newUUID()
	if err != nil {
		return nil, uploadError(UploadFailureInternal, "request_identity_failed")
	}
	payload := struct {
		ProtocolVersion string `json:"protocol_version"`
		ProtocolSHA256  string `json:"protocol_sha256"`
		MessageType     string `json:"message_type"`
		RequestID       string `json:"request_id"`
		GroupID         string `json:"group_id"`
		JobID           string `json:"job_id"`
	}{ControlProtocolVersion, ControlProtocolSHA256, "job_status_request", requestID, runtime.state.GroupID, runtime.state.StagingJobID}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > MaxControlRequest {
		return nil, uploadError(UploadFailureInternal, "status_request_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v1/jobs/status", body)
	clearBytes(body)
	if err != nil {
		return nil, err
	}
	status, err := validateJobStatus(response.Body, requestID, runtime.state.GroupID, runtime.state.StagingJobID)
	if err != nil {
		return nil, err
	}
	runtime.replayed = runtime.replayed || status.Replayed
	return status, nil
}

// reconcileStagingStatus binds the replayed begin response to the locally
// reconstructed immutable upload. The begin idempotency key is HMAC-derived
// from the local plan identity and scan fingerprint, so Core's replay response
// binds those values without exposing them. Snapshot/count equality is checked
// here; the idempotent commit below proves the exact ordered part descriptors
// and content-manifest hash before any continuation result is accepted.
func (runtime *uploadRuntime) reconcileStagingStatus(status *jobStatusResponse) (bool, error) {
	if status.Operation != "snapshot_ingest" || status.Staging == nil {
		return false, uploadError(UploadFailureContract, "replay_status_mismatch")
	}
	if status.ErrorCode != nil && !validSafeReasonCode(*status.ErrorCode) {
		return false, uploadError(UploadFailureContract, "replay_status_mismatch")
	}
	staging := status.Staging
	expected := len(runtime.state.Parts)
	if staging.ExpectedParts != expected || staging.ReceivedParts < 0 || staging.ReceivedParts > expected || status.Progress.Total != expected || status.Progress.Completed != staging.ReceivedParts || staging.CorpusEligible || staging.IndexEligible {
		return false, uploadError(UploadFailureContract, "replay_count_mismatch")
	}
	switch staging.State {
	case "expired":
		return false, uploadError(UploadFailureTerminal, "staging_expired")
	case "failed":
		return false, uploadError(UploadFailureTerminal, safeSnapshotReason(status.State, status.ErrorCode))
	case "open":
		if status.Result != nil {
			return false, uploadError(UploadFailureContract, "replay_status_mismatch")
		}
		switch status.State {
		case "queued", "running":
			if status.ErrorCode != nil {
				return false, uploadError(UploadFailureContract, "replay_status_mismatch")
			}
		case "retryable_failed":
			return false, uploadError(UploadFailureRetryable, safeSnapshotReason(status.State, status.ErrorCode))
		case "terminal_failed", "blocked", "cancelled":
			return false, uploadError(UploadFailureTerminal, safeSnapshotReason(status.State, status.ErrorCode))
		default:
			return false, uploadError(UploadFailureContract, "replay_status_mismatch")
		}
	case "sealed":
		if status.State != "succeeded" || status.Result == nil || staging.ReceivedParts != expected || status.Result.SnapshotID != runtime.state.SnapshotID || status.Result.StagingState != "sealed" || status.Result.CorpusEligible || status.Result.IndexEligible || status.ErrorCode != nil {
			return false, uploadError(UploadFailureContract, "replay_status_mismatch")
		}
	default:
		return false, uploadError(UploadFailureContract, "replay_status_mismatch")
	}
	if len(runtime.state.CompletedParts) > staging.ReceivedParts {
		return false, uploadError(UploadFailureContract, "replay_count_mismatch")
	}
	runtime.state.CompletedParts = append([]uploadStatePart(nil), runtime.state.Parts[:staging.ReceivedParts]...)
	runtime.state.SafeState = "staging_" + staging.State
	runtime.state.UpdatedAt = timestamp(runtime.options.now().UTC())
	runtime.result.PartCompleted = staging.ReceivedParts
	if err := runtime.store.save(runtime.stateID, runtime.state); err != nil {
		return false, err
	}
	return staging.State == "sealed", nil
}

func (runtime *uploadRuntime) refreshSealedRace(ctx context.Context) (bool, error) {
	status, err := runtime.stagingStatus(ctx)
	if err != nil {
		return false, err
	}
	return runtime.reconcileStagingStatus(status)
}

func (runtime *uploadRuntime) parts(ctx context.Context, scan *ScanResult) error {
	completed := len(runtime.state.CompletedParts)
	for index := completed; index < len(scan.Parts); index++ {
		part := scan.Parts[index]
		requestID := runtime.store.deriveUUID(fmt.Sprintf("snapshot-part-%d-request", part.Descriptor.PartOrdinal), runtime.mutationID)
		payload := struct {
			ProtocolVersion string        `json:"protocol_version"`
			ProtocolSHA256  string        `json:"protocol_sha256"`
			MessageType     string        `json:"message_type"`
			RequestID       string        `json:"request_id"`
			GroupID         string        `json:"group_id"`
			JobID           string        `json:"job_id"`
			SnapshotID      string        `json:"snapshot_id"`
			PartOrdinal     int           `json:"part_ordinal"`
			PartSHA256      string        `json:"part_sha256"`
			ContentItems    []ContentItem `json:"content_items"`
		}{ControlProtocolVersion, ControlProtocolSHA256, "snapshot_content_part", requestID, runtime.state.GroupID, runtime.state.StagingJobID, runtime.state.SnapshotID, part.Descriptor.PartOrdinal, part.Descriptor.PartSHA256, part.Items}
		body, err := canonicalJSONBytes(payload)
		planned := scan.Report.Parts[index].RequestBytes
		if err != nil || len(body) > planned || len(body) > MaxPartRequest {
			return uploadError(UploadFailureContract, "part_request_exceeds_plan")
		}
		response, err := runtime.postWithRetry(ctx, controlPath("/baseline/control/v1/snapshots/%s/parts", runtime.state.StagingJobID), body)
		clearBytes(body)
		if err != nil {
			return err
		}
		status, err := validateJobStatus(response.Body, requestID, runtime.state.GroupID, runtime.state.StagingJobID)
		if err != nil || status.Operation != "snapshot_ingest" || status.Staging == nil || status.Staging.ReceivedParts < index+1 || status.Staging.ExpectedParts != len(scan.Parts) {
			return uploadError(UploadFailureContract, "part_response_mismatch")
		}
		runtime.replayed = runtime.replayed || status.Replayed
		runtime.state.CompletedParts = append(runtime.state.CompletedParts, runtime.state.Parts[index])
		runtime.state.SafeState = "uploading_parts"
		runtime.state.UpdatedAt = timestamp(runtime.options.now().UTC())
		runtime.result.PartCompleted = len(runtime.state.CompletedParts)
		if err := runtime.store.save(runtime.stateID, runtime.state); err != nil {
			return err
		}
		runtime.progress("parts", index+1, len(scan.Parts))
	}
	return nil
}

func (runtime *uploadRuntime) commit(ctx context.Context, scan *ScanResult) error {
	descriptors := make([]PartDescriptor, len(scan.Parts))
	for index := range scan.Parts {
		descriptors[index] = scan.Parts[index].Descriptor
	}
	requestID := runtime.store.deriveUUID("snapshot-commit-request", runtime.mutationID)
	payload := struct {
		ProtocolVersion     string           `json:"protocol_version"`
		ProtocolSHA256      string           `json:"protocol_sha256"`
		MessageType         string           `json:"message_type"`
		RequestID           string           `json:"request_id"`
		GroupID             string           `json:"group_id"`
		JobID               string           `json:"job_id"`
		SnapshotID          string           `json:"snapshot_id"`
		Parts               []PartDescriptor `json:"parts"`
		ContentManifestHash string           `json:"content_manifest_hash"`
	}{ControlProtocolVersion, ControlProtocolSHA256, "snapshot_commit", requestID, runtime.state.GroupID, runtime.state.StagingJobID, runtime.state.SnapshotID, descriptors, runtime.state.ContentManifestHash}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > scan.Report.CommitRequestBytes || len(body) > MaxControlRequest {
		return uploadError(UploadFailureContract, "commit_request_exceeds_plan")
	}
	response, err := runtime.postWithRetry(ctx, controlPath("/baseline/control/v1/snapshots/%s/commit", runtime.state.StagingJobID), body)
	clearBytes(body)
	if err != nil {
		return err
	}
	status, err := validateJobStatus(response.Body, requestID, runtime.state.GroupID, runtime.state.StagingJobID)
	if err != nil || status.Operation != "snapshot_ingest" || status.State != "succeeded" || status.Result == nil || status.Result.SnapshotID != runtime.state.SnapshotID || status.Result.StagingState != "sealed" || status.Result.CorpusEligible || status.Result.IndexEligible || status.Staging == nil || status.Staging.State != "sealed" || status.Staging.ReceivedParts != len(runtime.state.Parts) || status.Staging.ExpectedParts != len(runtime.state.Parts) || status.Progress.Completed != len(runtime.state.Parts) || status.Progress.Total != len(runtime.state.Parts) || status.ErrorCode != nil {
		return uploadError(UploadFailureContract, "commit_response_mismatch")
	}
	runtime.replayed = runtime.replayed || status.Replayed
	runtime.state.CompletedParts = append([]uploadStatePart(nil), runtime.state.Parts...)
	runtime.result.PartCompleted = len(runtime.state.Parts)
	runtime.state.SafeState = "snapshot_committed"
	runtime.state.UpdatedAt = timestamp(runtime.options.now().UTC())
	if err := runtime.store.save(runtime.stateID, runtime.state); err != nil {
		return err
	}
	runtime.progress("commit", 1, 1)
	return nil
}

func (runtime *uploadRuntime) waitForContinuation(ctx context.Context) error {
	for {
		status, err := runtime.continuationStatus(ctx)
		if err != nil {
			return err
		}
		done, err := runtime.applyContinuationStatus(status)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if err := runtime.options.sleep(ctx, runtime.options.PollInterval); err != nil {
			return uploadError(UploadFailureRetryable, "wait_timeout")
		}
	}
}

func (runtime *uploadRuntime) recoverContinuation(ctx context.Context) error {
	status, err := runtime.continuationStatus(ctx)
	if err != nil {
		return err
	}
	done, err := runtime.applyContinuationStatus(status)
	if err != nil {
		return err
	}
	if !done {
		runtime.result.State = "snapshot_committed"
		runtime.result.ReasonCode = ""
	}
	return nil
}

func (runtime *uploadRuntime) continuationStatus(ctx context.Context) (*continuationStatusResponse, error) {
	requestID, err := newUUID()
	if err != nil {
		return nil, uploadError(UploadFailureInternal, "request_identity_failed")
	}
	payload := struct {
		SchemaVersion     string  `json:"schema_version"`
		MessageType       string  `json:"message_type"`
		RequestID         string  `json:"request_id"`
		GroupID           string  `json:"group_id"`
		StagingJobID      *string `json:"staging_job_id"`
		ContinuationJobID *string `json:"continuation_job_id"`
	}{SchemaVersion: "baseline-snapshot-continuation.v1", MessageType: "continuation_job_status_request", RequestID: requestID, GroupID: runtime.state.GroupID}
	if runtime.state.ContinuationJobID == "" {
		payload.StagingJobID = &runtime.state.StagingJobID
	} else {
		payload.ContinuationJobID = &runtime.state.ContinuationJobID
	}
	body, err := canonicalJSONBytes(payload)
	if err != nil || len(body) > MaxControlRequest {
		return nil, uploadError(UploadFailureInternal, "status_request_failed")
	}
	response, err := runtime.postWithRetry(ctx, "/baseline/control/v1/continuations/status", body)
	clearBytes(body)
	if err != nil {
		return nil, err
	}
	var status continuationStatusResponse
	if err := decodeStrictResponseJSON(response.Body, &status); err != nil {
		return nil, uploadError(UploadFailureContract, "continuation_response_mismatch")
	}
	if status.SchemaVersion != "baseline-snapshot-continuation.v1" || status.MessageType != "continuation_job_status" || status.RequestID != requestID || status.GroupID != runtime.state.GroupID || status.StagingJobID != runtime.state.StagingJobID || !validUUID(status.JobID) || status.Operation != "sealed_snapshot_continue" {
		return nil, uploadError(UploadFailureContract, "continuation_response_mismatch")
	}
	if status.Result.SnapshotID != runtime.state.SnapshotID || status.Result.StagingState != "sealed" || status.Staging.State != "sealed" || status.Staging.ReceivedParts != len(runtime.state.Parts) || status.Staging.ExpectedParts != len(runtime.state.Parts) || status.Staging.CorpusEligible || status.Staging.IndexEligible {
		return nil, uploadError(UploadFailureContract, "continuation_response_mismatch")
	}
	succeeded := status.State == "succeeded"
	wantCompleted := 0
	if succeeded {
		wantCompleted = 1
	}
	if status.Progress.Total != 1 || status.Progress.Completed != wantCompleted || status.Continuation.JobID != status.JobID || status.Continuation.Operation != "sealed_snapshot_continue" || status.Continuation.State != status.State || status.Continuation.CorpusIngestionComplete != succeeded || status.Continuation.CorpusEligible != succeeded || status.Continuation.IndexEligible || status.Continuation.BaselineEligible {
		return nil, uploadError(UploadFailureContract, "continuation_response_mismatch")
	}
	if runtime.state.ContinuationJobID != "" && runtime.state.ContinuationJobID != status.JobID {
		return nil, uploadError(UploadFailureContract, "continuation_identity_mismatch")
	}
	if status.ErrorCode != nil && !validSafeReasonCode(*status.ErrorCode) {
		return nil, uploadError(UploadFailureContract, "continuation_response_mismatch")
	}
	if status.Continuation.ErrorCode != nil && !validSafeReasonCode(*status.Continuation.ErrorCode) {
		return nil, uploadError(UploadFailureContract, "continuation_response_mismatch")
	}
	if (status.ErrorCode == nil) != (status.Continuation.ErrorCode == nil) || (status.ErrorCode != nil && *status.ErrorCode != *status.Continuation.ErrorCode) {
		return nil, uploadError(UploadFailureContract, "continuation_response_mismatch")
	}
	return &status, nil
}

func (runtime *uploadRuntime) applyContinuationStatus(status *continuationStatusResponse) (bool, error) {
	runtime.state.ContinuationJobID = status.JobID
	runtime.result.ContinuationJobID = status.JobID
	runtime.state.SafeState = status.State
	runtime.state.UpdatedAt = timestamp(runtime.options.now().UTC())
	if err := runtime.store.save(runtime.stateID, runtime.state); err != nil {
		return false, err
	}
	runtime.progress("continuation", status.Progress.Completed, status.Progress.Total)
	switch status.State {
	case "succeeded":
		if !status.Result.CorpusIngestionComplete || !status.Result.CorpusEligible || status.Result.IndexEligible || status.Result.BaselineEligible || !validUUID(status.Result.CorpusID) || !validUUID(status.Result.CorpusGenerationID) || !validSafeIdentity(status.Result.CorpusGenerationVersion) {
			return false, uploadError(UploadFailureContract, "continuation_result_mismatch")
		}
		runtime.result.State = "succeeded"
		runtime.result.CorpusID = status.Result.CorpusID
		runtime.result.CorpusGenerationID = status.Result.CorpusGenerationID
		runtime.result.CorpusGenerationVersion = status.Result.CorpusGenerationVersion
		return true, nil
	case "queued", "running", "retryable_failed":
		return false, nil
	case "terminal_failed", "blocked", "cancelled":
		return false, uploadError(UploadFailureTerminal, safeContinuationReason(status.State, status.ErrorCode))
	default:
		return false, uploadError(UploadFailureContract, "continuation_state_incompatible")
	}
}

func validateJobStatus(body []byte, requestID, groupID, jobID string) (*jobStatusResponse, error) {
	var status jobStatusResponse
	if err := decodeStrictResponseJSON(body, &status); err != nil || status.ProtocolVersion != ControlProtocolVersion || status.ProtocolSHA256 != ControlProtocolSHA256 || status.MessageType != "job_status" || status.RequestID != requestID || status.GroupID != groupID || status.JobID != jobID {
		return nil, uploadError(UploadFailureContract, "job_status_response_mismatch")
	}
	return &status, nil
}

func (runtime *uploadRuntime) postWithRetry(ctx context.Context, endpoint string, body []byte) (controlResponse, error) {
	var last error
	for attempt := 0; attempt < runtime.options.RetryAttempts; attempt++ {
		response, err := runtime.client.post(ctx, endpoint, body)
		runtime.counter.bytes += response.BodyBytes
		runtime.counter.count++
		if err == nil {
			return response, nil
		}
		last = classifyControlError(err)
		if UploadFailure(last) != UploadFailureRetryable || attempt+1 >= runtime.options.RetryAttempts {
			break
		}
		delay := runtime.options.RetryBaseDelay << attempt
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		if err := runtime.options.sleep(ctx, jitter(delay)); err != nil {
			last = uploadError(UploadFailureRetryable, "wait_timeout")
			break
		}
	}
	if last == nil {
		last = uploadError(UploadFailureRetryable, "retry_exhausted")
	}
	return controlResponse{}, last
}

func classifyControlError(err error) error {
	if err == nil {
		return nil
	}
	var upload *UploadError
	if errors.As(err, &upload) {
		return upload
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return uploadError(UploadFailureRetryable, "wait_timeout")
	}
	var control *ControlHTTPError
	if errors.As(err, &control) {
		if control.StatusCode == http.StatusUnauthorized || control.StatusCode == http.StatusForbidden || control.Code == "not_found_or_forbidden" || control.Code == "repository_not_authorized" || control.Code == "source_not_authorized" {
			return uploadError(UploadFailureAuthentication, control.Code)
		}
		if control.Code == "staging_expired" || control.Code == "staging_not_open" {
			return uploadError(UploadFailureTerminal, control.Code)
		}
		if control.Retryable || control.StatusCode == http.StatusRequestTimeout || control.StatusCode == http.StatusTooManyRequests || control.StatusCode >= 500 {
			return uploadError(UploadFailureRetryable, control.Code)
		}
		return uploadError(UploadFailureContract, control.Code)
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, io.ErrUnexpectedEOF) {
		return uploadError(UploadFailureRetryable, "transport_retry_exhausted")
	}
	return uploadError(UploadFailureRetryable, "transport_retry_exhausted")
}

func (runtime *uploadRuntime) failureExecution(err error, cleanup bool) (*UploadExecution, error) {
	runtime.result.State = failureState(err)
	runtime.result.ReasonCode = SafeUploadReason(err)
	runtime.refreshResult()
	return &UploadExecution{Result: *runtime.result, store: runtime.store, stateID: runtime.stateID, cleanupState: cleanup}, err
}

func (runtime *uploadRuntime) refreshResult() {
	runtime.result.TransmittedRequestBytes = runtime.counter.bytes
	runtime.result.TransmittedRequestCount = runtime.counter.count
	runtime.result.Replayed = runtime.replayed
	runtime.result.UpdatedAt = timestamp(runtime.options.now().UTC())
	if runtime.state != nil {
		runtime.result.StagingJobID = runtime.state.StagingJobID
		runtime.result.ContinuationJobID = runtime.state.ContinuationJobID
		runtime.result.PartCompleted = len(runtime.state.CompletedParts)
	}
}

func failureState(err error) string {
	switch UploadFailure(err) {
	case UploadFailureRetryable:
		return "retryable_incomplete"
	case UploadFailureTerminal:
		return "terminal_failed"
	default:
		return "failed"
	}
}

func shouldDiscardState(err error) bool {
	return UploadFailure(err) == UploadFailureTerminal || (UploadFailure(err) == UploadFailureContract && isUnusableConflict(SafeUploadReason(err)))
}

func isUnusableConflict(reason string) bool {
	switch reason {
	case "idempotency_conflict", "part_conflict", "commit_conflict", "content_hash_mismatch", "incomplete_staging", "staging_expired", "staging_not_open", "stale_snapshot":
		return true
	default:
		return false
	}
}

func scannerReason(err error) string {
	message := SafeScannerDiagnostic(err)
	switch message {
	case finalRevisionDriftReason, "changed repository revisions do not match the immutable plan", "sibling repository revision does not match the immutable plan":
		return "immutable_revision_mismatch"
	default:
		if ScanFailure(err) == ScanFailureRepository {
			return "immutable_repository_unavailable"
		}
		if ScanFailure(err) == ScanFailureContract {
			return "scan_contract_failed"
		}
		return "scan_internal_failure"
	}
}

func safeContinuationReason(state string, code *string) string {
	if code != nil && *code != "" {
		return *code
	}
	return "continuation_" + state
}

func safeSnapshotReason(state string, code *string) string {
	if code != nil && *code != "" {
		return *code
	}
	return "snapshot_" + state
}

func validSafeReasonCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validSafeIdentity(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && repositoryIDPattern.MatchString(value)
}

func (runtime *uploadRuntime) progress(stage string, completed, total int) {
	if runtime.options.Progress != nil {
		runtime.options.Progress(stage, completed, total)
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func jitter(duration time.Duration) time.Duration {
	if duration <= 1 {
		return duration
	}
	value := make([]byte, 1)
	if _, err := rand.Read(value); err != nil {
		return duration
	}
	// 75%..124% bounded jitter.
	return duration * time.Duration(75+int(value[0])%50) / 100
}

func newUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	hexValue := fmt.Sprintf("%x", value)
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32], nil
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func EncodeUploadResult(writer io.Writer, result UploadResult) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}

// StableUploadJSONFields is used by contract tests to pin output order without
// exposing protected request material.
func StableUploadJSONFields() []string {
	fields := []string{"schema_version", "protocol_version", "protocol_sha256", "group_id", "scan_fingerprint", "snapshot_id", "staging_job_id", "continuation_job_id", "canonical_manifest_hash", "content_manifest_hash", "part_total", "part_completed", "transmitted_request_bytes", "transmitted_request_count", "state", "corpus_id", "corpus_generation_id", "corpus_generation_version", "resumed", "replayed", "started_at", "updated_at", "reason_code"}
	return append([]string(nil), fields...)
}

func sortedParts(parts []uploadStatePart) []uploadStatePart {
	result := append([]uploadStatePart(nil), parts...)
	sort.Slice(result, func(left, right int) bool { return result[left].Ordinal < result[right].Ordinal })
	return result
}

func containsSensitiveUploadField(value string) bool {
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"content_utf8", "raw_diff", "local_path", "remote_url", "idempotency_key", "lease_token", "authorization", "auth_token"} {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}
