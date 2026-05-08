package compair

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var reviewOpenSystem bool
var reviewDetach bool
var reviewNow bool
var reviewNowYes bool
var reviewNowModel string
var reviewNowMaxFindings int
var reviewNowSkipIndex bool

func defaultNowReportPath() string {
	_ = os.MkdirAll(".compair", 0o755)
	return filepath.Join(".compair", "latest_feedback_now.md")
}

func resolveNowDocumentIDs(args []string, groupID string) ([]string, error) {
	store, err := db.Open()
	if err != nil {
		return nil, err
	}
	defer store.Close()

	ctx := context.Background()
	ids := map[string]struct{}{}
	items, err := store.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if syncAll {
		for _, item := range items {
			if strings.TrimSpace(item.DocumentID) != "" {
				ids[item.DocumentID] = struct{}{}
			}
		}
	} else {
		scopes := []string{}
		if len(args) > 0 {
			for _, p := range args {
				ap, _ := filepath.Abs(p)
				scopes = append(scopes, ap)
			}
		} else {
			cwd, _ := os.Getwd()
			scopes = append(scopes, cwd)
		}
		for _, scope := range scopes {
			found, _ := store.ListUnderPrefix(ctx, scope, groupID)
			for _, item := range found {
				if strings.TrimSpace(item.DocumentID) != "" {
					ids[item.DocumentID] = struct{}{}
				}
			}
		}
		repoRoots, err := collectRepoRoots(args, groupID, syncAll)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if strings.TrimSpace(item.DocumentID) == "" {
				continue
			}
			if _, ok := repoRoots[item.RepoRoot]; ok {
				ids[item.DocumentID] = struct{}{}
				continue
			}
			if item.Kind == "repo" {
				if _, ok := repoRoots[item.Path]; ok {
					ids[item.DocumentID] = struct{}{}
				}
			}
		}
	}

	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func confirmNowReview(groupID string, documentIDs []string) error {
	if reviewNowYes {
		return nil
	}
	docScope := "the active group's accessible documents"
	if len(documentIDs) > 0 {
		docScope = fmt.Sprintf("%d targeted document(s)", len(documentIDs))
	}
	warn := fmt.Sprintf(
		"This will upload current changes without generating the normal per-chunk feedback, then run a one-shot `review --now` against %s in group `%s` using the configured OpenAI-compatible model server.",
		docScope,
		groupID,
	)
	if reviewNowSkipIndex {
		warn += "\nIndex refresh will be skipped for this run, so standard indexed retrieval for these documents may remain stale until you run a normal sync/review later."
	}
	warn += "\nThis may be slow and expensive.\nContinue? [y/N]: "
	fmt.Fprint(os.Stderr, warn)
	in := bufio.NewReader(os.Stdin)
	line, _ := in.ReadString('\n')
	choice := strings.TrimSpace(strings.ToLower(line))
	if choice != "y" && choice != "yes" {
		return fmt.Errorf("aborting now review")
	}
	return nil
}

var reviewCmd = &cobra.Command{
	Use:          "review [PATH ...]",
	Short:        "Run a full Compair review and write the latest report",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if reviewNowSkipIndex && !reviewNow {
			return fmt.Errorf("--skip-index requires --now")
		}
		if reviewNow {
			return runNowReview(cmd, args)
		}
		if reviewDetach {
			return runSyncCommand(cmd, args, syncInvocationMode{Detach: true})
		}

		reportPath := writeMD
		if reportPath == "" {
			reportPath = defaultReportPath()
			writeMD = reportPath
		}

		var before time.Time
		if info, err := os.Stat(reportPath); err == nil {
			before = info.ModTime()
		}

		if err := runSyncCommand(cmd, args, syncInvocationMode{}); err != nil {
			return err
		}

		info, err := os.Stat(reportPath)
		if err != nil {
			return nil
		}
		if !before.IsZero() && !info.ModTime().After(before) {
			return nil
		}
		if reviewOpenSystem {
			return openWithSystem(reportPath)
		}
		return renderSingle(feedbackReport{Path: reportPath, ModTime: info.ModTime().UnixNano()})
	},
}

func runNowReview(cmd *cobra.Command, args []string) error {
	client := api.NewClient(viper.GetString("api.base"))
	groupID, _, err := groups.ResolveWithAuto(client, "", viper.GetString("group"))
	if err != nil {
		return err
	}
	documentIDs, err := resolveNowDocumentIDs(args, groupID)
	if err != nil {
		return err
	}
	if err := confirmNowReview(groupID, documentIDs); err != nil {
		return err
	}

	reportPath := writeMD
	if reportPath == "" {
		reportPath = defaultNowReportPath()
		writeMD = reportPath
	}
	if filepath.Ext(reportPath) == "" {
		reportPath += ".md"
	}

	noFeedback := false
	if err := runSyncCommand(cmd, args, syncInvocationMode{
		PushOnly:         true,
		AwaitProcessing:  true,
		GenerateFeedback: &noFeedback,
		SkipIndex:        reviewNowSkipIndex,
	}); err != nil {
		return err
	}

	resp, err := client.ReviewNow(api.ReviewNowOptions{
		GroupID:     groupID,
		DocumentIDs: documentIDs,
		MaxFindings: reviewNowMaxFindings,
		Model:       strings.TrimSpace(reviewNowModel),
	})
	if err != nil {
		return err
	}

	if err := os.WriteFile(reportPath, []byte(resp.Markdown), 0o644); err != nil {
		return err
	}
	info, err := os.Stat(reportPath)
	if err != nil {
		return nil
	}
	if reviewOpenSystem {
		return openWithSystem(reportPath)
	}
	return renderSingle(feedbackReport{Path: reportPath, ModTime: info.ModTime().UnixNano()})
}

func init() {
	rootCmd.AddCommand(reviewCmd)
	addSyncFlags(reviewCmd, true)
	reviewCmd.Flags().BoolVar(&reviewOpenSystem, "system", false, "Open the generated report using the system default viewer")
	reviewCmd.Flags().BoolVar(&reviewDetach, "detach", false, "Submit the review work and return immediately; use 'compair wait' or 'compair status' to follow progress")
	reviewCmd.Flags().BoolVar(&reviewNow, "now", false, "Run a one-shot whole-bundle review against the configured OpenAI-compatible model")
	reviewCmd.Flags().BoolVarP(&reviewNowYes, "yes", "y", false, "Skip the `review --now` confirmation prompt")
	reviewCmd.Flags().StringVar(&reviewNowModel, "now-model", "", "Override the model used for `review --now`")
	reviewCmd.Flags().IntVar(&reviewNowMaxFindings, "now-max-findings", 12, "Maximum findings to request from `review --now`")
	reviewCmd.Flags().BoolVar(&reviewNowSkipIndex, "skip-index", false, "For `review --now`, upload the latest snapshot text without refreshing chunk embeddings or indexed retrieval state")
	hideCommandFlags(reviewCmd,
		"feedback-wait",
		"process-timeout-sec",
		"write-md",
		"push-only",
		"fetch-only",
	)
}
