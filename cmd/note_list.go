package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
)

// noteListCmd represents the note list command
var noteListCmd = &cobra.Command{
	Use:   "list [category]",
	Short: "List notes",
	Long: `List all notes or notes in a specific category.

When the store reads other stores (stores.read), their notes are listed too,
each naming its store.

Examples:
  claudemem note list              # List all notes
  claudemem note list work         # List notes in work category`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		category := ""
		if len(args) > 0 {
			category = args[0]
		}

		stores, err := openReadStores()
		if err != nil {
			return err
		}
		defer closeReadStores(stores)

		// List every store, by category and title across them
		var listed []noteInStore
		for i, rs := range stores {
			found, err := rs.Store.ListNotes(category)
			if err != nil {
				if err := readStoreFailed(stores, i, fmt.Errorf("failed to list notes: %w", err)); err != nil {
					return err
				}
				continue
			}
			for _, n := range found {
				listed = append(listed, noteInStore{Note: n, Store: storeLabel(stores, i)})
			}
		}
		if len(stores) > 1 {
			sort.SliceStable(listed, func(i, j int) bool {
				a, b := listed[i].Note, listed[j].Note
				if a.Category != b.Category {
					return a.Category < b.Category
				}
				return a.Title < b.Title
			})
		}
		// nil when nothing matched, so that JSON prints null as it always has
		var notes []*models.Note
		for _, l := range listed {
			notes = append(notes, l.Note)
		}

		// Output results
		if outputFormat == "json" {
			if len(stores) > 1 {
				return OutputJSON(listed)
			}
			return OutputJSON(notes)
		}

		if len(notes) == 0 {
			if category != "" {
				OutputText("No notes found in category \"%s\"", category)
			} else {
				OutputText("No notes found")
			}
			return nil
		}

		// Group by category for text output
		byCategory := make(map[string][]noteInStore)
		for _, l := range listed {
			byCategory[l.Note.Category] = append(byCategory[l.Note.Category], l)
		}

		// Sort categories
		var categories []string
		for cat := range byCategory {
			categories = append(categories, cat)
		}
		for i := 0; i < len(categories); i++ {
			for j := i + 1; j < len(categories); j++ {
				if categories[j] < categories[i] {
					categories[i], categories[j] = categories[j], categories[i]
				}
			}
		}

		// Display notes
		for _, cat := range categories {
			OutputText("\n%s:", cat)
			for _, l := range byCategory[cat] {
				note := l.Note
				idShort := note.ID
				if len(idShort) > 8 {
					idShort = idShort[:8]
				}
				OutputText("  [%s] %s%s", idShort, note.Title, storeSuffix(l.Store))
				if len(note.Tags) > 0 {
					OutputText("        Tags: %s", strings.Join(note.Tags, ", "))
				}
			}
		}
		OutputText("")

		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteListCmd)
}
