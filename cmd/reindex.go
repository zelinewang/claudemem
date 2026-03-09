package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
)

var (
	reindexVectors bool
	reindexAll     bool
)

var reindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Rebuild search indexes from markdown files",
	Long: `Rebuild the SQLite search index from the source-of-truth markdown files.

Use --vectors to rebuild the TF-IDF vector index for semantic search.
Use --all to rebuild both FTS5 and vector indexes.

Examples:
  claudemem reindex              # Rebuild FTS5 index only
  claudemem reindex --vectors    # Rebuild vector index only
  claudemem reindex --all        # Rebuild both indexes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := getFileStore()
		if err != nil {
			return err
		}
		defer store.Close()

		doFTS := !reindexVectors || reindexAll
		doVectors := reindexVectors || reindexAll

		if doFTS {
			count, err := store.Reindex()
			if err != nil {
				return fmt.Errorf("FTS reindex failed: %w", err)
			}
			OutputText("FTS5 index rebuilt: %d entries indexed", count)
		}

		if doVectors {
			// Check feature flag
			cfg, cfgErr := config.Load(getStoreDir())
			if cfgErr != nil {
				return fmt.Errorf("failed to load config: %w", cfgErr)
			}
			if cfg.GetString("features.semantic_search") != "true" {
				return fmt.Errorf("semantic search not enabled; run: claudemem config set features.semantic_search true")
			}

			// Initialize vector store
			if err := store.InitVectorStore(); err != nil {
				return fmt.Errorf("failed to initialize vector store: %w", err)
			}

			backend := store.VectorBackend()
			count, err := store.ReindexVectors()
			if err != nil {
				return fmt.Errorf("vector reindex failed: %w", err)
			}
			OutputText("Vector index rebuilt: %d documents indexed (backend: %s)", count, backend)
		}

		return nil
	},
}

func init() {
	reindexCmd.Flags().BoolVar(&reindexVectors, "vectors", false, "Rebuild vector index for semantic search")
	reindexCmd.Flags().BoolVar(&reindexAll, "all", false, "Rebuild both FTS5 and vector indexes")
	rootCmd.AddCommand(reindexCmd)
}
