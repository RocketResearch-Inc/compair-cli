package compair

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
)

var (
	demoMode         string
	demoName         string
	demoFeedbackWait int
)

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Create a disposable demo workspace and run a real Compair review",
	Long: "Create two temporary git repos with an intentional API/client mismatch, " +
		"track them in a disposable demo group, and run a real Compair review.\n\n" +
		"Use --mode local to run against the managed local Core runtime, or --mode cloud to run against the current Cloud API base. " +
		"If --mode is omitted on an interactive terminal, Compair will prompt.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, err := resolveDemoMode()
		if err != nil {
			return err
		}
		if err := prepareDemoAPI(mode); err != nil {
			return err
		}

		client := api.NewClient(viper.GetString("api.base"))
		caps, err := ensureDemoSession(client, mode)
		if err != nil {
			return err
		}
		if caps != nil && !caps.Inputs.Repos {
			return fmt.Errorf("this API does not advertise repository inputs; choose a profile/server that supports repo review")
		}

		groupName := strings.TrimSpace(demoName)
		if groupName == "" {
			groupName = "compair-demo-" + time.Now().Format("20060102-150405")
		}
		groupID, err := createDemoGroup(client, groupName)
		if err != nil {
			return err
		}
		viper.Set("group", groupID)

		if err := enableDemoSelfFeedback(client); err != nil {
			fmt.Printf("Warning: could not enable self-feedback automatically: %v\n", err)
		}

		workspace, roots, err := createDemoWorkspace()
		if err != nil {
			return err
		}

		fmt.Println("Created demo group:", groupID)
		fmt.Println("Demo workspace:", workspace)
		for _, root := range roots {
			fmt.Println("  -", root)
		}

		for _, root := range roots {
			remote, resolvedRoot, err := resolveLocalRepo(root, "path")
			if err != nil {
				return err
			}
			if _, err := registerRepoDocument(client, groupID, remote, resolvedRoot, repoRegistrationOptions{
				InitialSync: false,
				CommitLimit: 10,
				ExtDetail:   false,
				Unpublished: false,
			}); err != nil {
				return err
			}
		}

		oldWriteMD := writeMD
		oldFeedbackWait := feedbackWaitSec
		oldSnapshotMode := snapshotMode
		oldProcessTimeout := syncProcessTimeoutSec
		defer func() {
			writeMD = oldWriteMD
			feedbackWaitSec = oldFeedbackWait
			snapshotMode = oldSnapshotMode
			syncProcessTimeoutSec = oldProcessTimeout
		}()

		writeMD = filepath.Join(workspace, "demo-feedback.md")
		feedbackWaitSec = demoFeedbackWait
		if feedbackWaitSec <= 0 {
			feedbackWaitSec = 90
		}
		snapshotMode = "snapshot"
		syncProcessTimeoutSec = 0

		fmt.Println()
		fmt.Println("Running demo review...")
		if err := runSyncCommand(cmd, roots, syncInvocationMode{}); err != nil {
			return err
		}

		if info, err := os.Stat(writeMD); err == nil {
			fmt.Println()
			fmt.Println("Demo report:", writeMD)
			_ = renderSingle(feedbackReport{Path: writeMD, ModTime: info.ModTime().UnixNano()})
		} else {
			fmt.Println()
			fmt.Println("Demo review completed. No Markdown report was written.")
		}

		fmt.Println()
		fmt.Println("You can inspect the demo repos at:")
		fmt.Println(" ", workspace)
		fmt.Println("To remove them later:")
		fmt.Printf("  rm -rf %q\n", workspace)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(demoCmd)
	demoCmd.Flags().StringVar(&demoMode, "mode", "", "Demo mode: local or cloud")
	demoCmd.Flags().StringVar(&demoName, "group-name", "", "Optional group name for the disposable demo group")
	demoCmd.Flags().IntVar(&demoFeedbackWait, "feedback-wait", 90, "Seconds to wait for feedback during the demo review")
}

func resolveDemoMode() (string, error) {
	mode := strings.ToLower(strings.TrimSpace(demoMode))
	switch mode {
	case "local", "cloud":
		return mode, nil
	case "":
	default:
		return "", fmt.Errorf("unsupported demo mode %q (expected local or cloud)", demoMode)
	}

	if !isInteractiveTerminal() {
		return "", fmt.Errorf("demo mode is required in non-interactive mode; use --mode local or --mode cloud")
	}

	fmt.Println("Choose a demo mode:")
	fmt.Println("  1. Local Core (managed Docker container, no hosted account required)")
	fmt.Println("  2. Cloud (hosted API, login required)")
	for {
		choice, err := promptLine("Choose demo mode [1/2]: ")
		if err != nil {
			return "", err
		}
		switch strings.TrimSpace(strings.ToLower(choice)) {
		case "1", "local", "core":
			return "local", nil
		case "2", "cloud", "hosted":
			return "cloud", nil
		}
		fmt.Println("Enter 1 for local Core or 2 for Cloud.")
	}
}

func isInteractiveTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func prepareDemoAPI(mode string) error {
	switch mode {
	case "local":
		cfg, err := config.LoadCoreRuntime()
		if err != nil {
			return err
		}
		viper.Set("api.base", cfg.APIBase())
		viper.Set("profile.active", "local")
		status, err := dockerContainerStatus(cfg.ContainerName)
		if err != nil || status != "running" {
			fmt.Println("Starting local Compair Core...")
			if err := runCoreUp(); err != nil {
				return err
			}
		}
		return nil
	case "cloud":
		base := strings.TrimSpace(viper.GetString("api.base"))
		resetToCloud := false
		if base == "" || looksLikeLocalAPIBase(base) {
			if profs, err := config.LoadProfiles(); err == nil {
				if cloudBase := strings.TrimSpace(profs.Profiles["cloud"].APIBase); cloudBase != "" {
					base = cloudBase
					resetToCloud = true
				}
			}
		}
		if base == "" || looksLikeLocalAPIBase(base) {
			base = "https://app.compair.sh/api"
			resetToCloud = true
		}
		viper.Set("api.base", base)
		if resetToCloud || strings.TrimSpace(viper.GetString("profile.active")) == "" {
			viper.Set("profile.active", "cloud")
		}
		return nil
	default:
		return fmt.Errorf("unsupported demo mode %q", mode)
	}
}

func looksLikeLocalAPIBase(base string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(base))
	return strings.Contains(trimmed, "localhost") || strings.Contains(trimmed, "127.0.0.1")
}

func ensureDemoSession(client *api.Client, mode string) (*api.Capabilities, error) {
	caps, err := client.Capabilities(10 * time.Minute)
	if err != nil {
		return nil, err
	}
	if !caps.Auth.Required {
		return caps, loginSingleUser(client, caps)
	}
	if _, err := client.EnsureSession(); err == nil {
		return caps, nil
	}
	if !isInteractiveTerminal() {
		return nil, fmt.Errorf("no valid session found for %s demo; run 'compair login' first or rerun interactively", mode)
	}
	fmt.Println("No valid session found. Starting login...")
	method, err := chooseLoginMethod(caps)
	if err != nil {
		return nil, err
	}
	switch method {
	case "browser":
		return caps, loginWithBrowser(client, caps)
	case "password":
		return caps, loginWithPassword(client, caps, "", "")
	default:
		return nil, fmt.Errorf("unsupported login method %q", method)
	}
}

func createDemoGroup(client *api.Client, name string) (string, error) {
	if err := client.CreateGroup(name, "", "", "", ""); err != nil {
		return "", err
	}
	items, err := client.ListGroups(true)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if strings.TrimSpace(item.Name) == name {
			if id := groupItemID(item); id != "" {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("created demo group %q but could not resolve its ID", name)
}

func enableDemoSelfFeedback(client *api.Client) error {
	return client.UpdateUser(map[string]string{
		"include_own_documents_in_feedback": "true",
	})
}

func createDemoWorkspace() (string, []string, error) {
	root, err := os.MkdirTemp("", "compair-demo-*")
	if err != nil {
		return "", nil, err
	}
	apiRepo := filepath.Join(root, "demo-api")
	clientRepo := filepath.Join(root, "demo-client")

	if err := createDemoRepo(apiRepo, "git@demo.local:compair/demo-api.git", map[string]string{
		"README.md": `# Demo API

This repo defines the backend review contract.
The review response uses the fields "severity", "category", and "rationale".
`,
		"api/openapi.yaml": `openapi: 3.1.0
info:
  title: Demo Review API
  version: 1.0.0
paths:
  /reviews:
    get:
      summary: List reviews
      responses:
        '200':
          description: Review results
          content:
            application/json:
              schema:
                type: object
                properties:
                  reviews:
                    type: array
                    items:
                      type: object
                      required: [severity, category, rationale]
                      properties:
                        severity:
                          type: string
                          enum: [high, medium, low]
                        category:
                          type: string
                        rationale:
                          type: string
`,
		"src/server.py": `def serialize_review(review: dict) -> dict:
    return {
        "severity": review["severity"],
        "category": review["category"],
        "rationale": review["rationale"],
    }
`,
	}); err != nil {
		return "", nil, err
	}

	if err := createDemoRepo(clientRepo, "git@demo.local:compair/demo-client.git", map[string]string{
		"README.md": `# Demo Client

This repo consumes the review API.
The client currently expects the fields "priority" and "type".
`,
		"src/reviewClient.ts": `export type Review = {
  priority: "high" | "medium" | "low";
  type: string;
  rationale: string;
};

export function normalizeReview(payload: any): Review {
  return {
    priority: payload.priority ?? "low",
    type: payload.type ?? "general",
    rationale: payload.rationale ?? "",
  };
}
`,
		"src/renderReview.ts": `import { normalizeReview } from "./reviewClient";

export function renderReviewCard(payload: any): string {
  const review = normalizeReview(payload);
  return review.priority.toUpperCase() + ": " + review.type + " - " + review.rationale;
}
`,
	}); err != nil {
		return "", nil, err
	}

	return root, []string{apiRepo, clientRepo}, nil
}

func createDemoRepo(root, remote string, files map[string]string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for rel, content := range files {
		target := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}
	steps := [][]string{
		{"git", "-C", root, "init"},
		{"git", "-C", root, "checkout", "-B", "main"},
		{"git", "-C", root, "config", "user.email", "demo@compair.local"},
		{"git", "-C", root, "config", "user.name", "Compair Demo"},
		{"git", "-C", root, "remote", "add", "origin", remote},
		{"git", "-C", root, "add", "."},
		{"git", "-C", root, "commit", "-m", "Create demo workspace"},
	}
	for _, step := range steps {
		cmd := exec.Command(step[0], step[1:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %s", strings.Join(step, " "), strings.TrimSpace(string(out)))
		}
	}
	return nil
}
