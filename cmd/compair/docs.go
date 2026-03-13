package compair

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Inspect documents in Compair",
}

var docsAllGroups bool
var docsOwnOnly bool
var docsFilter string
var docsPage int
var docsPageSize int
var docsAllPages bool
var docsJSON bool

var docsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List documents",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		groupID := ""
		if !docsAllGroups {
			var err error
			groupID, _, err = groups.ResolveWithAuto(client, "", viper.GetString("group"))
			if err != nil {
				return err
			}
		}
		opts := api.ListDocumentsOptions{
			GroupID:    groupID,
			OwnOnly:    docsOwnOnly,
			FilterType: docsFilter,
			Page:       docsPage,
			PageSize:   docsPageSize,
			AllPages:   docsAllPages,
		}
		docs, err := client.ListDocumentsWithOptions(opts)
		if err != nil {
			return err
		}
		if docsJSON {
			printer.PrintJSON(docs)
			return nil
		}
		if len(docs) == 0 {
			fmt.Println("No documents found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tTitle\tPublished\tUpdated\tType")
		for _, doc := range docs {
			updated := formatTimestamp(doc.DatetimeModified)
			if updated == "" {
				updated = formatTimestamp(doc.DatetimeCreated)
			}
			pub := "no"
			if doc.IsPublished {
				pub = "yes"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", doc.DocumentID, doc.Title, pub, updated, doc.DocType)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(docsCmd)
	docsCmd.AddCommand(docsListCmd)
	docsListCmd.Flags().BoolVar(&docsAllGroups, "all-groups", false, "Include documents from all groups")
	docsListCmd.Flags().BoolVar(&docsOwnOnly, "own-only", false, "Only include documents you authored")
	docsListCmd.Flags().StringVar(&docsFilter, "filter", "", "Filter type (recently_updated, recently_compaired, unpublished)")
	docsListCmd.Flags().IntVar(&docsPage, "page", 1, "Page number")
	docsListCmd.Flags().IntVar(&docsPageSize, "page-size", 20, "Items per page")
	docsListCmd.Flags().BoolVar(&docsAllPages, "all-pages", false, "Fetch all pages")
	docsListCmd.Flags().BoolVar(&docsJSON, "json", false, "Output raw JSON")
}
