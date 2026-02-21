package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
)

var (
	addTitle   string
	addContent string
	addTags    string
)

// noteAddCmd represents the note add command
var noteAddCmd = &cobra.Command{
	Use:   "add <category> [title] [content]",
	Short: "Add a new note",
	Long: `Add a new note to the specified category.

You can provide the title and content via flags or as positional arguments.
If content is not provided, it will be read from stdin if available.

Examples:
  claudemem note add work --title "Meeting Notes" --content "Discussed Q1 goals" --tags "meeting,goals"
  claudemem note add personal "Shopping List" "Milk, Eggs, Bread"
  echo "Content from pipe" | claudemem note add ideas --title "Random Thought"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		category := args[0]

		// Handle positional arguments
		if len(args) >= 2 && addTitle == "" {
			addTitle = args[1]
		}
		if len(args) >= 3 && addContent == "" {
			addContent = strings.Join(args[2:], " ")
		}

		// Read from stdin if content not provided
		if addContent == "" {
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				// Data is being piped
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("failed to read from stdin: %w", err)
				}
				addContent = strings.TrimSpace(string(data))
			}
		}

		// Validate required fields
		if addTitle == "" {
			return fmt.Errorf("title is required (use --title flag)")
		}
		if addContent == "" {
			return fmt.Errorf("content is required (use --content flag or pipe to stdin)")
		}

		// Create note
		note := models.NewNote(category, addTitle, addContent)

		// Add tags if provided
		if addTags != "" {
			tags := strings.Split(addTags, ",")
			for i, tag := range tags {
				tags[i] = strings.TrimSpace(tag)
			}
			note.Tags = tags
		}

		// Get store and add note
		store, err := getStore()
		if err != nil {
			return err
		}

		if err := store.AddNote(note); err != nil {
			return fmt.Errorf("failed to add note: %w", err)
		}

		// Output result
		if outputFormat == "json" {
			return OutputJSON(note)
		}

		idShort := note.ID
		if len(idShort) > 8 {
			idShort = idShort[:8]
		}
		OutputText("✓ Added note to %s: \"%s\" (id: %s)", category, addTitle, idShort)
		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteAddCmd)
	noteAddCmd.Flags().StringVar(&addTitle, "title", "", "Note title")
	noteAddCmd.Flags().StringVar(&addContent, "content", "", "Note content")
	noteAddCmd.Flags().StringVar(&addTags, "tags", "", "Comma-separated tags")
}