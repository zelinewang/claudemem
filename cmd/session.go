package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zanelabz/claudemem/pkg/storage"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage Claude conversation sessions",
	Long:  `Save, list, search, and retrieve session summaries from Claude conversations.`,
}

func init() {
	rootCmd.AddCommand(sessionCmd)
}

// getSessionStore returns a configured file store for sessions
func getSessionStore() (storage.UnifiedStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	baseDir := storeDir
	if baseDir == "" {
		baseDir = fmt.Sprintf("%s/.claudemem", home)
	}

	store, err := storage.NewFileStore(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	return store, nil
}