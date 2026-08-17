package baseline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
)

var runResultArtifactHashes = map[string]string{
	"baseline-run-result.v1.md":                    "f1a9456cf1c9ed20f706a85e47e9ae03fe4f9b776cab0c12b680b05578fae5b9",
	"baseline-run-result.v1.schema.json":           "d1681bb22b63e0e6c56499bf7a24131e4bf8e5f1babacc91f2c165fb094c3b96",
	"fixtures/baseline-run-result.v1.valid.json":   "3148f4fae5ac197288c3d8b868eb64cabe67ec6dc0d4756e8766272ae385bf29",
	"fixtures/baseline-run-result.v1.invalid.json": "785a3eb6b00d9cfe7564ea29e0d00b444b82af1a3a23badbb69ec8852d53deaa",
}

func TestRunResultArtifactsAreFrozenAndMatchCorePins(t *testing.T) {
	for relative, expected := range runResultArtifactHashes {
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

func TestRunResultValidFixturesEnforceDocumentLevelEffects(t *testing.T) {
	var fixtures struct {
		Results []RunResult `json:"results"`
	}
	if err := json.Unmarshal(readProtocolFile(t, "fixtures", "baseline-run-result.v1.valid.json"), &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Results) != 4 {
		t.Fatalf("valid result count = %d", len(fixtures.Results))
	}
	seen := map[string]bool{}
	for _, result := range fixtures.Results {
		if err := validateRunResult(result); err != nil {
			t.Fatalf("state %s: %v", result.State, err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if RunResultForbiddenFields(encoded) {
			t.Fatalf("state %s contains a protected field", result.State)
		}
		seen[result.State] = true
		if result.EvidenceCount != result.ReferenceCount || result.EvidenceCount > 4 {
			t.Fatalf("state %s has non-job-wide evidence effects", result.State)
		}
		if result.PersistedRetrievalRunID != nil && !validUUID(*result.PersistedRetrievalRunID) {
			t.Fatalf("state %s has invalid single retrieval run identity", result.State)
		}
	}
	for _, state := range []string{"feedback_persisted", "references_persisted", "insufficient"} {
		if !seen[state] {
			t.Fatalf("missing %s fixture", state)
		}
	}
	zero := fixtures.Results[1]
	if zero.State != "feedback_persisted" || zero.FeedbackCount != 0 || !zero.GenerationInvoked || zero.NotificationOutboxCount != 0 || zero.EvidenceCount < 1 || zero.PersistedRetrievalRunID == nil {
		t.Fatalf("zero-finding success is contradictory: %#v", zero)
	}
	insufficient := fixtures.Results[3]
	if insufficient.PersistedRetrievalRunID != nil || insufficient.EvidenceCount != 0 || insufficient.ReferenceCount != 0 || insufficient.FeedbackCount != 0 || insufficient.GenerationInvoked {
		t.Fatalf("insufficient result has durable effects: %#v", insufficient)
	}
}

func TestRunResultInvalidFixturesCoverFrozenSafetyCases(t *testing.T) {
	var fixtures struct {
		Cases []struct {
			CaseID string `json:"case_id"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(readProtocolFile(t, "fixtures", "baseline-run-result.v1.invalid.json"), &fixtures); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range fixtures.Cases {
		seen[item.CaseID] = true
	}
	for _, required := range []string{
		"obsolete_protocol_hash", "raw_query_forbidden", "feedback_text_forbidden",
		"zero_finding_outbox", "zero_finding_generation_not_invoked",
		"references_are_per_chunk", "insufficient_has_reference",
		"too_many_job_wide_references", "mismatched_reference_evidence_counts",
	} {
		if !seen[required] {
			t.Fatalf("missing invalid fixture %s", required)
		}
	}

	var valid struct {
		Results []RunResult `json:"results"`
	}
	if err := json.Unmarshal(readProtocolFile(t, "fixtures", "baseline-run-result.v1.valid.json"), &valid); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*RunResult){
		func(value *RunResult) { value.ProtocolSHA256 = v2ObsoleteSpecSHA256 },
		func(value *RunResult) { value.NotificationOutboxCount = 1 },
		func(value *RunResult) { value.GenerationInvoked = false },
		func(value *RunResult) { value.ReferenceCount = 5 },
		func(value *RunResult) { value.ReferenceCount = 1 },
	}
	bases := []int{0, 1, 1, 2, 0}
	for index, mutate := range mutations {
		value := valid.Results[bases[index]]
		mutate(&value)
		if err := validateRunResult(value); err == nil {
			t.Fatalf("semantic mutation %d passed", index)
		}
	}
	for _, protected := range [][]byte{
		[]byte(`{"retrieval_query":"secret"}`),
		[]byte(`{"feedback_text":"secret"}`),
		[]byte(`{"child_runs":[],"raw_diff":"secret"}`),
	} {
		if !RunResultForbiddenFields(protected) {
			t.Fatalf("protected result field was accepted: %s", protected)
		}
	}
}
