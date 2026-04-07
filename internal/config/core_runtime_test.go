package config

import "testing"

func TestCoreRuntimeResolvedOpenAIBaseURLPrefersSavedConfig(t *testing.T) {
	t.Setenv("COMPAIR_OPENAI_BASE_URL", "https://env.example/v1")
	cfg := &CoreRuntime{OpenAIBaseURL: "https://saved.example/v1"}

	if got := cfg.ResolvedOpenAIBaseURL(); got != "https://saved.example/v1" {
		t.Fatalf("expected saved base URL, got %q", got)
	}
}

func TestCoreRuntimeResolvedOpenAIBaseURLFallsBackToEnv(t *testing.T) {
	t.Setenv("COMPAIR_OPENAI_BASE_URL", "https://env.example/v1")
	cfg := &CoreRuntime{}

	if got := cfg.ResolvedOpenAIBaseURL(); got != "https://env.example/v1" {
		t.Fatalf("expected env base URL, got %q", got)
	}
}

func TestCoreRuntimeResolvedOpenAIModelOverrides(t *testing.T) {
	cfg := &CoreRuntime{
		OpenAIModel:      "gpt-5-mini",
		OpenAICodeModel:  "gpt-5-code",
		OpenAINotifModel: "gpt-5-score",
	}

	if got := cfg.ResolvedOpenAICodeModel(); got != "gpt-5-code" {
		t.Fatalf("expected explicit code model, got %q", got)
	}
	if got := cfg.ResolvedOpenAINotifModel(); got != "gpt-5-score" {
		t.Fatalf("expected explicit notification model, got %q", got)
	}
}

func TestCoreRuntimeResolvedOpenAIModelDefaults(t *testing.T) {
	cfg := &CoreRuntime{OpenAIModel: "gpt-5-mini"}

	if got := cfg.ResolvedOpenAICodeModel(); got != "gpt-5-mini" {
		t.Fatalf("expected code model to inherit primary model, got %q", got)
	}
	if got := cfg.ResolvedOpenAINotifModel(); got != defaultOpenAINotifModel {
		t.Fatalf("expected notif model default %q, got %q", defaultOpenAINotifModel, got)
	}
}
