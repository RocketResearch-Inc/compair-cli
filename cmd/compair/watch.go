package compair

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	"github.com/RocketResearch-Inc/compair-cli/internal/git"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/notify"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var watchInterval time.Duration
var watchNotify bool
var watchQuietNoChange bool
var onChangeCmd string
var watchAll bool

type watchRepo struct {
	Root        string
	DocID       string
	Last        string
	Unpublished bool
}

var watchCmd = &cobra.Command{
	Use:   "watch [PATH ...]",
	Short: "Continuously sync local repos on an interval and notify on local changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		gid, _, err := groups.ResolveWithAuto(client, "", viper.GetString("group"))
		if err != nil {
			return err
		}
		group := gid

		// Build set of repo roots
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
		} else if watchAll {
			store, err := db.Open()
			if err != nil {
				return err
			}
			rs, err := store.ListRepoRoots(cmd.Context(), group)
			_ = store.Close()
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
		}
		if watchInterval == 0 {
			watchInterval = 60 * time.Second
		}

		// Load per-repo config for doc IDs
		repos := []watchRepo{}
		for root := range roots {
			cfg, err := config.ReadProjectConfig(root)
			if err != nil || len(cfg.Repos) == 0 || cfg.Repos[0].DocumentID == "" {
				continue
			}
			repos = append(repos, watchRepo{
				Root:        root,
				DocID:       cfg.Repos[0].DocumentID,
				Last:        cfg.Repos[0].LastSyncedCommit,
				Unpublished: cfg.Repos[0].Unpublished,
			})
		}
		if len(repos) == 0 {
			fmt.Println("No repos with document_id found.")
			return nil
		}

		fmt.Printf("Watching %d repo(s) every %s (notify=%v)\n", len(repos), watchInterval, watchNotify)
		lastSeen := map[string]string{} // DocID -> last commit
		for _, r := range repos {
			lastSeen[r.DocID] = r.Last
		}

		for {
			totalCommits := 0
			totalFeedback := 0
			var jsonPath string

			for i := range repos {
				r := &repos[i]
				if !r.Unpublished {
					ensureRepoDocumentPublished(client, r.DocID, r.Root)
				}
				text, latest := git.CollectChangeTextAt(r.Root, lastSeen[r.DocID])
				if len(text) == 0 {
					continue
				}
				resp, err := client.ProcessDoc(r.DocID, text, true)
				if err != nil {
					if !watchQuietNoChange {
						fmt.Println("process error:", err)
					}
					continue
				}
				st, err := client.GetTaskStatus(resp.TaskID)
				if err != nil {
					if !watchQuietNoChange {
						fmt.Println("status error:", err)
					}
					continue
				}
				// dump JSON to temp file for hook
				b, _ := json.Marshal(st.Result)
				tmp := filepath.Join(os.TempDir(), fmt.Sprintf("compair-sync-%d.json", time.Now().UnixNano()))
				_ = os.WriteFile(tmp, b, 0o600)
				jsonPath = tmp
				if latest != "" && lastSeen[r.DocID] != latest {
					totalCommits++
					lastSeen[r.DocID] = latest
					// persist back to repo config
					if cfg, err := config.ReadProjectConfig(r.Root); err == nil && len(cfg.Repos) > 0 {
						cfg.Repos[0].LastSyncedCommit = latest
						_ = config.WriteProjectConfig(r.Root, cfg)
					}
				}
				if st.Status == "SUCCESS" {
					totalFeedback++
				}
			}

			if totalCommits == 0 && totalFeedback == 0 {
				if !watchQuietNoChange {
					fmt.Println("No new relevant changes.")
				}
				time.Sleep(watchInterval)
				continue
			}

			if watchNotify {
				notify.Try("Compair: updates detected", fmt.Sprintf("%d commits, %d feedback items", totalCommits, totalFeedback))
			}
			if onChangeCmd != "" {
				c := exec.Command("sh", "-lc", onChangeCmd)
				c.Env = append(os.Environ(),
					fmt.Sprintf("COMPAIR_COMMITS=%d", totalCommits),
					fmt.Sprintf("COMPAIR_FEEDBACK_COUNT=%d", totalFeedback),
					fmt.Sprintf("COMPAIR_SYNC_JSON=%s", jsonPath),
				)
				c.Stdout, c.Stderr = os.Stdout, os.Stderr
				_ = c.Run()
			}
			time.Sleep(watchInterval)
		}
	},
}

func init() {
	rootCmd.AddCommand(watchCmd)
	watchCmd.Flags().DurationVar(&watchInterval, "interval", 60*time.Second, "Polling interval (e.g., 30s, 2m)")
	watchCmd.Flags().BoolVar(&watchNotify, "notify", true, "Send a system notification on changes")
	watchCmd.Flags().BoolVar(&watchQuietNoChange, "quiet-no-change", true, "Suppress output when nothing changed")
	watchCmd.Flags().StringVar(&onChangeCmd, "on-change", "", "Shell command to run when changes are detected")
	watchCmd.Flags().BoolVar(&watchAll, "all", false, "Watch all tracked repos in the active group")
}
