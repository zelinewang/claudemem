package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/storage"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

// healthDeep is the only runtime flag — "--quick" is the default and
// doesn't need a separate bool since `health` without flags IS quick.
var (
	healthDeep         bool
	healthTrafficLight bool
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
  I6  Cloud embedding backends have their configured API key env var present

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
	isDeep := healthDeep

	fileStore, err := getFileStore()
	if err != nil {
		if healthTrafficLight {
			printHealthTrafficLight(nil, err)
			return nil
		}
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
		if healthTrafficLight {
			printHealthTrafficLight(nil, err)
			return nil
		}
		return fmt.Errorf("health check failed: %w", err)
	}
	applyEmbeddingConfigHealth(report, cfg)

	if healthTrafficLight {
		printHealthTrafficLight(report, nil)
		return nil
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

	fmt.Fprintln(os.Stderr, "⚠ health issues detected")
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

func printHealthTrafficLight(r *vectors.HealthReport, err error) {
	if err != nil {
		fmt.Printf("claudemem health: RED check failed (%s)\n", err)
		return
	}
	if r == nil {
		fmt.Println("claudemem health: RED check failed")
		return
	}

	vectorLine := "no active vector backend"
	if r.ActiveBackend != "" {
		key := r.ActiveBackend + ":" + r.ActiveModel
		vectorLine = fmt.Sprintf("%d vectors for %s", r.VectorTotals[key], key)
	}

	if r.Healthy() {
		fmt.Printf("claudemem health: GREEN healthy (%d markdown, %d entries, %d FTS, %s)\n",
			r.MarkdownFiles, r.EntriesTotal, r.FTSTotal, vectorLine)
		return
	}

	codes := healthIssueCodes(r.Issues)
	if len(codes) == 0 {
		codes = append(codes, "drift")
	}
	if containsString(codes, "I6") {
		fmt.Printf("claudemem health: RED backend unavailable (%s) - export API key or run `claudemem setup`\n",
			strings.Join(codes, ","))
		return
	}
	fmt.Printf("claudemem health: YELLOW drift (%s) - run `claudemem repair` or `claudemem reindex --vectors`\n",
		strings.Join(codes, ","))
}

func applyEmbeddingConfigHealth(r *vectors.HealthReport, cfg *config.Config) {
	if r == nil || cfg == nil || !cfg.GetBool("features.semantic_search") {
		return
	}

	backend := strings.ToLower(cfg.GetString("embedding.backend"))
	if backend == "" {
		backend = "tfidf"
	}
	defaultKeyEnv := defaultEmbeddingAPIKeyEnv(backend)
	if defaultKeyEnv == "" {
		return
	}
	keyEnv := cfg.GetString("embedding.api_key_env")
	if keyEnv == "" {
		keyEnv = defaultKeyEnv
	}
	if os.Getenv(keyEnv) != "" {
		return
	}

	r.I6ActiveBackendConfigured = false
	r.Issues = append(r.Issues, fmt.Sprintf(
		"I6: active backend %s expects env var %s, but it is not set. Export it or run `claudemem setup`.",
		backend, keyEnv))
}

func defaultEmbeddingAPIKeyEnv(backend string) string {
	switch backend {
	case "gemini":
		return "GEMINI_API_KEY"
	case "voyage":
		return "VOYAGE_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}

func healthIssueCodes(issues []string) []string {
	codes := make([]string, 0, len(issues))
	for _, issue := range issues {
		if len(issue) >= 2 && issue[0] == 'I' {
			codes = append(codes, issue[:2])
		}
	}
	return codes
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func init() {
	// --quick is the default; we keep it as a no-op flag for docs/explicit
	// scripts that want to signal intent. --deep adds I4/I5 invariants.
	healthCmd.Flags().Bool("quick", false, "Quick mode (default; <100ms SessionStart-safe)")
	healthCmd.Flags().BoolVar(&healthDeep, "deep", false, "Deep mode: also check for orphans + config match (I4/I5)")
	healthCmd.Flags().BoolVar(&healthTrafficLight, "traffic-light", false, "Hook-safe one-line GREEN/YELLOW/RED health status; always exits 0")
	rootCmd.AddCommand(healthCmd)
}
