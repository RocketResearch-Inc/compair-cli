package compair

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	fsutil "github.com/RocketResearch-Inc/compair-cli/internal/fs"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type feedbackNotificationMeta struct {
	EventID        string
	Intent         string
	Severity       string
	DeliveryAction string
	CreatedAt      string
	Rationale      []string
	Rank           int
}

type feedbackRenderItem struct {
	Feedback api.FeedbackEntry
	Meta     *feedbackNotificationMeta
}

var writeMD string
var syncAll bool
var commitLimit int
var extDetail bool
var fetchOnly bool
var pushOnly bool
var feedbackWaitSec int
var snapshotMaxTree int
var snapshotMaxFiles int
var snapshotMaxTotalBytes int
var snapshotMaxFileBytes int
var snapshotMaxFileRead int
var snapshotMode string
var snapshotInclude []string
var snapshotExclude []string
var syncDryRun bool
var syncJSON bool
var syncGate string
var syncFailOnFeedback int
var syncFailOnSeverity []string
var syncFailOnType []string
var syncProcessTimeoutSec int

type syncInvocationMode struct {
	FetchOnly bool
	PushOnly  bool
}

var syncCmd = &cobra.Command{
	Use:   "sync [PATH ...]",
	Short: "Process recent changes and/or fetch feedback",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSyncCommand(cmd, args, syncInvocationMode{
			FetchOnly: fetchOnly,
			PushOnly:  pushOnly,
		})
	},
}

func runSyncCommand(cmd *cobra.Command, args []string, modeFlags syncInvocationMode) error {
	exitEarly, err := applySyncGatePreset(cmd)
	if err != nil {
		return err
	}
	if exitEarly {
		return nil
	}

	startedAt := time.Now()
	if modeFlags.FetchOnly && modeFlags.PushOnly {
		return fmt.Errorf("cannot combine --fetch-only with --push-only")
	}
	client := api.NewClient(viper.GetString("api.base"))
	caps, _ := client.Capabilities(10 * time.Minute)
	gclient := api.NewClient(viper.GetString("api.base"))
	group, _, err := groups.ResolveWithAuto(gclient, "", viper.GetString("group"))
	if err != nil {
		return fmt.Errorf("%w\nTip: run 'compair group ls' then 'compair group use <id>' (or pass --group).", err)
	}
	mode, err := normalizeSnapshotMode(snapshotMode)
	if err != nil {
		return err
	}
	snapshotMode = mode
	doUpload := !modeFlags.FetchOnly
	doFetch := !modeFlags.PushOnly
	waitForFeedback := doFetch && feedbackWaitSec > 0
	updatedDocs := map[string]struct{}{}
	gatedDocIDs := map[string]struct{}{}

	// Determine target repo roots
	roots := map[string]struct{}{}
	if len(args) > 0 {
		for _, p := range args {
			ap, _ := filepath.Abs(p)
			dir := ap
			fi, err := os.Stat(ap)
			if err == nil && !fi.IsDir() {
				dir = filepath.Dir(ap)
			}
			if r, err := git.RepoRootAt(dir); err == nil {
				roots[r] = struct{}{}
			}
		}
	} else if syncAll {
		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()
		rs, err := store.ListRepoRoots(cmd.Context(), group)
		if err != nil {
			return err
		}
		for _, r := range rs {
			roots[r] = struct{}{}
		}
	} else {
		if r, err := git.RepoRoot(); err == nil {
			roots[r] = struct{}{}
		}
		if len(roots) == 0 {
			// fallback: DB items under CWD
			store, err := db.Open()
			if err == nil {
				defer store.Close()
				cwd, _ := os.Getwd()
				items, _ := store.ListUnderPrefix(context.Background(), cwd, group)
				for _, it := range items {
					if it.RepoRoot != "" {
						roots[it.RepoRoot] = struct{}{}
					}
				}
			}
		}
	}

	if len(roots) == 0 {
		printer.Info("No repositories found to sync (continuing with files).")
	}
	rootList := make([]string, 0, len(roots))
	for root := range roots {
		rootList = append(rootList, root)
	}
	sort.Strings(rootList)
	repoProgress := newRepoProgressTracker(len(rootList))

	totalFeedback := 0
	reportPath := ""
	lines := []string{}

	snapshotOpts := resolveSnapshotOptions(cmd)
	if syncDryRun {
		if modeFlags.FetchOnly || modeFlags.PushOnly {
			printer.Warn("--fetch-only/--push-only ignored in --dry-run mode.")
		}
		if len(roots) == 0 {
			printer.Info("Dry-run only applies to repos; no repositories found.")
			return nil
		}
		ids := make([]string, 0, len(roots))
		for root := range roots {
			ids = append(ids, root)
		}
		sort.Strings(ids)
		for _, root := range ids {
			repoCfg := loadRepoConfig(root)
			payload, label, err := buildSyncPayload(root, group, repoCfg, snapshotOpts, snapshotMode)
			if err != nil {
				printer.Warn(fmt.Sprintf("Dry-run failed for %s: %v", root, err))
				continue
			}
			if strings.TrimSpace(payload) == "" {
				printer.Info("No payload for " + label)
				continue
			}
			fmt.Println("----- " + label + " -----")
			fmt.Println(payload)
			fmt.Println()
		}
		return nil
	}
	if doUpload {
		for idx, root := range rootList {
			if !caps.Inputs.Repos {
				printer.Warn(fmt.Sprintf("Skipping repo sync for %s: server does not support repository inputs (try 'compair profile use cloud').", root))
				continue
			}
			// Load repo-local config for document ID and last commit
			cfg, err := config.ReadProjectConfig(root)
			if err != nil {
				continue
			}
			if len(cfg.Repos) == 0 || cfg.Repos[0].DocumentID == "" {
				continue
			}
			r := &cfg.Repos[0]
			repoLabel := repoDisplayLabel(root, r.RemoteURL)
			repoStartedAt := time.Now()
			printer.Info(fmt.Sprintf("[%d/%d] Processing %s", idx+1, len(rootList), repoLabel))
			if !r.Unpublished {
				ensureRepoDocumentPublished(client, r.DocumentID, root)
			}
			gatedDocIDs[r.DocumentID] = struct{}{}
			if doFetch && strings.TrimSpace(r.PendingTaskID) != "" {
				printer.Info(fmt.Sprintf("[%d/%d] Resuming pending processing task for %s", idx+1, len(rootList), repoLabel))
				st, timedOut, err := waitForProcessingTask(cmd.Context(), client, r.PendingTaskID, func(elapsed time.Duration) {
					printer.Info(repoProgress.waitingLine(idx+1, repoLabel, elapsed))
				})
				if err != nil {
					return err
				}
				if timedOut {
					return fmt.Errorf(
						"processing timeout after %ds while waiting for the saved task for %s (rerun 'compair sync' to continue waiting without resubmitting)",
						syncProcessTimeoutSec,
						r.RemoteURL,
					)
				}
				switch strings.ToUpper(strings.TrimSpace(st.Status)) {
				case "SUCCESS":
					appendRepoServerResponse(&lines, r.RemoteURL, "", st.Result, false)
					latest := strings.TrimSpace(r.PendingTaskCommit)
					if latest != "" {
						finalizeRepoSync(root, group, cfg, r, latest)
					} else {
						clearPendingRepoTask(root, cfg, r)
					}
					updatedDocs[r.DocumentID] = struct{}{}
					printer.Info(repoProgress.completeLine(idx+1, repoLabel, time.Since(repoStartedAt)))
					if waitForFeedback {
						waitForNewFeedback(
							cmd.Context(),
							client,
							r.DocumentID,
							r.PendingTaskInitialFeedback,
							time.Duration(feedbackWaitSec)*time.Second,
							func(elapsed time.Duration, remaining time.Duration) {
								printer.Info(feedbackWaitLine(idx+1, len(rootList), repoLabel, elapsed, remaining))
							},
						)
					}
					continue
				case "PENDING":
					return fmt.Errorf(
						"saved processing task for %s is still pending (rerun 'compair sync' to continue waiting)",
						r.RemoteURL,
					)
				default:
					printer.Warn(fmt.Sprintf("Saved processing task for %s ended with status %s; resubmitting current changes", r.RemoteURL, st.Status))
					clearPendingRepoTask(root, cfg, r)
				}
			}
			initialCount := 0
			if waitForFeedback {
				initialCount = feedbackCount(client, r.DocumentID)
			}
			snapshotUsed := false
			var text string
			var latest string
			if snapshotMode == "snapshot" || (snapshotMode == "auto" && strings.TrimSpace(r.LastSyncedCommit) == "") {
				snapshotUsed = true
				res, err2 := buildRepoSnapshot(root, group, r, snapshotOpts)
				if err2 == nil {
					text = res.Text
					latest = res.Head
					maybeWarnSnapshotScope(root, res.Stats, snapshotOpts)
				}
				err = err2
				if err != nil {
					printer.Warn(fmt.Sprintf("Snapshot failed for %s: %v (falling back to diff mode)", r.RemoteURL, err))
					snapshotUsed = false
				}
			}
			if !snapshotUsed {
				text, latest = git.CollectChangeTextAtWithLimit(root, r.LastSyncedCommit, commitLimit, extDetail)
			}
			if strings.TrimSpace(text) == "" {
				printer.Info(fmt.Sprintf("[%d/%d] No new changes for %s", idx+1, len(rootList), repoLabel))
				continue
			}
			var resp api.ProcessDocResp
			if snapshotUsed {
				resp, err = client.ProcessDocWithMode(r.DocumentID, text, true, "client")
			} else {
				resp, err = client.ProcessDoc(r.DocumentID, text, true)
			}
			if err != nil {
				return err
			}
			if doFetch {
				persistPendingRepoTask(root, cfg, r, resp.TaskID, latest, initialCount)
				if strings.TrimSpace(resp.TaskID) != "" {
					printer.Info(fmt.Sprintf("[%d/%d] Submitted %s; waiting for server task %s", idx+1, len(rootList), repoLabel, shortTaskID(resp.TaskID)))
				}
				st, timedOut, err := waitForProcessingTask(cmd.Context(), client, resp.TaskID, func(elapsed time.Duration) {
					printer.Info(repoProgress.waitingLine(idx+1, repoLabel, elapsed))
				})
				if err != nil {
					return err
				}
				if timedOut {
					return fmt.Errorf(
						"processing timeout after %ds (rerun 'compair sync' to continue waiting without resubmitting this repo; increase with --process-timeout-sec or set 0 to wait indefinitely)",
						syncProcessTimeoutSec,
					)
				}
				appendRepoServerResponse(&lines, r.RemoteURL, text, st.Result, snapshotUsed)
			} else {
				if snapshotUsed {
					printer.Info("Uploaded baseline snapshot for " + r.RemoteURL)
				} else {
					printer.Info("Uploaded changes for " + r.RemoteURL)
				}
			}
			finalizeRepoSync(root, group, cfg, r, latest)
			updatedDocs[r.DocumentID] = struct{}{}
			printer.Info(repoProgress.completeLine(idx+1, repoLabel, time.Since(repoStartedAt)))
			if waitForFeedback {
				waitForNewFeedback(
					cmd.Context(),
					client,
					r.DocumentID,
					initialCount,
					time.Duration(feedbackWaitSec)*time.Second,
					func(elapsed time.Duration, remaining time.Duration) {
						printer.Info(feedbackWaitLine(idx+1, len(rootList), repoLabel, elapsed, remaining))
					},
				)
			}
		}
	}

	// Non-git tracked files under scope
	// Determine scopes to search (args or CWD)
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
	if store, err := db.Open(); err == nil {
		for _, sc := range scopes {
			items, _ := store.ListUnderPrefix(cmd.Context(), sc, group)
			for _, it := range items {
				if it.Kind == "repo" {
					continue
				}
				fi, err := os.Stat(it.Path)
				if err != nil || fi.IsDir() {
					continue
				}

				mode, err := detectFileProcessingMode(it.Path)
				if err != nil {
					printer.Warn(fmt.Sprintf("Skipping %s: %v", it.Path, err))
					continue
				}
				if !mode.Supported {
					if mode.Warning != "" {
						printer.Warn(fmt.Sprintf("Skipping %s: %s", it.Path, mode.Warning))
					} else {
						printer.Warn(fmt.Sprintf("Skipping %s: unsupported file type", it.Path))
					}
					continue
				}

				hash, size, mtime, err := fsutil.FastHash(it.Path)
				if err != nil {
					printer.Warn(fmt.Sprintf("Hash error for %s: %v", it.Path, err))
					continue
				}
				if !fetchOnly && it.ContentHash == hash && it.ContentHash != "" {
					continue
				}

				docID := it.DocumentID
				initialCount := 0
				if waitForFeedback && docID != "" {
					initialCount = feedbackCount(client, docID)
				}
				needContent := docID == "" || !fetchOnly
				var content string
				if needContent {
					switch mode.Mode {
					case fileModeText:
						if !caps.Inputs.Text {
							printer.Warn(fmt.Sprintf("Skipping %s: server does not accept text uploads (switch to a profile that supports text inputs).", it.Path))
							continue
						}
						bytes, err := os.ReadFile(it.Path)
						if err != nil {
							printer.Warn(fmt.Sprintf("Read error for %s: %v", it.Path, err))
							continue
						}
						content = string(bytes)
					case fileModeOCR:
						if !caps.Inputs.OCR {
							printer.Warn(fmt.Sprintf("Skipping %s: OCR is not available on this server (try 'compair profile use cloud').", it.Path))
							continue
						}
						if mode.Warning != "" {
							printer.Info(fmt.Sprintf("%s", mode.Warning))
						}
						extracted, err := runOCROnFile(cmd, client, it.Path, docID)
						if err != nil {
							printer.Warn(fmt.Sprintf("OCR failed for %s: %v", it.Path, err))
							continue
						}
						content = extracted
					}
				}

				if needContent && strings.TrimSpace(content) == "" {
					printer.Warn(fmt.Sprintf("No text extracted from %s; skipping", it.Path))
					continue
				}

				if docID == "" {
					doc, err := client.CreateDoc(filepath.Base(it.Path), "file", content, group, false)
					if err != nil {
						printer.Warn(fmt.Sprintf("Create document failed for %s: %v", it.Path, err))
						continue
					}
					docID = doc.DocumentID
					initialCount = 0
				}
				if docID != "" {
					gatedDocIDs[docID] = struct{}{}
				}

				if fetchOnly {
					if docID != it.DocumentID {
						_ = store.UpsertItem(cmd.Context(), &db.TrackedItem{Path: it.Path, Kind: it.Kind, GroupID: it.GroupID, DocumentID: docID, ContentHash: hash, Size: size, MTime: mtime, LastSyncedAt: time.Now().Unix(), Published: it.Published})
					}
					continue
				}

				syncStamp := time.Now().Unix()

				resp, err := client.ProcessDoc(docID, content, true)
				if err != nil {
					printer.Warn(fmt.Sprintf("Process failed for %s: %v", it.Path, err))
					continue
				}
				var st api.TaskStatus
				if strings.TrimSpace(resp.TaskID) == "" {
					st.Status = "SUCCESS"
					st.Result = map[string]any{"detail": "processing completed locally"}
				} else if doFetch {
					deadline := processingDeadline()
					for {
						st, err = client.GetTaskStatus(resp.TaskID)
						if err == nil && st.Status == "SUCCESS" {
							break
						}
						if !deadline.IsZero() && time.Now().After(deadline) {
							printer.Warn(fmt.Sprintf("Processing timeout for %s after %ds", it.Path, syncProcessTimeoutSec))
							break
						}
						time.Sleep(2 * time.Second)
					}
				}

				if doFetch {
					lines = append(lines, "File: "+it.Path)
					lines = append(lines, "Server Response:")
					if st.Result != nil {
						bb, _ := json.MarshalIndent(st.Result, "", "  ")
						lines = append(lines, strings.Split(strings.TrimSpace(string(bb)), "\n")...)
					} else {
						lines = append(lines, "Processing completed.")
					}
				} else {
					printer.Info("Uploaded " + it.Path)
				}
				_ = store.UpsertItem(cmd.Context(), &db.TrackedItem{Path: it.Path, Kind: it.Kind, GroupID: it.GroupID, DocumentID: docID, ContentHash: hash, Size: size, MTime: mtime, LastSyncedAt: syncStamp, Published: it.Published})
				updatedDocs[docID] = struct{}{}
				if waitForFeedback {
					waitForNewFeedback(
						cmd.Context(),
						client,
						docID,
						initialCount,
						time.Duration(feedbackWaitSec)*time.Second,
						func(elapsed time.Duration, remaining time.Duration) {
							printer.Info(fmt.Sprintf("Waiting for new feedback on %s (%s elapsed, est. %s remaining)", filepath.Base(it.Path), humanDuration(elapsed), humanDuration(remaining)))
						},
					)
				}
			}
		}
		_ = store.Close()
	}

	if doFetch {
		// Fetch feedback for repos and files
		docSet := map[string]struct{}{}
		for _, root := range rootList {
			cfg, err := config.ReadProjectConfig(root)
			if err == nil && len(cfg.Repos) > 0 && cfg.Repos[0].DocumentID != "" {
				docSet[cfg.Repos[0].DocumentID] = struct{}{}
				gatedDocIDs[cfg.Repos[0].DocumentID] = struct{}{}
			}
		}
		if store2, err := db.Open(); err == nil {
			items, _ := store2.ListByGroup(cmd.Context(), group)
			for _, it := range items {
				if it.DocumentID != "" {
					docSet[it.DocumentID] = struct{}{}
					gatedDocIDs[it.DocumentID] = struct{}{}
				}
			}
			_ = store2.Close()
		}
		for docID := range updatedDocs {
			docSet[docID] = struct{}{}
		}
		cachePath := filepath.Join(".compair", "feedback_cache.json")
		cache, err := loadFeedbackCache(cachePath)
		if err != nil {
			printer.Warn(fmt.Sprintf("Could not load feedback cache: %v", err))
			cache = map[string]map[string]struct{}{}
		}
		ids := make([]string, 0, len(docSet))
		for docID := range docSet {
			ids = append(ids, docID)
		}
		sort.Strings(ids)
		notificationIndex := buildNotificationIndex(client, group)
		for _, docID := range ids {
			doc, _ := client.GetDocumentByID(docID)
			title := strings.TrimSpace(doc.Title)
			if title == "" {
				title = "(untitled)"
			}
			fbs, err := client.ListFeedback(docID)
			if err != nil {
				continue
			}
			if len(fbs) == 0 {
				continue
			}
			needChunkLookup := false
			for _, fb := range fbs {
				if strings.TrimSpace(fb.ChunkContent) == "" {
					needChunkLookup = true
					break
				}
			}
			cmap := map[string]string{}
			if needChunkLookup {
				chunks, _ := client.LoadChunks(docID)
				for _, ch := range chunks {
					c := ch.Content
					if c == "" {
						c = ch.Text
					}
					cmap[ch.ChunkID] = c
				}
			}
			legacyRefs := map[string][]api.Reference{}
			seen := cache[docID]
			if seen == nil {
				seen = map[string]struct{}{}
			}
			items := make([]feedbackRenderItem, 0, len(fbs))
			for _, fb := range fbs {
				if _, ok := seen[fb.FeedbackID]; ok {
					continue
				}
				items = append(items, feedbackRenderItem{
					Feedback: fb,
					Meta:     notificationIndex[fb.ChunkID],
				})
			}
			sortFeedbackRenderItems(items)
			newLines := []string{}
			for _, item := range items {
				fb := item.Feedback
				totalFeedback++
				seen[fb.FeedbackID] = struct{}{}
				ts := fmt.Sprint(fb.Timestamp)
				entry := []string{fmt.Sprintf("- Time: %s", ts)}
				if item.Meta != nil {
					rankLine := fmt.Sprintf(
						"  Notification: %s %s via %s",
						strings.ToUpper(strings.TrimSpace(item.Meta.Severity)),
						item.Meta.Intent,
						item.Meta.DeliveryAction,
					)
					if item.Meta.Rank > 0 {
						rankLine += fmt.Sprintf(" (rank %d)", item.Meta.Rank)
					}
					entry = append(entry, rankLine)
					if strings.TrimSpace(item.Meta.CreatedAt) != "" {
						entry = append(entry, "  Notification time: "+item.Meta.CreatedAt)
					}
					if len(item.Meta.Rationale) > 0 {
						entry = append(entry, "  Rationale:")
						for _, line := range item.Meta.Rationale {
							if trimmed := strings.TrimSpace(line); trimmed != "" {
								entry = append(entry, "    - "+trimmed)
							}
						}
					}
				}
				ctx := strings.TrimSpace(fb.ChunkContent)
				if ctx == "" {
					ctx = cmap[fb.ChunkID]
				}
				if ctx != "" {
					ctx = trimContext(ctx, 12, 500)
					entry = append(entry, "  Context: "+ctx)
				}
				entry = append(entry, "  Feedback: "+strings.TrimSpace(fb.Feedback))
				if len(fb.References) > 0 {
					entry = append(entry, "  References:")
					for _, r := range fb.References {
						rtitle := strings.TrimSpace(r.Title)
						if rtitle == "" {
							rtitle = r.DocumentID
						}
						line := "    - " + rtitle
						if strings.TrimSpace(r.Author) != "" {
							line += " (author: " + r.Author + ")"
						}
						entry = append(entry, line)
					}
				} else {
					refs, ok := legacyRefs[fb.ChunkID]
					if !ok {
						refs, _ = client.LoadReferences(fb.ChunkID)
						legacyRefs[fb.ChunkID] = refs
					}
					if len(refs) > 0 {
						entry = append(entry, "  References:")
						for _, r := range refs {
							rtitle := strings.TrimSpace(r.Document.Title)
							if rtitle == "" {
								rtitle = r.Document.DocumentID
							}
							entry = append(entry, "    - "+rtitle+" (author: "+r.DocumentAuthor+")")
						}
					}
				}
				newLines = append(newLines, entry...)
				newLines = append(newLines, "")
			}
			if len(newLines) == 0 {
				cache[docID] = seen
				continue
			}
			if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
				newLines = newLines[:len(newLines)-1]
			}
			cache[docID] = seen
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, "Document: "+title+" ("+docID+")")
			lines = append(lines, newLines...)
		}
		if err := saveFeedbackCache(cachePath, cache); err != nil {
			printer.Warn(fmt.Sprintf("Could not update feedback cache: %v", err))
		}
	}

	if doFetch && len(lines) > 0 {
		outPath := writeMD
		if outPath == "" {
			outPath = defaultReportPath()
		}
		if filepath.Ext(outPath) == "" {
			outPath += ".md"
		}
		if err := printer.WriteMarkdownReport(outPath, "Compair Sync Report", lines); err != nil {
			return err
		}
		reportPath = outPath
		printer.Success("Markdown report written to " + outPath)
	}
	gateResult, gateErr := evaluateNotificationGate(client, group, gatedDocIDs, startedAt)

	if doFetch && totalFeedback == 0 {
		printer.Info("No new feedback generated.")
	}
	if gateErr != nil && detailedNotificationGateEnabled() {
		printer.Warn("Notification gate unavailable; falling back to count-based gating if configured.")
	}
	if syncJSON {
		payload := map[string]any{
			"group_id":            group,
			"gate_preset":         syncGate,
			"mode":                map[string]bool{"upload": doUpload, "fetch": doFetch},
			"repo_roots":          len(roots),
			"updated_documents":   len(updatedDocs),
			"new_feedback":        totalFeedback,
			"notification_gate":   gateResult,
			"process_timeout_sec": syncProcessTimeoutSec,
			"report_path":         reportPath,
			"generated_at":        time.Now().UTC().Format(time.RFC3339),
			"duration_ms":         time.Since(startedAt).Milliseconds(),
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(data))
	}

	if gateResult.Enabled && gateErr == nil {
		if gateResult.MatchedCount > 0 {
			return fmt.Errorf(
				"notification gate matched %d event(s): %s",
				gateResult.MatchedCount,
				strings.Join(gateResult.Matches, ", "),
			)
		}
		return nil
	}

	if syncFailOnFeedback > 0 && totalFeedback >= syncFailOnFeedback {
		if gateResult.Enabled && gateErr != nil {
			return fmt.Errorf(
				"notification gate unavailable (%v); fallback feedback threshold exceeded: %d >= %d",
				gateErr,
				totalFeedback,
				syncFailOnFeedback,
			)
		}
		return fmt.Errorf("new feedback threshold exceeded: %d >= %d", totalFeedback, syncFailOnFeedback)
	}
	return nil
}

func buildNotificationIndex(client *api.Client, group string) map[string]*feedbackNotificationMeta {
	index := map[string]*feedbackNotificationMeta{}
	resp, err := client.ListNotificationEvents(api.NotificationEventsOptions{
		GroupID:             group,
		Page:                1,
		PageSize:            200,
		IncludeAcknowledged: true,
		IncludeDismissed:    true,
	})
	if err != nil {
		return index
	}
	for _, event := range resp.Events {
		chunkID := strings.TrimSpace(event.TargetChunkID)
		if chunkID == "" {
			continue
		}
		meta := &feedbackNotificationMeta{
			EventID:        strings.TrimSpace(event.EventID),
			Intent:         strings.TrimSpace(event.Intent),
			Severity:       strings.TrimSpace(event.Severity),
			DeliveryAction: strings.TrimSpace(event.DeliveryAction),
			CreatedAt:      formatTimestamp(event.CreatedAt),
			Rationale:      event.Rationale,
			Rank:           notificationRank(event),
		}
		prev := index[chunkID]
		if prev == nil || meta.Rank > prev.Rank || (meta.Rank == prev.Rank && meta.CreatedAt > prev.CreatedAt) {
			index[chunkID] = meta
		}
	}
	return index
}

func sortFeedbackRenderItems(items []feedbackRenderItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		leftRank := 0
		rightRank := 0
		leftTime := fmt.Sprint(left.Feedback.Timestamp)
		rightTime := fmt.Sprint(right.Feedback.Timestamp)
		if left.Meta != nil {
			leftRank = left.Meta.Rank
			if left.Meta.CreatedAt != "" {
				leftTime = left.Meta.CreatedAt
			}
		}
		if right.Meta != nil {
			rightRank = right.Meta.Rank
			if right.Meta.CreatedAt != "" {
				rightTime = right.Meta.CreatedAt
			}
		}
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		return leftTime > rightTime
	})
}

func notificationRank(event api.NotificationEvent) int {
	score := 0
	switch strings.ToUpper(strings.TrimSpace(event.Severity)) {
	case "HIGH":
		score += 60
	case "MEDIUM":
		score += 35
	case "LOW":
		score += 15
	}
	switch strings.ToUpper(strings.TrimSpace(event.Certainty)) {
	case "HIGH":
		score += 22
	case "MEDIUM":
		score += 12
	case "LOW":
		score += 4
	}
	switch strings.ToUpper(strings.TrimSpace(event.Relevance)) {
	case "HIGH":
		score += 18
	case "MEDIUM":
		score += 10
	case "LOW":
		score += 3
	}
	switch strings.ToUpper(strings.TrimSpace(event.Novelty)) {
	case "HIGH":
		score += 12
	case "MEDIUM":
		score += 7
	case "LOW":
		score += 2
	}
	if strings.EqualFold(strings.TrimSpace(event.DeliveryAction), "push") {
		score += 15
	}
	switch strings.TrimSpace(event.Intent) {
	case "potential_conflict":
		score += 12
	case "relevant_update":
		score += 8
	case "hidden_overlap":
		score += 4
	case "quiet_validation":
		score += 1
	}
	return score
}

func defaultReportPath() string {
	_ = os.MkdirAll(".compair", 0o755)
	return filepath.Join(".compair", "latest_feedback_sync.md")
}

func init() {
	rootCmd.AddCommand(syncCmd)
	addSyncFlags(syncCmd, true)
}

const (
	fileModeText = "text"
	fileModeOCR  = "ocr"
)

type fileProcessingMode struct {
	Mode      string
	Supported bool
	Warning   string
}

func detectFileProcessingMode(path string) (fileProcessingMode, error) {
	info := fileProcessingMode{}
	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return info, err
	}
	buf = buf[:n]
	mimeType := http.DetectContentType(buf)
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")

	switch {
	case ext == "pdf" || mimeType == "application/pdf":
		info.Mode = fileModeOCR
		info.Supported = true
		info.Warning = fmt.Sprintf("Detected PDF (%s); performing OCR", filepath.Base(path))
		return info, nil
	case ext == "doc" || ext == "docx" || ext == "ppt" || ext == "pptx" || ext == "xls" || ext == "xlsx" || mimeType == "application/msword" || mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || mimeType == "application/vnd.ms-powerpoint" || mimeType == "application/vnd.openxmlformats-officedocument.presentationml.presentation" || mimeType == "application/vnd.ms-excel" || mimeType == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		info.Mode = fileModeOCR
		info.Supported = true
		info.Warning = fmt.Sprintf("Detected Office document (%s); performing OCR", filepath.Base(path))
		return info, nil
	}

	if strings.HasPrefix(mimeType, "text/") || mimeType == "application/json" || mimeType == "application/xml" {
		info.Mode = fileModeText
		info.Supported = true
		return info, nil
	}
	if looksLikeText(buf) {
		info.Mode = fileModeText
		info.Supported = true
		return info, nil
	}

	info.Supported = false
	if ext != "" {
		info.Warning = fmt.Sprintf("unsupported type .%s (%s)", ext, mimeType)
	} else {
		info.Warning = fmt.Sprintf("unsupported type (%s)", mimeType)
	}
	return info, nil
}

func looksLikeText(buf []byte) bool {
	if len(buf) == 0 {
		return true
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		return false
	}
	return utf8.Valid(buf)
}

func feedbackCount(client *api.Client, docID string) int {
	if docID == "" {
		return 0
	}
	fbs, err := client.ListFeedback(docID)
	if err != nil {
		return 0
	}
	return len(fbs)
}

func waitForNewFeedback(ctx context.Context, client *api.Client, docID string, prevCount int, timeout time.Duration, onProgress func(time.Duration, time.Duration)) {
	if docID == "" || timeout <= 0 {
		return
	}
	startedAt := time.Now()
	deadline := startedAt.Add(timeout)
	lastProgressAt := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		fbs, err := client.ListFeedback(docID)
		if err == nil && len(fbs) > prevCount {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		if onProgress != nil && (lastProgressAt.IsZero() || time.Since(lastProgressAt) >= 15*time.Second) {
			elapsed := time.Since(startedAt)
			remaining := time.Until(deadline)
			if remaining < 0 {
				remaining = 0
			}
			onProgress(elapsed, remaining)
			lastProgressAt = time.Now()
		}
	}
}

func processingDeadline() time.Time {
	if syncProcessTimeoutSec <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(syncProcessTimeoutSec) * time.Second)
}

func waitForProcessingTask(ctx context.Context, client *api.Client, taskID string, onProgress func(time.Duration)) (api.TaskStatus, bool, error) {
	deadline := processingDeadline()
	startedAt := time.Now()
	lastProgressAt := time.Time{}
	for {
		st, err := client.GetTaskStatus(taskID)
		if err != nil {
			return api.TaskStatus{}, false, err
		}
		switch strings.ToUpper(strings.TrimSpace(st.Status)) {
		case "SUCCESS":
			return st, false, nil
		case "FAILURE", "FAILED", "REVOKED":
			return st, false, nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return st, true, nil
		}
		if onProgress != nil && (lastProgressAt.IsZero() || time.Since(lastProgressAt) >= 30*time.Second) {
			onProgress(time.Since(startedAt))
			lastProgressAt = time.Now()
		}
		select {
		case <-ctx.Done():
			return api.TaskStatus{}, false, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

type repoProgressTracker struct {
	total         int
	completed     int
	totalDuration time.Duration
}

func newRepoProgressTracker(total int) *repoProgressTracker {
	return &repoProgressTracker{total: total}
}

func (t *repoProgressTracker) waitingLine(index int, label string, elapsed time.Duration) string {
	eta := t.estimateRemaining(index, elapsed)
	if eta > 0 {
		return fmt.Sprintf("[%d/%d] Still processing %s (%s elapsed, est. %s remaining)", index, t.total, label, humanDuration(elapsed), humanDuration(eta))
	}
	return fmt.Sprintf("[%d/%d] Still processing %s (%s elapsed)", index, t.total, label, humanDuration(elapsed))
}

func (t *repoProgressTracker) completeLine(index int, label string, elapsed time.Duration) string {
	t.completed++
	t.totalDuration += elapsed
	eta := t.estimateRemaining(index, 0)
	if eta > 0 {
		return fmt.Sprintf("[%d/%d] Completed %s in %s. Est. %s remaining.", index, t.total, label, humanDuration(elapsed), humanDuration(eta))
	}
	return fmt.Sprintf("[%d/%d] Completed %s in %s.", index, t.total, label, humanDuration(elapsed))
}

func (t *repoProgressTracker) estimateRemaining(index int, currentElapsed time.Duration) time.Duration {
	if t.total <= 0 {
		return 0
	}
	remaining := t.total - index
	if remaining <= 0 {
		return 0
	}
	var avg time.Duration
	if currentElapsed > 0 {
		if t.completed > 0 {
			avg = (t.totalDuration + currentElapsed) / time.Duration(t.completed+1)
		} else {
			avg = currentElapsed
		}
	} else if t.completed > 0 {
		avg = t.totalDuration / time.Duration(t.completed)
	}
	if avg <= 0 {
		return 0
	}
	return avg * time.Duration(remaining)
}

func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

func feedbackWaitLine(index int, total int, label string, elapsed time.Duration, remaining time.Duration) string {
	if total <= 0 {
		total = 1
	}
	return fmt.Sprintf("[%d/%d] Waiting for new feedback on %s (%s elapsed, est. %s remaining)", index, total, label, humanDuration(elapsed), humanDuration(remaining))
}

func shortTaskID(taskID string) string {
	value := strings.TrimSpace(taskID)
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func repoDisplayLabel(root, remote string) string {
	if trimmed := strings.TrimSpace(remote); trimmed != "" {
		return trimmed
	}
	return filepath.Base(root)
}

func appendRepoServerResponse(lines *[]string, remoteURL, text string, result any, snapshotUsed bool) {
	b, _ := json.MarshalIndent(result, "", "  ")
	label := "Repo: " + remoteURL
	if snapshotUsed {
		label += " (baseline snapshot)"
	}
	*lines = append(*lines, label)
	if strings.TrimSpace(text) != "" {
		*lines = append(*lines, "Changes:")
		*lines = append(*lines, strings.Split(strings.TrimSpace(text), "\n")...)
	}
	*lines = append(*lines, "Server Response:")
	*lines = append(*lines, strings.Split(strings.TrimSpace(string(b)), "\n")...)
}

func persistPendingRepoTask(root string, cfg config.Project, repo *config.Repo, taskID, latest string, initialFeedbackCount int) {
	if strings.TrimSpace(taskID) == "" {
		return
	}
	repo.PendingTaskID = taskID
	repo.PendingTaskCommit = latest
	repo.PendingTaskInitialFeedback = initialFeedbackCount
	repo.PendingTaskStartedAt = time.Now().UTC().Format(time.RFC3339)
	_ = config.WriteProjectConfig(root, cfg)
}

func clearPendingRepoTask(root string, cfg config.Project, repo *config.Repo) {
	if strings.TrimSpace(repo.PendingTaskID) == "" &&
		strings.TrimSpace(repo.PendingTaskCommit) == "" &&
		repo.PendingTaskInitialFeedback == 0 &&
		strings.TrimSpace(repo.PendingTaskStartedAt) == "" {
		return
	}
	repo.PendingTaskID = ""
	repo.PendingTaskCommit = ""
	repo.PendingTaskInitialFeedback = 0
	repo.PendingTaskStartedAt = ""
	_ = config.WriteProjectConfig(root, cfg)
}

func finalizeRepoSync(root, group string, cfg config.Project, repo *config.Repo, latest string) {
	repo.LastSyncedCommit = latest
	repo.PendingTaskID = ""
	repo.PendingTaskCommit = ""
	repo.PendingTaskInitialFeedback = 0
	repo.PendingTaskStartedAt = ""
	_ = config.WriteProjectConfig(root, cfg)
	upsertRepoWorkspaceBinding(root, group, repo.DocumentID, latest, repo.Unpublished)
}

func ensureRepoDocumentPublished(client *api.Client, docID, root string) {
	if strings.TrimSpace(docID) == "" {
		return
	}
	doc, err := client.GetDocumentByID(docID)
	if err != nil || doc.IsPublished {
		return
	}
	if err := client.SetDocumentPublished(docID, true); err != nil {
		printer.Warn(fmt.Sprintf("Could not auto-publish repo document for %s: %v", root, err))
		return
	}
	printer.Info("Auto-published repo document for " + root + " to enable cross-repo feedback")
}

func runOCROnFile(cmd *cobra.Command, client *api.Client, path, docID string) (string, error) {
	resp, err := client.UploadOCRFile(path, docID)
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		res, err := client.GetOCRFileResult(resp.TaskID)
		if err != nil {
			return "", err
		}
		switch strings.ToUpper(res.Status) {
		case "SUCCESS":
			var payload struct {
				ExtractedText string `json:"extracted_text"`
			}
			if len(res.Result) > 0 {
				if err := json.Unmarshal(res.Result, &payload); err != nil {
					return "", err
				}
			}
			if strings.TrimSpace(payload.ExtractedText) == "" {
				return "", fmt.Errorf("OCR returned empty text")
			}
			return payload.ExtractedText, nil
		case "FAILURE", "REVOKED":
			var msg string
			if len(res.Result) > 0 {
				_ = json.Unmarshal(res.Result, &msg)
			}
			if msg == "" {
				msg = fmt.Sprintf("status %s", res.Status)
			}
			return "", fmt.Errorf("%s", msg)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("OCR timeout for %s", filepath.Base(path))
		}
		select {
		case <-cmd.Context().Done():
			return "", cmd.Context().Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func loadFeedbackCache(path string) (map[string]map[string]struct{}, error) {
	cache := map[string]map[string]struct{}{}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cache, nil
		}
		return nil, err
	}
	var raw map[string][]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for docID, list := range raw {
		set := make(map[string]struct{}, len(list))
		for _, id := range list {
			if id != "" {
				set[id] = struct{}{}
			}
		}
		cache[docID] = set
	}
	return cache, nil
}

func saveFeedbackCache(path string, cache map[string]map[string]struct{}) error {
	raw := make(map[string][]string, len(cache))
	for docID, set := range cache {
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		raw[docID] = ids
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func trimContext(s string, maxLines int, maxChars int) string {
	if maxLines <= 0 {
		maxLines = 12
	}
	if maxChars <= 0 {
		maxChars = 500
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		head := strings.Join(lines[:maxLines/2], "\n")
		tail := strings.Join(lines[len(lines)-maxLines/2:], "\n")
		s = head + "\n...\n" + tail
	}
	if len(s) > maxChars {
		if maxChars < 20 {
			return s[:maxChars] + "..."
		}
		half := maxChars/2 - 3
		if half < 0 {
			half = maxChars / 2
		}
		s = s[:half] + " ... " + s[len(s)-half:]
	}
	return s
}

func normalizeSnapshotMode(mode string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "", "auto":
		return "auto", nil
	case "snapshot", "full", "baseline":
		return "snapshot", nil
	case "diff", "delta":
		return "diff", nil
	default:
		return "", fmt.Errorf("invalid --snapshot-mode %q (use auto, snapshot, or diff)", mode)
	}
}

func resolveSnapshotOptions(cmd *cobra.Command) snapshotOptions {
	opts := defaultSnapshotOptions()
	if prof := loadActiveProfileSnapshot(); prof != nil {
		applySnapshotOverrides(&opts, *prof)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-tree"); ok {
		opts.MaxTreeEntries = normalizeSnapshotLimit(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-files"); ok {
		opts.MaxFiles = normalizeSnapshotLimit(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-total-bytes"); ok {
		opts.MaxTotalBytes = normalizeSnapshotLimit(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-file-bytes"); ok {
		opts.MaxFileBytes = normalizeSnapshotLimit(v)
	}
	if v, ok := getIntFlagIfChanged(cmd, "snapshot-max-file-read"); ok {
		opts.MaxFileRead = normalizeSnapshotLimit(v)
	}
	if v, ok := getStringArrayFlagIfChanged(cmd, "snapshot-include"); ok {
		opts.IncludeGlobs = v
	}
	if v, ok := getStringArrayFlagIfChanged(cmd, "snapshot-exclude"); ok {
		opts.ExcludeGlobs = v
	}
	return opts
}

func applySnapshotOverrides(opts *snapshotOptions, cfg config.SnapshotConfig) {
	if cfg.MaxTreeEntries > 0 {
		opts.MaxTreeEntries = cfg.MaxTreeEntries
	}
	if cfg.MaxFiles > 0 {
		opts.MaxFiles = cfg.MaxFiles
	}
	if cfg.MaxTotalBytes > 0 {
		opts.MaxTotalBytes = cfg.MaxTotalBytes
	}
	if cfg.MaxFileBytes > 0 {
		opts.MaxFileBytes = cfg.MaxFileBytes
	}
	if cfg.MaxFileRead > 0 {
		opts.MaxFileRead = cfg.MaxFileRead
	}
	if len(cfg.IncludeGlobs) > 0 {
		opts.IncludeGlobs = cfg.IncludeGlobs
	}
	if len(cfg.ExcludeGlobs) > 0 {
		opts.ExcludeGlobs = cfg.ExcludeGlobs
	}
}

func addSyncFlags(cmd *cobra.Command, includeModeFlags bool) {
	defaultSnapshot := defaultSnapshotOptions()
	cmd.Flags().StringVar(&writeMD, "write-md", "", "Write a Markdown report to the given path")
	cmd.Flags().BoolVar(&syncAll, "all", false, "Sync all tracked repos in the active group")
	cmd.Flags().IntVar(&commitLimit, "commits", 10, "Number of commits to include when no prior sync exists")
	cmd.Flags().BoolVar(&extDetail, "ext-detail", false, "Include per-commit detailed patches and names in sync payload")
	if includeModeFlags {
		cmd.Flags().BoolVar(&fetchOnly, "fetch-only", false, "Only fetch feedback; do not send updates")
		cmd.Flags().BoolVar(&pushOnly, "push-only", false, "Submit local updates but skip feedback retrieval")
	}
	cmd.Flags().IntVar(&feedbackWaitSec, "feedback-wait", 45, "Seconds to wait for new feedback after submitting updates (0 to disable)")
	cmd.Flags().IntVar(&snapshotMaxTree, "snapshot-max-tree", defaultSnapshot.MaxTreeEntries, "Snapshot limit: max tree entries (0 = full repo)")
	cmd.Flags().IntVar(&snapshotMaxFiles, "snapshot-max-files", defaultSnapshot.MaxFiles, "Snapshot limit: max included files (0 = full repo)")
	cmd.Flags().IntVar(&snapshotMaxTotalBytes, "snapshot-max-total-bytes", defaultSnapshot.MaxTotalBytes, "Snapshot limit: total content budget in bytes (0 = full repo)")
	cmd.Flags().IntVar(&snapshotMaxFileBytes, "snapshot-max-file-bytes", defaultSnapshot.MaxFileBytes, "Snapshot limit: max bytes per file (0 = full file)")
	cmd.Flags().IntVar(&snapshotMaxFileRead, "snapshot-max-file-read", defaultSnapshot.MaxFileRead, "Snapshot limit: max bytes read per file (0 = no read cap)")
	cmd.Flags().StringVar(&snapshotMode, "snapshot-mode", "auto", "Snapshot mode: auto (baseline if no last_synced_commit), snapshot, diff")
	cmd.Flags().StringArrayVar(&snapshotInclude, "snapshot-include", nil, "Snapshot include glob (repeatable)")
	cmd.Flags().StringArrayVar(&snapshotExclude, "snapshot-exclude", nil, "Snapshot exclude glob (repeatable)")
	cmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "Show the payload that would be sent and exit")
	cmd.Flags().BoolVar(&syncJSON, "json", false, "Output machine-readable sync summary JSON")
	cmd.Flags().StringVar(&syncGate, "gate", "", "Named gating preset: off, api-contract, cross-product, review, strict (or 'help')")
	cmd.Flags().IntVar(&syncFailOnFeedback, "fail-on-feedback", 0, "Exit non-zero when new feedback count is at or above this threshold (0 disables)")
	cmd.Flags().StringArrayVar(&syncFailOnSeverity, "fail-on-severity", nil, "Primary gate: fail when new notification severity matches (repeatable or comma-separated)")
	cmd.Flags().StringArrayVar(&syncFailOnType, "fail-on-type", nil, "Primary gate: fail when new notification type/intent matches (repeatable or comma-separated)")
	cmd.Flags().IntVar(&syncProcessTimeoutSec, "process-timeout-sec", 600, "Max seconds to wait for backend processing per document (0 waits indefinitely)")
}

func normalizeSnapshotLimit(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func maybeWarnSnapshotScope(root string, stats snapshotStats, opts snapshotOptions) {
	capped := false
	if opts.MaxFiles > 0 && stats.TotalFiles > stats.IncludedFiles && stats.IncludedFiles >= opts.MaxFiles {
		capped = true
	}
	if opts.MaxTreeEntries > 0 && stats.TotalFiles > opts.MaxTreeEntries {
		capped = true
	}
	if opts.MaxTotalBytes > 0 && stats.IncludedBytes >= opts.MaxTotalBytes {
		capped = true
	}
	if opts.MaxFileRead > 0 && stats.TooLargeFiles > 0 {
		capped = true
	}
	if capped {
		printer.Warn(
			fmt.Sprintf(
				"Snapshot for %s is running with explicit limits. To index the full repo, rerun with the snapshot limits set to 0 (for example: --snapshot-max-files 0 --snapshot-max-total-bytes 0).",
				root,
			),
		)
		return
	}
	if stats.IncludedFiles >= 200 || stats.IncludedBytes >= 2_000_000 {
		printer.Warn(
			fmt.Sprintf(
				"Large full-repo snapshot for %s: %d files, %s sent. Compair does not currently advertise per-plan repo size caps; if this is slow or your worker rejects it, try --snapshot-max-total-bytes 300000 --snapshot-max-files 60.",
				root,
				stats.IncludedFiles,
				formatBytes(int64(stats.IncludedBytes)),
			),
		)
	}
}

func loadActiveProfileSnapshot() *config.SnapshotConfig {
	name := strings.TrimSpace(viper.GetString("profile.active"))
	if name == "" {
		return nil
	}
	profs, err := config.LoadProfiles()
	if err != nil {
		return nil
	}
	prof, ok := profs.Profiles[name]
	if !ok {
		return nil
	}
	return &prof.Snapshot
}
