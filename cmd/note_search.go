package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	searchCategory string
	searchTags     string
)

// noteSearchCmd represents the note search command
var noteSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search for notes",
	Long: `Search for notes matching the given query.

The search looks in note titles and content. You can optionally filter
by category and tags.

Examples:
  claudemem note search "meeting"
  claudemem note search "api" --in work
  claudemem note search "design" --tag architecture,backend`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		// Parse tags
		var tags []string
		if searchTags != "" {
			tags = strings.Split(searchTags, ",")
			for i, tag := range tags {
				tags[i] = strings.TrimSpace(tag)
			}
		}

		// Get store and search
		store, err := getStore()
		if err != nil {
			return err
		}

		notes, err := store.SearchNotes(query, searchCategory, tags)
		if err != nil {
			return fmt.Errorf("failed to search notes: %w", err)
		}

		// Output results
		if outputFormat == "json" {
			return OutputJSON(notes)
		}

		if len(notes) == 0 {
			OutputText("No notes found matching \"%s\"", query)
			return nil
		}

		OutputText("Found %d note(s) matching \"%s\":\n", len(notes), query)
		for _, note := range notes {
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

			OutputText("  %s [%s] %s", idShort, note.Category, note.Title)
			OutputText("    %s", preview)
			if len(note.Tags) > 0 {
				OutputText("    Tags: %s", strings.Join(note.Tags, ", "))
			}
			OutputText("")
		}

		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteSearchCmd)
	noteSearchCmd.Flags().StringVar(&searchCategory, "in", "", "Filter by category")
	noteSearchCmd.Flags().StringVar(&searchTags, "tag", "", "Filter by tags (comma-separated)")
}