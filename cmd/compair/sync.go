package compair

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	ParseMode      string
	Model          string
	Rationale      []string
	DedupeKey      string
	EvidenceTarget string
	EvidencePeer   string
	PeerDocIDs     []string
	Rank           int
}

type feedbackRenderItem struct {
	Feedback api.FeedbackEntry
	Meta     *feedbackNotificationMeta
}

type reportReferenceSummary struct {
	Title    string
	Author   string
	Excerpts int
}

type reportFeedbackSummary struct {
	DocumentCount       int
	FeedbackCount       int
	CollapsedDuplicates int
	ByIntent            map[string]int
	BySeverity          map[string]int
}

type reportDetailLevel int

const (
	reportDetailBrief reportDetailLevel = iota
	reportDetailDetailed
	reportDetailVerbose
)

type reportRenderOptions struct {
	DetailLevel  reportDetailLevel
	IncludeDebug bool
}

type feedbackEvidenceProfile struct {
	Intent    string
	Severity  string
	Timestamp time.Time
	Paths     map[string]struct{}
	Refs      map[string]struct{}
	Anchors   map[string]struct{}
	Tokens    map[string]struct{}
}

type taskProgressMeta struct {
	Stage               string
	Message             string
	StartedAt           time.Time
	LastProgressAt      time.Time
	TotalChunks         int
	NewChunksTotal      int
	IndexedChunksDone   int
	IndexedChunksTotal  int
	FeedbackChunksTotal int
	ChunkTaskIDs        []string
}

type pendingInitialSync struct {
	Root   string
	Label  string
	Config config.Project
}

type pairwiseRepo struct {
	Root       string
	DocumentID string
	Label      string
}

var (
	reportDiffPathPattern     = regexp.MustCompile(`(?m)^diff --git a/([^\s]+) b/[^\s]+$`)
	reportFileHeaderPattern   = regexp.MustCompile(`(?m)^### File:\s+([^\n(]+)`)
	reportBacktickCodePattern = regexp.MustCompile("`([^`\\n]+)`")
	reportTokenPattern        = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
	reportSpecialPathNames    = map[string]struct{}{
		".compairignore": {},
		".dockerignore":  {},
		".env":           {},
		".gitattributes": {},
		".gitignore":     {},
		".npmignore":     {},
		"dockerfile":     {},
		"license":        {},
		"makefile":       {},
		"readme":         {},
		"readme.md":      {},
	}
)

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
var syncReanalyzeExisting bool
var syncPairwise bool
var syncCrossRepoOnly bool

type syncInvocationMode struct {
	FetchOnly           bool
	PushOnly            bool
	AwaitProcessing     bool
	Detach              bool
	GenerateFeedback    *bool
	ReanalyzeExisting   bool
	ReportDetailLevel   *reportDetailLevel
	SkipInitialSyncWait bool
	SkipIndex           bool
}

var syncCmd = &cobra.Command{
	Use:          "sync [PATH ...]",
	Short:        "Process recent changes and/or fetch feedback",
	SilenceUsage: true,
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
	reportOptions := resolveReportRenderOptions(client)
	if modeFlags.ReportDetailLevel != nil {
		reportOptions.DetailLevel = *modeFlags.ReportDetailLevel
	}
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
	reanalyzeExisting := syncReanalyzeExisting || modeFlags.ReanalyzeExisting
	generateFeedback := true
	if modeFlags.GenerateFeedback != nil {
		generateFeedback = *modeFlags.GenerateFeedback
	}
	if reanalyzeExisting && snapshotMode != "snapshot" {
		return fmt.Errorf("--reanalyze-existing requires --snapshot-mode snapshot")
	}
	doUpload := !modeFlags.FetchOnly
	doFetch := !modeFlags.PushOnly
	if modeFlags.Detach {
		doUpload = true
		doFetch = false
	}
	if reanalyzeExisting && !doUpload {
		return fmt.Errorf("--reanalyze-existing requires an upload pass; remove --fetch-only")
	}
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

	if shouldWaitForPendingInitialSyncs(doUpload, generateFeedback, modeFlags) {
		if modeFlags.Detach {
			printer.Info("Waiting for unfinished initial syncs before submitting detached review work.")
		}
		if err := waitForPendingInitialSyncs(cmd.Context(), client, group); err != nil {
			return err
		}
	}
	waitForProcessing := !modeFlags.Detach && (doFetch || modeFlags.AwaitProcessing)
	if err := validatePairwiseMode(doUpload, waitForProcessing, generateFeedback, modeFlags); err != nil {
		return err
	}
	pairwiseScopes := map[string][]pairwiseRepo{}
	if syncPairwise {
		pairwiseScopes, err = resolvePairwiseRepoScopes(group, rootList, syncCrossRepoOnly)
		if err != nil {
			return err
		}
	}

	totalFeedback := 0
	reportPath := ""
	lines := []string{}
	feedbackSummary := reportFeedbackSummary{
		ByIntent:   map[string]int{},
		BySeverity: map[string]int{},
	}

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
			if waitForProcessing && strings.TrimSpace(r.PendingTaskID) != "" {
				if stale, age, cutoff := isPendingRepoTaskStale(r.PendingTaskStartedAt); stale {
					printer.Warn(
						fmt.Sprintf(
							"[%d/%d] Saved processing task for %s is stale (%s old; cutoff %s). Resubmitting current snapshot.",
							idx+1,
							len(rootList),
							repoLabel,
							humanDuration(age),
							humanDuration(cutoff),
						),
					)
					clearPendingRepoTask(root, cfg, r)
				}
			}
			if modeFlags.Detach && strings.TrimSpace(r.PendingTaskID) != "" {
				printer.Info(
					fmt.Sprintf(
						"[%d/%d] Reusing saved processing task for %s. Run 'compair wait' or 'compair status' to follow progress.",
						idx+1,
						len(rootList),
						repoLabel,
					),
				)
				continue
			}
			if waitForProcessing && strings.TrimSpace(r.PendingTaskID) != "" {
				printer.Info(fmt.Sprintf("[%d/%d] Resuming pending processing task for %s", idx+1, len(rootList), repoLabel))
				st, timedOut, err := waitForProcessingTask(cmd.Context(), client, r.PendingTaskID, func(st api.TaskStatus, elapsed time.Duration) {
					printer.Info(formatTaskProgressLine(idx+1, len(rootList), "Still processing", repoLabel, st, elapsed))
				})
				if err != nil {
					return err
				}
				if timedOut {
					return fmt.Errorf(
						"processing timeout after %ds while waiting for the saved task for %s (run 'compair wait --timeout 20m' or rerun the same command to continue waiting without resubmitting)",
						syncProcessTimeoutSec,
						r.RemoteURL,
					)
				}
				switch strings.ToUpper(strings.TrimSpace(st.Status)) {
				case "SUCCESS":
				case "PROGRESS", "STARTED":
					if len(extractChunkTaskIDsFromStatus(st)) == 0 {
						return fmt.Errorf(
							"saved processing task for %s is still in progress without recoverable chunk tasks",
							r.RemoteURL,
						)
					}
				case "PENDING":
					return fmt.Errorf(
						"saved processing task for %s is still pending (run 'compair wait' or rerun the same command to continue waiting)",
						r.RemoteURL,
					)
				default:
					printer.Warn(fmt.Sprintf("Saved processing task for %s ended with status %s; resubmitting current changes", r.RemoteURL, st.Status))
					clearPendingRepoTask(root, cfg, r)
					continue
				}
				chunkTaskIDs := extractChunkTaskIDsFromStatus(st)
				if err := waitForChunkTaskIDs(cmd.Context(), client, chunkTaskIDs, repoLabel, func(taskIndex int, taskTotal int, elapsed time.Duration) {
					printer.Info(
						fmt.Sprintf(
							"[%d/%d] Waiting for chunk task %d/%d for %s (%s elapsed)",
							idx+1,
							len(rootList),
							taskIndex,
							taskTotal,
							repoLabel,
							humanDuration(elapsed),
						),
					)
				}); err != nil {
					clearPendingRepoTaskOnTerminalChunkFailure(root, cfg, r, err)
					return err
				}
				if doFetch {
					appendRepoServerResponse(&lines, r.RemoteURL, "", st.Result, false, reportOptions)
				}
				latest := strings.TrimSpace(r.PendingTaskCommit)
				if latest != "" {
					finalizeRepoSync(root, group, cfg, r, latest)
				} else {
					clearPendingRepoTask(root, cfg, r)
				}
				updatedDocs[r.DocumentID] = struct{}{}
				printer.Info(repoProgress.completeLine(idx+1, repoLabel, time.Since(repoStartedAt)))
				if waitForFeedback && len(chunkTaskIDs) > 0 {
					waitForNewFeedback(
						cmd.Context(),
						client,
						r.DocumentID,
						feedbackSnapshotFromPending(r.PendingTaskInitialFeedback, r.PendingTaskInitialFeedbackLatest),
						time.Duration(feedbackWaitSec)*time.Second,
						func(elapsed time.Duration, remaining time.Duration) {
							printer.Info(feedbackWaitLine(idx+1, len(rootList), repoLabel, elapsed, remaining))
						},
					)
				}
				continue
			}
			initialFeedback := feedbackSnapshot{}
			if waitForFeedback {
				initialFeedback = feedbackSnapshotForDoc(client, r.DocumentID)
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
			if syncPairwise {
				peers := pairwiseScopes[root]
				if len(peers) == 0 {
					printer.Warn(fmt.Sprintf("[%d/%d] No eligible pairwise peers found for %s in the active group", idx+1, len(rootList), repoLabel))
					continue
				}
				pairFeedbackSnapshot := initialFeedback
				for _, peer := range peers {
					pairLabel := fmt.Sprintf("%s vs %s", repoLabel, peer.Label)
					resp, err := submitRepoProcessDoc(
						client,
						r.DocumentID,
						text,
						generateFeedback,
						snapshotUsed,
						reanalyzeExisting,
						modeFlags.SkipIndex,
						[]string{peer.DocumentID},
					)
					if err != nil {
						return err
					}
					if _, err := awaitRepoProcessDocTask(
						cmd.Context(),
						client,
						root,
						cfg,
						r,
						resp,
						latest,
						pairFeedbackSnapshot,
						pairLabel,
						idx+1,
						len(rootList),
						text,
						fmt.Sprintf("%s vs %s", r.RemoteURL, peer.Label),
						snapshotUsed,
						doFetch,
						reportOptions,
						&lines,
					); err != nil {
						return err
					}
					if waitForFeedback {
						waitForNewFeedback(
							cmd.Context(),
							client,
							r.DocumentID,
							pairFeedbackSnapshot,
							time.Duration(feedbackWaitSec)*time.Second,
							func(elapsed time.Duration, remaining time.Duration) {
								printer.Info(
									fmt.Sprintf(
										"[%d/%d] Waiting for new feedback on %s (%s elapsed, est. %s remaining)",
										idx+1,
										len(rootList),
										pairLabel,
										humanDuration(elapsed),
										humanDuration(remaining),
									),
								)
							},
						)
						pairFeedbackSnapshot = feedbackSnapshotForDoc(client, r.DocumentID)
					}
				}
				finalizeRepoSync(root, group, cfg, r, latest)
				updatedDocs[r.DocumentID] = struct{}{}
				printer.Info(repoProgress.completeLine(idx+1, repoLabel, time.Since(repoStartedAt)))
				continue
			}

			resp, err := submitRepoProcessDoc(
				client,
				r.DocumentID,
				text,
				generateFeedback,
				snapshotUsed,
				reanalyzeExisting,
				modeFlags.SkipIndex,
				nil,
			)
			if err != nil {
				return err
			}
			if modeFlags.Detach {
				if strings.TrimSpace(resp.TaskID) != "" {
					persistPendingRepoTask(root, cfg, r, resp.TaskID, latest, initialFeedback)
					printer.Info(
						fmt.Sprintf(
							"[%d/%d] Submitted %s in the background as task %s. Run 'compair wait' or 'compair status' to follow progress.",
							idx+1,
							len(rootList),
							repoLabel,
							shortTaskID(resp.TaskID),
						),
					)
					continue
				}
				if snapshotUsed {
					printer.Info("Uploaded baseline snapshot for " + r.RemoteURL)
				} else {
					printer.Info("Uploaded changes for " + r.RemoteURL)
				}
				finalizeRepoSync(root, group, cfg, r, latest)
				updatedDocs[r.DocumentID] = struct{}{}
				printer.Info(repoProgress.completeLine(idx+1, repoLabel, time.Since(repoStartedAt)))
				continue
			}
			var st api.TaskStatus
			if waitForProcessing {
				st, err = awaitRepoProcessDocTask(
					cmd.Context(),
					client,
					root,
					cfg,
					r,
					resp,
					latest,
					initialFeedback,
					repoLabel,
					idx+1,
					len(rootList),
					text,
					r.RemoteURL,
					snapshotUsed,
					doFetch,
					reportOptions,
					&lines,
				)
				if err != nil {
					return err
				}
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
			if waitForFeedback && (!waitForProcessing || strings.TrimSpace(resp.TaskID) == "" || len(extractChunkTaskIDsFromStatus(st)) > 0) {
				waitForNewFeedback(
					cmd.Context(),
					client,
					r.DocumentID,
					initialFeedback,
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
				initialFeedback := feedbackSnapshot{}
				if waitForFeedback && docID != "" {
					initialFeedback = feedbackSnapshotForDoc(client, docID)
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
					initialFeedback = feedbackSnapshot{}
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

				resp, err := client.ProcessDoc(docID, content, generateFeedback)
				if err != nil {
					printer.Warn(fmt.Sprintf("Process failed for %s: %v", it.Path, err))
					continue
				}
				var st api.TaskStatus
				if strings.TrimSpace(resp.TaskID) == "" {
					st.Status = "SUCCESS"
					st.Result = map[string]any{"detail": "processing completed locally"}
				} else if waitForProcessing {
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

				if waitForProcessing {
					if err := waitForChunkTasks(cmd.Context(), client, st.Result, filepath.Base(it.Path), func(taskIndex int, taskTotal int, elapsed time.Duration) {
						printer.Info(
							fmt.Sprintf(
								"Waiting for chunk task %d/%d for %s (%s elapsed)",
								taskIndex,
								taskTotal,
								filepath.Base(it.Path),
								humanDuration(elapsed),
							),
						)
					}); err != nil {
						printer.Warn(fmt.Sprintf("Chunk processing failed for %s: %v", it.Path, err))
						continue
					}
					if doFetch && reportOptions.IncludeDebug {
						lines = append(lines, "File: "+it.Path)
						lines = append(lines, "Server Response:")
						if st.Result != nil {
							bb, _ := json.MarshalIndent(st.Result, "", "  ")
							lines = append(lines, strings.Split(strings.TrimSpace(string(bb)), "\n")...)
						} else {
							lines = append(lines, "Processing completed.")
						}
					}
				} else {
					printer.Info("Uploaded " + it.Path)
				}
				_ = store.UpsertItem(cmd.Context(), &db.TrackedItem{Path: it.Path, Kind: it.Kind, GroupID: it.GroupID, DocumentID: docID, ContentHash: hash, Size: size, MTime: mtime, LastSyncedAt: syncStamp, Published: it.Published})
				updatedDocs[docID] = struct{}{}
				if waitForFeedback && (!waitForProcessing || strings.TrimSpace(resp.TaskID) == "" || len(extractChunkTaskIDs(st.Result)) > 0) {
					waitForNewFeedback(
						cmd.Context(),
						client,
						docID,
						initialFeedback,
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
			doc, _ := client.GetDocumentMetadataByID(docID)
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
			items, collapsedCount := collapseDuplicateFeedbackItems(items)
			feedbackSummary.CollapsedDuplicates += collapsedCount
			newLines := []string{}
			for itemIdx, item := range items {
				fb := item.Feedback
				totalFeedback++
				seen[fb.FeedbackID] = struct{}{}
				ts := fmt.Sprint(fb.Timestamp)
				entry := []string{feedbackHeading(item, itemIdx+1), ""}
				entry = append(entry, "**Time:** "+ts)
				if item.Meta != nil {
					if label := humanizeIntentLabel(item.Meta.Intent); label != "" {
						entry = append(entry, "**Type:** "+label)
					}
					if severity := strings.ToUpper(strings.TrimSpace(item.Meta.Severity)); severity != "" {
						entry = append(entry, "**Severity:** "+severity)
					}
					if delivery := strings.TrimSpace(item.Meta.DeliveryAction); delivery != "" {
						entry = append(entry, "**Delivery:** "+delivery)
					}
					if strings.TrimSpace(item.Meta.CreatedAt) != "" {
						entry = append(entry, "**Notification Time:** "+item.Meta.CreatedAt)
					}
					if includeReportDebugMetadata(reportOptions) {
						if parseMode := strings.TrimSpace(item.Meta.ParseMode); parseMode != "" {
							entry = append(entry, "**Scoring Parse Mode:** "+parseMode)
						}
						if len(item.Meta.PeerDocIDs) > 0 {
							entry = append(entry, "**Peer Docs:** "+strings.Join(item.Meta.PeerDocIDs, ", "))
						}
					}
					if len(item.Meta.Rationale) > 0 {
						entry = append(entry, "", "**Notification Rationale**")
						for _, line := range item.Meta.Rationale {
							if trimmed := strings.TrimSpace(line); trimmed != "" {
								entry = append(entry, "- "+trimmed)
							}
						}
					}
				}
				appendFeedbackComparedFiles(&entry, item, reportOptions)
				appendFeedbackEvidence(&entry, item.Meta, reportOptions)
				feedbackSummary.FeedbackCount++
				if item.Meta != nil {
					intentKey := strings.TrimSpace(item.Meta.Intent)
					if intentKey != "" {
						feedbackSummary.ByIntent[intentKey]++
					}
					severityKey := strings.ToLower(strings.TrimSpace(item.Meta.Severity))
					if severityKey != "" {
						feedbackSummary.BySeverity[severityKey]++
					}
				}
				ctx := strings.TrimSpace(fb.ChunkContent)
				if ctx == "" {
					ctx = cmap[fb.ChunkID]
				}
				appendFeedbackContext(&entry, ctx, reportOptions)
				appendFeedbackReferenceExcerpts(&entry, fb.References, fb.Feedback, ctx, item.Meta, reportOptions)
				entry = append(entry, "**Feedback**", "")
				entry = append(entry, strings.Split(strings.TrimSpace(fb.Feedback), "\n")...)
				entry = append(entry, "")
				if len(fb.References) > 0 {
					refSummaries := summarizeFeedbackReferences(fb.References, nil)
					if len(refSummaries) > 0 {
						entry = append(entry, "**References**")
						for _, ref := range refSummaries {
							entry = append(entry, "- "+formatReportReference(ref))
						}
						entry = append(entry, "")
					}
				} else {
					refs, ok := legacyRefs[fb.ChunkID]
					if !ok {
						refs, _ = client.LoadReferences(fb.ChunkID)
						legacyRefs[fb.ChunkID] = refs
					}
					refSummaries := summarizeFeedbackReferences(nil, refs)
					if len(refSummaries) > 0 {
						entry = append(entry, "**References**")
						for _, ref := range refSummaries {
							entry = append(entry, "- "+formatReportReference(ref))
						}
						entry = append(entry, "")
					}
				}
				newLines = append(newLines, entry...)
			}
			newLines = trimTrailingBlankLines(newLines)
			if len(newLines) == 0 {
				cache[docID] = seen
				continue
			}
			cache[docID] = seen
			feedbackSummary.DocumentCount++
			appendMarkdownHeading(&lines, "## Document: "+title)
			if includeReportDebugMetadata(reportOptions) {
				lines = append(lines, "Document ID: `"+docID+"`", "")
			}
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
		if summaryLines := buildReportFeedbackSummaryLines(feedbackSummary); len(summaryLines) > 0 {
			lines = append(summaryLines, lines...)
		}
		if err := printer.WriteMarkdownReport(outPath, "Compair Sync Report", lines); err != nil {
			return err
		}
		reportPath = outPath
		printer.Success("Markdown report written to " + outPath)
	}
	var gateResult notificationGateResult
	var gateErr error
	if available, capErr := notificationEventsAvailable(client, caps, group); !available {
		gateResult = notificationGateResult{
			Enabled:    detailedNotificationGateEnabled(),
			Available:  false,
			Severities: append([]string(nil), syncFailOnSeverity...),
			Types:      append([]string(nil), syncFailOnType...),
			Error:      "notification events are unavailable on this server according to /capabilities",
		}
		if capErr != nil {
			gateResult.Error = capErr.Error()
		}
		if gateResult.Enabled {
			gateErr = fmt.Errorf("%s", gateResult.Error)
		}
	} else {
		gateResult, gateErr = evaluateNotificationGate(client, group, gatedDocIDs, startedAt, notificationGateWaitBudget(doUpload, doFetch, len(updatedDocs)))
	}

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
				"%s\nnotification gate matched %d event(s): %s",
				notificationGateBlockedHeadline(gateResult),
				gateResult.MatchedCount,
				strings.Join(gateResult.Matches, ", "),
			)
		}
		if !syncJSON {
			printer.Success("PASS: no gated notifications matched.")
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
		if isDropNotificationEvent(event) {
			continue
		}
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
			ParseMode:      strings.TrimSpace(event.ParseMode),
			Model:          strings.TrimSpace(event.Model),
			Rationale:      event.Rationale,
			DedupeKey:      strings.TrimSpace(event.DedupeKey),
			EvidenceTarget: strings.TrimSpace(event.EvidenceTarget),
			EvidencePeer:   strings.TrimSpace(event.EvidencePeer),
			PeerDocIDs:     append([]string(nil), event.PeerDocIDs...),
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

func feedbackHeading(item feedbackRenderItem, index int) string {
	if item.Meta != nil {
		if label := humanizeIntentLabel(item.Meta.Intent); label != "" {
			return fmt.Sprintf("### %s %d", label, index)
		}
	}
	return fmt.Sprintf("### Feedback %d", index)
}

func humanizeIntentLabel(intent string) string {
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case "":
		return ""
	case "potential_conflict":
		return "Potential Conflict"
	case "relevant_update":
		return "Relevant Update"
	case "hidden_overlap":
		return "Hidden Overlap"
	case "quiet_validation":
		return "Quiet Validation"
	case "information_gap":
		return "Information Gap"
	}
	parts := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(intent)))
	if len(parts) == 0 {
		return ""
	}
	for i, part := range parts {
		lower := strings.ToLower(part)
		parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(parts, " ")
}

func buildReportFeedbackSummaryLines(summary reportFeedbackSummary) []string {
	if summary.FeedbackCount == 0 {
		return nil
	}
	lines := []string{
		"## Summary",
		"",
		fmt.Sprintf("- %s across %s.", pluralizeCount(summary.FeedbackCount, "notification", "notifications"), pluralizeCount(summary.DocumentCount, "document", "documents")),
	}
	if breakdown := formatCountBreakdown(summary.ByIntent, humanizeIntentLabel); breakdown != "" {
		lines = append(lines, "- Notification mix: "+breakdown+".")
	}
	if breakdown := formatCountBreakdown(summary.BySeverity, func(value string) string {
		return strings.ToUpper(strings.TrimSpace(value))
	}); breakdown != "" {
		lines = append(lines, "- Severity mix: "+breakdown+".")
	}
	if summary.CollapsedDuplicates > 0 {
		lines = append(lines, fmt.Sprintf("- Collapsed %s in the report view.", pluralizeCount(summary.CollapsedDuplicates, "near-duplicate", "near-duplicates")))
	}
	lines = append(lines, "")
	return lines
}

func formatCountBreakdown(counts map[string]int, labelFn func(string) string) string {
	if len(counts) == 0 {
		return ""
	}
	type entry struct {
		Key   string
		Count int
		Label string
	}
	entries := make([]entry, 0, len(counts))
	for key, count := range counts {
		if count <= 0 {
			continue
		}
		label := strings.TrimSpace(labelFn(key))
		if label == "" {
			continue
		}
		entries = append(entries, entry{Key: key, Count: count, Label: label})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Label < entries[j].Label
	})
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, fmt.Sprintf("%s x%d", entry.Label, entry.Count))
	}
	return strings.Join(parts, ", ")
}

func pluralizeCount(count int, singular string, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func includeReportDebugMetadata(options reportRenderOptions) bool {
	return options.IncludeDebug || options.DetailLevel >= reportDetailVerbose
}

func feedbackComparedFiles(item feedbackRenderItem) []string {
	pathScores := map[string]float64{}
	anchorTokens := feedbackTokenSet(strings.Join([]string{
		strings.TrimSpace(item.Feedback.ChunkContent),
		strings.TrimSpace(item.Feedback.Feedback),
		itemMetaEvidenceTarget(item.Meta),
		itemMetaEvidencePeer(item.Meta),
	}, "\n"))
	addWeightedPaths := func(weight float64, values ...string) {
		for _, value := range values {
			for path := range extractReportPaths(value) {
				score := weight
				if overlap := reportPathTokenOverlap(path, anchorTokens); overlap > 0 {
					score += float64(overlap) * 0.75
				}
				pathScores[path] += score
			}
		}
	}
	addWeightedPaths(
		6.0,
		strings.TrimSpace(item.Feedback.ChunkContent),
	)
	addWeightedPaths(
		5.0,
		strings.TrimSpace(item.Feedback.Feedback),
		itemMetaEvidenceTarget(item.Meta),
		itemMetaEvidencePeer(item.Meta),
	)
	for _, ref := range item.Feedback.References {
		addWeightedPaths(1.0, strings.TrimSpace(ref.Title))
		addWeightedPaths(2.0, strings.TrimSpace(ref.Content))
	}
	if len(pathScores) == 0 {
		return nil
	}
	type scoredPath struct {
		Path  string
		Score float64
	}
	scored := make([]scoredPath, 0, len(pathScores))
	for path, score := range pathScores {
		if suppressComparedFilePath(path) {
			continue
		}
		if score < 3.0 {
			continue
		}
		scored = append(scored, scoredPath{Path: path, Score: score})
	}
	if len(scored) == 0 {
		return nil
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Path < scored[j].Path
		}
		return scored[i].Score > scored[j].Score
	})
	out := make([]string, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.Path)
	}
	return out
}

func suppressComparedFilePath(path string) bool {
	normalized := strings.ToLower(strings.TrimSpace(filepath.ToSlash(path)))
	if normalized == "" {
		return true
	}
	// Hide local CLI state and auth material from user-facing report summaries.
	if strings.HasPrefix(normalized, ".compair/") ||
		strings.HasPrefix(normalized, "~/.compair/") ||
		strings.Contains(normalized, "/.compair/") {
		return true
	}
	return false
}

func appendFeedbackComparedFiles(entry *[]string, item feedbackRenderItem, options reportRenderOptions) {
	files := feedbackComparedFiles(item)
	if len(files) == 0 {
		return
	}
	limit := len(files)
	switch options.DetailLevel {
	case reportDetailBrief:
		if limit > 5 {
			limit = 5
		}
	default:
		if limit > 8 {
			limit = 8
		}
	}
	*entry = append(*entry, "", "**Compared Files**")
	for _, path := range files[:limit] {
		*entry = append(*entry, "- `"+path+"`")
	}
	if len(files) > limit {
		*entry = append(*entry, fmt.Sprintf("- ... +%d more", len(files)-limit))
	}
	*entry = append(*entry, "")
}

func notificationGateBlockedHeadline(result notificationGateResult) string {
	if result.MatchedCount == 1 && len(result.Matches) > 0 {
		match := strings.TrimSpace(result.Matches[0])
		match = strings.SplitN(match, "@", 2)[0]
		severity := ""
		intent := ""
		if left, right, ok := strings.Cut(match, "/"); ok {
			severity = strings.ToLower(strings.TrimSpace(left))
			intent = strings.TrimSpace(right)
		}
		parts := []string{}
		if severity != "" {
			parts = append(parts, severity+"-severity")
		}
		if label := strings.ToLower(strings.TrimSpace(humanizeIntentLabel(intent))); label != "" {
			parts = append(parts, label)
		}
		if len(parts) > 0 {
			return "BLOCKED: " + strings.Join(parts, " ")
		}
	}
	return fmt.Sprintf("BLOCKED: %s matched the notification gate", pluralizeCount(result.MatchedCount, "event", "events"))
}

func collapseDuplicateFeedbackItems(items []feedbackRenderItem) ([]feedbackRenderItem, int) {
	if len(items) < 2 {
		return items, 0
	}
	kept := make([]feedbackRenderItem, 0, len(items))
	profiles := make([]feedbackEvidenceProfile, 0, len(items))
	collapsed := 0
	for _, item := range items {
		profile := buildFeedbackEvidenceProfile(item)
		duplicateIndex := -1
		for i := range kept {
			if shouldCollapseFeedbackItems(kept[i], profiles[i], item, profile) {
				duplicateIndex = i
				break
			}
		}
		if duplicateIndex < 0 {
			kept = append(kept, item)
			profiles = append(profiles, profile)
			continue
		}
		collapsed++
		if feedbackItemScore(item, profile) > feedbackItemScore(kept[duplicateIndex], profiles[duplicateIndex]) {
			kept[duplicateIndex] = item
			profiles[duplicateIndex] = profile
		}
	}
	return kept, collapsed
}

func resolveReportRenderOptions(client *api.Client) reportRenderOptions {
	return reportRenderOptions{
		DetailLevel:  resolveReportDetailLevel(client),
		IncludeDebug: viper.GetBool("verbose"),
	}
}

func resolveReportDetailLevel(client *api.Client) reportDetailLevel {
	if client == nil {
		return reportDetailDetailed
	}
	session, err := client.EnsureSession()
	if err != nil {
		return reportDetailDetailed
	}
	userID := strings.TrimSpace(session.UserID)
	if userID == "" {
		return reportDetailDetailed
	}
	user, err := client.LoadUserByID(userID)
	if err != nil {
		return reportDetailDetailed
	}
	return parseReportDetailLevel(user.PreferredFeedbackLength)
}

func parseReportDetailLevel(value string) reportDetailLevel {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "brief":
		return reportDetailBrief
	case "verbose", "full":
		return reportDetailVerbose
	default:
		return reportDetailDetailed
	}
}

func shouldCollapseFeedbackItems(
	left feedbackRenderItem,
	leftProfile feedbackEvidenceProfile,
	right feedbackRenderItem,
	rightProfile feedbackEvidenceProfile,
) bool {
	if strings.TrimSpace(left.Feedback.FeedbackID) != "" && left.Feedback.FeedbackID == right.Feedback.FeedbackID {
		return true
	}
	if strings.TrimSpace(left.Feedback.ChunkID) != "" && left.Feedback.ChunkID == right.Feedback.ChunkID {
		return true
	}
	if leftProfile.Intent != "" && rightProfile.Intent != "" && leftProfile.Intent != rightProfile.Intent {
		return false
	}
	if leftProfile.Severity != "" && rightProfile.Severity != "" && leftProfile.Severity != rightProfile.Severity {
		return false
	}
	if !withinFeedbackWindow(leftProfile.Timestamp, rightProfile.Timestamp, 10*time.Minute) {
		return false
	}

	pathOverlap := setOverlapRatio(leftProfile.Paths, rightProfile.Paths)
	refOverlap := setOverlapRatio(leftProfile.Refs, rightProfile.Refs)
	anchorOverlap := setOverlapRatio(leftProfile.Anchors, rightProfile.Anchors)
	tokenOverlap := setOverlapRatio(leftProfile.Tokens, rightProfile.Tokens)

	if refOverlap >= 0.75 && anchorOverlap >= 0.5 {
		return true
	}
	if pathOverlap >= 0.75 && anchorOverlap >= 0.5 {
		return true
	}
	if pathOverlap >= 0.75 && refOverlap >= 0.75 && tokenOverlap >= 0.45 {
		return true
	}
	if pathOverlap >= 0.75 && tokenOverlap >= 0.68 {
		return true
	}
	if refOverlap >= 0.75 && tokenOverlap >= 0.55 {
		return true
	}
	return false
}

func buildFeedbackEvidenceProfile(item feedbackRenderItem) feedbackEvidenceProfile {
	profile := feedbackEvidenceProfile{
		Intent:   strings.ToLower(strings.TrimSpace(firstNonEmpty(itemMetaIntent(item.Meta), ""))),
		Severity: strings.ToLower(strings.TrimSpace(itemMetaSeverity(item.Meta))),
		Paths:    extractReportPaths(strings.TrimSpace(item.Feedback.ChunkContent), strings.TrimSpace(item.Feedback.Feedback), itemMetaEvidenceTarget(item.Meta), itemMetaEvidencePeer(item.Meta)),
		Refs:     extractFeedbackReferenceKeys(item),
		Anchors:  extractFeedbackAnchors(strings.TrimSpace(item.Feedback.ChunkContent), strings.TrimSpace(item.Feedback.Feedback), itemMetaEvidenceTarget(item.Meta), itemMetaEvidencePeer(item.Meta)),
		Tokens:   feedbackTokenSet(strings.TrimSpace(item.Feedback.Feedback)),
	}
	if item.Meta != nil {
		if ts, ok := timestampAsTime(item.Meta.CreatedAt); ok {
			profile.Timestamp = ts
		}
	}
	if profile.Timestamp.IsZero() {
		if ts, ok := timestampAsTime(item.Feedback.Timestamp); ok {
			profile.Timestamp = ts
		}
	}
	return profile
}

func feedbackItemScore(item feedbackRenderItem, profile feedbackEvidenceProfile) float64 {
	score := float64(len(strings.TrimSpace(item.Feedback.Feedback))) / 48.0
	score += float64(len(strings.TrimSpace(item.Feedback.ChunkContent))) / 120.0
	score += float64(len(item.Feedback.References)) * 8.0
	score += float64(len(profile.Paths)) * 4.0
	score += float64(len(profile.Refs)) * 3.0
	score += float64(len(profile.Anchors)) * 2.0
	score += float64(len(profile.Tokens)) * 0.1
	if item.Meta != nil {
		score += float64(item.Meta.Rank)
		score += float64(len(nonEmptyLines(item.Meta.Rationale)))
	}
	return score
}

func withinFeedbackWindow(left time.Time, right time.Time, window time.Duration) bool {
	if left.IsZero() || right.IsZero() {
		return false
	}
	delta := left.Sub(right)
	if delta < 0 {
		delta = -delta
	}
	return delta <= window
}

func setOverlapRatio(left map[string]struct{}, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0.0
	}
	shared := 0
	for key := range left {
		if _, ok := right[key]; ok {
			shared++
		}
	}
	denominator := len(left)
	if len(right) < denominator {
		denominator = len(right)
	}
	if denominator == 0 {
		return 0.0
	}
	return float64(shared) / float64(denominator)
}

func extractReportPaths(values ...string) map[string]struct{} {
	paths := map[string]struct{}{}
	addPath := func(candidate string) {
		if normalized := normalizeReportPath(candidate); normalized != "" {
			paths[normalized] = struct{}{}
		}
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		for _, match := range reportDiffPathPattern.FindAllStringSubmatch(value, -1) {
			if len(match) > 1 {
				addPath(match[1])
			}
		}
		for _, match := range reportFileHeaderPattern.FindAllStringSubmatch(value, -1) {
			if len(match) > 1 {
				addPath(match[1])
			}
		}
		for _, match := range reportBacktickCodePattern.FindAllStringSubmatch(value, -1) {
			if len(match) > 1 {
				addPath(match[1])
			}
		}
	}
	return paths
}

func normalizeReportPath(value string) string {
	candidate := strings.TrimSpace(value)
	if candidate == "" {
		return ""
	}
	candidate = strings.Trim(candidate, "`'\"()[]{}<>")
	candidate = strings.TrimRight(candidate, ".,;")
	if candidate == "" {
		return ""
	}
	lowerCandidate := strings.ToLower(candidate)
	if strings.Contains(candidate, "${") ||
		strings.Contains(candidate, "$(") ||
		strings.Contains(candidate, "://") ||
		strings.Contains(candidate, "@") ||
		strings.Contains(candidate, "%") ||
		strings.ContainsAny(candidate, "*?") {
		return ""
	}
	if strings.Contains(candidate, ":") {
		head, _, _ := strings.Cut(candidate, ":")
		if strings.Contains(head, "/") || strings.Contains(head, "\\") || filepath.Ext(head) != "" {
			candidate = head
		}
	}
	if strings.Contains(candidate, " ") {
		return ""
	}
	if strings.HasPrefix(candidate, "a/") || strings.HasPrefix(candidate, "b/") {
		candidate = candidate[2:]
	}
	candidate = strings.TrimPrefix(candidate, "./")
	candidate = filepath.ToSlash(candidate)
	if candidate == "" {
		return ""
	}
	if strings.HasPrefix(candidate, "/") && filepath.Ext(candidate) == "" {
		return ""
	}
	base := strings.ToLower(filepath.Base(candidate))
	dir := filepath.ToSlash(strings.TrimSpace(filepath.Dir(candidate)))
	if strings.HasSuffix(candidate, "/") {
		return ""
	}
	if base == "" || base == "." || base == ".." {
		return ""
	}
	if _, ok := reportSpecialPathNames[base]; ok {
		return strings.ToLower(candidate)
	}
	if filepath.Ext(base) != "" {
		if dir == "." && !strings.Contains(candidate, "/") && !strings.HasPrefix(base, ".") {
			return ""
		}
		if strings.HasPrefix(base, ".") && !strings.Contains(base[1:], ".") {
			return ""
		}
		if strings.HasPrefix(lowerCandidate, "package/") && strings.Count(candidate, "/") < 2 {
			return ""
		}
		return strings.ToLower(candidate)
	}
	return ""
}

func reportPathTokenOverlap(path string, anchors map[string]struct{}) int {
	if len(anchors) == 0 {
		return 0
	}
	candidate := strings.TrimSpace(filepath.ToSlash(path))
	if candidate == "" {
		return 0
	}
	rawParts := strings.FieldsFunc(candidate, func(r rune) bool {
		switch r {
		case '/', '\\', '.', '-', '_':
			return true
		default:
			return false
		}
	})
	seen := map[string]struct{}{}
	overlap := 0
	for _, part := range rawParts {
		token := strings.ToLower(strings.TrimSpace(part))
		if len(token) < 3 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		if _, ok := anchors[token]; ok {
			overlap++
		}
	}
	return overlap
}

func extractFeedbackReferenceKeys(item feedbackRenderItem) map[string]struct{} {
	refs := map[string]struct{}{}
	add := func(values ...string) {
		value := strings.ToLower(strings.TrimSpace(firstNonEmpty(values...)))
		if value == "" {
			return
		}
		refs[value] = struct{}{}
	}
	for _, ref := range item.Feedback.References {
		add(ref.Title, ref.DocumentID, ref.NoteID)
	}
	if item.Meta != nil {
		for _, peerDocID := range item.Meta.PeerDocIDs {
			add(peerDocID)
		}
	}
	return refs
}

func feedbackTokenSet(value string) map[string]struct{} {
	stopwords := map[string]struct{}{
		"about": {}, "after": {}, "again": {}, "also": {}, "because": {}, "before": {}, "being": {}, "between": {},
		"could": {}, "does": {}, "from": {}, "have": {}, "into": {}, "just": {}, "more": {}, "only": {}, "other": {},
		"same": {}, "than": {}, "that": {}, "their": {}, "there": {}, "these": {}, "they": {}, "this": {}, "those": {},
		"through": {}, "when": {}, "where": {}, "which": {}, "while": {}, "with": {}, "would": {}, "your": {},
	}
	tokens := map[string]struct{}{}
	for _, match := range reportTokenPattern.FindAllString(value, -1) {
		token := strings.ToLower(strings.TrimSpace(match))
		if len(token) < 4 {
			continue
		}
		if _, ok := stopwords[token]; ok {
			continue
		}
		tokens[token] = struct{}{}
		if len(tokens) >= 64 {
			break
		}
	}
	return tokens
}

func extractFeedbackAnchors(values ...string) map[string]struct{} {
	anchors := map[string]struct{}{}
	addToken := func(token string) {
		token = strings.ToLower(strings.TrimSpace(token))
		if len(token) < 3 {
			return
		}
		anchors[token] = struct{}{}
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		for _, match := range reportBacktickCodePattern.FindAllStringSubmatch(value, -1) {
			if len(match) < 2 {
				continue
			}
			for _, token := range reportTokenPattern.FindAllString(match[1], -1) {
				addToken(token)
			}
		}
		for _, line := range strings.Split(value, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "+++") || strings.HasPrefix(trimmed, "---") {
				continue
			}
			if strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") {
				for _, token := range reportTokenPattern.FindAllString(trimmed, -1) {
					addToken(token)
					if len(anchors) >= 48 {
						return anchors
					}
				}
			}
		}
	}
	return anchors
}

func itemMetaIntent(meta *feedbackNotificationMeta) string {
	if meta == nil {
		return ""
	}
	return meta.Intent
}

func itemMetaSeverity(meta *feedbackNotificationMeta) string {
	if meta == nil {
		return ""
	}
	return meta.Severity
}

func itemMetaEvidenceTarget(meta *feedbackNotificationMeta) string {
	if meta == nil {
		return ""
	}
	return meta.EvidenceTarget
}

func itemMetaEvidencePeer(meta *feedbackNotificationMeta) string {
	if meta == nil {
		return ""
	}
	return meta.EvidencePeer
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

type feedbackSnapshot struct {
	Count           int
	IDs             map[string]struct{}
	LatestTimestamp time.Time
}

func parseFeedbackTimestamp(value interface{}) time.Time {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" || raw == "<nil>" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed
	}
	return time.Time{}
}

func feedbackSnapshotForDoc(client *api.Client, docID string) feedbackSnapshot {
	if docID == "" {
		return feedbackSnapshot{}
	}
	fbs, err := client.ListFeedback(docID)
	if err != nil {
		return feedbackSnapshot{}
	}
	snapshot := feedbackSnapshot{
		Count: len(fbs),
		IDs:   make(map[string]struct{}, len(fbs)),
	}
	for _, fb := range fbs {
		if id := strings.TrimSpace(fb.FeedbackID); id != "" {
			snapshot.IDs[id] = struct{}{}
		}
		if ts := parseFeedbackTimestamp(fb.Timestamp); ts.After(snapshot.LatestTimestamp) {
			snapshot.LatestTimestamp = ts
		}
	}
	return snapshot
}

func feedbackSnapshotFromPending(count int, latest string) feedbackSnapshot {
	snapshot := feedbackSnapshot{Count: count}
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(latest)); err == nil {
		snapshot.LatestTimestamp = parsed
	} else if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(latest)); err == nil {
		snapshot.LatestTimestamp = parsed
	}
	return snapshot
}

func hasNewFeedback(fbs []api.FeedbackEntry, baseline feedbackSnapshot) bool {
	if len(fbs) > baseline.Count {
		return true
	}
	latest := time.Time{}
	for _, fb := range fbs {
		if id := strings.TrimSpace(fb.FeedbackID); id != "" && len(baseline.IDs) > 0 {
			if _, ok := baseline.IDs[id]; !ok {
				return true
			}
		}
		if ts := parseFeedbackTimestamp(fb.Timestamp); ts.After(latest) {
			latest = ts
		}
	}
	return !baseline.LatestTimestamp.IsZero() && latest.After(baseline.LatestTimestamp)
}

func waitForNewFeedback(ctx context.Context, client *api.Client, docID string, baseline feedbackSnapshot, timeout time.Duration, onProgress func(time.Duration, time.Duration)) {
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
		if err == nil && hasNewFeedback(fbs, baseline) {
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

func taskMetaString(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func taskMetaInt(meta map[string]any, key string) int {
	if len(meta) == 0 {
		return 0
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
		return n
	default:
		n, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(typed)))
		return n
	}
}

func parseTaskProgressMeta(st api.TaskStatus) taskProgressMeta {
	meta := st.Meta
	startedAt := taskMetaTime(meta, "started_at")
	lastProgressAt := taskMetaTime(meta, "last_progress_at")
	return taskProgressMeta{
		Stage:               taskMetaString(meta, "stage"),
		Message:             taskMetaString(meta, "message"),
		StartedAt:           startedAt,
		LastProgressAt:      lastProgressAt,
		TotalChunks:         taskMetaInt(meta, "total_chunks"),
		NewChunksTotal:      taskMetaInt(meta, "new_chunks_total"),
		IndexedChunksDone:   taskMetaInt(meta, "indexed_chunks_done"),
		IndexedChunksTotal:  taskMetaInt(meta, "indexed_chunks_total"),
		FeedbackChunksTotal: taskMetaInt(meta, "feedback_chunks_total"),
		ChunkTaskIDs:        extractChunkTaskIDs(meta),
	}
}

func taskMetaTime(meta map[string]any, key string) time.Time {
	raw := taskMetaString(meta, key)
	if raw == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed
	}
	return time.Time{}
}

func taskStatusElapsed(st api.TaskStatus, fallbackStartedAt time.Time) time.Duration {
	progress := parseTaskProgressMeta(st)
	if !progress.StartedAt.IsZero() {
		if elapsed := time.Since(progress.StartedAt); elapsed > 0 {
			return elapsed
		}
	}
	return time.Since(fallbackStartedAt)
}

func processProgressStaleAfter() time.Duration {
	const defaultCutoff = 15 * time.Minute
	raw := strings.TrimSpace(os.Getenv("COMPAIR_PROCESS_PROGRESS_STALE_AFTER_SEC"))
	if raw == "" {
		return defaultCutoff
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultCutoff
	}
	return time.Duration(seconds) * time.Second
}

func pendingStatusStaleAfter() time.Duration {
	const defaultCutoff = 5 * time.Minute
	raw := strings.TrimSpace(os.Getenv("COMPAIR_PENDING_STATUS_STALE_AFTER_SEC"))
	if raw == "" {
		return defaultCutoff
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return defaultCutoff
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func isPendingStatusWithoutProgress(st api.TaskStatus) bool {
	if strings.ToUpper(strings.TrimSpace(st.Status)) != "PENDING" {
		return false
	}
	progress := parseTaskProgressMeta(st)
	return progress.Stage == "" &&
		progress.Message == "" &&
		progress.TotalChunks == 0 &&
		progress.NewChunksTotal == 0 &&
		progress.IndexedChunksTotal == 0 &&
		progress.FeedbackChunksTotal == 0 &&
		len(progress.ChunkTaskIDs) == 0 &&
		len(extractChunkTaskIDsFromStatus(st)) == 0
}

func displayTaskStatus(st api.TaskStatus, err error) string {
	status := strings.TrimSpace(st.Status)
	if status != "" {
		return status
	}
	if err != nil {
		msg := strings.TrimSpace(err.Error())
		if msg != "" {
			return "unknown (" + msg + ")"
		}
	}
	return "unknown"
}

func isTaskProgressStale(progress taskProgressMeta, fallbackStartedAt time.Time) (bool, time.Duration, time.Duration) {
	cutoff := processProgressStaleAfter()
	if cutoff <= 0 {
		return false, 0, cutoff
	}
	reference := progress.LastProgressAt
	if reference.IsZero() {
		reference = progress.StartedAt
	}
	if reference.IsZero() {
		reference = fallbackStartedAt
	}
	if reference.IsZero() {
		return false, 0, cutoff
	}
	age := time.Since(reference)
	return age >= cutoff, age, cutoff
}

func extractChunkTaskIDsFromStatus(st api.TaskStatus) []string {
	if ids := extractChunkTaskIDs(st.Result); len(ids) > 0 {
		return ids
	}
	return extractChunkTaskIDs(st.Meta)
}

func taskProgressSummary(progress taskProgressMeta) string {
	switch progress.Stage {
	case "preparing":
		if progress.Message != "" {
			return progress.Message
		}
		return "preparing document"
	case "chunking":
		if progress.TotalChunks > 0 {
			if progress.NewChunksTotal > 0 {
				return fmt.Sprintf("prepared %d chunk(s); %d new", progress.TotalChunks, progress.NewChunksTotal)
			}
			return fmt.Sprintf("prepared %d chunk(s)", progress.TotalChunks)
		}
	case "embedding":
		if progress.IndexedChunksTotal > 0 {
			return fmt.Sprintf("creating embeddings for %d chunk(s)", progress.IndexedChunksTotal)
		}
	case "indexing":
		if progress.IndexedChunksTotal > 0 {
			done := progress.IndexedChunksDone
			if done < 0 {
				done = 0
			}
			percent := int(float64(done) / float64(progress.IndexedChunksTotal) * 100)
			if percent < 0 {
				percent = 0
			}
			if percent > 100 {
				percent = 100
			}
			return fmt.Sprintf("indexing %d/%d chunk(s) (%d%%)", done, progress.IndexedChunksTotal, percent)
		}
	case "queueing_feedback":
		if progress.FeedbackChunksTotal > 0 {
			return fmt.Sprintf("queueing %d feedback task(s)", progress.FeedbackChunksTotal)
		}
	case "queued_feedback":
		if progress.FeedbackChunksTotal > 0 {
			return fmt.Sprintf("queued %d feedback task(s)", progress.FeedbackChunksTotal)
		}
		if progress.IndexedChunksTotal > 0 {
			return fmt.Sprintf("indexed %d chunk(s); no feedback queued", progress.IndexedChunksTotal)
		}
	case "complete":
		if progress.Message != "" {
			return progress.Message
		}
		return "indexing complete"
	}
	if progress.Message != "" {
		return progress.Message
	}
	return ""
}

func taskProgressRemaining(progress taskProgressMeta, elapsed time.Duration) time.Duration {
	if progress.IndexedChunksTotal <= 0 || progress.IndexedChunksDone <= 0 || progress.IndexedChunksDone >= progress.IndexedChunksTotal {
		return 0
	}
	perChunk := float64(elapsed) / float64(progress.IndexedChunksDone)
	if perChunk <= 0 {
		return 0
	}
	remainingChunks := progress.IndexedChunksTotal - progress.IndexedChunksDone
	return time.Duration(perChunk * float64(remainingChunks))
}

func formatTaskProgressLine(index int, total int, action string, label string, st api.TaskStatus, elapsed time.Duration) string {
	progress := parseTaskProgressMeta(st)
	detail := taskProgressSummary(progress)
	if detail == "" && isPendingStatusWithoutProgress(st) {
		detail = "queued on server; waiting for worker progress"
	}
	remaining := taskProgressRemaining(progress, elapsed)
	if detail != "" {
		if remaining > 0 {
			return fmt.Sprintf("[%d/%d] %s %s (%s, %s elapsed, est. %s remaining)", index, total, action, label, detail, humanDuration(elapsed), humanDuration(remaining))
		}
		return fmt.Sprintf("[%d/%d] %s %s (%s, %s elapsed)", index, total, action, label, detail, humanDuration(elapsed))
	}
	if remaining > 0 {
		return fmt.Sprintf("[%d/%d] %s %s (%s elapsed, est. %s remaining)", index, total, action, label, humanDuration(elapsed), humanDuration(remaining))
	}
	return fmt.Sprintf("[%d/%d] %s %s (%s elapsed)", index, total, action, label, humanDuration(elapsed))
}

func waitForProcessingTask(ctx context.Context, client *api.Client, taskID string, onProgress func(api.TaskStatus, time.Duration)) (api.TaskStatus, bool, error) {
	deadline := processingDeadline()
	startedAt := time.Now()
	lastProgressAt := time.Time{}
	consecutivePollErrors := 0
	for {
		st, err := client.GetTaskStatus(taskID)
		if err != nil {
			if isRetryableStatusPollError(err) && consecutivePollErrors < 5 {
				consecutivePollErrors++
				select {
				case <-ctx.Done():
					return api.TaskStatus{}, false, ctx.Err()
				case <-time.After(2 * time.Second):
					continue
				}
			}
			return api.TaskStatus{}, false, err
		}
		consecutivePollErrors = 0
		switch strings.ToUpper(strings.TrimSpace(st.Status)) {
		case "SUCCESS":
			return st, false, nil
		case "FAILURE", "FAILED", "REVOKED":
			return st, false, nil
		}
		progress := parseTaskProgressMeta(st)
		if isPendingStatusWithoutProgress(st) {
			if cutoff := pendingStatusStaleAfter(); cutoff > 0 && time.Since(startedAt) >= cutoff {
				return st, false, fmt.Errorf(
					"processing task %s is still PENDING after %s with no worker progress; this usually means the worker has not consumed the task, the API and worker are using different result backends, or task status was lost after completion",
					shortTaskID(taskID),
					humanDuration(time.Since(startedAt)),
				)
			}
		}
		if stale, age, cutoff := isTaskProgressStale(progress, startedAt); stale {
			if len(extractChunkTaskIDsFromStatus(st)) > 0 {
				return st, false, nil
			}
			return st, false, fmt.Errorf(
				"processing task %s appears stalled; last progress update was %s ago (cutoff %s)",
				shortTaskID(taskID),
				humanDuration(age),
				humanDuration(cutoff),
			)
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return st, true, nil
		}
		if onProgress != nil && (lastProgressAt.IsZero() || time.Since(lastProgressAt) >= 15*time.Second) {
			onProgress(st, taskStatusElapsed(st, startedAt))
			lastProgressAt = time.Now()
		}
		select {
		case <-ctx.Done():
			return api.TaskStatus{}, false, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func isRetryableStatusPollError(err error) bool {
	if err == nil {
		return false
	}
	if os.IsTimeout(err) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "operation timed out") ||
		strings.Contains(msg, "client.timeout") ||
		strings.Contains(msg, "timeout awaiting response headers") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "tls handshake timeout")
}

func waitForChunkTasks(ctx context.Context, client *api.Client, result any, label string, onProgress func(int, int, time.Duration)) error {
	taskIDs := extractChunkTaskIDs(result)
	return waitForChunkTaskIDs(ctx, client, taskIDs, label, onProgress)
}

func waitForChunkTaskIDs(ctx context.Context, client *api.Client, taskIDs []string, label string, onProgress func(int, int, time.Duration)) error {
	for idx, taskID := range taskIDs {
		st, timedOut, err := waitForProcessingTask(ctx, client, taskID, func(_ api.TaskStatus, elapsed time.Duration) {
			if onProgress != nil {
				onProgress(idx+1, len(taskIDs), elapsed)
			}
		})
		if err != nil {
			return err
		}
		if timedOut {
			return fmt.Errorf(
				"processing timeout after %ds while waiting for chunk task %s for %s",
				syncProcessTimeoutSec,
				shortTaskID(taskID),
				label,
			)
		}
		switch strings.ToUpper(strings.TrimSpace(st.Status)) {
		case "SUCCESS":
		default:
			return fmt.Errorf("chunk task %s for %s ended with status %s", shortTaskID(taskID), label, st.Status)
		}
	}
	return nil
}

func extractChunkTaskIDs(result any) []string {
	if result == nil {
		return nil
	}
	payload, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := payload["chunk_task_ids"]
	if !ok || raw == nil {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if trimmed := strings.TrimSpace(fmt.Sprint(value)); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}

type repoProgressTracker struct {
	total             int
	completed         int
	observedCompleted int
	totalObservedWait time.Duration
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
	t.observedCompleted++
	t.totalObservedWait += elapsed
	eta := t.estimateRemaining(index, 0)
	if eta > 0 {
		return fmt.Sprintf("[%d/%d] Completed %s in %s. Est. %s remaining.", index, t.total, label, humanDuration(elapsed), humanDuration(eta))
	}
	return fmt.Sprintf("[%d/%d] Completed %s in %s.", index, t.total, label, humanDuration(elapsed))
}

func (t *repoProgressTracker) alreadyCompleteLine(index int, label string, taskAge time.Duration) string {
	t.completed++
	eta := t.estimateRemaining(index, 0)
	if eta > 0 {
		return fmt.Sprintf("[%d/%d] %s already completed before this check (submitted %s ago). Est. %s remaining.", index, t.total, label, humanDuration(taskAge), humanDuration(eta))
	}
	return fmt.Sprintf("[%d/%d] %s already completed before this check (submitted %s ago).", index, t.total, label, humanDuration(taskAge))
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
		if t.observedCompleted > 0 {
			avg = (t.totalObservedWait + currentElapsed) / time.Duration(t.observedCompleted+1)
		} else {
			avg = currentElapsed
		}
	} else if t.observedCompleted > 0 {
		avg = t.totalObservedWait / time.Duration(t.observedCompleted)
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

func pendingTaskElapsed(startedAt string, fallback time.Time) time.Duration {
	ts := strings.TrimSpace(startedAt)
	if ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			if elapsed := time.Since(parsed); elapsed > 0 {
				return elapsed
			}
		} else if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			if elapsed := time.Since(parsed); elapsed > 0 {
				return elapsed
			}
		}
	}
	return time.Since(fallback)
}

func shouldWaitForPendingInitialSyncs(doUpload bool, generateFeedback bool, modeFlags syncInvocationMode) bool {
	if modeFlags.SkipInitialSyncWait {
		return false
	}
	return doUpload && generateFeedback
}

func validatePairwiseMode(doUpload bool, waitForProcessing bool, generateFeedback bool, modeFlags syncInvocationMode) error {
	if syncCrossRepoOnly && !syncPairwise {
		return fmt.Errorf("--cross-repo-only requires --pairwise")
	}
	if !syncPairwise {
		return nil
	}
	if !doUpload {
		return fmt.Errorf("--pairwise requires an upload pass; remove --fetch-only")
	}
	if !generateFeedback {
		return fmt.Errorf("--pairwise requires feedback generation")
	}
	if modeFlags.Detach {
		return fmt.Errorf("--pairwise does not support --detach yet; run it attached so Compair can walk the repo pairs in sequence")
	}
	if !waitForProcessing {
		return fmt.Errorf("--pairwise currently requires waiting for backend processing; omit --push-only")
	}
	return nil
}

func resolvePairwiseRepoScopes(group string, targetRoots []string, crossRepoOnly bool) (map[string][]pairwiseRepo, error) {
	scopes := map[string][]pairwiseRepo{}
	if len(targetRoots) == 0 {
		return scopes, nil
	}

	allRoots := map[string]struct{}{}
	for _, root := range targetRoots {
		if trimmed := strings.TrimSpace(root); trimmed != "" {
			allRoots[trimmed] = struct{}{}
		}
	}

	store, err := db.Open()
	if err == nil {
		defer store.Close()
		groupRoots, listErr := store.ListRepoRoots(context.Background(), group)
		if listErr != nil {
			return nil, listErr
		}
		for _, root := range groupRoots {
			if trimmed := strings.TrimSpace(root); trimmed != "" {
				allRoots[trimmed] = struct{}{}
			}
		}
	}

	allRootList := make([]string, 0, len(allRoots))
	for root := range allRoots {
		allRootList = append(allRootList, root)
	}
	sort.Strings(allRootList)

	reposByRoot := map[string]pairwiseRepo{}
	for _, root := range allRootList {
		cfg, err := config.ReadProjectConfig(root)
		if err != nil || len(cfg.Repos) == 0 {
			continue
		}
		repo := cfg.Repos[0]
		docID := strings.TrimSpace(repo.DocumentID)
		if docID == "" {
			continue
		}
		reposByRoot[root] = pairwiseRepo{
			Root:       root,
			DocumentID: docID,
			Label:      repoDisplayLabel(root, repo.RemoteURL),
		}
	}

	for _, targetRoot := range targetRoots {
		targetRepo, ok := reposByRoot[targetRoot]
		if !ok {
			continue
		}
		peers := make([]pairwiseRepo, 0, len(allRootList))
		for _, root := range allRootList {
			peer, ok := reposByRoot[root]
			if !ok {
				continue
			}
			if crossRepoOnly && root == targetRoot {
				continue
			}
			peers = append(peers, peer)
		}
		sort.SliceStable(peers, func(i, j int) bool {
			leftSelf := peers[i].Root == targetRepo.Root
			rightSelf := peers[j].Root == targetRepo.Root
			if leftSelf != rightSelf {
				return !leftSelf && rightSelf
			}
			return peers[i].Label < peers[j].Label
		})
		scopes[targetRoot] = peers
	}

	return scopes, nil
}

func waitForPendingInitialSyncs(ctx context.Context, client *api.Client, group string) error {
	pending, err := listPendingInitialSyncs(ctx, group)
	if err != nil || len(pending) == 0 {
		return err
	}
	printer.Info(fmt.Sprintf("Waiting for %d pending initial sync(s) in the active group before generating feedback.", len(pending)))
	progress := newRepoProgressTracker(len(pending))
	for idx, item := range pending {
		cfg := item.Config
		if len(cfg.Repos) == 0 {
			continue
		}
		repo := &cfg.Repos[0]
		if strings.TrimSpace(repo.PendingTaskID) == "" {
			continue
		}
		waitStartedAt := time.Now()
		var st api.TaskStatus
		var err error
		if stale, age, cutoff := isPendingRepoTaskStale(repo.PendingTaskStartedAt); stale {
			st, err = client.GetTaskStatus(repo.PendingTaskID)
			if err != nil || strings.ToUpper(strings.TrimSpace(st.Status)) != "SUCCESS" {
				status := displayTaskStatus(st, err)
				clearPendingRepoTask(item.Root, cfg, repo)
				printer.Warn(fmt.Sprintf(
					"[%d/%d] Cleared stale pending initial sync for %s (%s old; cutoff %s; server status %s). The next review will resubmit current content.",
					idx+1,
					len(pending),
					item.Label,
					humanDuration(age),
					humanDuration(cutoff),
					status,
				))
				continue
			}
			printer.Info(fmt.Sprintf("[%d/%d] Pending initial sync for %s already completed on the server; finalizing local sync state", idx+1, len(pending), item.Label))
		} else {
			var timedOut bool
			st, timedOut, err = waitForProcessingTask(ctx, client, repo.PendingTaskID, func(st api.TaskStatus, elapsed time.Duration) {
				printer.Info(formatTaskProgressLine(idx+1, len(pending), "Waiting for initial indexing of", item.Label, st, elapsed))
			})
			if err != nil {
				return err
			}
			if timedOut {
				return fmt.Errorf(
					"processing timeout after %ds while waiting for pending initial sync of %s (run 'compair wait --all --timeout 20m' or rerun the original command to continue waiting; use 'compair wait --all --timeout 0' to wait indefinitely)",
					syncProcessTimeoutSec,
					item.Label,
				)
			}
		}
		switch strings.ToUpper(strings.TrimSpace(st.Status)) {
		case "SUCCESS":
		default:
			return fmt.Errorf("pending initial sync task %s for %s ended with status %s", shortTaskID(repo.PendingTaskID), item.Label, st.Status)
		}
		if err := waitForChunkTasks(ctx, client, st.Result, item.Label, func(taskIndex int, taskTotal int, elapsed time.Duration) {
			printer.Info(
				fmt.Sprintf(
					"[%d/%d] Waiting for chunk task %d/%d for %s (%s elapsed)",
					idx+1,
					len(pending),
					taskIndex,
					taskTotal,
					item.Label,
					humanDuration(elapsed),
				),
			)
		}); err != nil {
			clearPendingRepoTaskOnTerminalChunkFailure(item.Root, cfg, repo, err)
			return err
		}
		latest := strings.TrimSpace(repo.PendingTaskCommit)
		localWaitElapsed := time.Since(waitStartedAt)
		taskAge := pendingTaskElapsed(repo.PendingTaskStartedAt, waitStartedAt)
		if latest != "" {
			finalizeRepoSync(item.Root, group, cfg, repo, latest)
		} else {
			clearPendingRepoTask(item.Root, cfg, repo)
		}
		if localWaitElapsed <= 3*time.Second {
			printer.Info(progress.alreadyCompleteLine(idx+1, item.Label, taskAge))
		} else {
			printer.Info(progress.completeLine(idx+1, item.Label, localWaitElapsed))
		}
	}
	return nil
}

func waitForSavedPendingRepoTasks(ctx context.Context, client *api.Client, group string, roots []string) (int, error) {
	pending, err := listPendingRepoTasks(roots)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	printer.Info(fmt.Sprintf("Waiting for %d saved pending task(s).", len(pending)))
	progress := newRepoProgressTracker(len(pending))
	completed := 0
	for idx, item := range pending {
		cfg := item.Config
		if len(cfg.Repos) == 0 {
			continue
		}
		repo := &cfg.Repos[0]
		if strings.TrimSpace(repo.PendingTaskID) == "" {
			continue
		}
		waitStartedAt := time.Now()
		var st api.TaskStatus
		var err error
		if stale, age, cutoff := isPendingRepoTaskStale(repo.PendingTaskStartedAt); stale {
			st, err = client.GetTaskStatus(repo.PendingTaskID)
			status := strings.ToUpper(strings.TrimSpace(st.Status))
			recoverable := status == "SUCCESS" || ((status == "PROGRESS" || status == "STARTED") && len(extractChunkTaskIDsFromStatus(st)) > 0)
			if err != nil || !recoverable {
				displayStatus := displayTaskStatus(st, err)
				clearPendingRepoTask(item.Root, cfg, repo)
				printer.Warn(fmt.Sprintf(
					"[%d/%d] Cleared stale saved task for %s (%s old; cutoff %s; server status %s). Rerun 'compair review' to resubmit current changes.",
					idx+1,
					len(pending),
					item.Label,
					humanDuration(age),
					humanDuration(cutoff),
					displayStatus,
				))
				continue
			}
			printer.Info(fmt.Sprintf("[%d/%d] Saved processing task for %s is stale locally but recoverable from server status %s", idx+1, len(pending), item.Label, st.Status))
		} else {
			printer.Info(fmt.Sprintf("[%d/%d] Resuming pending processing task for %s", idx+1, len(pending), item.Label))
			var timedOut bool
			st, timedOut, err = waitForProcessingTask(ctx, client, repo.PendingTaskID, func(st api.TaskStatus, elapsed time.Duration) {
				printer.Info(formatTaskProgressLine(idx+1, len(pending), "Still processing", item.Label, st, elapsed))
			})
			if err != nil {
				return completed, err
			}
			if timedOut {
				return completed, fmt.Errorf(
					"processing timeout after %ds while waiting for the saved task for %s (rerun 'compair wait --timeout 20m' to continue waiting, or use 'compair wait --timeout 0' to wait indefinitely)",
					syncProcessTimeoutSec,
					item.Label,
				)
			}
		}
		switch strings.ToUpper(strings.TrimSpace(st.Status)) {
		case "SUCCESS":
		case "PROGRESS", "STARTED":
			if len(extractChunkTaskIDsFromStatus(st)) == 0 {
				return completed, fmt.Errorf(
					"saved processing task for %s is still in progress without recoverable chunk tasks",
					item.Label,
				)
			}
		case "PENDING":
			return completed, fmt.Errorf(
				"saved processing task for %s is still pending (rerun 'compair wait' to continue waiting)",
				item.Label,
			)
		default:
			clearPendingRepoTask(item.Root, cfg, repo)
			return completed, fmt.Errorf("saved processing task %s for %s ended with status %s", shortTaskID(repo.PendingTaskID), item.Label, st.Status)
		}
		chunkTaskIDs := extractChunkTaskIDsFromStatus(st)
		if err := waitForChunkTaskIDs(ctx, client, chunkTaskIDs, item.Label, func(taskIndex int, taskTotal int, elapsed time.Duration) {
			printer.Info(
				fmt.Sprintf(
					"[%d/%d] Waiting for chunk task %d/%d for %s (%s elapsed)",
					idx+1,
					len(pending),
					taskIndex,
					taskTotal,
					item.Label,
					humanDuration(elapsed),
				),
			)
		}); err != nil {
			clearPendingRepoTaskOnTerminalChunkFailure(item.Root, cfg, repo, err)
			return completed, err
		}
		latest := strings.TrimSpace(repo.PendingTaskCommit)
		localWaitElapsed := time.Since(waitStartedAt)
		taskAge := pendingTaskElapsed(repo.PendingTaskStartedAt, waitStartedAt)
		if latest != "" {
			finalizeRepoSync(item.Root, group, cfg, repo, latest)
		} else {
			clearPendingRepoTask(item.Root, cfg, repo)
		}
		completed++
		if localWaitElapsed <= 3*time.Second {
			printer.Info(progress.alreadyCompleteLine(idx+1, item.Label, taskAge))
		} else {
			printer.Info(progress.completeLine(idx+1, item.Label, localWaitElapsed))
		}
	}
	return completed, nil
}

func clearPendingRepoTaskOnTerminalChunkFailure(root string, cfg config.Project, repo *config.Repo, err error) {
	if err == nil {
		return
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "ended with status") {
		clearPendingRepoTask(root, cfg, repo)
	}
}

func listPendingInitialSyncs(ctx context.Context, group string) ([]pendingInitialSync, error) {
	store, err := db.Open()
	if err != nil {
		return nil, err
	}
	defer store.Close()
	roots, err := store.ListRepoRoots(ctx, group)
	if err != nil {
		return nil, err
	}
	out := make([]pendingInitialSync, 0, len(roots))
	for _, root := range roots {
		cfg, err := config.ReadProjectConfig(root)
		if err != nil || len(cfg.Repos) == 0 {
			continue
		}
		repo := cfg.Repos[0]
		if strings.TrimSpace(repo.PendingTaskID) == "" {
			continue
		}
		if strings.TrimSpace(repo.LastSyncedCommit) != "" {
			continue
		}
		out = append(out, pendingInitialSync{
			Root:   root,
			Label:  repoDisplayLabel(root, repo.RemoteURL),
			Config: cfg,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.TrimSpace(out[i].Config.Repos[0].PendingTaskStartedAt)
		right := strings.TrimSpace(out[j].Config.Repos[0].PendingTaskStartedAt)
		if left == right {
			return out[i].Label < out[j].Label
		}
		if left == "" {
			return false
		}
		if right == "" {
			return true
		}
		return left < right
	})
	return out, nil
}

func listPendingRepoTasks(roots []string) ([]pendingInitialSync, error) {
	out := make([]pendingInitialSync, 0, len(roots))
	for _, root := range roots {
		cfg, err := config.ReadProjectConfig(root)
		if err != nil || len(cfg.Repos) == 0 {
			continue
		}
		repo := cfg.Repos[0]
		if strings.TrimSpace(repo.PendingTaskID) == "" {
			continue
		}
		out = append(out, pendingInitialSync{
			Root:   root,
			Label:  repoDisplayLabel(root, repo.RemoteURL),
			Config: cfg,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.TrimSpace(out[i].Config.Repos[0].PendingTaskStartedAt)
		right := strings.TrimSpace(out[j].Config.Repos[0].PendingTaskStartedAt)
		if left == right {
			return out[i].Label < out[j].Label
		}
		if left == "" {
			return false
		}
		if right == "" {
			return true
		}
		return left < right
	})
	return out, nil
}

func repoDisplayLabel(root, remote string) string {
	if trimmed := strings.TrimSpace(remote); trimmed != "" {
		return trimmed
	}
	return filepath.Base(root)
}

func submitRepoProcessDoc(
	client *api.Client,
	docID string,
	text string,
	generateFeedback bool,
	snapshotUsed bool,
	reanalyzeExisting bool,
	skipIndex bool,
	referenceDocIDs []string,
) (api.ProcessDocResp, error) {
	opts := api.ProcessDocOptions{
		ReferenceDocIDs: referenceDocIDs,
		SkipIndex:       skipIndex,
	}
	if snapshotUsed {
		opts.ChunkMode = "client"
		opts.ReanalyzeExisting = reanalyzeExisting
	}
	if snapshotUsed || reanalyzeExisting || len(referenceDocIDs) > 0 || skipIndex {
		return client.ProcessDocWithOptions(docID, text, generateFeedback, opts)
	}
	return client.ProcessDoc(docID, text, generateFeedback)
}

func awaitRepoProcessDocTask(
	ctx context.Context,
	client *api.Client,
	root string,
	cfg config.Project,
	repo *config.Repo,
	resp api.ProcessDocResp,
	latest string,
	initialFeedback feedbackSnapshot,
	displayLabel string,
	progressIndex int,
	progressTotal int,
	text string,
	responseLabel string,
	snapshotUsed bool,
	doFetch bool,
	reportOptions reportRenderOptions,
	lines *[]string,
) (api.TaskStatus, error) {
	var st api.TaskStatus
	if strings.TrimSpace(resp.TaskID) == "" {
		st.Status = "SUCCESS"
		if resp.SkippedIndex {
			st.Result = map[string]any{"detail": "document content updated without index refresh"}
		} else {
			st.Result = map[string]any{"detail": "processing completed locally"}
		}
		if doFetch {
			appendRepoServerResponse(lines, responseLabel, text, st.Result, snapshotUsed, reportOptions)
		}
		return st, nil
	}
	persistPendingRepoTask(root, cfg, repo, resp.TaskID, latest, initialFeedback)
	if strings.TrimSpace(resp.TaskID) != "" {
		printer.Info(fmt.Sprintf("[%d/%d] Submitted %s; waiting for server task %s", progressIndex, progressTotal, displayLabel, shortTaskID(resp.TaskID)))
	}
	st, timedOut, err := waitForProcessingTask(ctx, client, resp.TaskID, func(st api.TaskStatus, elapsed time.Duration) {
		printer.Info(formatTaskProgressLine(progressIndex, progressTotal, "Still processing", displayLabel, st, elapsed))
	})
	if err != nil {
		return api.TaskStatus{}, err
	}
	if timedOut {
		return api.TaskStatus{}, fmt.Errorf(
			"processing timeout after %ds (run 'compair wait --timeout 20m' or rerun the same command to continue waiting without resubmitting this repo; use 'compair wait --timeout 0' to wait indefinitely)",
			syncProcessTimeoutSec,
		)
	}
	switch strings.ToUpper(strings.TrimSpace(st.Status)) {
	case "SUCCESS":
	case "PROGRESS", "STARTED":
		if len(extractChunkTaskIDsFromStatus(st)) == 0 {
			clearPendingRepoTask(root, cfg, repo)
			return api.TaskStatus{}, fmt.Errorf(
				"processing task %s for %s is still in progress without recoverable chunk tasks",
				shortTaskID(resp.TaskID),
				displayLabel,
			)
		}
	default:
		clearPendingRepoTask(root, cfg, repo)
		return api.TaskStatus{}, fmt.Errorf("processing task %s for %s ended with status %s", shortTaskID(resp.TaskID), displayLabel, st.Status)
	}
	chunkTaskIDs := extractChunkTaskIDsFromStatus(st)
	if err := waitForChunkTaskIDs(ctx, client, chunkTaskIDs, displayLabel, func(taskIndex int, taskTotal int, elapsed time.Duration) {
		printer.Info(
			fmt.Sprintf(
				"[%d/%d] Waiting for chunk task %d/%d for %s (%s elapsed)",
				progressIndex,
				progressTotal,
				taskIndex,
				taskTotal,
				displayLabel,
				humanDuration(elapsed),
			),
		)
	}); err != nil {
		clearPendingRepoTaskOnTerminalChunkFailure(root, cfg, repo, err)
		return api.TaskStatus{}, err
	}
	if doFetch {
		appendRepoServerResponse(lines, responseLabel, text, st.Result, snapshotUsed, reportOptions)
	}
	return st, nil
}

func appendRepoServerResponse(lines *[]string, remoteURL, text string, result any, snapshotUsed bool, options reportRenderOptions) {
	label := "## Repo: `" + remoteURL + "`"
	if snapshotUsed {
		label += " (baseline snapshot)"
	}
	appendMarkdownHeading(lines, label)
	if strings.TrimSpace(text) != "" {
		*lines = append(*lines, "### Changes", "")
		if options.DetailLevel >= reportDetailVerbose {
			appendFencedMarkdownBlock(lines, "diff", strings.TrimSpace(text))
		} else {
			appendFencedMarkdownBlock(lines, "text", summarizeRepoChanges(text, options.DetailLevel))
		}
	}
	if options.IncludeDebug && result != nil {
		b, _ := json.MarshalIndent(result, "", "  ")
		*lines = append(*lines, "### Server Response", "")
		appendFencedMarkdownBlock(lines, "json", strings.TrimSpace(string(b)))
	}
}

func summarizeRepoChanges(text string, level reportDetailLevel) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	headerLines := []string{}
	for _, line := range strings.Split(trimmed, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			break
		}
		line = strings.TrimRight(line, " ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		headerLines = append(headerLines, line)
	}
	if len(headerLines) == 0 {
		if level == reportDetailBrief {
			return trimContext(trimmed, 4, 220)
		}
		return trimContext(trimmed, 8, 360)
	}
	if level == reportDetailBrief {
		condensed := make([]string, 0, len(headerLines))
		if len(headerLines) > 0 {
			condensed = append(condensed, headerLines[0])
		}
		for _, line := range headerLines[1:] {
			if strings.Contains(line, "|") || strings.Contains(line, "file changed") || strings.Contains(line, "files changed") {
				condensed = append(condensed, line)
			}
		}
		if len(condensed) > 0 {
			return strings.Join(condensed, "\n")
		}
	}
	return strings.Join(headerLines, "\n")
}

func appendFeedbackContext(entry *[]string, ctx string, options reportRenderOptions) {
	if strings.TrimSpace(ctx) == "" || options.DetailLevel < reportDetailDetailed {
		return
	}
	ctx = trimContext(ctx, 8, 520)
	*entry = append(*entry, "", "**Context**", "")
	appendFencedMarkdownBlock(entry, "text", ctx)
}

func appendFeedbackEvidence(entry *[]string, meta *feedbackNotificationMeta, options reportRenderOptions) {
	if meta == nil || options.DetailLevel < reportDetailDetailed {
		return
	}
	if snippet := strings.TrimSpace(meta.EvidenceTarget); snippet != "" {
		*entry = append(*entry, "", "**Target Evidence**", "")
		appendFencedMarkdownBlock(entry, "text", snippet)
	}
	if snippet := strings.TrimSpace(meta.EvidencePeer); snippet != "" {
		*entry = append(*entry, "", "**Peer Evidence**", "")
		appendFencedMarkdownBlock(entry, "text", snippet)
	}
}

func appendFeedbackReferenceExcerpts(entry *[]string, refs []api.FeedbackReference, feedbackText string, feedbackContext string, meta *feedbackNotificationMeta, options reportRenderOptions) {
	if options.DetailLevel < reportDetailDetailed || len(refs) == 0 {
		return
	}
	seen := map[string]struct{}{}
	added := 0
	paths := extractReportPaths(feedbackContext, feedbackText, itemMetaEvidenceTarget(meta), itemMetaEvidencePeer(meta))
	anchors := extractFeedbackAnchors(feedbackContext, feedbackText, itemMetaEvidenceTarget(meta), itemMetaEvidencePeer(meta))
	tokens := feedbackTokenSet(strings.Join([]string{feedbackText, itemMetaEvidenceTarget(meta), itemMetaEvidencePeer(meta)}, "\n"))
	for _, ref := range refs {
		content := strings.TrimSpace(ref.Content)
		if content == "" {
			continue
		}
		sectionLabel, excerpt := selectRelevantReferenceExcerpt(content, paths, anchors, tokens)
		if strings.TrimSpace(excerpt) == "" {
			continue
		}
		key := strings.TrimSpace(ref.Title) + "\x00" + strings.TrimSpace(sectionLabel) + "\x00" + excerpt
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if added == 0 {
			*entry = append(*entry, "", "**Reference Excerpts**", "")
		}
		label := strings.TrimSpace(ref.Title)
		if label == "" {
			label = "Reference"
		}
		if trimmedSection := strings.TrimSpace(sectionLabel); trimmedSection != "" {
			label += " (" + trimmedSection + ")"
		}
		*entry = append(*entry, "Source: "+label)
		appendFencedMarkdownBlock(entry, "text", excerpt)
		added++
		if added >= 2 {
			return
		}
	}
}

type referenceSection struct {
	Path    string
	Content string
}

func selectRelevantReferenceExcerpt(content string, paths map[string]struct{}, anchors map[string]struct{}, tokens map[string]struct{}) (string, string) {
	sections := splitReferenceSections(content)
	if len(sections) == 0 {
		return "", trimContext(content, 6, 320)
	}
	best := referenceSection{}
	bestScore := -1
	for _, section := range sections {
		score := scoreReferenceSection(section, paths, anchors, tokens)
		if score > bestScore {
			bestScore = score
			best = section
		}
	}
	excerpt := bestReferenceExcerpt(best, anchors, tokens)
	return best.Path, excerpt
}

func splitReferenceSections(content string) []referenceSection {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	sections := []referenceSection{}
	current := []string{}
	currentPath := ""
	flush := func() {
		trimmed := strings.TrimSpace(strings.Join(current, "\n"))
		if trimmed == "" {
			current = nil
			currentPath = ""
			return
		}
		sections = append(sections, referenceSection{
			Path:    currentPath,
			Content: trimmed,
		})
		current = nil
		currentPath = ""
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == snapshotChunkDelimiter {
			flush()
			continue
		}
		if matches := reportFileHeaderPattern.FindStringSubmatch(line); len(matches) > 1 {
			flush()
			currentPath = strings.TrimSpace(matches[1])
		}
		current = append(current, line)
	}
	flush()
	if len(sections) == 0 && strings.TrimSpace(content) != "" {
		return []referenceSection{{Content: strings.TrimSpace(content)}}
	}
	return sections
}

func scoreReferenceSection(section referenceSection, paths map[string]struct{}, anchors map[string]struct{}, tokens map[string]struct{}) int {
	score := 0
	normalizedPath := normalizeReportPath(section.Path)
	if normalizedPath != "" {
		if _, ok := paths[normalizedPath]; ok {
			score += 120
		}
	}
	sectionText := strings.ToLower(section.Content)
	for anchor := range anchors {
		if strings.Contains(sectionText, anchor) {
			score += 8
		}
	}
	for token := range tokens {
		if strings.Contains(sectionText, token) {
			score += 2
		}
	}
	if normalizedPath != "" {
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(normalizedPath), filepath.Ext(normalizedPath)))
		if base != "" && strings.Contains(sectionText, base) {
			score += 6
		}
	}
	return score
}

func bestReferenceExcerpt(section referenceSection, anchors map[string]struct{}, tokens map[string]struct{}) string {
	lines := strings.Split(section.Content, "\n")
	bestIndex := -1
	bestScore := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == snapshotChunkDelimiter {
			continue
		}
		score := 0
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "### File:") {
			score += 1
		}
		for anchor := range anchors {
			if strings.Contains(lower, anchor) {
				score += 8
			}
		}
		for token := range tokens {
			if strings.Contains(lower, token) {
				score += 2
			}
		}
		if score > bestScore {
			bestScore = score
			bestIndex = i
		}
	}
	if bestIndex < 0 {
		return trimContext(section.Content, 6, 320)
	}
	start := bestIndex - 2
	if start < 0 {
		start = 0
	}
	end := bestIndex + 4
	if end > len(lines) {
		end = len(lines)
	}
	if section.Path != "" && start > 0 {
		start--
		if !strings.HasPrefix(strings.TrimSpace(lines[start]), "### File:") {
			start++
		}
	}
	return trimContext(strings.Join(lines[start:end], "\n"), 6, 320)
}

func appendMarkdownHeading(lines *[]string, heading string) {
	if len(*lines) > 0 {
		*lines = trimTrailingBlankLines(*lines)
		*lines = append(*lines, "")
	}
	*lines = append(*lines, heading, "")
}

func appendFencedMarkdownBlock(lines *[]string, language string, content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	fence := "~~~"
	if trimmed := strings.TrimSpace(language); trimmed != "" {
		fence += trimmed
	}
	*lines = append(*lines, fence)
	*lines = append(*lines, strings.Split(strings.TrimRight(content, "\n"), "\n")...)
	*lines = append(*lines, "~~~", "")
}

func trimTrailingBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func summarizeFeedbackReferences(direct []api.FeedbackReference, legacy []api.Reference) []reportReferenceSummary {
	order := []string{}
	index := map[string]int{}
	summaries := []reportReferenceSummary{}
	add := func(title, author, fallback string) {
		title = strings.TrimSpace(title)
		author = strings.TrimSpace(author)
		if title == "" {
			title = strings.TrimSpace(fallback)
		}
		if title == "" {
			title = "(untitled reference)"
		}
		key := strings.ToLower(title) + "\x00" + strings.ToLower(author)
		if idx, ok := index[key]; ok {
			summaries[idx].Excerpts++
			return
		}
		index[key] = len(summaries)
		order = append(order, key)
		summaries = append(summaries, reportReferenceSummary{
			Title:    title,
			Author:   author,
			Excerpts: 1,
		})
	}
	for _, ref := range direct {
		add(ref.Title, ref.Author, firstNonEmpty(ref.DocumentID, ref.NoteID))
	}
	for _, ref := range legacy {
		add(ref.Document.Title, ref.DocumentAuthor, ref.Document.DocumentID)
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].Title == summaries[j].Title {
			return summaries[i].Author < summaries[j].Author
		}
		return summaries[i].Title < summaries[j].Title
	})
	return summaries
}

func formatReportReference(ref reportReferenceSummary) string {
	parts := []string{}
	if ref.Author != "" {
		parts = append(parts, "author: "+ref.Author)
	}
	if ref.Excerpts > 1 {
		parts = append(parts, fmt.Sprintf("%d excerpts", ref.Excerpts))
	}
	if len(parts) == 0 {
		return ref.Title
	}
	return ref.Title + " (" + strings.Join(parts, "; ") + ")"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func pendingTaskStaleAfter() time.Duration {
	const defaultCutoff = 2 * time.Hour
	raw := strings.TrimSpace(os.Getenv("COMPAIR_PENDING_TASK_STALE_AFTER_SEC"))
	if raw == "" {
		return defaultCutoff
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultCutoff
	}
	return time.Duration(seconds) * time.Second
}

func isPendingRepoTaskStale(startedAt string) (bool, time.Duration, time.Duration) {
	cutoff := pendingTaskStaleAfter()
	if cutoff <= 0 {
		return false, 0, cutoff
	}
	ts := strings.TrimSpace(startedAt)
	if ts == "" {
		return false, 0, cutoff
	}
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false, 0, cutoff
	}
	age := time.Since(parsed)
	return age >= cutoff, age, cutoff
}

func persistPendingRepoTask(root string, cfg config.Project, repo *config.Repo, taskID, latest string, initialFeedback feedbackSnapshot) {
	if strings.TrimSpace(taskID) == "" {
		return
	}
	repo.PendingTaskID = taskID
	repo.PendingTaskCommit = latest
	repo.PendingTaskInitialFeedback = initialFeedback.Count
	if !initialFeedback.LatestTimestamp.IsZero() {
		repo.PendingTaskInitialFeedbackLatest = initialFeedback.LatestTimestamp.UTC().Format(time.RFC3339Nano)
	} else {
		repo.PendingTaskInitialFeedbackLatest = ""
	}
	repo.PendingTaskStartedAt = time.Now().UTC().Format(time.RFC3339)
	_ = config.WriteProjectConfig(root, cfg)
}

func clearPendingRepoTask(root string, cfg config.Project, repo *config.Repo) {
	if strings.TrimSpace(repo.PendingTaskID) == "" &&
		strings.TrimSpace(repo.PendingTaskCommit) == "" &&
		repo.PendingTaskInitialFeedback == 0 &&
		strings.TrimSpace(repo.PendingTaskInitialFeedbackLatest) == "" &&
		strings.TrimSpace(repo.PendingTaskStartedAt) == "" {
		return
	}
	repo.PendingTaskID = ""
	repo.PendingTaskCommit = ""
	repo.PendingTaskInitialFeedback = 0
	repo.PendingTaskInitialFeedbackLatest = ""
	repo.PendingTaskStartedAt = ""
	_ = config.WriteProjectConfig(root, cfg)
}

func finalizeRepoSync(root, group string, cfg config.Project, repo *config.Repo, latest string) {
	repo.LastSyncedCommit = latest
	repo.PendingTaskID = ""
	repo.PendingTaskCommit = ""
	repo.PendingTaskInitialFeedback = 0
	repo.PendingTaskInitialFeedbackLatest = ""
	repo.PendingTaskStartedAt = ""
	_ = config.WriteProjectConfig(root, cfg)
	upsertRepoWorkspaceBinding(root, group, repo.DocumentID, latest, repo.Unpublished)
}

func ensureRepoDocumentPublished(client *api.Client, docID, root string) {
	if strings.TrimSpace(docID) == "" {
		return
	}
	doc, err := client.GetDocumentMetadataByID(docID)
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
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "diff --git ") || strings.HasPrefix(trimmed, "### File:") {
			if maxChars < 20 {
				return s[:maxChars] + "..."
			}
			return strings.TrimRight(s[:maxChars-4], "\n ") + "\n..."
		}
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
		cmd.Flags().BoolVar(&syncPairwise, "pairwise", false, "Run one review pass per target/peer repo combination instead of using the shared peer pool")
		cmd.Flags().BoolVar(&syncCrossRepoOnly, "cross-repo-only", false, "When used with --pairwise, skip same-repo review pairs and only compare against other tracked repos")
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
	cmd.Flags().BoolVar(&syncReanalyzeExisting, "reanalyze-existing", false, "When used with --snapshot-mode snapshot, generate feedback from already-indexed content instead of only new chunks")
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
