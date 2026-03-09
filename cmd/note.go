package cmd

import (
	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

// noteCmd represents the note command
var noteCmd = &cobra.Command{
	Use:   "note",
	Short: "Manage notes in your memory store",
	Long: `Manage notes organized by categories with optional tags.

Notes are stored as markdown files with metadata and can be searched,
listed, and organized by category and tags.`,
}

func init() {
	rootCmd.AddCommand(noteCmd)
}

// getStore creates and returns a FileStore instance.
// Also initializes vector store if semantic search feature is enabled.
func getStore() (storage.NoteStore, error) {
	store, err := getFileStoreWithVectors()
	if err != nil {
		return nil, err
	}
	return store, nil
}