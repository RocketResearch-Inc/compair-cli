package baseline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
)

var indexResultArtifactHashes = map[string]string{
	"baseline-index-result.v1.md":                    "3686c6533a149a588613bb9ff53c8a8a9ffd5b035affc491466cb1f5d337857a",
	"baseline-index-result.v1.schema.json":           "49a67fc7a79f31136b51858a3ad75ae662b89bb4b66d0cf7330be9aa4f051cbe",
	"fixtures/baseline-index-result.v1.valid.json":   "2c5696c122880069122f0ae43904b88f5c43a013542b7d3f8c49d3e860789034",
	"fixtures/baseline-index-result.v1.invalid.json": "627df61845ad318f27b7a32028bc2ca27dfb87cff30e4fc72b3aaf67e4f0dc9c",
}

func TestIndexResultArtifactsAreFrozenAndMatchCorePins(t *testing.T) {
	for relative, expected := range indexResultArtifactHashes {
		parts := []string{relative}
		if filepath.Dir(relative) != "." {
			parts = []string{filepath.Dir(relative), filepath.Base(relative)}
		}
		cli := readProtocolFile(t, parts...)
		digest := sha256.Sum256(cli)
		if got := hex.EncodeToString(digest[:]); got != expected {
			t.Fatalf("%s SHA-256 = %s", relative, got)
		}
	}
}

func validateIndexResultFixture(value map[string]any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var result IndexResult
	if err := decodeStrictResponseJSON(encoded, &result); err != nil {
		return err
	}
	if result.SchemaVersion != IndexResultSchemaVersion || result.ProtocolVersion != IndexControlProtocolVersion || result.ProtocolSHA256 != IndexControlProtocolSHA256 || result.GroupID == "" || !validTimestamp(result.UpdatedAt) {
		return indexError(IndexFailureContract, "result_contract_mismatch")
	}
	if IndexResultForbiddenFields(encoded) {
		return indexError(IndexFailureContract, "result_contains_forbidden_field")
	}
	switch result.State {
	case "succeeded":
		if result.ExitClassification != "success" || result.CompatiblePublicationID == nil || result.IndexFingerprint == nil || result.IndexIntentFingerprint == nil || result.ReasonCode != nil || result.IndexedDocumentCount == nil || result.VectorCount == nil || *result.IndexedDocumentCount != *result.VectorCount {
			return indexError(IndexFailureContract, "result_success_inconsistent")
		}
	case "queued", "running", "retryable_failed":
		if result.ExitClassification != "pending" || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil {
			return indexError(IndexFailureContract, "result_pending_inconsistent")
		}
	case "retryable_incomplete":
		if result.ExitClassification != "retryable" || result.ReasonCode == nil || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil {
			return indexError(IndexFailureContract, "result_retryable_inconsistent")
		}
	case "terminal_failed", "blocked", "failed":
		if result.ExitClassification != "failed" || result.ReasonCode == nil || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil {
			return indexError(IndexFailureContract, "result_failure_inconsistent")
		}
	case "cancelled":
		if result.ExitClassification != "cancelled" || result.ReasonCode == nil || result.CompatiblePublicationID != nil || result.IndexFingerprint != nil {
			return indexError(IndexFailureContract, "result_cancelled_inconsistent")
		}
	default:
		return indexError(IndexFailureContract, "result_state_invalid")
	}
	return nil
}

func TestIndexResultValidAndInvalidFixtures(t *testing.T) {
	var valid struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(readProtocolFile(t, "fixtures", "baseline-index-result.v1.valid.json"), &valid); err != nil {
		t.Fatal(err)
	}
	for _, result := range valid.Results {
		if err := validateIndexResultFixture(result); err != nil {
			t.Fatal(err)
		}
	}
	var invalid struct {
		Cases []struct {
			CaseID string         `json:"case_id"`
			Value  map[string]any `json:"value"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(readProtocolFile(t, "fixtures", "baseline-index-result.v1.invalid.json"), &invalid); err != nil {
		t.Fatal(err)
	}
	for _, test := range invalid.Cases {
		if err := validateIndexResultFixture(test.Value); err == nil {
			t.Fatalf("invalid fixture passed: %s", test.CaseID)
		}
	}
}
