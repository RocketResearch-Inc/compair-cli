package compair

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

func nowQuoteString(meta map[string]interface{}, key string) string {
	if meta == nil {
		return ""
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func nowQuoteInt(meta map[string]interface{}, key string) (int, bool) {
	if meta == nil {
		return 0, false
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		return i, err == nil
	default:
		return 0, false
	}
}

func nowQuoteBool(meta map[string]interface{}, key string) bool {
	if meta == nil {
		return false
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return err == nil && parsed
	default:
		return false
	}
}

func nowQuoteMap(meta map[string]interface{}, key string) map[string]interface{} {
	if meta == nil {
		return nil
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return nil
	}
	switch v := value.(type) {
	case map[string]interface{}:
		return v
	default:
		return nil
	}
}

func nowQuoteCost(meta map[string]interface{}) (float64, bool) {
	if meta == nil {
		return 0, false
	}
	raw, ok := meta["cost_estimate_usd"]
	if !ok || raw == nil {
		return 0, false
	}
	cost, ok := raw.(map[string]interface{})
	if !ok {
		return 0, false
	}
	total, ok := cost["total_cost_usd"]
	if !ok || total == nil {
		return 0, false
	}
	switch v := total.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func nowQuoteCanRun(meta map[string]interface{}) (bool, string) {
	billing := nowQuoteMap(meta, "billing")
	if billing == nil {
		return true, ""
	}
	if raw, ok := billing["can_run"]; ok {
		switch v := raw.(type) {
		case bool:
			if !v {
				return false, nowQuoteString(billing, "blocking_reason")
			}
			return true, ""
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(v))
			if err == nil && !parsed {
				return false, nowQuoteString(billing, "blocking_reason")
			}
		}
	}
	return true, ""
}

func formatNowTokens(value int, estimated bool) string {
	suffix := ""
	if estimated {
		suffix = " estimated"
	}
	return fmt.Sprintf("%d%s", value, suffix)
}

func printNowReviewQuote(groupID string, documentIDs []string, quote *api.ReviewNowQuoteResp) {
	docScope := "the active group's accessible documents"
	if len(documentIDs) > 0 {
		docScope = fmt.Sprintf("%d targeted document(s)", len(documentIDs))
	}

	meta := map[string]interface{}{}
	if quote != nil && quote.Meta != nil {
		meta = quote.Meta
	}
	model := nowQuoteString(meta, "model")
	if model == "" {
		model = "configured default"
	}
	docCount, ok := nowQuoteInt(meta, "document_count")
	if !ok {
		docCount = len(documentIDs)
	}
	promptTokens, promptOK := nowQuoteInt(meta, "prompt_estimated_tokens")
	maxOutputTokens, outputOK := nowQuoteInt(meta, "max_output_tokens")
	tokenMethod := nowQuoteString(meta, "prompt_token_count_method")
	if tokenMethod == "" {
		tokenMethod = "unknown"
	}

	fmt.Fprintf(os.Stderr, "Compair review --now quote\n")
	fmt.Fprintf(os.Stderr, "  Scope: %s in group `%s`\n", docScope, groupID)
	fmt.Fprintf(os.Stderr, "  Model: %s\n", model)
	if docCount > 0 {
		fmt.Fprintf(os.Stderr, "  Documents: %d\n", docCount)
	}
	if promptOK {
		fmt.Fprintf(os.Stderr, "  Prompt tokens: %s (%s)\n", formatNowTokens(promptTokens, nowQuoteBool(meta, "prompt_tokens_estimated")), tokenMethod)
	}
	if outputOK {
		fmt.Fprintf(os.Stderr, "  Output token budget: %d\n", maxOutputTokens)
	}
	if cost, ok := nowQuoteCost(meta); ok {
		fmt.Fprintf(os.Stderr, "  Estimated maximum model cost: $%.6f\n", cost)
	} else {
		fmt.Fprintf(os.Stderr, "  Estimated maximum model cost: unavailable; configure now-review pricing rates on the Core server to display this.\n")
	}
	if billing := nowQuoteMap(meta, "billing"); billing != nil {
		if balance, ok := nowQuoteInt(billing, "balance_cents"); ok {
			fmt.Fprintf(os.Stderr, "  Cloud credit balance: $%.2f\n", float64(balance)/100.0)
		}
		if estimated, ok := nowQuoteInt(billing, "estimated_cost_cents"); ok {
			fmt.Fprintf(os.Stderr, "  Cloud credit hold: $%.2f max\n", float64(estimated)/100.0)
		}
		if canRun, reason := nowQuoteCanRun(meta); !canRun {
			if reason == "" {
				reason = "not allowed"
			}
			fmt.Fprintf(os.Stderr, "  Cloud credit status: blocked (%s)\n", reason)
		}
	}
	fmt.Fprintf(os.Stderr, "  Final provider usage will be included in the generated report when the model returns it.\n")
	if reviewNowSkipIndex {
		fmt.Fprintf(os.Stderr, "  Note: index refresh was skipped, so standard indexed retrieval may remain stale until a normal sync/review later.\n")
	}
}

func confirmNowReview(groupID string, documentIDs []string, quote *api.ReviewNowQuoteResp) error {
	printNowReviewQuote(groupID, documentIDs, quote)
	if reviewNowYes {
		fmt.Fprintln(os.Stderr, "Continuing because --yes was set.")
		return nil
	}
	fmt.Fprint(os.Stderr, "Run the one-shot model review now? [y/N]: ")
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
	documentIDs, err := resolveNowDocumentIDs(args, groupID)
	if err != nil {
		return err
	}

	nowOpts := api.ReviewNowOptions{
		GroupID:     groupID,
		DocumentIDs: documentIDs,
		MaxFindings: reviewNowMaxFindings,
		Model:       strings.TrimSpace(reviewNowModel),
	}
	quote, err := client.ReviewNowQuote(nowOpts)
	if err != nil {
		return fmt.Errorf("could not fetch review --now quote: %w", err)
	}
	if quoteID := nowQuoteString(quote.Meta, "quote_id"); quoteID != "" {
		nowOpts.QuoteID = quoteID
	}
	if canRun, reason := nowQuoteCanRun(quote.Meta); !canRun {
		printNowReviewQuote(groupID, documentIDs, &quote)
		if reason == "insufficient_credits" {
			checkout, checkoutErr := client.CreateReviewNowCreditCheckout()
			if checkoutErr == nil && strings.TrimSpace(checkout.URL) != "" {
				return fmt.Errorf("review --now needs prepaid Cloud credits for this quote. Buy credits here: %s", strings.TrimSpace(checkout.URL))
			}
			if checkoutErr != nil {
				return fmt.Errorf("review --now needs prepaid Cloud credits for this quote, but checkout could not be created: %w", checkoutErr)
			}
		}
		if reason == "" {
			reason = "not allowed"
		}
		return fmt.Errorf("review --now quote is blocked: %s", reason)
	}
	if err := confirmNowReview(groupID, documentIDs, &quote); err != nil {
		return err
	}

	resp, err := client.ReviewNow(nowOpts)
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
