package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	searchType  string
	searchLimit int
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search notes and sessions",
	Long: `Search through notes and sessions using full-text search.

Examples:
  claudemem search "api rate limits"
  claudemem search "tiktok" --type note
  claudemem search "build" --type session --limit 10`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		// Get unified store
		store, err := getUnifiedStore()
		if err != nil {
			return err
		}

		// Perform search
		results, err := store.Search(query, searchType, searchLimit)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		// Output results
		if outputFormat == "json" {
			return OutputJSON(results)
		}

		// Text output
		if len(results) == 0 {
			OutputText("No results found for query: %s", query)
			return nil
		}

		OutputText("Found %d results for \"%s\":\n", len(results), query)

		for i, r := range results {
			// Choose icon based on type
			icon := "📝"
			if r.Type == "session" {
				icon = "📋"
			}

			// Format metadata
			var metadata []string
			if r.Category != "" {
				metadata = append(metadata, fmt.Sprintf("category: %s", r.Category))
			}
			if r.Branch != "" {
				metadata = append(metadata, fmt.Sprintf("branch: %s", r.Branch))
			}
			if r.Project != "" {
				metadata = append(metadata, fmt.Sprintf("project: %s", r.Project))
			}
			if len(r.Tags) > 0 {
				metadata = append(metadata, fmt.Sprintf("tags: %s", strings.Join(r.Tags, ", ")))
			}

			// Output entry
			OutputText("%d. %s [%s] %s", i+1, icon, r.Type, r.Title)
			if len(metadata) > 0 {
				OutputText("   %s", strings.Join(metadata, " | "))
			}
			if r.Preview != "" {
				OutputText("   %s", r.Preview)
			}
			if r.Score > 0 {
				OutputText("   Score: %.2f", r.Score)
			}
			OutputText("")
		}

		return nil
	},
}

func getUnifiedStore() (storage.UnifiedStore, error) {
	// Use the existing getSessionStore which returns UnifiedStore
	return getSessionStore()
}

func init() {
	searchCmd.Flags().StringVar(&searchType, "type", "", "Filter by type: note, session")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "Maximum number of results")
	rootCmd.AddCommand(searchCmd)
}