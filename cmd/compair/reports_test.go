package compair

import (
	"testing"

	"github.com/spf13/viper"
)

func TestShouldRenderPlainMarkdownUsesNoColorFlag(t *testing.T) {
	prev := viper.GetBool("no_color")
	viper.Set("no_color", true)
	t.Cleanup(func() {
		viper.Set("no_color", prev)
	})

	if !shouldRenderPlainMarkdown() {
		t.Fatal("expected plain markdown rendering when --no-color is enabled")
	}
}

func TestRenderMarkdownFallsBackToPlainText(t *testing.T) {
	prev := viper.GetBool("no_color")
	viper.Set("no_color", true)
	t.Cleanup(func() {
		viper.Set("no_color", prev)
	})

	out, err := renderMarkdown("# Demo")
	if err != nil {
		t.Fatalf("renderMarkdown returned error: %v", err)
	}
	if out != "# Demo\n" {
		t.Fatalf("unexpected plain markdown output: %q", out)
	}
}
