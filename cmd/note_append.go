package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// noteAppendCmd represents the note append command
var noteAppendCmd = &cobra.Command{
	Use:   "append <id> <content>",
	Short: "Append content to an existing note",
	Long: `Append additional content to an existing note.

The new content will be added to the end of the note with a newline separator.

Examples:
  claudemem note append abc12345 "Additional information"
  claudemem note append abc12345 "- New bullet point"`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		additionalContent := strings.Join(args[1:], " ")

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

		// Append content
		if note.Content != "" && !strings.HasSuffix(note.Content, "\n") {
			note.Content += "\n"
		}
		note.Content += additionalContent

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
		OutputText("✓ Appended to note: \"%s\" (id: %s)", note.Title, idShort)

		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteAppendCmd)
}