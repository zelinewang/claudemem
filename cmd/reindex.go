package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
)

var (
	reindexVectors bool
	reindexAll     bool
	reindexMissing bool
)

var reindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Rebuild search indexes from markdown files",
	Long: `Rebuild the SQLite search index from the source-of-truth markdown files.

Use --vectors to rebuild the vector index for the configured embedding backend.
Use --all to rebuild both FTS5 and vector indexes.
Use --vectors --missing to embed ONLY documents that have no vector yet
(incremental: a daily sync job stays under the embedding quota instead of
re-embedding every entry — a full rebuild of ~6,000 entries hit Vertex 429).

Examples:
  claudemem reindex                     # Rebuild FTS5 index only
  claudemem reindex --vectors           # Rebuild vector index only (full re-embed)
  claudemem reindex --vectors --missing # Embed only documents missing a vector
  claudemem reindex --all               # Rebuild both indexes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateReindexFlags(reindexVectors, reindexAll, reindexMissing); err != nil {
			return err
		}
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
			if reindexMissing {
				indexed, missing, err := store.IndexMissingVectors()
				if err != nil {
					return fmt.Errorf("vector backfill failed: %w", err)
				}
				OutputText("Vector index backfilled: %d of %d missing documents embedded (backend: %s)", indexed, missing, backend)
				return partialBackfillError(indexed, missing)
			}
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
	reindexCmd.Flags().BoolVar(&reindexMissing, "missing", false, "With --vectors: embed only documents that have no vector yet (incremental backfill)")
	rootCmd.AddCommand(reindexCmd)
}

// validateReindexFlags rejects --missing without a vector mode: --missing is a
// modifier of vector indexing, and silently rebuilding only FTS behind a
// success message would leave every missing vector untouched.
func validateReindexFlags(vectors, all, missing bool) error {
	if missing && !vectors && !all {
		return fmt.Errorf("--missing only applies to vector indexing; add --vectors (or --all)")
	}
	return nil
}

// partialBackfillError turns "embedded fewer than were missing" into a
// non-zero exit so cron/sync automation never records an incomplete semantic
// index as healthy. Per-document reasons were already logged on stderr.
func partialBackfillError(indexed, missing int) error {
	if indexed >= missing {
		return nil
	}
	return fmt.Errorf("%d of %d missing documents could not be embedded (see stderr); rerun `claudemem reindex --vectors --missing` once the backend recovers", missing-indexed, missing)
}
