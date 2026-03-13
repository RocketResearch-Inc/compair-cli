package compair

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/RocketResearch-Inc/compair-cli/internal/printer"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var notesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Create or list document notes",
}

var notesListJSON bool
var notesListCmd = &cobra.Command{
	Use:   "list <document_id>",
	Short: "List notes for a document",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		notes, err := client.ListNotes(args[0])
		if err != nil {
			return err
		}
		if notesListJSON {
			printer.PrintJSON(notes)
			return nil
		}
		if len(notes) == 0 {
			fmt.Println("No notes found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tAuthor\tCreated\tPreview")
		for _, note := range notes {
			author := note.AuthorID
			if note.Author != nil {
				if note.Author.Name != "" {
					author = note.Author.Name
				} else if note.Author.Username != "" {
					author = note.Author.Username
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				note.NoteID,
				author,
				formatTimestamp(note.DatetimeCreated),
				truncateText(note.Content, 80),
			)
		}
		return w.Flush()
	},
}

var notesAddContent string
var notesAddFile string
var notesAddGroup string
var notesAddCmd = &cobra.Command{
	Use:   "add <document_id>",
	Short: "Add a note to a document",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		content := notesAddContent
		if notesAddFile != "" {
			data, err := os.ReadFile(notesAddFile)
			if err != nil {
				return err
			}
			content = string(data)
		}
		if strings.TrimSpace(content) == "" {
			return fmt.Errorf("note content is required (use --content or --file)")
		}
		client := api.NewClient(viper.GetString("api.base"))
		groupID := ""
		if strings.TrimSpace(notesAddGroup) != "" {
			var err error
			groupID, _, err = groups.ResolveWithAuto(client, notesAddGroup, viper.GetString("group"))
			if err != nil {
				return err
			}
		}
		note, err := client.CreateNote(args[0], content, groupID)
		if err != nil {
			return err
		}
		printer.Success("Created note " + note.NoteID)
		return nil
	},
}

var notesGetJSON bool
var notesGetCmd = &cobra.Command{
	Use:   "get <note_id>",
	Short: "Get a note by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		note, err := client.GetNote(args[0])
		if err != nil {
			return err
		}
		if notesGetJSON {
			printer.PrintJSON(note)
			return nil
		}
		fmt.Println(note.Content)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(notesCmd)
	notesCmd.AddCommand(notesListCmd)
	notesCmd.AddCommand(notesAddCmd)
	notesCmd.AddCommand(notesGetCmd)

	notesListCmd.Flags().BoolVar(&notesListJSON, "json", false, "Output raw JSON")
	notesAddCmd.Flags().StringVar(&notesAddContent, "content", "", "Note content")
	notesAddCmd.Flags().StringVar(&notesAddFile, "file", "", "Path to a file to use as note content")
	notesAddCmd.Flags().StringVar(&notesAddGroup, "group", "", "Group id or name to associate with the note")
	notesGetCmd.Flags().BoolVar(&notesGetJSON, "json", false, "Output raw JSON")
}
