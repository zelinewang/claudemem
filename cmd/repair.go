package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

var (
	repairYes bool // auto-accept all fixes (for CI / scripting)
)

var repairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Fix drift detected by `claudemem health`",
	Long: `Run health checks, then offer to repair any detected drift:
  - FTS5 out of sync → run reindex --fts
  - Vectors missing for active backend → run reindex --vectors
  - Orphan rows → delete

Interactive by default. Use --yes to accept all fixes non-interactively
(safe in CI where healthy state is expected but drift from e.g.
SIGKILL during reindex needs automatic recovery).`,
	RunE: runRepair,
}

func runRepair(cmd *cobra.Command, args []string) error {
	fileStore, err := getFileStore()
	if err != nil {
		return err
	}
	defer fileStore.Close()

	// Same gate as cmd/health.go: only activate vector-based invariants
	// when the user has opted into semantic search. Otherwise health +
	// repair would disagree for the same state — health passes (vectors
	// skipped) while repair would offer to rebuild TF-IDF vectors the
	// user never asked for. Keeps both commands consistent.
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
	report, err := vectors.CheckHealthDeep(in)
	if err != nil {
		return err
	}
	if report.Healthy() {
		OutputText("✓ healthy, nothing to repair")
		return nil
	}

	fmt.Fprintln(os.Stderr, "⚠ drift detected:")
	for _, issue := range report.Issues {
		fmt.Fprintf(os.Stderr, "   %s\n", issue)
	}
	fmt.Fprintln(os.Stderr, "")

	reader := bufio.NewReader(os.Stdin)
	repairs := 0

	// Fix 1: FTS drift → reindex FTS
	if !report.I1MarkdownMatchesEntries || !report.I2EntriesMatchesFTS {
		if confirmRepair(reader, "Rebuild FTS5 index from markdown?") {
			count, err := fileStore.Reindex()
			if err != nil {
				return fmt.Errorf("FTS reindex: %w", err)
			}
			OutputText("  ✓ FTS index rebuilt (%d entries)", count)
			repairs++
		}
	}

	// Fix 2: vectors missing for active backend → reindex vectors
	if !report.I3VectorsMatchActiveBackend && fileStore.HasVectorStore() {
		prompt := fmt.Sprintf("Rebuild vector index for %s:%s?",
			report.ActiveBackend, report.ActiveModel)
		if confirmRepair(reader, prompt) {
			count, err := fileStore.ReindexVectors()
			if err != nil {
				return fmt.Errorf("vector reindex: %w", err)
			}
			OutputText("  ✓ Vector index rebuilt (%d documents, backend: %s)",
				count, fileStore.VectorBackend())
			repairs++
		}
	}

	// Fix 3: orphan rows → delete.
	// DESTRUCTIVE operation — errors must surface to the user. Previous
	// version silently swallowed Exec errors into a "Removed 0" message,
	// which violates the "no silent fallback" rule for the repair path.
	if report.DidDeepCheck && !report.I4NoOrphanRows {
		if confirmRepair(reader, "Delete orphan FTS / vector rows?") {
			var removed int
			ftsResult, err := fileStore.DB().Exec(`DELETE FROM memory_fts WHERE id NOT IN (SELECT id FROM entries)`)
			if err != nil {
				return fmt.Errorf("delete orphan FTS rows: %w", err)
			}
			if n, _ := ftsResult.RowsAffected(); n > 0 {
				removed += int(n)
			}
			vecResult, err := fileStore.DB().Exec(`DELETE FROM vectors WHERE doc_id NOT IN (SELECT id FROM entries)`)
			if err != nil {
				return fmt.Errorf("delete orphan vector rows: %w", err)
			}
			if n, _ := vecResult.RowsAffected(); n > 0 {
				removed += int(n)
			}
			OutputText("  ✓ Removed %d orphan rows", removed)
			repairs++
		}
	}

	if repairs == 0 {
		OutputText("No repairs performed.")
		return nil
	}
	OutputText("\nRe-running health check...")
	report2, err := vectors.CheckHealthDeep(in)
	if err != nil {
		return err
	}
	if report2.Healthy() {
		OutputText("✓ healthy now")
	} else {
		OutputText("⚠ some drift remains, see `claudemem health --deep`")
	}
	return nil
}

func confirmRepair(reader *bufio.Reader, prompt string) bool {
	if repairYes {
		fmt.Fprintf(os.Stderr, "%s [auto-yes]\n", prompt)
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [Y/n] ", prompt)
	line, _ := reader.ReadString('\n')
	return strings.ToLower(strings.TrimSpace(line)) != "n"
}

func init() {
	repairCmd.Flags().BoolVar(&repairYes, "yes", false, "Accept all fixes non-interactively")
	rootCmd.AddCommand(repairCmd)
}
