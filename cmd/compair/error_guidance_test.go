package compair

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestFormatCLIErrorAddsHostedChunkFailureGuidance(t *testing.T) {
	setErrorGuidanceState(t, "https://app.compair.sh/api", "cloud")

	root := &cobra.Command{Use: "compair"}
	syncCmd := &cobra.Command{Use: "sync"}
	root.AddCommand(syncCmd)

	out := formatCLIError(syncCmd, errString("chunk task 1680f5b3 for repo ended with status FAILURE"))
	if !strings.Contains(out, "Run 'compair doctor'") {
		t.Fatalf("expected doctor guidance, got %q", out)
	}
	if !strings.Contains(out, "support@compair.sh") {
		t.Fatalf("expected hosted support guidance, got %q", out)
	}
	if !strings.Contains(out, "compair review --detach") {
		t.Fatalf("expected async large-repo guidance, got %q", out)
	}
	if !strings.Contains(out, "compair wait") {
		t.Fatalf("expected wait guidance, got %q", out)
	}
	if !strings.Contains(out, "--process-timeout-sec") {
		t.Fatalf("expected process-timeout guidance, got %q", out)
	}
}

func TestFormatCLIErrorAddsLocalCoreGuidance(t *testing.T) {
	setErrorGuidanceState(t, "http://localhost:8000", "local")

	root := &cobra.Command{Use: "compair"}
	coreCmd := &cobra.Command{Use: "core"}
	root.AddCommand(coreCmd)

	out := formatCLIError(coreCmd, errString("docker is not available on PATH"))
	if !strings.Contains(out, "Run 'compair core doctor'") {
		t.Fatalf("expected core doctor guidance, got %q", out)
	}
	if !strings.Contains(out, "Docker Desktop or Docker Engine") {
		t.Fatalf("expected docker start guidance, got %q", out)
	}
}

func TestFormatCLIErrorAvoidsHostedSupportForSelfHostedRemote(t *testing.T) {
	setErrorGuidanceState(t, "https://code.example.internal/api", "selfhosted")

	root := &cobra.Command{Use: "compair"}
	syncCmd := &cobra.Command{Use: "sync"}
	root.AddCommand(syncCmd)

	out := formatCLIError(syncCmd, errString("chunk task 1680f5b3 for repo ended with status FAILURE"))
	if strings.Contains(out, "support@compair.sh") {
		t.Fatalf("expected self-hosted guidance without hosted support, got %q", out)
	}
	if !strings.Contains(out, "check the API/worker logs") {
		t.Fatalf("expected self-hosted server-log guidance, got %q", out)
	}
}

func TestFormatCLIErrorAddsLoginGuidance(t *testing.T) {
	setErrorGuidanceState(t, "https://app.compair.sh/api", "cloud")

	out := formatCLIError(nil, errString("not logged in"))
	if !strings.Contains(out, "Run 'compair doctor'") {
		t.Fatalf("expected doctor guidance, got %q", out)
	}
	if !strings.Contains(out, "'compair login'") {
		t.Fatalf("expected login guidance, got %q", out)
	}
}

func TestFormatCLIErrorAvoidsBlindLoginHintForLocalCore401(t *testing.T) {
	setErrorGuidanceState(t, "http://localhost:8000", "local")

	out := formatCLIError(nil, errString("GET /load_session: 401 unauthorized"))
	if !strings.Contains(out, "Run 'compair core doctor'") {
		t.Fatalf("expected core doctor guidance, got %q", out)
	}
	if !strings.Contains(out, "single-user") {
		t.Fatalf("expected auth-mode guidance, got %q", out)
	}
}

func TestFormatCLIErrorAddsGroupBindingGuidanceForCreateDocAccessErrors(t *testing.T) {
	setErrorGuidanceState(t, "https://app.compair.sh/api", "cloud")

	out := formatCLIError(nil, errString(`POST /create_doc: {"detail":"You do not belong to one or more requested groups."}`))
	if !strings.Contains(out, "Run 'compair doctor'") {
		t.Fatalf("expected doctor guidance, got %q", out)
	}
	if !strings.Contains(out, "compair group ls") {
		t.Fatalf("expected group guidance, got %q", out)
	}
}

func TestFormatCLIErrorLeavesUnmatchedErrorsAlone(t *testing.T) {
	msg := "expected one of: brief, detailed, verbose"
	out := formatCLIError(nil, errString(msg))
	if out != msg {
		t.Fatalf("expected raw message, got %q", out)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func setErrorGuidanceState(t *testing.T, apiBase, profile string) {
	t.Helper()
	prevAPI := viper.GetString("api.base")
	prevProfile := viper.GetString("profile.active")
	viper.Set("api.base", apiBase)
	viper.Set("profile.active", profile)
	t.Cleanup(func() {
		viper.Set("api.base", prevAPI)
		viper.Set("profile.active", prevProfile)
	})
}
