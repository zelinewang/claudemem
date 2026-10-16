package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
)

var (
	searchCategory string
	searchTags     []string
)

// noteSearchCmd represents the note search command
var noteSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search for notes",
	Long: `Search for notes matching the given query.

The search looks in note titles and content. You can optionally filter
by category and tags. When the store reads other stores (stores.read), they
are searched too, and each note names its store.

Examples:
  claudemem note search "meeting"
  claudemem note search "api" --in work
  claudemem note search "design" --tag architecture,backend`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		tags, err := tagFlag(cmd, "tag")
		if err != nil {
			return err
		}

		stores, err := openReadStores()
		if err != nil {
			return err
		}
		defer closeReadStores(stores)

		// Search every store; the ranked lists are merged by rank
		lists := make([][]noteInStore, 0, len(stores))
		for i, rs := range stores {
			found, err := rs.Store.SearchNotes(query, searchCategory, tags)
			if err != nil {
				if err := readStoreFailed(stores, i, fmt.Errorf("failed to search notes: %w", err)); err != nil {
					return err
				}
				continue
			}
			list := make([]noteInStore, len(found))
			for j, n := range found {
				list[j] = noteInStore{Note: n, Store: storeLabel(stores, i)}
			}
			lists = append(lists, list)
		}
		listed := interleave(lists)
		if len(listed) > maxNoteSearchResults {
			listed = listed[:maxNoteSearchResults]
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
			OutputText("No notes found matching \"%s\"", query)
			return nil
		}

		OutputText("Found %d note(s) matching \"%s\":\n", len(notes), query)
		for i, note := range notes {
			idShort := note.ID
			if len(idShort) > 8 {
				idShort = idShort[:8]
			}

			// Create content preview
			preview := note.Content
			if len(preview) > 60 {
				preview = preview[:57] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", " ")

			OutputText("  %s [%s] %s%s", idShort, note.Category, note.Title, storeSuffix(listed[i].Store))
			OutputText("    %s", preview)
			if len(note.Tags) > 0 {
				OutputText("    Tags: %s", strings.Join(note.Tags, ", "))
			}
			OutputText("")
		}

		return nil
	},
}

// maxNoteSearchResults is what SearchNotes returns from one store; merged results from several
// stores are cut to the same number.
const maxNoteSearchResults = 50

func init() {
	noteCmd.AddCommand(noteSearchCmd)
	noteSearchCmd.Flags().StringVar(&searchCategory, "in", "", "Filter by category")
	noteSearchCmd.Flags().StringArrayVar(&searchTags, "tag", nil, "Filter by tags, all required (comma-separated or repeated)")
}
