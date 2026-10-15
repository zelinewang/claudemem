package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	moveTo     string
	moveFile   string
	moveDryRun bool
)

var moveCmd = &cobra.Command{
	Use:   "move --to <store> --file <ids>",
	Short: "Move entries to another store, keeping their ids",
	Long: `Move the listed notes and sessions from this store (--store) to another store,
keeping each entry's id, its file and its index rows (the search row, the access
log and the vectors, except TF-IDF ones, which belong to one store's corpus).

The file lists one full id per line; blank lines and everything after a # are
skipped, and --file - reads standard input. The whole file is checked before
anything moves.

Each entry moves on its own, under the write locks of both stores: its file is
written to the target under the same name (or, if another file has that name
there, the next free one), its index rows are copied, then it leaves this store.
A run that stops half way leaves each entry whole in one store or both, and
running the same file again finishes it. Notes move first, then sessions. A
session moves only when every note it lists as related is in the target or moves
in the same run, since a session file names its related notes by title.

The report lists the entries moved, already in the target, missing (in neither
store) and refused, with the reason; missing or refused entries make the exit
code 1. With --dry-run nothing changes.

Examples:
  claudemem move --to ~/team-memory --file ids.txt --dry-run
  claudemem move --to ~/team-memory --file ids.txt
  claudemem --store ~/team-memory move --to ~/.claudemem --file back.txt`,
	Args: cobra.NoArgs,
	RunE: runMove,
}

func init() {
	moveCmd.Flags().StringVar(&moveTo, "to", "", "the store to move the entries to (required; absolute or ~/ path, the directory must exist)")
	moveCmd.Flags().StringVar(&moveFile, "file", "", "file of ids to move, one per line (required; - reads standard input)")
	moveCmd.Flags().BoolVar(&moveDryRun, "dry-run", false, "report what would move, change nothing")
	rootCmd.AddCommand(moveCmd)
}

func runMove(cmd *cobra.Command, args []string) error {
	if moveTo == "" || moveFile == "" {
		return fmt.Errorf("move needs --to <store> and --file <ids>")
	}
	toDir, err := expandStorePath(moveTo)
	if err != nil {
		return fmt.Errorf("--to: %w", err)
	}
	if info, statErr := os.Stat(toDir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("--to: store %s not found; it must be an existing directory", displayStoreName(toDir))
	}
	fromDir, err := primaryStoreDir()
	if err != nil {
		return err
	}
	if sameDirKey(fromDir) == sameDirKey(toDir) {
		return fmt.Errorf("--to is this store itself (%s)", displayStoreName(fromDir))
	}

	var in io.Reader = os.Stdin
	if moveFile != "-" {
		f, err := os.Open(moveFile)
		if err != nil {
			return fmt.Errorf("open ids: %w", err)
		}
		defer f.Close()
		in = f
	}
	ids, err := storage.ParseMoveIDs(in)
	if err != nil {
		return err
	}

	// No vector store: a move copies vector rows as they are and never embeds.
	from, err := storage.NewFileStore(fromDir)
	if err != nil {
		return fmt.Errorf("failed to open store: %w", err)
	}
	defer from.Close()
	to, err := storage.NewFileStore(toDir)
	if err != nil {
		return fmt.Errorf("open store %s: %w", displayStoreName(toDir), err)
	}
	defer to.Close()

	report, err := storage.MoveEntries(from, to, ids, moveDryRun, func(done, total int) {
		if done%500 == 0 && done < total {
			fmt.Fprintf(os.Stderr, "move: %d/%d\n", done, total)
		}
	})
	if err != nil {
		return err
	}
	if outputFormat == "json" {
		if err := OutputJSON(report); err != nil {
			return err
		}
	} else {
		printMoveReport(report, displayStoreName(fromDir), displayStoreName(toDir))
	}
	if len(report.Missing) > 0 || len(report.Refused) > 0 {
		return fmt.Errorf("move: %d missing, %d refused", len(report.Missing), len(report.Refused))
	}
	return nil
}

func printMoveReport(r *storage.MoveReport, from, to string) {
	header, verb := "move", "moved"
	if r.DryRun {
		header, verb = "move (dry run)", "to move"
	}
	OutputText("%s: %d ids from %s to %s: %d %s, %d already in the target, %d missing, %d refused",
		header, r.Entries, from, to, len(r.Moved), verb, len(r.AlreadyInTarget), len(r.Missing), len(r.Refused))
	for _, e := range r.Moved {
		line := fmt.Sprintf("  %s %s %s %q -> %s", verb, e.Kind, e.ID, e.Title, e.File)
		if e.Note != "" {
			line += " (" + e.Note + ")"
		}
		OutputText("%s", line)
	}
	for _, e := range r.AlreadyInTarget {
		OutputText("  already in the target: %s %s %q (%s)", e.Kind, e.ID, e.Title, e.File)
	}
	for _, issue := range r.Missing {
		OutputText("  missing, line %d: %s: %s", issue.Line, issue.ID, issue.Reason)
	}
	for _, issue := range r.Refused {
		OutputText("  refused, line %d: %s %s: %s", issue.Line, issue.Kind, issue.ID, issue.Reason)
	}
	for _, w := range r.Warnings {
		OutputText("  warning: %s", w)
	}
	if r.VectorsToEmbed > 0 {
		OutputText("  %d moved entries took no vectors along; if %s uses semantic search, run: claudemem --store %s reindex --vectors --missing", r.VectorsToEmbed, to, to)
	}
}
