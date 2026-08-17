package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	baselinePreviewSchemaVersion    = "baseline-preview.v1"
	baselinePreviewPath             = "/baseline/preview/v1"
	baselinePreviewMaxResponseBytes = 1 << 20
)

type BaselinePreviewRequest struct {
	SchemaVersion string `json:"schema_version"`
	RequestID     string `json:"request_id"`
	GroupID       string `json:"group_id"`
	JobID         string `json:"job_id,omitempty"`
	DigestID      string `json:"digest_id,omitempty"`
}

type BaselinePreviewFeedback struct {
	Ordinal    int    `json:"ordinal"`
	FeedbackID string `json:"feedback_id"`
	Feedback   string `json:"feedback"`
}

type BaselinePreviewControlJob struct {
	JobID                   string `json:"job_id"`
	State                   string `json:"state"`
	CompletedAt             string `json:"completed_at"`
	GenerationInvoked       bool   `json:"generation_invoked"`
	FeedbackCount           int    `json:"feedback_count"`
	NotificationOutboxCount int    `json:"notification_outbox_count"`
}

type BaselinePreviewRetrieval struct {
	PersistedRunID string `json:"persisted_run_id"`
	Status         string `json:"status"`
	EvidenceCount  int    `json:"evidence_count"`
	ReferenceCount int    `json:"reference_count"`
}

type BaselinePreviewSource struct {
	GroupID     string  `json:"group_id"`
	DocumentID  string  `json:"document_id"`
	SourceScope string  `json:"source_scope"`
	ChunkID     *string `json:"chunk_id"`
}

type BaselinePreviewDigest struct {
	DigestID              string `json:"digest_id"`
	State                 string `json:"state"`
	Channel               string `json:"channel"`
	FindingCount          int    `json:"finding_count"`
	FindingManifestSHA256 string `json:"finding_manifest_sha256"`
}

type BaselinePreviewProvenance struct {
	Retrieval struct {
		Engine              string `json:"engine"`
		Version             string `json:"version"`
		ResultSchemaVersion string `json:"result_schema_version"`
	} `json:"retrieval"`
	Query struct {
		SHA256 string `json:"sha256"`
		Length int    `json:"length"`
		Origin string `json:"origin"`
	} `json:"query"`
	Corpus struct {
		GenerationID      string `json:"generation_id"`
		GenerationVersion string `json:"generation_version"`
		ManifestSHA256    string `json:"manifest_sha256"`
	} `json:"corpus"`
	Index struct {
		PublicationID          string `json:"publication_id"`
		PublicationFingerprint string `json:"publication_fingerprint"`
		IndexID                string `json:"index_id"`
		Version                string `json:"version"`
		SchemaVersion          string `json:"schema_version"`
		Fingerprint            string `json:"fingerprint"`
		ConfigFingerprint      string `json:"config_fingerprint"`
	} `json:"index"`
	Embedding struct {
		Provider    string `json:"provider"`
		Model       string `json:"model"`
		Revision    string `json:"revision"`
		Dimension   int    `json:"dimension"`
		Fingerprint string `json:"fingerprint"`
	} `json:"embedding"`
	Generation struct {
		Provider          string `json:"provider"`
		Model             string `json:"model"`
		Version           string `json:"version"`
		InputFingerprint  string `json:"input_fingerprint"`
		OutputFingerprint string `json:"output_fingerprint"`
	} `json:"generation"`
}

type BaselinePreviewResponse struct {
	SchemaVersion string                    `json:"schema_version"`
	RequestID     string                    `json:"request_id"`
	ControlJob    BaselinePreviewControlJob `json:"control_job"`
	Retrieval     BaselinePreviewRetrieval  `json:"retrieval"`
	Source        BaselinePreviewSource     `json:"source"`
	Feedback      []BaselinePreviewFeedback `json:"feedback"`
	Digest        *BaselinePreviewDigest    `json:"digest"`
	Provenance    BaselinePreviewProvenance `json:"provenance"`
}

type BaselinePreviewHTTPError struct {
	StatusCode int
	method     string
	path       string
}

func (e *BaselinePreviewHTTPError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d", e.method, e.path, e.StatusCode)
}

func newBaselinePreviewRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("create baseline preview request identity")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}

func (c *Client) PostBaselinePreview(
	groupID, jobID, digestID string,
) (BaselinePreviewResponse, error) {
	if strings.TrimSpace(groupID) == "" || (jobID == "") == (digestID == "") {
		return BaselinePreviewResponse{}, errors.New("invalid baseline preview request")
	}
	requestID, err := newBaselinePreviewRequestID()
	if err != nil {
		return BaselinePreviewResponse{}, err
	}
	request := BaselinePreviewRequest{
		SchemaVersion: baselinePreviewSchemaVersion,
		RequestID:     requestID,
		GroupID:       groupID,
		JobID:         jobID,
		DigestID:      digestID,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return BaselinePreviewResponse{}, errors.New("encode baseline preview request")
	}
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		c.BaseURL+baselinePreviewPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return BaselinePreviewResponse{}, errors.New("create baseline preview request")
	}
	req.Header.Set("Content-Type", "application/json")
	applyDefaultHeaders(req)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		logHTTP(http.MethodPost, baselinePreviewPath, 0, time.Since(start), "", err)
		return BaselinePreviewResponse{}, err
	}
	defer resp.Body.Close()
	logHTTP(
		http.MethodPost,
		baselinePreviewPath,
		resp.StatusCode,
		time.Since(start),
		requestIDFromHeaders(resp.Header),
		nil,
	)
	if resp.StatusCode >= http.StatusMultipleChoices {
		return BaselinePreviewResponse{}, &BaselinePreviewHTTPError{
			StatusCode: resp.StatusCode,
			method:     http.MethodPost,
			path:       baselinePreviewPath,
		}
	}
	encoded, err := io.ReadAll(io.LimitReader(resp.Body, baselinePreviewMaxResponseBytes+1))
	if err != nil || len(encoded) > baselinePreviewMaxResponseBytes {
		return BaselinePreviewResponse{}, errors.New("invalid baseline preview response")
	}
	var out BaselinePreviewResponse
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return BaselinePreviewResponse{}, errors.New("invalid baseline preview response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BaselinePreviewResponse{}, errors.New("invalid baseline preview response")
	}
	if err := validateBaselinePreview(out, request); err != nil {
		return BaselinePreviewResponse{}, err
	}
	return out, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func validateBaselinePreview(
	preview BaselinePreviewResponse,
	request BaselinePreviewRequest,
) error {
	invalid := preview.SchemaVersion != baselinePreviewSchemaVersion ||
		preview.RequestID != request.RequestID ||
		preview.ControlJob.JobID == "" ||
		preview.ControlJob.State != "feedback_persisted" ||
		preview.ControlJob.CompletedAt == "" ||
		!preview.ControlJob.GenerationInvoked ||
		preview.ControlJob.FeedbackCount < 0 || preview.ControlJob.FeedbackCount > 4 ||
		preview.Retrieval.PersistedRunID == "" || preview.Retrieval.Status != "ok" ||
		preview.Retrieval.EvidenceCount < 1 || preview.Retrieval.EvidenceCount > 4 ||
		preview.Retrieval.ReferenceCount != preview.Retrieval.EvidenceCount ||
		preview.Source.GroupID != request.GroupID || preview.Source.DocumentID == "" ||
		len(preview.Feedback) != preview.ControlJob.FeedbackCount
	if invalid {
		return errors.New("invalid baseline preview response")
	}
	if _, err := time.Parse(time.RFC3339, preview.ControlJob.CompletedAt); err != nil {
		return errors.New("invalid baseline preview response")
	}
	if preview.Source.SourceScope == "control_document" {
		if preview.Source.ChunkID != nil {
			return errors.New("invalid baseline preview response")
		}
	} else if preview.Source.SourceScope != "legacy_chunk" ||
		preview.Source.ChunkID == nil || strings.TrimSpace(*preview.Source.ChunkID) == "" {
		return errors.New("invalid baseline preview response")
	}
	seen := make(map[string]struct{}, len(preview.Feedback))
	for index, finding := range preview.Feedback {
		_, duplicate := seen[finding.FeedbackID]
		if finding.Ordinal != index+1 || finding.FeedbackID == "" || duplicate ||
			strings.TrimSpace(finding.Feedback) == "" ||
			strings.EqualFold(strings.TrimSpace(finding.Feedback), "NONE") {
			return errors.New("invalid baseline preview response")
		}
		seen[finding.FeedbackID] = struct{}{}
	}
	if len(preview.Feedback) == 0 {
		if preview.Digest != nil || preview.ControlJob.NotificationOutboxCount != 0 {
			return errors.New("invalid baseline preview response")
		}
	} else if preview.Digest == nil ||
		preview.ControlJob.NotificationOutboxCount != 1 ||
		preview.Digest.Channel != "in_app" || preview.Digest.State == "" ||
		preview.Digest.FindingCount != len(preview.Feedback) ||
		!validSHA256(preview.Digest.FindingManifestSHA256) {
		return errors.New("invalid baseline preview response")
	}
	provenance := preview.Provenance
	if provenance.Retrieval.Engine != "baseline_v1" ||
		provenance.Retrieval.Version == "" || provenance.Retrieval.ResultSchemaVersion == "" ||
		provenance.Query.Origin != "explicit" || provenance.Query.Length < 1 ||
		!validSHA256(provenance.Query.SHA256) ||
		provenance.Corpus.GenerationID == "" || provenance.Corpus.GenerationVersion == "" ||
		!validSHA256(provenance.Corpus.ManifestSHA256) ||
		provenance.Index.PublicationID == "" ||
		provenance.Index.PublicationID != provenance.Index.IndexID ||
		provenance.Index.Version == "" || provenance.Index.SchemaVersion == "" ||
		!validSHA256(provenance.Index.PublicationFingerprint) ||
		!validSHA256(provenance.Index.Fingerprint) ||
		!validSHA256(provenance.Index.ConfigFingerprint) ||
		provenance.Embedding.Provider == "" || provenance.Embedding.Model == "" ||
		provenance.Embedding.Revision == "" || provenance.Embedding.Dimension < 1 ||
		!validSHA256(provenance.Embedding.Fingerprint) ||
		provenance.Generation.Provider == "" || provenance.Generation.Model == "" ||
		provenance.Generation.Version == "" ||
		!validSHA256(provenance.Generation.InputFingerprint) ||
		!validSHA256(provenance.Generation.OutputFingerprint) {
		return errors.New("invalid baseline preview response")
	}
	return nil
}
