package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

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
	Long: `Move the listed notes and sessions from this store (--store) to another existing store,
keeping each entry's id and its file. The target's index rows are built from the
file. No vectors go along, and the target's own vectors for a moved entry are
removed: the report counts the entries and prints the command that embeds them
in the target. The access log is not carried over: it records what a person
read in this store. The entry's access rows here are removed with it, so its
read counts start over in the target.

The file lists one full id per line; blank lines and everything after a # are
skipped, and --file - reads standard input. The whole file is checked before
anything moves.

Each entry moves on its own, under the write locks of both stores: its file is
put into the target under the same name (or, if another file has that name
there, the next free one), durably and never over another file, then its index
rows; only then does it leave this store. A run that stops half way leaves each
entry whole in one store or both, and running the same file again finishes it.
For the whole move and its reruns, stop everything else that writes to either
store (editors, an add-only sync, claudemem sync jobs): a program that writes
through a file it opened before the move can lose that write.
Two different files under one id, a file reached through a symlink or a bind
mount between the stores, and stores that overlap (also through a bind mount,
of a store or of a directory inside it) are refused. Notes move first, then sessions. A
session moves only when every note it lists under Related Notes (a heading with
that title at any level, in any case, with or without a colon) is in the target
or moves in the same run, since a session file names its related notes by title;
a ref in backticks there that is no note id keeps the session, and so does a note
id anywhere else in the file (a full id, or an id prefix of 8 or more characters
in backticks) that names a note staying here. A note counts as in a store when
its index lists it or a note file there holds it, and a related note that
neither store holds is only a warning, since that link is dead in both. Before a migration, run reindex and then health on this store, so
that its index matches its files.

The report lists the entries moved, already in the target, missing (in neither
store), refused (with the reason) and left to clean up (in the target, with
something left in this store); any of the last three makes the exit code 1. With
--dry-run both stores are opened read-only and no entry, file or index row changes
in either; SQLite may still create the -wal and -shm files next to an index in
WAL mode, as any reader does.

Examples:
  claudemem move --to ~/team-memory --file ids.txt --dry-run
  claudemem move --to ~/team-memory --file ids.txt
  claudemem --store ~/team-memory move --to ~/.claudemem --file back.txt`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runMove,
}

func init() {
	moveCmd.Flags().StringVar(&moveTo, "to", "", "the store to move the entries to (required; absolute or ~/ path of an existing store)")
	moveCmd.Flags().StringVar(&moveFile, "file", "", "file of ids to move, one per line (required; - reads standard input)")
	moveCmd.Flags().BoolVar(&moveDryRun, "dry-run", false, "report what would move; change no entry, file or index row")
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
	fromDir, err := primaryStoreDir()
	if err != nil {
		return err
	}
	// Before anything about the target: a directory inside this store (or holding it) is never a
	// target, and nothing below suggests making a store there.
	if sameDirKey(fromDir) == sameDirKey(toDir) {
		return fmt.Errorf("--to is this store itself (%s)", displayStoreName(fromDir))
	}
	if storage.StoresOverlap(fromDir, toDir) {
		return fmt.Errorf("--to %s and this store %s overlap: one sits inside the other; a move needs two separate stores", displayStoreName(toDir), displayStoreName(fromDir))
	}
	if info, statErr := os.Stat(toDir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("--to: store %s not found; it must be an existing store", displayStoreName(toDir))
	}
	if !storage.IsStoreDir(toDir) {
		return fmt.Errorf("--to: %s is not a claudemem store (no .index/search.db); a move needs an existing store as its target", displayStoreName(toDir))
	}
	if !storage.IsStoreDir(fromDir) {
		return fmt.Errorf("--store %s is not a claudemem store (no .index/search.db)", displayStoreName(fromDir))
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

	// No vector store: a move carries no vectors and never embeds. A dry run opens both stores
	// read-only, so it changes no entry, file or index row in either, nor the journal mode of an index.
	open := storage.NewFileStore
	if moveDryRun {
		open = storage.OpenReadOnly
	}
	from, err := open(fromDir)
	if err != nil {
		return fmt.Errorf("failed to open store: %w", err)
	}
	defer from.Close()
	to, err := open(toDir)
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
	embed := ""
	if report.VectorsToEmbed > 0 {
		embed = "claudemem --store " + shellWord(displayStoreName(toDir)) + " reindex --vectors --missing"
	}
	if outputFormat == "json" {
		if err := OutputJSON(struct {
			*storage.MoveReport
			EmbedCommand string `json:"embed_command,omitempty"` // embeds the moved entries in the target
		}{report, embed}); err != nil {
			return err
		}
	} else {
		printMoveReport(report, displayStoreName(fromDir), displayStoreName(toDir), embed)
	}
	if len(report.Missing) > 0 || len(report.Refused) > 0 || len(report.NeedsCleanup) > 0 {
		msg := fmt.Sprintf("move: %d missing, %d refused", len(report.Missing), len(report.Refused))
		if len(report.NeedsCleanup) > 0 {
			msg += fmt.Sprintf(", %d left to clean up", len(report.NeedsCleanup))
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func printMoveReport(r *storage.MoveReport, from, to, embed string) {
	header, verb := "move", "moved"
	if r.DryRun {
		header, verb = "move (dry run)", "to move"
	}
	summary := fmt.Sprintf("%s: %d ids from %s to %s: %d %s, %d already in the target, %d missing, %d refused",
		header, r.Entries, from, to, len(r.Moved), verb, len(r.AlreadyInTarget), len(r.Missing), len(r.Refused))
	if len(r.NeedsCleanup) > 0 {
		summary += fmt.Sprintf(", %d left to clean up", len(r.NeedsCleanup))
	}
	OutputText("%s", summary)
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
	for _, issue := range r.NeedsCleanup {
		OutputText("  left to clean up, line %d: %s %s: %s", issue.Line, issue.Kind, issue.ID, issue.Reason)
	}
	for _, w := range r.Warnings {
		OutputText("  warning: %s", w)
	}
	switch {
	case r.VectorsToEmbed > 0 && r.DryRun:
		OutputText("  %d entries would be in %s without vectors (a move carries none); afterwards embed them with: %s", r.VectorsToEmbed, to, embed)
	case r.VectorsToEmbed > 0:
		OutputText("  %d entries are in %s without vectors (a move carries none); embed them with: %s", r.VectorsToEmbed, to, embed)
	}
}

// shellWord is s as one shell word: as it is when it needs no quoting, otherwise in single quotes,
// with a leading ~/ left outside them so the shell still expands it.
func shellWord(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-./~+:@%=,", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	home := ""
	if rest, ok := strings.CutPrefix(s, "~/"); ok {
		home, s = "~/", rest
	}
	return home + "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
