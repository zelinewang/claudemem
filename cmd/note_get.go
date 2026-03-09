package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/storage"
)

// noteGetCmd represents the note get command
var noteGetCmd = &cobra.Command{
	Use:   "get <id-or-category> [title-pattern]",
	Short: "Get a specific note",
	Long: `Get a specific note by ID or by category and title.

Examples:
  claudemem note get abc12345                    # Get by ID (or ID prefix)
  claudemem note get work "Meeting Notes"        # Get by category and title
  claudemem note get work meeting                # Search in category by title pattern`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := getStore()
		if err != nil {
			return err
		}

		// Single argument: treat as ID
		if len(args) == 1 {
			note, err := store.GetNote(args[0])
			if err != nil {
				return fmt.Errorf("failed to get note: %w", err)
			}

			// Log access (non-blocking, best-effort)
			if fs, ok := store.(*storage.FileStore); ok {
				fs.LogAccess(note.ID, "get")
			}

			if outputFormat == "json" {
				return OutputJSON(note)
			}

			// Text output: formatted note
			OutputText("ID: %s", note.ID[:8])
			OutputText("Category: %s", note.Category)
			OutputText("Title: %s", note.Title)
			if len(note.Tags) > 0 {
				OutputText("Tags: %s", strings.Join(note.Tags, ", "))
			}
			OutputText("Created: %s", note.Created.Format("2006-01-02 15:04:05"))
			OutputText("Updated: %s", note.Updated.Format("2006-01-02 15:04:05"))
			OutputText("\n%s", note.Content)

			return nil
		}

		// Two arguments: category and title/pattern
		category := args[0]
		titlePattern := args[1]

		// First try exact match
		note, err := store.GetNoteByTitle(category, titlePattern)
		if err == nil {
			// Log access (non-blocking, best-effort)
			if fs, ok := store.(*storage.FileStore); ok {
				fs.LogAccess(note.ID, "get")
			}

			if outputFormat == "json" {
				return OutputJSON(note)
			}

			// Text output: formatted note
			OutputText("ID: %s", note.ID[:8])
			OutputText("Category: %s", note.Category)
			OutputText("Title: %s", note.Title)
			if len(note.Tags) > 0 {
				OutputText("Tags: %s", strings.Join(note.Tags, ", "))
			}
			OutputText("Created: %s", note.Created.Format("2006-01-02 15:04:05"))
			OutputText("Updated: %s", note.Updated.Format("2006-01-02 15:04:05"))
			OutputText("\n%s", note.Content)

			return nil
		}

		// If not found, try listing category and filtering
		notes, err := store.ListNotes(category)
		if err != nil {
			return fmt.Errorf("failed to list notes: %w", err)
		}

		// Filter by title pattern
		patternLower := strings.ToLower(titlePattern)
		var matches []*models.Note
		for _, n := range notes {
			if strings.Contains(strings.ToLower(n.Title), patternLower) {
				matches = append(matches, n)
			}
		}

		if len(matches) == 0 {
			return fmt.Errorf("no notes found in %s matching \"%s\"", category, titlePattern)
		}

		if len(matches) == 1 {
			// Single match, show it
			note = matches[0]

			// Log access (non-blocking, best-effort)
			if fs, ok := store.(*storage.FileStore); ok {
				fs.LogAccess(note.ID, "get")
			}

			if outputFormat == "json" {
				return OutputJSON(note)
			}

			OutputText("ID: %s", note.ID[:8])
			OutputText("Category: %s", note.Category)
			OutputText("Title: %s", note.Title)
			if len(note.Tags) > 0 {
				OutputText("Tags: %s", strings.Join(note.Tags, ", "))
			}
			OutputText("Created: %s", note.Created.Format("2006-01-02 15:04:05"))
			OutputText("Updated: %s", note.Updated.Format("2006-01-02 15:04:05"))
			OutputText("\n%s", note.Content)

			return nil
		}

		// Multiple matches, list them
		if outputFormat == "json" {
			return OutputJSON(matches)
		}

		OutputText("Multiple notes found in %s matching \"%s\":", category, titlePattern)
		for _, n := range matches {
			idShort := n.ID
			if len(idShort) > 8 {
				idShort = idShort[:8]
			}
			OutputText("  [%s] %s", idShort, n.Title)
		}
		OutputText("\nUse the full title or note ID to get a specific note")

		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteGetCmd)
}