package compair

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/db"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
)

var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Compare tracked local files/repos with server documents and report mismatches",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		store, err := db.Open()
		if err != nil {
			return err
		}
		defer store.Close()

		groupNames := map[string]string{}
		if glist, err := client.ListGroups(false); err == nil {
			for _, g := range glist {
				id := g.ID
				if id == "" {
					id = g.GroupID
				}
				if id != "" {
					groupNames[id] = g.Name
				}
			}
		}

		var groupIDs []string
		if reconcileAll {
			groups, err := store.DistinctGroups(cmd.Context())
			if err != nil {
				return err
			}
			if len(groups) == 0 {
				fmt.Println("No tracked groups to reconcile.")
				return nil
			}
			sort.Strings(groups)
			groupIDs = groups
		} else {
			gid, _, err := groups.ResolveWithAuto(client, "", viper.GetString("group"))
			if err != nil {
				return err
			}
			groupIDs = []string{gid}
		}

		for idx, gid := range groupIDs {
			if idx > 0 {
				fmt.Println()
			}
			name := groupNames[gid]
			if strings.TrimSpace(name) == "" {
				name = "(unknown)"
			}
			if err := reconcileGroup(cmd, client, store, gid, name); err != nil {
				return err
			}
		}
		return nil
	},
}

var reconcileAll bool

func init() {
	rootCmd.AddCommand(reconcileCmd)
	reconcileCmd.Flags().BoolVar(&reconcileAll, "all", false, "Reconcile tracked items across all groups")
}

func reconcileGroup(cmd *cobra.Command, client *api.Client, store *db.Store, gid, name string) error {
	docs, err := client.ListDocuments(gid, false)
	if err != nil {
		return err
	}

	items, err := store.ListByGroup(cmd.Context(), gid)
	if err != nil {
		return err
	}

	remoteByID := make(map[string]api.Document, len(docs))
	titleIndex := make(map[string][]api.Document)
	for _, doc := range docs {
		remoteByID[doc.DocumentID] = doc
		key := strings.ToLower(strings.TrimSpace(doc.Title))
		if key != "" {
			titleIndex[key] = append(titleIndex[key], doc)
		}
	}

	newLinks := 0
	for i := range items {
		it := &items[i]
		if it.DocumentID != "" {
			doc, ok := remoteByID[it.DocumentID]
			if !ok {
				continue
			}
			pub := int64(0)
			if doc.IsPublished {
				pub = 1
			}
			if it.Published != pub {
				it.Published = pub
				if err := store.UpsertItem(cmd.Context(), it); err != nil {
					fmt.Println("Publish flag sync error:", err)
				}
			}
			continue
		}
		if it.Kind != "file" && it.Kind != "repo" {
			continue
		}
		base := strings.ToLower(strings.TrimSpace(filepath.Base(it.Path)))
		if base == "" {
			continue
		}
		matches := titleIndex[base]
		if len(matches) == 1 {
			doc := matches[0]
			it.DocumentID = doc.DocumentID
			if doc.IsPublished {
				it.Published = 1
			} else {
				it.Published = 0
			}
			if err := store.UpsertItem(cmd.Context(), it); err != nil {
				fmt.Println("Link error:", err)
				it.DocumentID = ""
				continue
			}
			fmt.Println("Linked:", it.Path, "→", it.DocumentID)
			newLinks++
		} else if len(matches) > 1 {
			fmt.Printf("Ambiguous remote matches for %s (skipping)\n", it.Path)
		}
	}

	docToLocals := make(map[string][]db.TrackedItem)
	localOnly := make([]db.TrackedItem, 0)
	staleLinks := make([]db.TrackedItem, 0)

	for _, it := range items {
		if strings.TrimSpace(it.DocumentID) == "" {
			localOnly = append(localOnly, it)
			continue
		}
		if doc, ok := remoteByID[it.DocumentID]; ok {
			docToLocals[doc.DocumentID] = append(docToLocals[doc.DocumentID], it)
		} else {
			staleLinks = append(staleLinks, it)
		}
	}

	remoteOnly := make([]api.Document, 0)
	for _, doc := range docs {
		if _, ok := docToLocals[doc.DocumentID]; !ok {
			remoteOnly = append(remoteOnly, doc)
		}
	}

	linkedIDs := make([]string, 0, len(docToLocals))
	for id := range docToLocals {
		linkedIDs = append(linkedIDs, id)
	}
	sort.Slice(linkedIDs, func(i, j int) bool {
		a := remoteByID[linkedIDs[i]]
		b := remoteByID[linkedIDs[j]]
		at := strings.ToLower(strings.TrimSpace(a.Title))
		bt := strings.ToLower(strings.TrimSpace(b.Title))
		if at == bt {
			return linkedIDs[i] < linkedIDs[j]
		}
		return at < bt
	})

	sort.Slice(localOnly, func(i, j int) bool { return localOnly[i].Path < localOnly[j].Path })
	sort.Slice(staleLinks, func(i, j int) bool { return staleLinks[i].Path < staleLinks[j].Path })
	sort.Slice(remoteOnly, func(i, j int) bool {
		ti := strings.ToLower(strings.TrimSpace(remoteOnly[i].Title))
		tj := strings.ToLower(strings.TrimSpace(remoteOnly[j].Title))
		if ti == tj {
			return remoteOnly[i].DocumentID < remoteOnly[j].DocumentID
		}
		return ti < tj
	})

	fmt.Printf("Group: %s (%s)\n", name, gid)
	fmt.Printf("Local tracked items: %d\n", len(items))
	fmt.Printf("Server documents: %d\n", len(docs))
	if newLinks > 0 {
		fmt.Printf("New links established: %d\n", newLinks)
	}

	if len(linkedIDs) > 0 {
		fmt.Println("\nLinked items:")
		for _, docID := range linkedIDs {
			doc := remoteByID[docID]
			title := strings.TrimSpace(doc.Title)
			if title == "" {
				title = "(untitled)"
			}
			localPaths := docToLocals[docID]
			sort.Slice(localPaths, func(i, j int) bool { return localPaths[i].Path < localPaths[j].Path })
			paths := make([]string, 0, len(localPaths))
			for _, lp := range localPaths {
				paths = append(paths, lp.Path)
			}
			published := "no"
			if doc.IsPublished {
				published = "yes"
			}
			fmt.Printf(" - %s (doc_id=%s, published=%s) -> %s\n", title, docID, published, strings.Join(paths, ", "))
		}
	} else {
		fmt.Println("\nNo linked items.")
	}

	if len(localOnly) > 0 {
		fmt.Println("\nLocal only (no server document):")
		for _, it := range localOnly {
			fmt.Printf(" - %s [%s]\n", it.Path, it.Kind)
		}
	} else {
		fmt.Println("\nNo local-only items detected.")
	}

	if len(staleLinks) > 0 {
		fmt.Println("\nLocal references missing on server (stale document links):")
		for _, it := range staleLinks {
			fmt.Printf(" - %s [%s] doc_id=%s\n", it.Path, it.Kind, it.DocumentID)
		}
	}

	if len(remoteOnly) > 0 {
		fmt.Println("\nServer only (no local tracking):")
		for _, doc := range remoteOnly {
			title := strings.TrimSpace(doc.Title)
			if title == "" {
				title = "(untitled)"
			}
			published := "no"
			if doc.IsPublished {
				published = "yes"
			}
			fmt.Printf(" - %s (doc_id=%s, published=%s)\n", title, doc.DocumentID, published)
		}
	} else {
		fmt.Println("\nNo server-only items detected.")
	}

	return nil
}
