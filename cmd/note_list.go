package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zanelabz/claudemem/pkg/models"
)

// noteListCmd represents the note list command
var noteListCmd = &cobra.Command{
	Use:   "list [category]",
	Short: "List notes",
	Long: `List all notes or notes in a specific category.

Examples:
  claudemem note list              # List all notes
  claudemem note list work         # List notes in work category`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		category := ""
		if len(args) > 0 {
			category = args[0]
		}

		// Get store and list notes
		store, err := getStore()
		if err != nil {
			return err
		}

		notes, err := store.ListNotes(category)
		if err != nil {
			return fmt.Errorf("failed to list notes: %w", err)
		}

		// Output results
		if outputFormat == "json" {
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
		byCategory := make(map[string][]*models.Note)
		for _, note := range notes {
			byCategory[note.Category] = append(byCategory[note.Category], note)
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
			for _, note := range byCategory[cat] {
				idShort := note.ID
				if len(idShort) > 8 {
					idShort = idShort[:8]
				}
				OutputText("  [%s] %s", idShort, note.Title)
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