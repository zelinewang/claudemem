package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// noteDeleteCmd represents the note delete command
var noteDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a note",
	Long: `Delete a note by ID.

The note ID can be a full ID or a prefix (first 8 characters).

Examples:
  claudemem note delete abc12345
  claudemem note delete abc1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		// Get store
		store, err := getStore()
		if err != nil {
			return err
		}

		// Get note details before deletion (for confirmation message)
		note, err := store.GetNote(id)
		if err != nil {
			return fmt.Errorf("failed to get note: %w", err)
		}

		title := note.Title
		fullID := note.ID

		// Delete the note
		if err := store.DeleteNote(id); err != nil {
			return fmt.Errorf("failed to delete note: %w", err)
		}

		// Output result
		if outputFormat == "json" {
			return OutputJSON(map[string]string{
				"status": "deleted",
				"id":     fullID,
				"title":  title,
			})
		}

		idShort := fullID
		if len(idShort) > 8 {
			idShort = idShort[:8]
		}
		OutputText("✓ Deleted note: \"%s\" (id: %s)", title, idShort)

		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteDeleteCmd)
}