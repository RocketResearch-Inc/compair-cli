package compair

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
)

func TestRuntimeConfigMismatchesIncludesReferenceTraceAndTimeouts(t *testing.T) {
	cfg := &config.CoreRuntime{
		GenerationProvider:            "openai",
		EmbeddingProvider:             "openai",
		OpenAIModel:                   "gpt-5-nano",
		NotificationScoringTimeoutS:   90,
		NotificationScoringMaxRetries: 1,
		ReferenceTrace:                true,
		ReferenceSourceTrace:          true,
		ReferenceHybrid:               true,
		ReferenceAdjudicator:          true,
		ReferenceAdjudicatorTopK:      6,
	}
	env := map[string]string{
		"COMPAIR_GENERATION_PROVIDER":              "openai",
		"COMPAIR_EMBEDDING_PROVIDER":               "openai",
		"COMPAIR_OPENAI_MODEL":                     "gpt-5-nano",
		"COMPAIR_NOTIFICATION_SCORING_TIMEOUT_S":   "30",
		"COMPAIR_NOTIFICATION_SCORING_MAX_RETRIES": "2",
	}

	mismatches := runtimeConfigMismatches(cfg, env)

	if len(mismatches) != 7 {
		t.Fatalf("expected 7 mismatches, got %d: %#v", len(mismatches), mismatches)
	}
}

func TestRuntimeConfigMismatchesAllowsInheritedNotifModelAndMatchingTrace(t *testing.T) {
	cfg := &config.CoreRuntime{
		GenerationProvider:            "openai",
		EmbeddingProvider:             "openai",
		OpenAIModel:                   "gpt-5-nano",
		NotificationScoringTimeoutS:   90,
		NotificationScoringMaxRetries: 1,
		ReferenceTrace:                true,
		ReferenceSourceTrace:          true,
		ReferenceHybrid:               true,
		ReferenceAdjudicator:          true,
		ReferenceAdjudicatorTopK:      6,
	}
	env := map[string]string{
		"COMPAIR_GENERATION_PROVIDER":              "openai",
		"COMPAIR_EMBEDDING_PROVIDER":               "openai",
		"COMPAIR_OPENAI_MODEL":                     "gpt-5-nano",
		"COMPAIR_NOTIFICATION_SCORING_TIMEOUT_S":   "90",
		"COMPAIR_NOTIFICATION_SCORING_MAX_RETRIES": "1",
		"COMPAIR_REFERENCE_TRACE":                  "1",
		"COMPAIR_REFERENCE_SOURCE_TRACE":           "1",
		"COMPAIR_REFERENCE_HYBRID_ENABLED":         "1",
		"COMPAIR_REFERENCE_ADJUDICATOR_ENABLED":    "1",
		"COMPAIR_REFERENCE_ADJUDICATOR_TOP_K":      "6",
	}

	mismatches := runtimeConfigMismatches(cfg, env)

	if len(mismatches) != 0 {
		t.Fatalf("expected no mismatches, got %#v", mismatches)
	}
}

func TestRuntimeConfigMismatchesIncludesExtraEnv(t *testing.T) {
	cfg := &config.CoreRuntime{
		ExtraEnv: map[string]string{
			"COMPAIR_REFERENCE_HYBRID_RERANKER_BLEND": "0.75",
		},
	}
	env := map[string]string{
		"COMPAIR_REFERENCE_HYBRID_RERANKER_BLEND": "0.45",
	}

	mismatches := runtimeConfigMismatches(cfg, env)

	if len(mismatches) != 1 {
		t.Fatalf("expected 1 mismatch, got %#v", mismatches)
	}
}

func TestLoadExtraEnvFileReadsReplaySummaryRecommendedEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.json")
	content := []byte("{\"recommended_env\":{\"COMPAIR_REFERENCE_HYBRID_RERANKER_BLEND\":\"0.75\",\"COMPAIR_REFERENCE_ADJUDICATOR_TOP_K\":\"12\"}}\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	got, err := loadExtraEnvFile(path)
	if err != nil {
		t.Fatalf("load extra env file: %v", err)
	}
	if got["COMPAIR_REFERENCE_HYBRID_RERANKER_BLEND"] != "0.75" {
		t.Fatalf("unexpected hybrid reranker blend: %#v", got)
	}
	if got["COMPAIR_REFERENCE_ADJUDICATOR_TOP_K"] != "12" {
		t.Fatalf("unexpected adjudicator top-k: %#v", got)
	}
}

func TestLoadExtraEnvFileReadsDotenvFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tuning.env")
	content := []byte("# comment\nCOMPAIR_REFERENCE_HYBRID_RERANKER_BLEND=0.75\nexport COMPAIR_REFERENCE_ADJUDICATOR_TOP_K=12\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	got, err := loadExtraEnvFile(path)
	if err != nil {
		t.Fatalf("load extra env file: %v", err)
	}
	if got["COMPAIR_REFERENCE_HYBRID_RERANKER_BLEND"] != "0.75" {
		t.Fatalf("unexpected hybrid reranker blend: %#v", got)
	}
	if got["COMPAIR_REFERENCE_ADJUDICATOR_TOP_K"] != "12" {
		t.Fatalf("unexpected adjudicator top-k: %#v", got)
	}
}

func TestExpectedReferenceRerankerContainerPathUsesLatestManifestForDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, coreReferenceRerankerLatestManifestName)
	if err := os.WriteFile(manifestPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	got := expectedReferenceRerankerContainerPath(tmpDir)
	if got != coreReferenceRerankerLatestContainerPath {
		t.Fatalf("expected latest container path, got %q", got)
	}
}

func TestResolveReferenceRerankerMountUsesDirectoryForLatestManifestFile(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, coreReferenceRerankerLatestManifestName)
	if err := os.WriteFile(manifestPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	hostMount, containerMount, containerModelPath, err := resolveReferenceRerankerMount(manifestPath)
	if err != nil {
		t.Fatalf("resolve mount: %v", err)
	}
	if hostMount != tmpDir {
		t.Fatalf("expected host mount %q, got %q", tmpDir, hostMount)
	}
	if containerMount != coreReferenceRerankerContainerDirPath {
		t.Fatalf("expected container mount %q, got %q", coreReferenceRerankerContainerDirPath, containerMount)
	}
	if containerModelPath != coreReferenceRerankerLatestContainerPath {
		t.Fatalf("expected model path %q, got %q", coreReferenceRerankerLatestContainerPath, containerModelPath)
	}
}
