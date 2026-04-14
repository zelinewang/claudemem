package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/storage"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

var (
	healthQuick bool
	healthDeep  bool
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Report FTS + vector index drift (read-only)",
	Long: `Run a parity check across markdown files, the SQLite entries+FTS tables,
and the per-(backend,model) vector rows. Reports drift without making
changes — use 'claudemem repair' to fix.

Invariants (quick mode, <100ms, runs on SessionStart):
  I1  Every markdown file has a row in entries
  I2  Every entry has a row in memory_fts
  I3  Every entry has a vector row for the CURRENTLY CONFIGURED (backend, model)

Deep mode (--deep, slower) additionally validates:
  I4  No orphan FTS / vector rows (parent entry deleted)
  I5  vector_meta.index_backend matches the active embedder

Typical output:
  $ claudemem health
  ✓ healthy (1086 notes · 1086 entries · 1086 FTS · 1086 vectors for ollama:qwen3-embedding:4b)

Drift output:
  $ claudemem health
  ⚠ drift detected
     I3: active backend gemini:gemini-embedding-001 has 0 vectors but 1086 entries...
     Run 'claudemem repair' to backfill.
  Exit 1`,
	RunE: runHealth,
}

func runHealth(cmd *cobra.Command, args []string) error {
	// --quick is the default; --deep overrides
	isDeep := healthDeep

	fileStore, err := getFileStore()
	if err != nil {
		return err
	}
	defer fileStore.Close()

	// Only activate vector-based invariants (I3 for active backend, I5 for
	// config match) when the user has explicitly opted in to semantic
	// search. Otherwise existing installs that never enabled it would see
	// I3/I5 drift for vectors they never asked for. Matches the precedent
	// set in cmd/reindex.go and cmd/search.go.
	cfg, _ := config.Load(getStoreDir())
	if cfg != nil && cfg.GetBool("features.semantic_search") {
		_ = fileStore.InitVectorStore()
	}

	in := vectors.HealthInputs{
		DB:          fileStore.DB(),
		NotesDir:    fileStore.NotesDir(),
		SessionsDir: fileStore.SessionsDir(),
	}
	if fileStore.HasVectorStore() {
		in.Embedder = fileStore.VectorStoreEmbedder()
	}

	var report *vectors.HealthReport
	if isDeep {
		report, err = vectors.CheckHealthDeep(in)
	} else {
		report, err = vectors.CheckHealth(in)
	}
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	printHealthReport(report, fileStore)
	if !report.Healthy() {
		os.Exit(1)
	}
	return nil
}

func printHealthReport(r *vectors.HealthReport, fs *storage.FileStore) {
	if outputFormat == "json" {
		_ = OutputJSON(r)
		return
	}

	if r.Healthy() {
		// Terse one-liner so SessionStart output stays compact
		vectorLine := "no embedder configured"
		if r.ActiveBackend != "" {
			vectorLine = fmt.Sprintf("%d vectors for %s:%s",
				r.VectorTotals[r.ActiveBackend+":"+r.ActiveModel],
				r.ActiveBackend, r.ActiveModel)
		}
		fmt.Printf("✓ healthy (%d notes · %d entries · %d FTS · %s)\n",
			r.MarkdownFiles, r.EntriesTotal, r.FTSTotal, vectorLine)
		return
	}

	fmt.Fprintln(os.Stderr, "⚠ drift detected")
	for _, issue := range r.Issues {
		fmt.Fprintf(os.Stderr, "   %s\n", issue)
	}
	// Show the per-backend vector breakdown so user sees cross-machine state
	if len(r.VectorTotals) > 0 {
		fmt.Fprintln(os.Stderr, "\nVectors present:")
		for bm, n := range r.VectorTotals {
			marker := " "
			if bm == r.ActiveBackend+":"+r.ActiveModel {
				marker = "*" // active
			}
			fmt.Fprintf(os.Stderr, "  %s %s: %d\n", marker, bm, n)
		}
	}
}

func init() {
	healthCmd.Flags().BoolVar(&healthQuick, "quick", false, "Quick mode (default; <100ms SessionStart-safe)")
	healthCmd.Flags().BoolVar(&healthDeep, "deep", false, "Deep mode: also check for orphans + config match (I4/I5)")
	rootCmd.AddCommand(healthCmd)
}
