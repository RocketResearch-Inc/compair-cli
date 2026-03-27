package compair

import (
	"strings"
	"testing"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
)

func TestDemoDriftRecapLinesSummarizesInjectedMismatch(t *testing.T) {
	lines := demoDriftRecapLines()
	out := strings.Join(lines, "\n")
	want := []string{
		"Seeded drift recap:",
		`payload.reviews -> payload.items`,
		`review.severity/category -> review.priority/type`,
		`render severity/category -> priority/type`,
	}
	for _, snippet := range want {
		if !strings.Contains(out, snippet) {
			t.Fatalf("expected demo drift recap to contain %q, got:\n%s", snippet, out)
		}
	}
}

func TestRunDemoCommandReportsProcessErrorWhenOutputIsEmpty(t *testing.T) {
	err := runDemoCommand([]string{"compair-definitely-missing-binary"})
	if err == nil {
		t.Fatal("expected an error for a missing command")
	}
	if !strings.Contains(err.Error(), "compair-definitely-missing-binary") {
		t.Fatalf("expected command name in error, got %q", err)
	}
	if !strings.Contains(err.Error(), "executable file not found") && !strings.Contains(err.Error(), "not found in") {
		t.Fatalf("expected process lookup error in message, got %q", err)
	}
}

func TestRunDemoCommandRejectsEmptyStep(t *testing.T) {
	err := runDemoCommand(nil)
	if err == nil {
		t.Fatal("expected an error for an empty command")
	}
	if got := err.Error(); got != "empty demo command" {
		t.Fatalf("unexpected error %q", got)
	}
}

func TestShouldRecommendOpenAIDemo(t *testing.T) {
	cfg := &config.CoreRuntime{
		GenerationProvider: "local",
		EmbeddingProvider:  "local",
		OpenAIAPIKey:       "sk-demo",
	}
	if !shouldRecommendOpenAIDemo(cfg) {
		t.Fatal("expected recommendation when OpenAI key is present but generation is local")
	}

	cfg.GenerationProvider = "openai"
	if shouldRecommendOpenAIDemo(cfg) {
		t.Fatal("did not expect recommendation when generation is openai and embeddings stay local")
	}
}
