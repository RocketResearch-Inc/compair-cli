package compair

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

func TestApplySyncGatePresetAppliesDefaults(t *testing.T) {
	resetSyncGateTestState()
	cmd := newSyncGateTestCommand()
	if err := cmd.ParseFlags([]string{"--gate", "api-contract"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	exitEarly, err := applySyncGatePreset(cmd)
	if err != nil {
		t.Fatalf("apply preset: %v", err)
	}
	if exitEarly {
		t.Fatalf("expected normal execution, got exitEarly")
	}
	if syncFailOnFeedback != 1 {
		t.Fatalf("expected fallback count 1, got %d", syncFailOnFeedback)
	}
	if !reflect.DeepEqual(syncFailOnSeverity, []string{"high"}) {
		t.Fatalf("unexpected severities: %#v", syncFailOnSeverity)
	}
	if !reflect.DeepEqual(syncFailOnType, []string{"potential_conflict"}) {
		t.Fatalf("unexpected types: %#v", syncFailOnType)
	}
}

func TestApplySyncGatePresetRespectsExplicitOverrides(t *testing.T) {
	resetSyncGateTestState()
	cmd := newSyncGateTestCommand()
	if err := cmd.ParseFlags([]string{
		"--gate", "api-contract",
		"--fail-on-feedback", "3",
		"--fail-on-type", "quiet_validation",
	}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	exitEarly, err := applySyncGatePreset(cmd)
	if err != nil {
		t.Fatalf("apply preset: %v", err)
	}
	if exitEarly {
		t.Fatalf("expected normal execution, got exitEarly")
	}
	if syncFailOnFeedback != 3 {
		t.Fatalf("expected explicit fallback count 3, got %d", syncFailOnFeedback)
	}
	if !reflect.DeepEqual(syncFailOnSeverity, []string{"high"}) {
		t.Fatalf("expected preset severity to apply, got %#v", syncFailOnSeverity)
	}
	if !reflect.DeepEqual(syncFailOnType, []string{"quiet_validation"}) {
		t.Fatalf("expected explicit type override to win, got %#v", syncFailOnType)
	}
}

func TestApplySyncGatePresetHelpExitsEarly(t *testing.T) {
	resetSyncGateTestState()
	cmd := newSyncGateTestCommand()
	if err := cmd.ParseFlags([]string{"--gate", "help"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	exitEarly, err := applySyncGatePreset(cmd)
	if err != nil {
		t.Fatalf("apply preset: %v", err)
	}
	if !exitEarly {
		t.Fatalf("expected help preset to exit early")
	}
}

func newSyncGateTestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "sync"}
	cmd.Flags().StringVar(&syncGate, "gate", "", "")
	cmd.Flags().IntVar(&syncFailOnFeedback, "fail-on-feedback", 0, "")
	cmd.Flags().StringArrayVar(&syncFailOnSeverity, "fail-on-severity", nil, "")
	cmd.Flags().StringArrayVar(&syncFailOnType, "fail-on-type", nil, "")
	return cmd
}

func resetSyncGateTestState() {
	syncGate = ""
	syncFailOnFeedback = 0
	syncFailOnSeverity = nil
	syncFailOnType = nil
}
