package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	updateTitle      string
	updateContent    string
	updateTags       string
	updateAddTags    []string
	updateRemoveTags []string
)

// noteUpdateCmd represents the note update command
var noteUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update an existing note",
	Long: `Update an existing note's title, content, or tags.

The note ID can be a full ID or a prefix (first 8 characters).

--tags replaces the whole tag list. --add-tag and --remove-tag change only the
tags named and keep the others; on their own they rewrite just the tags line of
the note's file, so the content, the "updated" time and the embedding stay as
they are. --tags cannot be combined with them.

Examples:
  claudemem note update abc12345 --title "New Title"
  claudemem note update abc12345 --content "Updated content"
  claudemem note update abc12345 --tags "newtag,updated"
  claudemem note update abc12345 --add-tag team --remove-tag personal`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		addTags, err := tagFlag(cmd, "add-tag")
		if err != nil {
			return err
		}
		removeTags, err := tagFlag(cmd, "remove-tag")
		if err != nil {
			return err
		}
		tagEdit := len(addTags) > 0 || len(removeTags) > 0
		if updateTags != "" && tagEdit {
			return fmt.Errorf("--tags replaces the whole tag list; use it without --add-tag/--remove-tag")
		}
		if err := storage.ValidateTagEdit(addTags, removeTags); err != nil {
			return err
		}

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

		// A tags-only edit rewrites just the tags, under the store lock
		if tagEdit && updateTitle == "" && updateContent == "" {
			res, err := store.EditTags("note", note.ID, addTags, removeTags, false)
			if err != nil {
				return fmt.Errorf("failed to update note tags: %w", err)
			}
			return printTagEdit(res)
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
			return fmt.Errorf("no updates specified (use --title, --content, --tags, --add-tag or --remove-tag)")
		}
		if tagEdit {
			note.Tags = storage.EditTagList(note.Tags, addTags, removeTags)
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
	noteUpdateCmd.Flags().StringVar(&updateTags, "tags", "", "New tags, replacing the current ones (comma-separated)")
	noteUpdateCmd.Flags().StringArrayVar(&updateAddTags, "add-tag", nil, "Tags to add, keeping the others (comma-separated or repeated)")
	noteUpdateCmd.Flags().StringArrayVar(&updateRemoveTags, "remove-tag", nil, "Tags to remove, keeping the others (comma-separated or repeated)")
}
