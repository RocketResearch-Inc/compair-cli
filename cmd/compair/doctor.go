package compair

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
)

type doctorSummary struct {
	Warnings int
	Errors   int
}

type doctorCheck struct {
	Status string `json:"status"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type doctorReport struct {
	APIBase  string        `json:"api_base"`
	Profile  string        `json:"profile,omitempty"`
	Warnings int           `json:"warnings"`
	Errors   int           `json:"errors"`
	Checks   []doctorCheck `json:"checks"`
}

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose common auth, group, and repo binding problems",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		summary := doctorSummary{}
		report := doctorReport{
			APIBase: strings.TrimSpace(viper.GetString("api.base")),
		}
		if profile := strings.TrimSpace(viper.GetString("profile.active")); profile != "" {
			report.Profile = profile
		}
		emit := !doctorJSON

		if emit {
			fmt.Println("Compair doctor")
			fmt.Println()
		}

		printDoctorInfo(&report, emit, "API base", strings.TrimSpace(viper.GetString("api.base")))
		if profile := strings.TrimSpace(viper.GetString("profile.active")); profile != "" {
			printDoctorInfo(&report, emit, "Profile", profile)
		}

		if tok := strings.TrimSpace(auth.Token()); tok == "" {
			doctorWarn(&report, &summary, emit, "Auth token", "missing", "Run 'compair login' to create or save a session.")
		} else {
			doctorOK(&report, emit, "Auth token", "present")
		}

		session, sessionErr := client.EnsureSession()
		var userInfo api.UserInfo
		var userInfoLoaded bool
		if sessionErr != nil {
			doctorFail(&report, &summary, emit, "Server session", compactErr(sessionErr), "Run 'compair login' or confirm the API is reachable.")
		} else {
			label := session.UserID
			if session.Username != "" {
				label = fmt.Sprintf("%s (%s)", session.Username, session.UserID)
			}
			doctorOK(&report, emit, "Server session", label)
			if session.DatetimeValidUntil != "" {
				printDoctorInfo(&report, emit, "Session valid until", session.DatetimeValidUntil)
			}
			if session.UserID != "" {
				if user, err := client.LoadUserByID(session.UserID); err == nil {
					userInfo = user
					userInfoLoaded = true
					if user.IncludeOwnDocumentsInFeedback != nil {
						doctorOK(&report, emit, "Self-feedback", onOff(*user.IncludeOwnDocumentsInFeedback))
					}
				}
			}
		}

		caps, _ := client.Capabilities(10 * time.Minute)
		if caps != nil {
			doctorOK(&report, emit, "Repo inputs", yesNo(caps.Inputs.Repos))
		}

		var groupItems []api.GroupItem
		if sessionErr == nil {
			items, err := client.ListGroups(false)
			if err != nil {
				doctorWarn(&report, &summary, emit, "Group lookup", compactErr(err), "Run 'compair group ls' once the API is healthy.")
			} else {
				groupItems = items
				doctorOK(&report, emit, "Groups visible", fmt.Sprintf("%d", len(items)))
			}
		}

		activeGroup, err := config.ResolveActiveGroup(viper.GetString("group"))
		if err != nil {
			if sessionErr == nil {
				if autoGroup := suggestedGroupID(groupItems); autoGroup != "" {
					doctorWarn(&report, &summary, emit, "Active group", "not explicitly set", fmt.Sprintf("Commands can auto-select %s, but you should run 'compair group use %s'.", autoGroup, autoGroup))
				} else {
					doctorWarn(&report, &summary, emit, "Active group", "not set", "Run 'compair group ls' then 'compair group use <id>'.")
				}
			} else {
				doctorWarn(&report, &summary, emit, "Active group", "unknown", "Set one with 'compair group use <id>' after auth is working.")
			}
		} else {
			if containsGroupID(groupItems, activeGroup) || len(groupItems) == 0 {
				doctorOK(&report, emit, "Active group", activeGroup)
			} else {
				doctorFail(&report, &summary, emit, "Active group", activeGroup, "This group is not visible to the current user. Run 'compair group ls' and switch to a live group.")
			}
		}

		root, repoErr := git.RepoRoot()
		if repoErr != nil {
			doctorWarn(&report, &summary, emit, "Repo", "not inside a git repo", "Change into a repo before checking local bindings.")
			return finishDoctor(report, summary, emit)
		}
		doctorOK(&report, emit, "Repo root", root)

		cfgPath := config.ProjectConfigPath(root)
		cfg, cfgErr := config.ReadProjectConfig(root)
		if cfgErr != nil {
			doctorWarn(&report, &summary, emit, "Repo binding", fmt.Sprintf("missing %s", cfgPath), "Run 'compair track' to create a repo document and local binding, or set COMPAIR_PROJECT_CONFIG_PATH when the binding lives outside the repo.")
			return finishDoctor(report, summary, emit)
		}
		doctorOK(&report, emit, "Repo binding", cfgPath)

		if cfg.Group.ID != "" {
			if activeGroup != "" && cfg.Group.ID != activeGroup {
				doctorWarn(&report, &summary, emit, "Repo group", cfg.Group.ID, fmt.Sprintf("This repo is bound to a different group than the active one. Consider 'compair group use %s' or re-run 'compair track --group %s'.", cfg.Group.ID, activeGroup))
			} else {
				doctorOK(&report, emit, "Repo group", cfg.Group.ID)
			}
			if len(groupItems) > 0 && !containsGroupID(groupItems, cfg.Group.ID) {
				doctorFail(&report, &summary, emit, "Repo group binding", cfg.Group.ID, "This local binding points at a group you no longer belong to. Re-run 'compair track --group <current-group-id>'.")
			}
		}

		if len(cfg.Repos) == 0 {
			doctorFail(&report, &summary, emit, "Repo document", "missing", "Run 'compair track' to recreate the repo document binding.")
			return finishDoctor(report, summary, emit)
		}

		repo := cfg.Repos[0]
		if caps != nil {
			if caps.Inputs.Repos {
				doctorOK(&report, emit, "Repo review capability", "available")
			} else {
				doctorFail(&report, &summary, emit, "Repo review capability", "disabled by this API", "Switch to a profile/server that advertises repository inputs before using repo sync.")
			}
		}
		if repo.DocumentID == "" {
			doctorFail(&report, &summary, emit, "Repo document", "document_id missing", "Run 'compair track' to recreate the repo document binding.")
		} else if sessionErr != nil {
			doctorWarn(&report, &summary, emit, "Repo document", repo.DocumentID, "Auth is unavailable, so the server binding could not be verified.")
		} else {
			doc, err := client.GetDocumentByID(repo.DocumentID)
			if err != nil {
				doctorFail(&report, &summary, emit, "Repo document", repo.DocumentID, "The server could not load this document. Re-run 'compair track --group <current-group-id>' to repair the binding.")
			} else {
				doctorOK(&report, emit, "Repo document", fmt.Sprintf("%s (%s)", repo.DocumentID, doc.Title))
				if doc.DocType != "" && doc.DocType != "code-repo" {
					doctorWarn(&report, &summary, emit, "Repo document type", doc.DocType, "This repo is bound to a non-code document type. Re-run 'compair track' if review quality looks wrong.")
				}
				if !doc.IsPublished {
					doctorWarn(&report, &summary, emit, "Repo publish state", "unpublished", "Cross-repo feedback is reduced when repo docs are unpublished. Run 'compair review' or 'compair sync' to auto-publish, or re-track without --unpublished.")
				} else {
					doctorOK(&report, emit, "Repo publish state", "published")
				}
			}
		}

		if strings.TrimSpace(repo.LastSyncedCommit) == "" {
			doctorWarn(&report, &summary, emit, "Last synced commit", "missing", "Run 'compair review' or 'compair sync' to establish the first sync baseline.")
		} else {
			doctorOK(&report, emit, "Last synced commit", shortSHA(repo.LastSyncedCommit))
		}
		if strings.TrimSpace(repo.PendingTaskID) != "" {
			detail := repo.PendingTaskID
			if strings.TrimSpace(repo.PendingTaskCommit) != "" {
				detail = fmt.Sprintf("%s (%s)", repo.PendingTaskID, shortSHA(repo.PendingTaskCommit))
			}
			doctorWarn(
				&report,
				&summary,
				emit,
				"Pending processing task",
				detail,
				"Run 'compair wait' to reattach to the saved task, or rerun 'compair review' to continue waiting without resubmitting this repo.",
			)
		}

		compareGroup := strings.TrimSpace(cfg.Group.ID)
		if compareGroup == "" {
			compareGroup = strings.TrimSpace(activeGroup)
		}
		if compareGroup == "" {
			doctorWarn(&report, &summary, emit, "Workspace DB", "skipped", "No group was available to compare the local workspace binding.")
		} else if store, err := db.Open(); err != nil {
			doctorWarn(&report, &summary, emit, "Workspace DB", compactErr(err), "The local workspace database could not be opened for comparison.")
		} else {
			defer store.Close()
			item, err := store.FindByPathGroup(context.Background(), root, compareGroup)
			if err != nil {
				if err == sql.ErrNoRows {
					doctorWarn(&report, &summary, emit, "Workspace DB binding", "missing repo entry", "Run 'compair track' or 'compair review' to refresh ~/.compair/workspace.db.")
				} else {
					doctorWarn(&report, &summary, emit, "Workspace DB binding", compactErr(err), "The repo entry could not be read from ~/.compair/workspace.db.")
				}
			} else {
				doctorOK(&report, emit, "Workspace DB binding", fmt.Sprintf("%s (%s)", item.Path, compareGroup))
				if strings.TrimSpace(item.DocumentID) != strings.TrimSpace(repo.DocumentID) {
					doctorFail(&report, &summary, emit, "Workspace DB document", item.DocumentID, "The workspace DB points at a different document than .compair/config.yaml. Run 'compair track' or 'compair review' to repair it.")
				}
				if strings.TrimSpace(item.RepoRoot) != "" && strings.TrimSpace(item.RepoRoot) != strings.TrimSpace(root) {
					doctorFail(&report, &summary, emit, "Workspace DB repo root", item.RepoRoot, "The workspace DB repo root does not match the current repo path. Re-track this repo to repair it.")
				}
				if strings.TrimSpace(repo.LastSyncedCommit) != "" && strings.TrimSpace(item.LastSyncedCommit) != "" && strings.TrimSpace(repo.LastSyncedCommit) != strings.TrimSpace(item.LastSyncedCommit) {
					doctorWarn(&report, &summary, emit, "Workspace DB sync state", shortSHA(item.LastSyncedCommit), "The workspace DB and .compair/config.yaml disagree on the last synced commit. Run 'compair review' or 'compair sync' to reconcile them.")
				}
				configPublished := !repo.Unpublished
				dbPublished := item.Published == 1
				if configPublished != dbPublished {
					doctorWarn(&report, &summary, emit, "Workspace DB publish state", yesNo(dbPublished), "The workspace DB publish state disagrees with .compair/config.yaml. Run 'compair review' or 'compair sync' to reconcile it.")
				}
			}
		}

		if userInfoLoaded && userInfo.IncludeOwnDocumentsInFeedback == nil {
			doctorWarn(&report, &summary, emit, "Self-feedback", "not exposed by the server", "Redeploy the backend after the latest schema update to let the CLI read this setting.")
		}

		return finishDoctor(report, summary, emit)
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output machine-readable diagnostics JSON")
}

func doctorOK(report *doctorReport, emit bool, label, detail string) {
	appendDoctorCheck(report, doctorCheck{Status: "ok", Label: label, Detail: normalizeDoctorDetail(detail)})
	if emit {
		fmt.Printf("[ok]   %s: %s\n", label, normalizeDoctorDetail(detail))
	}
}

func doctorWarn(report *doctorReport, summary *doctorSummary, emit bool, label, detail, fix string) {
	if summary != nil {
		summary.Warnings++
	}
	appendDoctorCheck(report, doctorCheck{Status: "warn", Label: label, Detail: normalizeDoctorDetail(detail), Fix: strings.TrimSpace(fix)})
	if emit {
		fmt.Printf("[warn] %s: %s\n", label, normalizeDoctorDetail(detail))
		if strings.TrimSpace(fix) != "" {
			fmt.Printf("       fix: %s\n", fix)
		}
	}
}

func doctorFail(report *doctorReport, summary *doctorSummary, emit bool, label, detail, fix string) {
	if summary != nil {
		summary.Errors++
	}
	appendDoctorCheck(report, doctorCheck{Status: "fail", Label: label, Detail: normalizeDoctorDetail(detail), Fix: strings.TrimSpace(fix)})
	if emit {
		fmt.Printf("[fail] %s: %s\n", label, normalizeDoctorDetail(detail))
		if strings.TrimSpace(fix) != "" {
			fmt.Printf("       fix: %s\n", fix)
		}
	}
}

func printDoctorInfo(report *doctorReport, emit bool, label, detail string) {
	appendDoctorCheck(report, doctorCheck{Status: "info", Label: label, Detail: normalizeDoctorDetail(detail)})
	if emit {
		fmt.Printf("       %s: %s\n", label, normalizeDoctorDetail(detail))
	}
}

func appendDoctorCheck(report *doctorReport, check doctorCheck) {
	if report == nil {
		return
	}
	report.Checks = append(report.Checks, check)
}

func finishDoctor(report doctorReport, summary doctorSummary, emit bool) error {
	report.Errors = summary.Errors
	report.Warnings = summary.Warnings
	if emit {
		fmt.Println()
		if summary.Errors == 0 && summary.Warnings == 0 {
			fmt.Println("Doctor summary: no obvious problems found.")
			return nil
		}
		fmt.Printf("Doctor summary: %d error(s), %d warning(s).\n", summary.Errors, summary.Warnings)
		return nil
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func normalizeDoctorDetail(detail string) string {
	trimmed := strings.TrimSpace(detail)
	if trimmed == "" {
		return "(none)"
	}
	return trimmed
}

func containsGroupID(items []api.GroupItem, id string) bool {
	target := strings.TrimSpace(id)
	if target == "" {
		return false
	}
	for _, item := range items {
		candidate := strings.TrimSpace(item.ID)
		if candidate == "" {
			candidate = strings.TrimSpace(item.GroupID)
		}
		if candidate == target {
			return true
		}
	}
	return false
}

func suggestedGroupID(items []api.GroupItem) string {
	if len(items) == 0 {
		return ""
	}
	creds, _ := auth.Load()
	username := strings.TrimSpace(creds.Username)
	shortName := username
	if i := strings.Index(username, "@"); i > 0 {
		shortName = username[:i]
	}
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if strings.EqualFold(name, username) || strings.EqualFold(name, shortName) {
			if id := groupItemID(item); id != "" {
				return id
			}
		}
	}
	return groupItemID(items[0])
}

func groupItemID(item api.GroupItem) string {
	id := strings.TrimSpace(item.ID)
	if id == "" {
		id = strings.TrimSpace(item.GroupID)
	}
	return id
}

func compactErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 140 {
		msg = msg[:137] + "..."
	}
	return msg
}
