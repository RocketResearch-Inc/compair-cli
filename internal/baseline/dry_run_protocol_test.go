package baseline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRunV1FrozenArtifactsAndSemanticValidation(t *testing.T) {
	var reports []DryRunReport
	decoder := json.NewDecoder(bytes.NewReader(readProtocolFile(t, "fixtures", "baseline-scan-dry-run.v1.valid.json")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reports); err != nil || len(reports) != 1 {
		t.Fatalf("valid fixture decode = %v, reports = %d", err, len(reports))
	}
	if err := ValidateDryRunReportContract(reports[0]); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(reports[0])
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, field := range []string{"schema_version", "protocol_version", "protocol_sha256", "group_id", "changed_repository", "sibling_repositories", "snapshot_id", "canonical_manifest_hash", "scan_plan_jcs_sha256", "content_manifest_hash", "counts", "skip_reason_counts", "raw_diff", "parts", "manifest_request_bytes", "commit_request_bytes", "maximum_planned_upload_bytes", "scan_fingerprint", "warnings", "errors"} {
		position := bytes.Index(encoded, []byte(`"`+field+`"`))
		if position <= previous {
			t.Fatalf("field %q is absent or out of frozen order", field)
		}
		previous = position
	}
	if bytes.Contains(encoded, []byte("total_upload_bytes")) {
		t.Fatal("obsolete total_upload_bytes is still serialized")
	}

	mutations := []func(*DryRunReport){
		func(value *DryRunReport) { value.MaximumPlannedUploadBytes-- },
		func(value *DryRunReport) { value.Parts[0].Ordinal = 2 },
		func(value *DryRunReport) { value.Counts.SupportedFileCount++ },
		func(value *DryRunReport) { value.Warnings[0] = "uploaded" },
		func(value *DryRunReport) { value.Errors = []string{"forbidden"} },
	}
	for index, mutate := range mutations {
		value := reports[0]
		value.Parts = append([]DryRunPart(nil), reports[0].Parts...)
		value.Warnings = append([]string(nil), reports[0].Warnings...)
		mutate(&value)
		if err := ValidateDryRunReportContract(value); err == nil {
			t.Fatalf("semantic mutation %d unexpectedly passed", index)
		}
	}

	invalid := string(readProtocolFile(t, "fixtures", "baseline-scan-dry-run.v1.invalid.json"))
	for _, required := range []string{"legacy_total_upload_bytes", "inconsistent_upload_total", "part_ordinal_gap", "inconsistent_supported_count", "raw_content_forbidden", "local_path_forbidden", "diagnostics_changed", "errors_not_empty"} {
		if !strings.Contains(invalid, `"case": "`+required+`"`) {
			t.Fatalf("missing invalid fixture %s", required)
		}
	}
}

func TestDryRunV1ArtifactHashesArePinned(t *testing.T) {
	// Filled from the exact frozen bytes and reported in the phase handoff.
	expected := map[string]string{
		"baseline-scan-dry-run.v1.md":                    "080633b7af37a7dfed4998527a1e7d1877bee364385e55c9027a53cd81e66ca4",
		"baseline-scan-dry-run.v1.schema.json":           "9dc19feca68ee5aa655a397b7001c1d675592d6f146049c7469ebe6befe636fd",
		"fixtures/baseline-scan-dry-run.v1.valid.json":   "35ef126001808d4b6e9ebb1072dd6e9b12772775bb35f867441876221b7719f4",
		"fixtures/baseline-scan-dry-run.v1.invalid.json": "cf1e52d90d552f0b91d737ea38556ab439962733166476c31600888d497ce683",
	}
	for name, want := range expected {
		value, err := os.ReadFile(filepath.Join("..", "..", "protocol", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(value)
		got := hex.EncodeToString(digest[:])
		if got != want {
			t.Fatalf("%s hash = %s, want %s", name, got, want)
		}
	}
}
