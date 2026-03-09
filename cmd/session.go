package cmd

import (
	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage Claude conversation sessions",
	Long:  `Save, list, search, and retrieve session summaries from Claude conversations.`,
}

func init() {
	rootCmd.AddCommand(sessionCmd)
}

// getSessionStore returns a configured file store for sessions.
// Also initializes vector store if semantic search feature is enabled.
func getSessionStore() (storage.UnifiedStore, error) {
	store, err := getFileStoreWithVectors()
	if err != nil {
		return nil, err
	}
	return store, nil
}