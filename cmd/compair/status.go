package compair

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/auth"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
	cliTelemetry "github.com/RocketResearch-Inc/compair-cli/internal/telemetry"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current auth, repo, and sync defaults",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		caps, _ := client.Capabilities(10 * time.Minute)

		fmt.Println("Compair CLI status")
		fmt.Println()

		apiBase := strings.TrimSpace(viper.GetString("api.base"))
		if apiBase == "" {
			apiBase = "http://localhost:4000"
		}
		profileName := strings.TrimSpace(viper.GetString("profile.active"))
		profileSource := strings.TrimSpace(viper.GetString("profile.source"))

		fmt.Println("API:")
		fmt.Printf("  Base: %s\n", apiBase)
		if profileName != "" {
			if profileSource != "" {
				fmt.Printf("  Profile: %s %s\n", profileName, profileSource)
			} else {
				fmt.Printf("  Profile: %s\n", profileName)
			}
		} else {
			fmt.Println("  Profile: (none)")
		}
		if caps != nil {
			fmt.Printf("  Repo inputs: %s\n", yesNo(caps.Inputs.Repos))
		}
		fmt.Println()

		printAuthStatus(client, caps)
		fmt.Println()
		printGroupStatus(client)
		fmt.Println()
		printRepoStatus()
		fmt.Println()
		printSnapshotStatus()
		fmt.Println()
		printTelemetryStatusBlock()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func printAuthStatus(client *api.Client, caps *api.Capabilities) {
	fmt.Println("Auth:")
	creds, credErr := auth.Load()
	if credErr == nil && strings.TrimSpace(auth.Token()) != "" {
		fmt.Println("  Token cache: present")
	} else {
		fmt.Println("  Token cache: missing")
	}

	session, sessionErr := client.EnsureSession()
	if sessionErr != nil {
		msg := strings.TrimSpace(sessionErr.Error())
		if msg == "" {
			msg = "not authenticated"
		}
		fmt.Printf("  Session: unavailable (%s)\n", msg)
		fmt.Println("  Tip: run 'compair login' if you expected a valid session.")
		if creds.Username != "" {
			fmt.Printf("  Cached user: %s (%s)\n", creds.Username, strings.TrimSpace(creds.UserID))
		}
		return
	}

	userID := strings.TrimSpace(session.UserID)
	username := strings.TrimSpace(session.Username)
	var userInfo api.UserInfo
	var userInfoLoaded bool
	if userID != "" {
		if user, err := client.LoadUserByID(userID); err == nil {
			userInfo = user
			userInfoLoaded = true
			if strings.TrimSpace(user.Username) != "" {
				username = user.Username
			}
		}
	}
	if username != "" && userID != "" {
		fmt.Printf("  User: %s (%s)\n", username, userID)
	} else if userID != "" {
		fmt.Printf("  User: %s\n", userID)
	}
	if strings.TrimSpace(session.DatetimeValidUntil) != "" {
		fmt.Printf("  Session valid until: %s\n", session.DatetimeValidUntil)
	}
	if plan, err := client.LoadUserPlan(); err == nil && plan != "" {
		fmt.Printf("  Plan: %s\n", plan)
	}
	if caps != nil {
		fmt.Printf("  Feedback per day: %s\n", formatCapabilityLimit(caps.Limits.FeedbackPerDay))
		fmt.Printf("  Document slots: %s\n", formatCapabilityLimit(caps.Limits.Docs))
	}
	if !userInfoLoaded {
		fmt.Println("  Self-feedback: unavailable (could not load the current user profile from the server)")
	} else if userInfo.IncludeOwnDocumentsInFeedback != nil {
		fmt.Printf("  Self-feedback: %s\n", onOff(*userInfo.IncludeOwnDocumentsInFeedback))
	} else {
		fmt.Println("  Self-feedback: unavailable (server has not exposed this field yet)")
	}
	if !userInfoLoaded {
		fmt.Println("  Feedback length: unavailable (could not load the current user profile from the server)")
	} else if strings.TrimSpace(userInfo.PreferredFeedbackLength) != "" {
		fmt.Printf("  Feedback length: %s\n", userInfo.PreferredFeedbackLength)
	} else {
		fmt.Println("  Feedback length: unavailable (server has not exposed this field yet)")
	}
}

func printGroupStatus(client *api.Client) {
	fmt.Println("Group:")
	activeGroup, err := config.ResolveActiveGroup(viper.GetString("group"))
	if err != nil || strings.TrimSpace(activeGroup) == "" {
		fmt.Println("  Active: (none)")
		fmt.Println("  Tip: run 'compair group ls' then 'compair group use <id>'.")
		return
	}
	label := activeGroup
	if groups, err := client.ListGroups(false); err == nil {
		for _, g := range groups {
			id := strings.TrimSpace(g.ID)
			if id == "" {
				id = strings.TrimSpace(g.GroupID)
			}
			if id == activeGroup {
				name := strings.TrimSpace(g.Name)
				if name != "" {
					label = fmt.Sprintf("%s (%s)", activeGroup, name)
				}
				break
			}
		}
	}
	fmt.Printf("  Active: %s\n", label)
}

func printRepoStatus() {
	fmt.Println("Repo:")
	root, err := git.RepoRoot()
	if err != nil {
		fmt.Println("  Current directory: not inside a git repo")
		return
	}

	fmt.Printf("  Root: %s\n", root)
	remote, err := git.OriginURLAt(root)
	if err == nil && strings.TrimSpace(remote) != "" {
		fmt.Printf("  Remote: %s\n", remote)
	}

	cfg, err := config.ReadProjectConfig(root)
	if err != nil {
		fmt.Println("  Binding: not initialized (run 'compair track')")
		return
	}
	if cfg.Group.ID != "" {
		groupLabel := cfg.Group.ID
		if strings.TrimSpace(cfg.Group.Name) != "" && cfg.Group.Name != "Group" {
			groupLabel = fmt.Sprintf("%s (%s)", cfg.Group.ID, cfg.Group.Name)
		}
		fmt.Printf("  Group: %s\n", groupLabel)
	}
	if len(cfg.Repos) == 0 {
		fmt.Println("  Binding: config exists but no repo records were found")
		return
	}
	repo := cfg.Repos[0]
	if repo.DocumentID != "" {
		fmt.Printf("  Document: %s\n", repo.DocumentID)
	}
	fmt.Printf("  Published for cross-repo review: %s\n", yesNo(!repo.Unpublished))
	if repo.DefaultBranch != "" {
		fmt.Printf("  Default branch: %s\n", repo.DefaultBranch)
	}
	if repo.LastSyncedCommit != "" {
		fmt.Printf("  Last synced commit: %s\n", shortSHA(repo.LastSyncedCommit))
	} else {
		fmt.Println("  Last synced commit: (none yet)")
	}
	if strings.TrimSpace(repo.PendingTaskID) != "" {
		fmt.Printf("  Pending processing task: %s\n", repo.PendingTaskID)
		if strings.TrimSpace(repo.PendingTaskCommit) != "" {
			fmt.Printf("  Pending task commit: %s\n", shortSHA(repo.PendingTaskCommit))
		}
		fmt.Println("  Tip: rerun 'compair review' or 'compair sync' to continue waiting on the saved task.")
	}
}

func printSnapshotStatus() {
	fmt.Println("Snapshot defaults:")
	opts := defaultSnapshotOptions()
	if prof := loadActiveProfileSnapshot(); prof != nil {
		applySnapshotOverrides(&opts, *prof)
	}
	fmt.Printf("  Coverage: %s\n", describeSnapshotCoverage(opts))
	fmt.Printf("  Tree entries: %s\n", describeSnapshotCountLimit(opts.MaxTreeEntries))
	fmt.Printf("  Files: %s\n", describeSnapshotCountLimit(opts.MaxFiles))
	fmt.Printf("  Total content: %s\n", describeStatusByteLimit(opts.MaxTotalBytes, "full repo (no cap)"))
	fmt.Printf("  Per file: %s\n", describeStatusByteLimit(opts.MaxFileBytes, "full file (no cap)"))
	fmt.Printf("  Read cap: %s\n", describeStatusByteLimit(opts.MaxFileRead, "no read cap"))
	fmt.Printf("  Tip: use 'compair push --snapshot-max-total-bytes 300000 --snapshot-max-files 60' if a full-repo snapshot is too heavy for a given run.\n")
}

func printTelemetryStatusBlock() {
	fmt.Println("Telemetry:")
	status, err := cliTelemetry.CurrentStatus()
	if err != nil {
		fmt.Printf("  Status: unavailable (%s)\n", strings.TrimSpace(err.Error()))
		return
	}
	fmt.Printf("  CLI anonymous heartbeat: %s\n", onOff(status.Enabled))
	fmt.Printf("  Endpoint: %s\n", status.BaseURL)
	if strings.TrimSpace(status.LastHeartbeatAt) != "" {
		fmt.Printf("  Last heartbeat: %s\n", status.LastHeartbeatAt)
	} else {
		fmt.Println("  Last heartbeat: (none yet)")
	}
}

func describeSnapshotCoverage(opts snapshotOptions) string {
	if opts.MaxTreeEntries <= 0 && opts.MaxFiles <= 0 && opts.MaxTotalBytes <= 0 && opts.MaxFileBytes <= 0 && opts.MaxFileRead <= 0 {
		return "full repo by default"
	}
	return "explicit limits in effect"
}

func describeSnapshotCountLimit(limit int) string {
	if limit <= 0 {
		return "full repo (no cap)"
	}
	return fmt.Sprintf("%d", limit)
}

func formatCapabilityLimit(limit *int) string {
	if limit == nil {
		return "not advertised"
	}
	if *limit <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d", *limit)
}

func describeStatusByteLimit(limit int, zeroLabel string) string {
	if limit <= 0 {
		return zeroLabel
	}
	return formatBytes(int64(limit))
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func shortSHA(sha string) string {
	trimmed := strings.TrimSpace(sha)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}
