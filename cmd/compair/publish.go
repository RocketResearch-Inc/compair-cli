package compair

import (
	"fmt"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	fsutil "github.com/RocketResearch-Inc/compair-cli/internal/fs"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"path/filepath"
	"strings"
)

var publishUnpublish bool

var publishCmd = &cobra.Command{
	Use:   "publish [pattern ...]",
	Short: "Publish tracked files matching patterns (glob)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		gid, _, err := groups.ResolveWithAuto(client, "", viper.GetString("group"))
		if err != nil {
			return err
		}
		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()
		publish := !publishUnpublish
		seen := map[string]bool{}
		for _, pat := range args {
			// Expand glob; if no match, treat as literal
			matches, _ := filepath.Glob(pat)
			if len(matches) == 0 {
				matches = []string{pat}
			}
			for _, m := range matches {
				can, err := fsutil.CanonicalPath(m, true)
				if err != nil {
					continue
				}
				if seen[can] {
					continue
				}
				seen[can] = true
                ti, err := store.FindByPathGroup(cmd.Context(), can, gid)
                if err != nil {
                    fmt.Println("Not tracked:", can)
                    continue
                }
				if ti.DocumentID == "" {
					fmt.Println("Not a document:", ti.Path)
					continue
				}
				if err := client.SetDocumentPublished(ti.DocumentID, publish); err != nil {
					fmt.Println("Publish error:", err)
					continue
				}
				if publish {
					ti.Published = 1
				} else {
					ti.Published = 0
				}
				_ = store.UpsertItem(cmd.Context(), ti)
				state := "published"
				if !publish {
					state = "unpublished"
				}
				fmt.Println(strings.Title(state)+":", ti.Path)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(publishCmd)
	publishCmd.Flags().BoolVar(&publishUnpublish, "unpublish", false, "Unpublish instead of publish")
}
