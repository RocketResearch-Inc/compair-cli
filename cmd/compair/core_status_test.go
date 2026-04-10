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
	}
	env := map[string]string{
		"COMPAIR_GENERATION_PROVIDER":              "openai",
		"COMPAIR_EMBEDDING_PROVIDER":               "openai",
		"COMPAIR_OPENAI_MODEL":                     "gpt-5-nano",
		"COMPAIR_NOTIFICATION_SCORING_TIMEOUT_S":   "30",
		"COMPAIR_NOTIFICATION_SCORING_MAX_RETRIES": "2",
	}

	mismatches := runtimeConfigMismatches(cfg, env)

	if len(mismatches) != 3 {
		t.Fatalf("expected 3 mismatches, got %d: %#v", len(mismatches), mismatches)
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
	}
	env := map[string]string{
		"COMPAIR_GENERATION_PROVIDER":              "openai",
		"COMPAIR_EMBEDDING_PROVIDER":               "openai",
		"COMPAIR_OPENAI_MODEL":                     "gpt-5-nano",
		"COMPAIR_NOTIFICATION_SCORING_TIMEOUT_S":   "90",
		"COMPAIR_NOTIFICATION_SCORING_MAX_RETRIES": "1",
		"COMPAIR_REFERENCE_TRACE":                  "1",
	}

	mismatches := runtimeConfigMismatches(cfg, env)

	if len(mismatches) != 0 {
		t.Fatalf("expected no mismatches, got %#v", mismatches)
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
