package cmd

import (
	"fmt"
	"os"
	"path/filepath"

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

// getStore creates and returns a FileStore instance
func getStore() (storage.NoteStore, error) {
	if storeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		storeDir = filepath.Join(home, ".claudemem")
	}

	store, err := storage.NewFileStore(storeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	return store, nil
}