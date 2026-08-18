package baseline

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	v2ProtocolVersion                    = "baseline-control-plane.v2"
	v2SpecSHA256                         = "b278abe007779f05e92509db068f555701c03cba5cf236151e8df231a9b44091"
	v2ObsoleteSpecSHA256                 = "c9486b3deb1a494781513109df17d8e8df1281fbc9687960ace711485b50d174"
	v2SchemaSHA256                       = "10170faf5cecab1861a0e3c831080cbe1073f437b4c668b55c39dd3be9ca631a"
	v2ValidFixtureSHA256                 = "d06ea3ab7194c2ef58eea9af555835ed0f1d29eb8a431fb8d5c68976d2b76003"
	v2InvalidFixtureSHA256               = "64f06b80f17cc4804f72f8bfd599139dc1ab7e681c9f8d37c244f55612894e3a"
	v2RawQuerySHA256                     = "bd12e261a9563f3c8a6504080028a8b4ba2e2f42a36290447f4589d6172adbc6"
	v2MaximumRawQueryBytes               = 8_000_000
	v2MaximumRunRequestByte              = 8_100_000
	v2MaximumEvidenceChars               = 16_000
	generationOutputSpecSHA256           = "e670731777b253f9d5e3984405c2d99871ba26f637a17e6221cc82d97bc8beb1"
	generationOutputSchemaSHA256         = "fc5a85d5d38c18775afe0966987ea74e7e9ac072148822c1be60a199e32cca27"
	generationOutputValidFixtureSHA256   = "887e03e9749f63237507556c5a85df40c684bb856e74b474071c39d0807beaa5"
	generationOutputInvalidFixtureSHA256 = "935dec72319d6b46133a509464cd44cf34fa460f62956253de949576ea153a4a"
	obsoleteGenerationOutputSpecSHA256   = "1dccd3a11ec659a5e8705f9b8acf333a64a21f056265fcd7c96e9c6ac197bb20"
	obsoleteGenerationOutputSchemaSHA256 = "39f8e8eaf5e5a219e806d34f46af887d69268a88d5f1d06d45e6c56465e250ed"
)

func strictJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		result := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, duplicate := result[key]; duplicate {
				return nil, fmt.Errorf("duplicate JSON object key")
			}
			value, err := strictJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return result, nil
	case '[':
		var result []any
		for decoder.More() {
			value, err := strictJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}

func decodeStrictJSON(value []byte) (any, error) {
	if !utf8.Valid(value) {
		return nil, fmt.Errorf("request is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	result, err := strictJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return result, nil
}

func v2FixtureMessages(t *testing.T) []map[string]any {
	t.Helper()
	decoded, err := decodeStrictJSON(
		readProtocolFile(t, "fixtures", "baseline-control-plane.v2.valid.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	root := decoded.(map[string]any)
	values := root["messages"].([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		result = append(result, value.(map[string]any))
	}
	return result
}

func TestV2ProtocolArtifactsAreFrozenAndMatchCorePins(t *testing.T) {
	artifacts := []struct {
		parts  []string
		digest string
	}{
		{parts: []string{"baseline-control-plane.v2.md"}, digest: v2SpecSHA256},
		{parts: []string{"baseline-control-plane.v2.schema.json"}, digest: v2SchemaSHA256},
		{parts: []string{"fixtures", "baseline-control-plane.v2.valid.json"}, digest: v2ValidFixtureSHA256},
		{parts: []string{"fixtures", "baseline-control-plane.v2.invalid.json"}, digest: v2InvalidFixtureSHA256},
		{parts: []string{"baseline-generation-output.v2.md"}, digest: generationOutputSpecSHA256},
		{parts: []string{"baseline-generation-output.v2.schema.json"}, digest: generationOutputSchemaSHA256},
		{parts: []string{"fixtures", "baseline-generation-output.v2.valid.json"}, digest: generationOutputValidFixtureSHA256},
		{parts: []string{"fixtures", "baseline-generation-output.v2.invalid.json"}, digest: generationOutputInvalidFixtureSHA256},
	}
	for _, artifact := range artifacts {
		cliBytes := readProtocolFile(t, artifact.parts...)
		if got := hashBytes(cliBytes); got != artifact.digest {
			t.Fatalf("%s SHA-256 = %s", filepath.Join(artifact.parts...), got)
		}
	}

	// The pre-existing v1 pins remain a separate no-downgrade contract.
	if got := hashBytes(readProtocolFile(t, "baseline-control-plane.v1.md")); got != pinnedSpecSHA256 {
		t.Fatalf("v1 spec SHA-256 changed: %s", got)
	}
	if got := hashBytes(readProtocolFile(t, "baseline-control-plane.v1.schema.json")); got != pinnedSchemaSHA256 {
		t.Fatalf("v1 schema SHA-256 changed: %s", got)
	}
	if generationOutputSpecSHA256 == obsoleteGenerationOutputSpecSHA256 ||
		generationOutputSchemaSHA256 == obsoleteGenerationOutputSchemaSHA256 {
		t.Fatal("obsolete unreleased generation-output hash remains compatible")
	}
}

func validateGenerationOutput(value any) error {
	output, ok := value.(map[string]any)
	if !ok || len(output) != 3 {
		return fmt.Errorf("generation output must be an exact object")
	}
	if output["schema_version"] != "baseline-generation-output.v2" {
		return fmt.Errorf("generation output schema mismatch")
	}
	outcome, ok := output["outcome"].(string)
	if !ok {
		return fmt.Errorf("generation output outcome is invalid")
	}
	findings, ok := output["findings"].([]any)
	if !ok {
		return fmt.Errorf("generation output findings are invalid")
	}
	if outcome == "no_findings" {
		if len(findings) != 0 {
			return fmt.Errorf("no_findings must be empty")
		}
		return nil
	}
	if outcome != "findings" || len(findings) < 1 || len(findings) > 4 {
		return fmt.Errorf("finding count is invalid")
	}
	for _, item := range findings {
		finding, ok := item.(map[string]any)
		if !ok || len(finding) != 1 {
			return fmt.Errorf("finding must be an exact object")
		}
		feedback, ok := finding["feedback"].(string)
		if !ok || strings.TrimSpace(feedback) == "" {
			return fmt.Errorf("finding feedback is blank")
		}
	}
	return nil
}

func TestGenerationOutputV2ValidAndInvalidFixtures(t *testing.T) {
	validValue, err := decodeStrictJSON(
		readProtocolFile(t, "fixtures", "baseline-generation-output.v2.valid.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	outputs := validValue.(map[string]any)["outputs"].([]any)
	if len(outputs) != 6 {
		t.Fatalf("valid generation outputs = %d", len(outputs))
	}
	for _, output := range outputs {
		if err := validateGenerationOutput(output); err != nil {
			t.Fatal(err)
		}
	}
	ordered := outputs[5].(map[string]any)["findings"].([]any)
	for index, expected := range []string{
		"First ordered finding.",
		"Second ordered finding.",
		"Third ordered finding.",
		"Fourth ordered finding.",
	} {
		if ordered[index].(map[string]any)["feedback"] != expected {
			t.Fatalf("finding %d order changed", index+1)
		}
	}

	invalidValue, err := decodeStrictJSON(
		readProtocolFile(t, "fixtures", "baseline-generation-output.v2.invalid.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, rawCase := range invalidValue.(map[string]any)["cases"].([]any) {
		item := rawCase.(map[string]any)
		value, present := item["value"]
		if !present {
			decoded, decodeErr := decodeStrictJSON([]byte(item["raw_output"].(string)))
			if decodeErr != nil {
				continue
			}
			value = decoded
		}
		if err := validateGenerationOutput(value); err == nil {
			t.Fatalf("%s passed generation-output validation", item["case_id"])
		}
	}
}

func TestV2ZeroFindingSuccessIsDurablyResolved(t *testing.T) {
	var completed map[string]any
	for _, message := range v2FixtureMessages(t) {
		if message["message_type"] == "job_status" && message["operation"] == "baseline_run" && message["state"] == "feedback_persisted" {
			completed = message
			break
		}
	}
	if completed == nil || completed["terminal"] != true || completed["exit_classification"] != "success" {
		t.Fatalf("zero-finding success is not terminal success: %#v", completed)
	}
	effects := completed["effects"].(map[string]any)
	if effects["feedback_count"] != json.Number("0") ||
		effects["generation_invoked"] != true ||
		effects["notification_outbox_count"] != json.Number("0") ||
		effects["persisted_run_id"] == nil ||
		effects["evidence_count"] == json.Number("0") ||
		effects["evidence_count"] != effects["reference_count"] {
		t.Fatalf("zero-finding effects are contradictory: %#v", effects)
	}
}

func TestV2MessagesUseExactVersionAndHash(t *testing.T) {
	if v2SpecSHA256 == v2ObsoleteSpecSHA256 {
		t.Fatal("obsolete unreleased v2 hash remains negotiable")
	}
	messages := v2FixtureMessages(t)
	wantTypes := map[string]bool{
		"capabilities_request": false,
		"capabilities":         false,
		"index_build_submit":   false,
		"run_submit":           false,
		"job_accepted":         false,
		"job_status_request":   false,
		"job_status":           false,
		"error":                false,
	}
	for _, message := range messages {
		if message["protocol_version"] != v2ProtocolVersion ||
			message["protocol_sha256"] != v2SpecSHA256 {
			t.Fatalf("message uses mismatched protocol: %#v", message["message_type"])
		}
		messageType, ok := message["message_type"].(string)
		if !ok {
			t.Fatal("message_type is absent")
		}
		if _, known := wantTypes[messageType]; !known {
			t.Fatalf("unknown message type %q", messageType)
		}
		wantTypes[messageType] = true
	}
	for messageType, found := range wantTypes {
		if !found {
			t.Fatalf("missing %s fixture", messageType)
		}
	}

	decodedSchema, err := decodeStrictJSON(readProtocolFile(t, "baseline-control-plane.v2.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	definitions := decodedSchema.(map[string]any)["$defs"].(map[string]any)
	protocolHash := definitions["protocol_sha256"].(map[string]any)["const"]
	if protocolHash != v2SpecSHA256 {
		t.Fatalf("schema protocol hash = %v", protocolHash)
	}
}

func TestV2ProtectedQueryAndCapabilityFixtures(t *testing.T) {
	messages := v2FixtureMessages(t)
	var submit map[string]any
	var capability map[string]any
	for _, message := range messages {
		switch message["message_type"] {
		case "run_submit":
			submit = message
		case "capabilities":
			capability = message
		}
	}
	query := submit["retrieval_query"].(map[string]any)
	text := query["text"].(string)
	if !stringsContains(text, "café") || utf8.RuneCountInString(text) != 145 ||
		len([]byte(text)) != 149 || hashBytes([]byte(text)) != v2RawQuerySHA256 {
		t.Fatalf("non-ASCII retrieval query fixture is inconsistent")
	}
	if query["sha256"] != v2RawQuerySHA256 || query["byte_size"] != json.Number("149") {
		t.Fatalf("retrieval query declarations = %#v", query)
	}
	limits := capability["limits"].(map[string]any)
	if limits["raw_query_bytes"] != json.Number(fmt.Sprint(v2MaximumRawQueryBytes)) ||
		limits["run_request_bytes"] != json.Number(fmt.Sprint(v2MaximumRunRequestByte)) ||
		limits["selected_evidence_characters"] != json.Number(fmt.Sprint(v2MaximumEvidenceChars)) {
		t.Fatalf("capability limits = %#v", limits)
	}
	operations := capability["operations"].(map[string]any)
	for _, operation := range []string{"index_build", "baseline_run"} {
		value := operations[operation].(map[string]any)
		if value["submission"] != "unavailable" || value["endpoint"] != "unavailable" ||
			value["dispatch"] != "unavailable" || value["readiness"] != "unavailable" {
			t.Fatalf("D.0 capability %s is not fail-closed: %#v", operation, value)
		}
	}
}

func TestV2DocumentRunIsSingleScopedAndReplayStable(t *testing.T) {
	messages := v2FixtureMessages(t)
	var submit map[string]any
	accepted := []map[string]any{}
	for _, message := range messages {
		if message["message_type"] == "run_submit" {
			submit = message
		}
		if message["message_type"] == "job_accepted" && message["operation"] == "baseline_run" {
			accepted = append(accepted, message)
		}
		if message["message_type"] != "job_status" || message["operation"] != "baseline_run" {
			continue
		}
		for _, forbidden := range []string{"source_chunk_id", "child_runs", "chunk_outcomes"} {
			if _, present := message[forbidden]; present {
				t.Fatalf("document-level status contains %s", forbidden)
			}
		}
		effects := message["effects"].(map[string]any)
		evidenceCount, evidenceErr := effects["evidence_count"].(json.Number).Int64()
		referenceCount, referenceErr := effects["reference_count"].(json.Number).Int64()
		if evidenceErr != nil || referenceErr != nil || evidenceCount > 4 || referenceCount > 4 {
			t.Fatalf("job-wide evidence limit exceeded: %#v", effects)
		}
		if _, multiple := effects["persisted_run_ids"]; multiple {
			t.Fatalf("multiple retrieval-run identities are forbidden: %#v", effects)
		}
	}
	if submit == nil || submit["source_document_id"] == nil {
		t.Fatal("run submission lacks authoritative source document")
	}
	for _, forbidden := range []string{"source_chunk_id", "child_runs", "chunk_outcomes"} {
		if _, present := submit[forbidden]; present {
			t.Fatalf("document-level submission contains %s", forbidden)
		}
	}
	if len(accepted) != 2 || accepted[0]["replayed"] != false || accepted[1]["replayed"] != true {
		t.Fatalf("document-level replay fixtures = %#v", accepted)
	}
	for _, field := range []string{"group_id", "job_id", "processing_run_id"} {
		if accepted[0][field] != accepted[1][field] {
			t.Fatalf("replay changed %s", field)
		}
	}
}

func stringsContains(value, substring string) bool {
	return bytes.Contains([]byte(value), []byte(substring))
}

func TestV2RunStateExitClassificationIsStable(t *testing.T) {
	want := map[string]struct {
		terminal bool
		exit     string
	}{
		"queued":               {terminal: false, exit: "pending"},
		"references_persisted": {terminal: false, exit: "pending"},
		"feedback_persisted":   {terminal: true, exit: "success"},
		"insufficient":         {terminal: true, exit: "insufficient"},
		"retryable_failed":     {terminal: false, exit: "pending"},
		"terminal_failed":      {terminal: true, exit: "failed"},
		"blocked":              {terminal: true, exit: "blocked"},
		"cancelled":            {terminal: true, exit: "cancelled"},
	}
	seen := map[string]bool{}
	for _, message := range v2FixtureMessages(t) {
		if message["message_type"] != "job_status" || message["operation"] != "baseline_run" {
			continue
		}
		state := message["state"].(string)
		expected, ok := want[state]
		if !ok || message["terminal"] != expected.terminal || message["exit_classification"] != expected.exit {
			t.Fatalf("run state contract mismatch: %#v", message)
		}
		seen[state] = true
		if message["retrieval_status"] != "ok" {
			effects := message["effects"].(map[string]any)
			for _, field := range []string{"evidence_count", "reference_count", "feedback_count", "notification_outbox_count"} {
				if effects[field] != json.Number("0") {
					t.Fatalf("non-ok retrieval has %s: %#v", field, effects)
				}
			}
			if effects["generation_invoked"] != false || effects["persisted_run_id"] != nil {
				t.Fatalf("non-ok retrieval has durable effects: %#v", effects)
			}
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("fixture run states = %#v", seen)
	}
}

func TestV2SafeResponsesExcludeProtectedMaterial(t *testing.T) {
	messages := v2FixtureMessages(t)
	forbidden := map[string]bool{
		"content":                 true,
		"content_utf8":            true,
		"credentials":             true,
		"endpoint_url":            true,
		"idempotency_key":         true,
		"idempotency_intent_hash": true,
		"lease_token":             true,
		"parent_processing_key":   true,
		"raw_diff":                true,
		"relative_path":           true,
		"repository_path":         true,
		"retrieval_query":         true,
		"source_text":             true,
	}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if forbidden[key] {
					t.Fatalf("safe response includes protected field %q", key)
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	for _, message := range messages {
		switch message["message_type"] {
		case "job_accepted", "job_status", "error", "capabilities":
			visit(message)
		}
	}
}

func TestV2InvalidRawJSONFixturesFailStrictParsing(t *testing.T) {
	decoded, err := decodeStrictJSON(
		readProtocolFile(t, "fixtures", "baseline-control-plane.v2.invalid.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := decoded.(map[string]any)["cases"].([]any)
	found := map[string]bool{}
	caseIDs := map[string]bool{}
	for _, value := range cases {
		item := value.(map[string]any)
		caseID := item["case_id"].(string)
		caseIDs[caseID] = true
		if raw, ok := item["raw_json"].(string); ok {
			if _, err := decodeStrictJSON([]byte(raw)); err == nil {
				t.Fatalf("%s passed strict JSON parsing", caseID)
			}
			found[item["expected_error"].(string)] = true
		}
		if rawHex, ok := item["raw_bytes_hex"].(string); ok {
			raw, err := hex.DecodeString(rawHex)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeStrictJSON(raw); err == nil {
				t.Fatalf("%s passed UTF-8 parsing", caseID)
			}
			found[item["expected_error"].(string)] = true
		}
	}
	for _, expected := range []string{"duplicate_json_key", "non_finite_number", "invalid_utf8"} {
		if !found[expected] {
			t.Fatalf("missing strict parsing fixture for %s", expected)
		}
	}
	for _, required := range []string{
		"obsolete_unreleased_v2_hash_rejected",
		"job_wide_evidence_item_limit_exceeded",
		"job_wide_reference_limit_exceeded",
		"job_wide_evidence_character_limit_exceeded",
		"multiple_persisted_run_ids_forbidden",
		"per_chunk_child_manifest_forbidden",
		"aggregate_chunk_outcomes_forbidden",
		"source_chunk_authority_forbidden",
	} {
		if !caseIDs[required] {
			t.Fatalf("missing document-level invalid fixture %s", required)
		}
	}
}
