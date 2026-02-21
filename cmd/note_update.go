package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	updateTitle   string
	updateContent string
	updateTags    string
)

// noteUpdateCmd represents the note update command
var noteUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update an existing note",
	Long: `Update an existing note's title, content, or tags.

The note ID can be a full ID or a prefix (first 8 characters).

Examples:
  claudemem note update abc12345 --title "New Title"
  claudemem note update abc12345 --content "Updated content"
  claudemem note update abc12345 --tags "newtag,updated"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		// Get store
		store, err := getStore()
		if err != nil {
			return err
		}

		// Get existing note
		note, err := store.GetNote(id)
		if err != nil {
			return fmt.Errorf("failed to get note: %w", err)
		}

		// Track if anything changed
		changed := false

		// Apply updates
		if updateTitle != "" {
			note.Title = updateTitle
			changed = true
		}

		if updateContent != "" {
			note.Content = updateContent
			changed = true
		}

		if updateTags != "" {
			tags := strings.Split(updateTags, ",")
			for i, tag := range tags {
				tags[i] = strings.TrimSpace(tag)
			}
			note.Tags = tags
			changed = true
		}

		if !changed {
			return fmt.Errorf("no updates specified (use --title, --content, or --tags)")
		}

		// Update timestamp
		note.Updated = time.Now()

		// Save changes
		if err := store.UpdateNote(note); err != nil {
			return fmt.Errorf("failed to update note: %w", err)
		}

		// Output result
		if outputFormat == "json" {
			return OutputJSON(note)
		}

		idShort := note.ID
		if len(idShort) > 8 {
			idShort = idShort[:8]
		}
		OutputText("✓ Updated note: \"%s\" (id: %s)", note.Title, idShort)

		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteUpdateCmd)
	noteUpdateCmd.Flags().StringVar(&updateTitle, "title", "", "New title")
	noteUpdateCmd.Flags().StringVar(&updateContent, "content", "", "New content")
	noteUpdateCmd.Flags().StringVar(&updateTags, "tags", "", "New tags (comma-separated)")
}