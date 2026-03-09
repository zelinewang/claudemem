package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	searchType     string
	searchLimit    int
	searchCompact  bool
	searchFilterCategory string
	searchFilterTags     string
	searchAfter    string
	searchBefore   string
	searchSort     string
	searchSemantic bool
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search notes and sessions",
	Long: `Search through notes and sessions using full-text search.

Supports faceted filtering by category, tags, and date range.
Results are ranked by relevance with a recency boost (recent entries score higher).
Use --compact for token-efficient output (IDs + titles only).
Use --semantic for TF-IDF vector similarity search (requires feature flag).

Examples:
  claudemem search "api rate limits"
  claudemem search "tiktok" --type note
  claudemem search "auth" --category api --tag security
  claudemem search "deploy" --after 2025-01-01 --sort date
  claudemem search "auth" --compact --format json
  claudemem search "authentication" --semantic`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		// Get file store (concrete type for vector access)
		fileStore, err := getFileStore()
		if err != nil {
			return err
		}

		// Auto-enable hybrid search when feature flag is on
		useHybrid := searchSemantic
		cfg, cfgErr := config.Load(getStoreDir())
		if cfgErr == nil && cfg.GetBool("features.semantic_search") {
			useHybrid = true
		}
		if searchSemantic && !useHybrid {
			return fmt.Errorf("semantic search not enabled; run: claudemem config set features.semantic_search true && claudemem reindex --vectors")
		}
		if useHybrid {
			if err := fileStore.InitVectorStore(); err != nil {
				// Graceful: fall back to FTS5 if vector init fails
				useHybrid = false
			}
		}

		// Parse tags
		var tags []string
		if searchFilterTags != "" {
			for _, t := range strings.Split(searchFilterTags, ",") {
				if trimmed := strings.TrimSpace(t); trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		}

		opts := storage.SearchOpts{
			Query:    query,
			Type:     searchType,
			Category: searchFilterCategory,
			Tags:     tags,
			After:    searchAfter,
			Before:   searchBefore,
			Sort:     searchSort,
			Limit:    searchLimit,
		}

		// Choose search mode: hybrid (FTS5 + vectors) when available, FTS5 otherwise
		var results []storage.SearchResult
		if useHybrid && fileStore.HasVectorStore() {
			results, err = fileStore.HybridSearch(query, opts)
		} else {
			results, err = fileStore.SearchWithOpts(opts)
		}
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}

		if len(results) == 0 {
			if outputFormat == "json" {
				return OutputJSON([]struct{}{})
			}
			OutputText("No results found for query: %s", query)
			return nil
		}

		// Compact output: minimal data for token efficiency
		if searchCompact {
			if outputFormat == "json" {
				type CompactResult struct {
					ID    string  `json:"id"`
					Type  string  `json:"type"`
					Title string  `json:"title"`
					Score float64 `json:"score"`
				}
				compact := make([]CompactResult, len(results))
				for i, r := range results {
					compact[i] = CompactResult{
						ID:    r.ID,
						Type:  r.Type,
						Title: r.Title,
						Score: r.Score,
					}
				}
				return OutputJSON(compact)
			}
			// Compact text
			for i, r := range results {
				icon := "📝"
				if r.Type == "session" {
					icon = "📋"
				}
				idShort := r.ID
				if len(idShort) > 8 {
					idShort = idShort[:8]
				}
				OutputText("%d. %s %s — %s (%.2f)", i+1, icon, idShort, r.Title, r.Score)
			}
			return nil
		}

		// Full output (default)
		if outputFormat == "json" {
			return OutputJSON(results)
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

// getFileStore returns the concrete *FileStore for operations that need
// direct access (e.g., semantic search, vector reindex).
func getFileStore() (*storage.FileStore, error) {
	baseDir := getStoreDir()
	store, err := storage.NewFileStore(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}
	return store, nil
}

func init() {
	searchCmd.Flags().StringVar(&searchType, "type", "", "Filter by type: note, session")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "Maximum number of results")
	searchCmd.Flags().BoolVar(&searchCompact, "compact", false, "Compact output (ID + title + score only)")
	searchCmd.Flags().StringVar(&searchFilterCategory, "category", "", "Filter by category")
	searchCmd.Flags().StringVar(&searchFilterTags, "tag", "", "Filter by tags (comma-separated)")
	searchCmd.Flags().StringVar(&searchAfter, "after", "", "Filter entries after date (YYYY-MM-DD)")
	searchCmd.Flags().StringVar(&searchBefore, "before", "", "Filter entries before date (YYYY-MM-DD)")
	searchCmd.Flags().StringVar(&searchSort, "sort", "relevance", "Sort by: relevance, date")
	searchCmd.Flags().BoolVar(&searchSemantic, "semantic", false, "Use semantic search (TF-IDF vectors, requires features.semantic_search=true)")
	rootCmd.AddCommand(searchCmd)
}