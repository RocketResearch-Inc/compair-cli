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
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
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
		if err := ensureDemoPrereqs(); err != nil {
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
		snapshotMode = "diff"
		syncProcessTimeoutSec = 0

		fmt.Println()
		fmt.Println("Priming demo workspace...")
		if err := primeDemoWorkspace(cmd, client, groupID, roots); err != nil {
			return err
		}

		fmt.Println()
		fmt.Println("Introducing demo drift...")
		if err := introduceDemoDrift(roots); err != nil {
			return err
		}

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
	item, err := client.CreateGroup(name, "", "", "", "")
	if err != nil {
		return "", err
	}
	if id := groupItemID(item); id != "" {
		return id, nil
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

func primeDemoWorkspace(cmd *cobra.Command, client *api.Client, groupID string, roots []string) error {
	snapshotOpts := defaultSnapshotOptions()
	if prof := loadActiveProfileSnapshot(); prof != nil {
		applySnapshotOverrides(&snapshotOpts, *prof)
	}
	progress := newRepoProgressTracker(len(roots))
	for idx, root := range roots {
		cfg, err := config.ReadProjectConfig(root)
		if err != nil {
			return err
		}
		if len(cfg.Repos) == 0 || strings.TrimSpace(cfg.Repos[0].DocumentID) == "" {
			return fmt.Errorf("demo repo %s is missing Compair document metadata", root)
		}
		repo := &cfg.Repos[0]
		repoLabel := repoDisplayLabel(root, repo.RemoteURL)
		repoStartedAt := time.Now()
		fmt.Printf("[%d/%d] Priming %s\n", idx+1, len(roots), repoLabel)
		ensureRepoDocumentPublished(client, repo.DocumentID, root)

		text := ""
		latest := ""
		snapshotUsed := false

		res, err := buildRepoSnapshot(root, groupID, repo, snapshotOpts)
		if err == nil {
			text = res.Text
			latest = res.Head
			snapshotUsed = true
			maybeWarnSnapshotScope(root, res.Stats, snapshotOpts)
		} else {
			fmt.Printf("Warning: snapshot failed for %s: %v (falling back to diff mode)\n", repo.RemoteURL, err)
			text, latest = gitCollectDemoFallback(root)
		}

		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("no demo payload generated for %s", repo.RemoteURL)
		}

		var resp api.ProcessDocResp
		if snapshotUsed {
			resp, err = client.ProcessDocWithMode(repo.DocumentID, text, false, "client")
		} else {
			resp, err = client.ProcessDoc(repo.DocumentID, text, false)
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(resp.TaskID) != "" {
			st, timedOut, err := waitForProcessingTask(cmd.Context(), client, resp.TaskID, func(elapsed time.Duration) {
				fmt.Println(progress.waitingLine(idx+1, repoLabel, elapsed))
			})
			if err != nil {
				return err
			}
			if timedOut {
				return fmt.Errorf(
					"processing timeout after %ds while priming demo repo %s",
					syncProcessTimeoutSec,
					repo.RemoteURL,
				)
			}
			switch strings.ToUpper(strings.TrimSpace(st.Status)) {
			case "SUCCESS":
			default:
				return fmt.Errorf("demo priming failed for %s with status %s", repo.RemoteURL, st.Status)
			}
			if err := waitForChunkTasks(cmd.Context(), client, st.Result, repoLabel, func(taskIndex int, taskTotal int, elapsed time.Duration) {
				fmt.Printf(
					"[%d/%d] Waiting for chunk task %d/%d for %s (%s elapsed)\n",
					idx+1,
					len(roots),
					taskIndex,
					taskTotal,
					repoLabel,
					humanDuration(elapsed),
				)
			}); err != nil {
				return err
			}
		}
		finalizeRepoSync(root, groupID, cfg, repo, latest)
		fmt.Println(progress.completeLine(idx+1, repoLabel, time.Since(repoStartedAt)))
	}
	return nil
}

func gitCollectDemoFallback(root string) (string, string) {
	return git.CollectChangeTextAtWithLimit(root, "", 10, false)
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
The /reviews endpoint returns reviews[] objects with the fields "severity", "category", and "rationale".
Clients should not expect "items", "priority", or "type".
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
		"src/server.py": `def list_reviews() -> dict:
    return {
        "reviews": [
            {
                "severity": "high",
                "category": "api-contract",
                "rationale": "Frontend should render category and severity from the backend contract.",
            }
        ]
    }

def serialize_review(review: dict) -> dict:
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
The UI expects /reviews to return reviews[] with the fields "severity", "category", and "rationale".
It renders the backend contract fields directly.
`,
		"src/reviewClient.ts": `export type Review = {
  severity: "high" | "medium" | "low";
  category: string;
  rationale: string;
};

export function normalizeReview(payload: any): Review {
  return {
    severity: payload.severity ?? "low",
    category: payload.category ?? "general",
    rationale: payload.rationale ?? "",
  };
}
`,
		"src/reviewFeed.ts": `import { renderReviewCard } from "./renderReview";

export async function loadReviewFeed(): Promise<string[]> {
  const response = await fetch("/reviews");
  const payload = await response.json();
  return (payload.reviews ?? []).map((item: any) => renderReviewCard(item));
}
`,
		"src/renderReview.ts": `import { normalizeReview } from "./reviewClient";

export function renderReviewCard(payload: any): string {
  const review = normalizeReview(payload);
  return review.severity.toUpperCase() + ": " + review.category + " - " + review.rationale;
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
		if err := runDemoCommand(step); err != nil {
			return err
		}
	}
	return nil
}

func introduceDemoDrift(roots []string) error {
	if len(roots) < 2 {
		return fmt.Errorf("demo workspace is missing the client repo")
	}
	clientRepo := roots[1]
	updates := map[string]string{
		"README.md": `# Demo Client

This repo consumes the review API.
The UI currently expects /reviews to return items[] with the fields "priority", "type", and "rationale".
If the payload uses "reviews", "severity", or "category", the list silently renders fallback values.
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
		"src/reviewFeed.ts": `import { renderReviewCard } from "./renderReview";

export async function loadReviewFeed(): Promise<string[]> {
  const response = await fetch("/reviews");
  const payload = await response.json();
  return (payload.items ?? []).map((item: any) => renderReviewCard(item));
}
`,
		"src/renderReview.ts": `import { normalizeReview } from "./reviewClient";

export function renderReviewCard(payload: any): string {
  const review = normalizeReview(payload);
  return review.priority.toUpperCase() + ": " + review.type + " - " + review.rationale;
}
`,
	}
	for rel, content := range updates {
		target := filepath.Join(clientRepo, rel)
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}
	steps := [][]string{
		{"git", "-C", clientRepo, "add", "README.md", "src/renderReview.ts", "src/reviewClient.ts", "src/reviewFeed.ts"},
		{"git", "-C", clientRepo, "commit", "-m", "Introduce demo client contract drift"},
	}
	for _, step := range steps {
		if err := runDemoCommand(step); err != nil {
			return err
		}
	}
	fmt.Println("Committed an intentional contract regression in", clientRepo)
	return nil
}

func ensureDemoPrereqs() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("compair demo requires git on PATH: %w", err)
	}
	return nil
}

func runDemoCommand(step []string) error {
	if len(step) == 0 {
		return fmt.Errorf("empty demo command")
	}
	cmd := exec.Command(step[0], step[1:]...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("%s: %s", strings.Join(step, " "), msg)
}
