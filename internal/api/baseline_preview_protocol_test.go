package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

var frozenPreviewArtifacts = map[string]string{
	"baseline-preview.v1.md":                    "3716537f88a7a9db21f83fcd032c0522823f28c13396711ed898f1d6f7756baf",
	"baseline-preview.v1.schema.json":           "eda7f9c71a17832340c846115024fecd3401bfbd602475d72aa347bd9b8cc45b",
	"fixtures/baseline-preview.v1.valid.json":   "827f18cdfca62ee56a76c5bc2229c9b7e475276beb372f1e9cd3b6dd0123c3d9",
	"fixtures/baseline-preview.v1.invalid.json": "a2308d43ec4b2afe1e517ed54dd6f1f1af6bca998a879c56612e28042c800035",
}

func TestFrozenBaselinePreviewArtifacts(t *testing.T) {
	for relative, expected := range frozenPreviewArtifacts {
		encoded, err := os.ReadFile(filepath.Join("..", "..", "protocol", relative))
		if err != nil {
			t.Fatal(err)
		}
		actual := sha256.Sum256(encoded)
		if hex.EncodeToString(actual[:]) != expected {
			t.Fatalf("%s hash changed: %x", relative, actual)
		}
	}
}

func TestFrozenBaselinePreviewValidFixtureScopesAndOutcomes(t *testing.T) {
	encoded, err := os.ReadFile(
		filepath.Join("..", "..", "protocol", "fixtures", "baseline-preview.v1.valid.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Requests  []json.RawMessage `json:"requests"`
		Responses []struct {
			CaseID string                  `json:"case_id"`
			Value  BaselinePreviewResponse `json:"value"`
		} `json:"responses"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Responses) != 3 {
		t.Fatalf("response fixtures = %d", len(fixture.Responses))
	}
	seen := map[string]bool{}
	for _, item := range fixture.Responses {
		seen[item.CaseID] = true
		value := item.Value
		request := BaselinePreviewRequest{
			SchemaVersion: baselinePreviewSchemaVersion,
			RequestID:     value.RequestID,
			GroupID:       value.Source.GroupID,
			JobID:         value.ControlJob.JobID,
		}
		if err := validateBaselinePreview(value, request); err != nil {
			t.Fatalf("%s: %v", item.CaseID, err)
		}
	}
	for _, required := range []string{
		"zero_findings_control_document",
		"positive_findings_control_document",
		"positive_findings_legacy_chunk",
	} {
		if !seen[required] {
			t.Fatalf("missing fixture %s", required)
		}
	}
}
