package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/storage"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

var (
	searchType            string
	searchLimit           int
	searchCompact         bool
	searchFilterCategory  string
	searchFilterTags      []string
	searchAfter           string
	searchBefore          string
	searchSort            string
	searchSemantic        bool
	searchFTSOnly         bool // P4: explicit opt-in to skip semantic search for this query
	searchAutoFallbackFTS bool // Non-TTY opt-in: fall back to FTS (warn on stderr) instead of exit 1 when backend is unreachable
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search notes and sessions",
	Long: `Search through notes and sessions using full-text search.

Supports faceted filtering by category, tags, and date range.
Results are ranked by relevance with a recency boost (recent entries score higher).
Use --compact for token-efficient output (IDs + titles only).
Use --semantic to require semantic (vector) search via the configured embedding backend.
When the store reads other stores (stores.read), they are searched too: by keyword, and by their
vectors of this store's embedding backend and model. Their own settings are never used.

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
		tags, err := tagFlag(cmd, "tag")
		if err != nil {
			return err
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

		stores, err := openReadStores()
		if err != nil {
			return err
		}
		defer closeReadStores(stores)

		// --store is searched with its own settings. The stores it reads never choose an embedding
		// backend: each answers with keyword search, and, when --store searches with a non-TF-IDF
		// embedder, also with its own vectors of that backend, model and dimensions, matched against
		// the query as --store embedded it (once). The lists are merged by rank, or by date with
		// --sort date.
		lists := make([][]storage.SearchResult, 0, len(stores))
		var shared vectors.Embedder
		for i, rs := range stores {
			var found []storage.SearchResult
			if i == 0 {
				found, shared, err = searchPrimaryStore(rs, query, opts)
				if err != nil {
					return err
				}
			} else {
				found, err = searchReadStore(rs, opts, query, shared)
				if err != nil {
					if err := readStoreFailed(stores, i, err); err != nil {
						return err
					}
					continue
				}
			}
			for j := range found {
				found[j].Store = storeLabel(stores, i)
			}
			lists = append(lists, found)
		}
		results := mergeSearchResults(lists, opts)

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
					Store string  `json:"store,omitempty"`
				}
				compact := make([]CompactResult, len(results))
				for i, r := range results {
					compact[i] = CompactResult{
						ID:    r.ID,
						Type:  r.Type,
						Title: r.Title,
						Score: r.Score,
						Store: r.Store,
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
				if r.Store != "" {
					OutputText("%d. %s %s — %s (%.2f) [%s]", i+1, icon, idShort, r.Title, r.Score, r.Store)
					continue
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
			if r.Store != "" {
				metadata = append(metadata, fmt.Sprintf("store: %s", r.Store))
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

// searchPrimaryStore searches --store the way the command always searched its single store, with its
// own semantic-search setting: --semantic forces hybrid search, the store's features.semantic_search
// turns it on, --fts-only turns it off. It also returns the store's query embedder for the stores it
// reads (nil without hybrid search or with TF-IDF).
func searchPrimaryStore(rs readStore, query string, opts storage.SearchOpts) ([]storage.SearchResult, vectors.Embedder, error) {
	fileStore := rs.Store

	// Auto-enable hybrid search when feature flag is on
	useHybrid := searchSemantic
	cfg, cfgErr := config.Load(rs.Dir)
	if cfgErr == nil && cfg.GetBool("features.semantic_search") {
		useHybrid = true
	}
	// --fts-only is an explicit per-query opt-out. Honored before init
	// so we don't even try to reach the configured backend.
	if searchFTSOnly {
		useHybrid = false
	}
	if useHybrid {
		if err := fileStore.InitVectorStore(); err != nil {
			// P4 "no silent fallback": surface the failure to the user.
			// Fail loud in non-TTY; offer interactive recovery in TTY.
			choice, cerr := handleBackendFailure(err, useHybrid)
			if cerr != nil {
				return nil, nil, cerr
			}
			switch choice {
			case recoveryRetry:
				if err2 := fileStore.InitVectorStore(); err2 != nil {
					return nil, nil, fmt.Errorf("retry failed: %w", err2)
				}
			case recoveryFTSOnly:
				useHybrid = false
			case recoverySetup:
				return nil, nil, fmt.Errorf("run `claudemem setup` to reconfigure, then retry")
			case recoveryExit:
				return nil, nil, fmt.Errorf("aborted")
			}
		}
	}

	// Choose search mode: hybrid (FTS5 + vectors) when available, FTS5 otherwise
	var results []storage.SearchResult
	var shared vectors.Embedder
	var err error
	if useHybrid && fileStore.HasVectorStore() {
		shared = fileStore.ShareQueryEmbedder()
		results, err = fileStore.HybridSearch(query, opts)
		// HybridSearch may fail at embed time (backend went down between
		// init and query). Surface as a recovery prompt. If the user
		// accepts --fts-only, retry with keyword search; otherwise bail.
		if err != nil && vectors.IsBackendUnavailable(err) {
			choice, cerr := handleBackendFailure(err, true)
			if cerr != nil {
				return nil, nil, cerr
			}
			if choice == recoveryFTSOnly {
				shared = nil
				results, err = fileStore.SearchWithOpts(opts)
			} else {
				return nil, nil, fmt.Errorf("search aborted: %w", err)
			}
		}
	} else {
		results, err = fileStore.SearchWithOpts(opts)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("search failed: %w", err)
	}
	return results, shared, nil
}

// searchReadStore searches a store that --store reads: keyword search, and also its own vectors when
// --store shared a query embedder and this store holds vectors of that embedder's backend, model and
// dimensions. This store's own embedding settings are never read: it chooses no backend, no endpoint
// and no key.
func searchReadStore(rs readStore, opts storage.SearchOpts, query string, shared vectors.Embedder) ([]storage.SearchResult, error) {
	if shared != nil && rs.Store.AttachReadVectors(shared) == nil {
		return rs.Store.HybridSearch(query, opts)
	}
	return rs.Store.SearchWithOpts(opts)
}

// mergeSearchResults merges the result lists of several stores: by date with --sort date, else by
// rank within each store (see interleave), then cut to the limit. One list is returned as it is.
func mergeSearchResults(lists [][]storage.SearchResult, opts storage.SearchOpts) []storage.SearchResult {
	if len(lists) == 1 {
		return lists[0]
	}
	var merged []storage.SearchResult
	if opts.Sort == "date" {
		for _, list := range lists {
			merged = append(merged, list...)
		}
		sort.SliceStable(merged, func(i, j int) bool { return merged[i].Created.After(merged[j].Created) })
	} else {
		merged = interleave(lists)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
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
	searchCmd.Flags().StringArrayVar(&searchFilterTags, "tag", nil, "Only entries carrying all these tags, matched as whole tags (comma-separated or repeated)")
	searchCmd.Flags().StringVar(&searchAfter, "after", "", "Filter entries after date (YYYY-MM-DD)")
	searchCmd.Flags().StringVar(&searchBefore, "before", "", "Filter entries before date (YYYY-MM-DD)")
	searchCmd.Flags().StringVar(&searchSort, "sort", "relevance", "Sort by: relevance, date")
	searchCmd.Flags().BoolVar(&searchSemantic, "semantic", false, "Require semantic search via the configured embedding backend (fails if features.semantic_search=false)")
	searchCmd.Flags().BoolVar(&searchFTSOnly, "fts-only", false, "Skip semantic search for this query (FTS5 keyword only). Useful when the embedding backend is down and you want to search anyway.")
	searchCmd.Flags().BoolVar(&searchAutoFallbackFTS, "auto-fallback-fts", false, "In non-TTY mode, if the embedding backend is unreachable, fall back to FTS keyword search (with a stderr warning) instead of exiting 1. Useful for cron / hooks / CI. Interactive mode is unaffected.")
	rootCmd.AddCommand(searchCmd)
}

// --- P4: fail-loud + interactive recovery ---

type recoveryChoice int

const (
	recoveryFail    recoveryChoice = iota // default: error out
	recoveryRetry                         // user asked to retry the backend call
	recoveryFTSOnly                       // fall back to FTS5 just for this query
	recoverySetup                         // user wants to run `claudemem setup`
	recoveryExit                          // user cancelled
)

// handleBackendFailure produces either a recovery action (TTY interactive)
// or a verbose error-exit (non-TTY). The semantics match claudemem's
// "no silent fallback" rule: the user ALWAYS knows the backend failed
// and what their options are.
func handleBackendFailure(err error, wasHybridRequested bool) (recoveryChoice, error) {
	// Unwrap to the ErrBackendUnavailable if there is one
	var ebu *vectors.ErrBackendUnavailable
	if errors.As(err, &ebu) {
		return handleSpecificBackendFailure(ebu, wasHybridRequested)
	}
	// Generic vector-store init failure (e.g., bad config) — treat similarly
	return handleGenericEmbeddingFailure(err)
}

func handleSpecificBackendFailure(ebu *vectors.ErrBackendUnavailable, wasHybridRequested bool) (recoveryChoice, error) {
	if !isInteractive() {
		// Non-TTY + explicit opt-in: emit a warning and degrade to FTS keyword
		// search for this query. Keeps automation (cron, hooks, /wrapup, CI)
		// from breaking on transient backend outages while still being
		// completely transparent about what happened (stderr warning + the
		// FTS-only result set is clearly distinguishable from semantic).
		if searchAutoFallbackFTS {
			fmt.Fprintf(os.Stderr, "[warn] claudemem: semantic backend %q unavailable (%v) — auto-falling back to FTS keyword search\n", ebu.Backend, ebu.Cause)
			fmt.Fprintf(os.Stderr, "       to resolve permanently: %s\n", ebu.Hint)
			return recoveryFTSOnly, nil
		}
		// Non-TTY default: emit the full error with recovery options, exit 1.
		// Strict by design — "no silent fallback" — callers must opt in via
		// --auto-fallback-fts if they want graceful degradation.
		fmt.Fprintf(os.Stderr, "Error: embedding backend %q unreachable: %v\n", ebu.Backend, ebu.Cause)
		fmt.Fprintf(os.Stderr, "\n  Recovery options:\n")
		fmt.Fprintf(os.Stderr, "    - %s\n", ebu.Hint)
		fmt.Fprintf(os.Stderr, "    - Re-run this search with --fts-only to use keyword search this one time\n")
		fmt.Fprintf(os.Stderr, "    - Re-run with --auto-fallback-fts to degrade gracefully in future runs\n")
		fmt.Fprintf(os.Stderr, "    - Run `claudemem setup` to switch backend\n")
		return recoveryFail, fmt.Errorf("%w", ebu)
	}

	// TTY: interactive prompt
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "⚠ Embedding backend %q unreachable: %v\n", ebu.Backend, ebu.Cause)
	fmt.Fprintf(os.Stderr, "  Hint: %s\n", ebu.Hint)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "What do you want to do?")
	fmt.Fprintln(os.Stderr, "  1) Retry — I just fixed it")
	fmt.Fprintln(os.Stderr, "  2) Search FTS-only for this query (no semantic; keyword match)")
	fmt.Fprintln(os.Stderr, "  3) Run `claudemem setup` to switch backend (exits; re-run your command after)")
	fmt.Fprintln(os.Stderr, "  4) Exit")
	fmt.Fprint(os.Stderr, "> ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		return recoveryRetry, nil
	case "2":
		return recoveryFTSOnly, nil
	case "3":
		return recoverySetup, nil
	case "4", "":
		return recoveryExit, nil
	default:
		return recoveryExit, fmt.Errorf("unrecognized choice %q", strings.TrimSpace(line))
	}
}

func handleGenericEmbeddingFailure(err error) (recoveryChoice, error) {
	// Config-shaped failure — tell user to run setup regardless of TTY.
	fmt.Fprintf(os.Stderr, "Error: vector store init failed: %v\n", err)
	fmt.Fprintln(os.Stderr, "  Try: claudemem setup")
	return recoveryFail, err
}

// isInteractive reports whether stdin is connected to a TTY. Used to
// branch between "ask the user" and "fail loud with recovery message."
// Declared as a var so tests can substitute a deterministic value —
// `go test` itself may or may not have a TTY depending on the invoker.
var isInteractive = func() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
