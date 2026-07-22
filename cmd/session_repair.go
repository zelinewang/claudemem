package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	sessionRepairApply     bool   // write repairs (default: dry-run)
	sessionRepairBackupDir string // pre-repair backup copies (required with --apply)
)

var sessionRepairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Repair historical Problems & Solutions corruption in session files",
	Long: `Scan stored session markdown for the RECOVERABLE corruption left by the
historical Solution-loss defect (fused "Problem ... **Solution**: ..." lines,
glued empty markers, bold-colon residue) and repair it in place.

Dry-run is the DEFAULT: a full per-file, per-line before/after report is
printed and NOTHING is written.

--apply writes the repairs, and REQUIRES --backup-dir <dir>: every file about
to change is first copied there, byte-for-byte, so the operation is fully
recoverable. Mode-A files (solution honestly destroyed at save time) and
healthy files are never touched. A second run is a no-op.

Markdown is the source of truth; after a successful apply the SQLite search
index is rebuilt (when one already exists) so search sees repaired bodies.`,
	RunE: runSessionRepair,
}

func init() {
	sessionRepairCmd.Flags().BoolVar(&sessionRepairApply, "apply", false, "write repairs in place (requires --backup-dir)")
	sessionRepairCmd.Flags().StringVar(&sessionRepairBackupDir, "backup-dir", "", "directory receiving pre-repair original copies (required with --apply)")
	sessionCmd.AddCommand(sessionRepairCmd)
}

// sessionRepairFileResult holds one repairable file's scan outcome.
type sessionRepairFileResult struct {
	name     string
	changes  []storage.RepairChange
	original string
	repaired string
}

func runSessionRepair(cmd *cobra.Command, args []string) error {
	sessionsDir := filepath.Join(getStoreDir(), "sessions")

	// Safety pins, checked before ANY filesystem read of user content.
	if sessionRepairApply {
		if sessionRepairBackupDir == "" {
			return fmt.Errorf("--apply requires --backup-dir <dir>: repairs are only written with a recoverable backup")
		}
		absBackup, err := filepath.Abs(sessionRepairBackupDir)
		if err != nil {
			return fmt.Errorf("resolve --backup-dir: %w", err)
		}
		absSessions, err := filepath.Abs(sessionsDir)
		if err != nil {
			return fmt.Errorf("resolve sessions dir: %w", err)
		}
		// Backups inside the scanned tree would be re-scanned as sessions and
		// pollute the corpus — refuse outright.
		if absBackup == absSessions || strings.HasPrefix(absBackup, absSessions+string(os.PathSeparator)) {
			return fmt.Errorf("--backup-dir must not be inside the sessions directory (%s)", sessionsDir)
		}
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			OutputText("no sessions directory at %s — nothing to repair", sessionsDir)
			return nil
		}
		return fmt.Errorf("read sessions dir: %w", err)
	}

	scanned := 0
	var results []sessionRepairFileResult
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), "._") {
			continue
		}
		scanned++
		path := filepath.Join(sessionsDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		repaired, changes := storage.RepairSessionMarkdown(string(data))
		if len(changes) == 0 {
			continue
		}
		results = append(results, sessionRepairFileResult{
			name:     e.Name(),
			changes:  changes,
			original: string(data),
			repaired: repaired,
		})
	}

	printSessionRepairReport(results, scanned, sessionRepairApply)

	if !sessionRepairApply || len(results) == 0 {
		if sessionRepairApply {
			OutputText("nothing to repair — no files written, no backup created")
		}
		return nil
	}

	if err := os.MkdirAll(sessionRepairBackupDir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	for _, r := range results {
		src := filepath.Join(sessionsDir, r.name)
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		if err := os.WriteFile(filepath.Join(sessionRepairBackupDir, r.name), []byte(r.original), info.Mode()); err != nil {
			return fmt.Errorf("backup %s: %w", r.name, err)
		}
		if err := os.WriteFile(src, []byte(r.repaired), info.Mode()); err != nil {
			return fmt.Errorf("write %s: %w", src, err)
		}
	}
	OutputText("applied: %d file(s) repaired, originals backed up to %s", len(results), sessionRepairBackupDir)

	// Index coherence: markdown is the source of truth and the SQLite FTS5
	// index is a regenerable cache — rebuild it so search sees the repaired
	// bodies. Only when an index already exists: repair must never create a
	// store (dirs + DB) as a side effect.
	dbPath := filepath.Join(getStoreDir(), ".index", "search.db")
	if _, err := os.Stat(dbPath); err != nil {
		OutputText("no search index at %s — run `claudemem reindex` after creating a store if you use search", dbPath)
		return nil
	}
	store, err := getFileStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot open store for reindex: %v\nrun `claudemem reindex` to rebuild the search index\n", err)
		return nil
	}
	defer store.Close()
	count, err := store.Reindex()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: reindex failed: %v\nrun `claudemem reindex` to rebuild the search index\n", err)
		return nil
	}
	OutputText("search index rebuilt: %d entries reindexed", count)
	return nil
}

// printSessionRepairReport renders the per-file, per-line before/after report
// a human reviews before daring --apply (S3).
func printSessionRepairReport(results []sessionRepairFileResult, scanned int, applied bool) {
	mode := "DRY-RUN (no files written)"
	if applied {
		mode = "APPLY"
	}
	OutputText("session repair report — %s — %d session file(s) scanned", mode, scanned)

	counts := map[storage.RepairAction]int{}
	total := 0
	for _, r := range results {
		OutputText("")
		OutputText("%s: %d repair(s)", r.name, len(r.changes))
		for _, ch := range r.changes {
			counts[ch.Action]++
			total++
			OutputText("  L%d [%s]", ch.Line, ch.Action)
			OutputText("    - %s", ch.Before)
			if len(ch.After) == 0 {
				OutputText("    + (line removed)")
			}
			for _, after := range ch.After {
				OutputText("    + %s", after)
			}
		}
	}

	OutputText("")
	if total == 0 {
		OutputText("no repairable corruption found")
		return
	}
	parts := make([]string, 0, len(counts))
	for _, a := range []storage.RepairAction{
		storage.RepairActionFusedSplit,
		storage.RepairActionGluedEmptyStrip,
		storage.RepairActionBoldColonStrip,
		storage.RepairActionOrphanConsumed,
	} {
		if counts[a] > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", a, counts[a]))
		}
	}
	OutputText("summary: %d file(s) to repair, %d line repair(s) (%s)", len(results), total, strings.Join(parts, ", "))
	if !applied {
		OutputText("re-run with --apply --backup-dir <dir> to write these repairs")
	}
}
