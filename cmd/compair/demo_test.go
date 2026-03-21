package compair

import (
	"strings"
	"testing"
)

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
