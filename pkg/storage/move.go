package storage

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/zelinewang/claudemem/pkg/vectors"
)

// MoveID is one line of a `claudemem move` file: a full entry id.
type MoveID struct {
	Line int
	ID   string
}

// ParseMoveIDs reads a move file: one full id per line; blank lines and everything after a # are
// skipped, so a line can carry a comment ("<id>  # Deploy runbook"). The whole file is checked before
// anything moves: a line with more than one word or an id listed twice fails the file and names the
// bad lines. Ids are exact, never prefixes.
func ParseMoveIDs(r io.Reader) ([]MoveID, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var (
		ids      []MoveID
		problems []string
		seen     = map[string]int{}
		line     int
	)
	for sc.Scan() {
		line++
		text := sc.Text()
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if strings.IndexFunc(text, unicode.IsSpace) >= 0 {
			problems = append(problems, fmt.Sprintf("line %d: one full id per line, got %q", line, text))
			continue
		}
		if first, dup := seen[text]; dup {
			problems = append(problems, fmt.Sprintf("line %d: %s is already on line %d", line, text, first))
			continue
		}
		seen[text] = line
		ids = append(ids, MoveID{Line: line, ID: text})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read ids: %w", err)
	}
	if len(problems) > 0 {
		shown := problems
		if len(shown) > maxReportedProblems {
			shown = append(shown[:maxReportedProblems:maxReportedProblems], fmt.Sprintf("... and %d more", len(problems)-maxReportedProblems))
		}
		return nil, fmt.Errorf("%d invalid lines, nothing moved:\n  %s", len(problems), strings.Join(shown, "\n  "))
	}
	return ids, nil
}

// MovedEntry is an entry that MoveEntries moved (or, in a dry run, would move), or found already in
// the target.
type MovedEntry struct {
	Line  int    `json:"line"`
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	File  string `json:"file"`           // its file in the target store, relative to it
	Note  string `json:"note,omitempty"` // what was out of the ordinary, for example a file renamed
}

// MoveIssue is an entry that MoveEntries did not move, or moved without finishing.
type MoveIssue struct {
	Line   int    `json:"line"`
	ID     string `json:"id"`
	Kind   string `json:"kind,omitempty"`
	Reason string `json:"reason"`
}

// MoveReport is what MoveEntries did, or in a dry run would do.
type MoveReport struct {
	DryRun          bool         `json:"dry_run"`
	Entries         int          `json:"entries"`
	Moved           []MovedEntry `json:"moved"`
	AlreadyInTarget []MovedEntry `json:"already_in_target"`
	Missing         []MoveIssue  `json:"missing"`
	Refused         []MoveIssue  `json:"refused"`
	// NeedsCleanup lists entries that are in the target but left something in this store: rows or a
	// file the move could not remove (running it again finishes them), or a different copy of the file.
	NeedsCleanup []MoveIssue `json:"needs_cleanup"`
	Warnings     []string    `json:"warnings"`
	// VectorsToEmbed counts the entries this run put into the target (moved, or in the target with
	// something left here), and those an earlier run put there that still have no vectors there: a
	// move carries no vectors, so the target embeds each of them itself.
	VectorsToEmbed int `json:"vectors_to_embed"`
}

// MoveEntries moves the listed notes and sessions from one store to another, keeping each entry's
// id and its file (under the same name, unless another file holds that name in the target). The
// target's index rows are built from the file, as a reindex builds them. No vectors go along, and
// the target's own vectors for a moved entry are removed: nothing shows that a store's vectors were
// embedded from the file as it is now, so the target embeds the moved entries itself (the report
// counts them). The access log is not carried over: it records what a person read in this store.
// The entry's access rows here are removed with its other rows, so its read counts start over in
// the target.
//
// Each entry moves on its own, under the write locks of both stores (taken in one order, so two
// moves in opposite directions cannot deadlock): the file is put into the target first, durably and
// never over another file, then its index rows; only then do the file and the rows leave this store.
// A run that stops half way leaves every entry whole in one store or in both, and running the same
// ids again finishes it. Two different files under one id, a file reached through a symlink between
// the stores, and a stale row in the target are refused. Stores that overlap are refused.
//
// Notes move before sessions. A session moves only if every note it lists under Related Notes is
// in the target, or moved there in this run: a session file names its related notes by title, so it
// never moves without them. A note in neither store is only a warning, since the link is dead in both.
// A note may move alone. A dry run changes no entry, file or index row; it also runs on stores opened
// read-only.
func MoveEntries(from, to *FileStore, ids []MoveID, dryRun bool, progress func(done, total int)) (*MoveReport, error) {
	if sameStoreDir(from.baseDir, to.baseDir) {
		return nil, fmt.Errorf("the target is this store itself")
	}
	if StoresOverlap(from.baseDir, to.baseDir) {
		return nil, fmt.Errorf("the two stores overlap: one sits inside the other")
	}
	if holdsAliasOf(from.baseDir, to.baseDir) || holdsAliasOf(to.baseDir, from.baseDir) {
		return nil, fmt.Errorf("the two stores overlap: one sits inside the other through a bind mount of one of its directories")
	}
	if (from.readOnly || to.readOnly) && !dryRun {
		return nil, fmt.Errorf("both stores must be open for writing")
	}
	for _, s := range []struct {
		name string
		db   *sql.DB
	}{{"this store", from.db}, {"the target", to.db}} {
		if err := vectors.CheckIndexSchema(s.db); err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
	}
	m := &mover{
		from:         from,
		to:           to,
		dryRun:       dryRun,
		inTarget:     map[string]bool{},
		refusedNotes: map[string]bool{},
		unreadable:   map[string]bool{},
		report: &MoveReport{
			DryRun: dryRun, Entries: len(ids),
			Moved: []MovedEntry{}, AlreadyInTarget: []MovedEntry{},
			Missing: []MoveIssue{}, Refused: []MoveIssue{}, NeedsCleanup: []MoveIssue{}, Warnings: []string{},
		},
	}
	if !dryRun {
		// The target's commits must be on disk before anything leaves this store.
		if _, err := to.db.Exec(`PRAGMA synchronous=FULL`); err != nil {
			return nil, fmt.Errorf("make the target's index sync each commit: %w", err)
		}
	}
	// What a stopped run put into the target without committing its rows goes first.
	if err := m.recoverPending(); err != nil {
		return nil, fmt.Errorf("the target's move journal: %w", err)
	}
	want := make(map[string]bool, len(ids))
	for _, e := range ids {
		want[e.ID] = true
	}
	var fromUnreadable, toUnreadable []unreadablePath
	m.fromFiles, m.fromNoteFiles, fromUnreadable = entryFilesByID(from.baseDir, want)
	m.toFiles, m.toNoteFiles, toUnreadable = entryFilesByID(to.baseDir, want)
	m.reportUnreadable("this store", fromUnreadable)
	m.reportUnreadable("the target", toUnreadable)
	moveStep("after-walk")

	var notes, sessions []MoveID
	for _, e := range ids {
		kind, err := m.kindOf(e.ID)
		switch {
		case err != nil:
			m.refuse(e, "", "read the index: %v", err)
		case kind == "note":
			notes = append(notes, e)
		case kind == "session":
			sessions = append(sessions, e)
		default:
			m.missing(e)
		}
	}
	done := len(ids) - len(notes) - len(sessions)
	queue := append(notes, sessions...)
	for i, e := range queue {
		if err := m.moveOne(e); err != nil {
			// The locks cannot be taken: every entry left would wait just as long.
			for _, rest := range queue[i:] {
				kind, _ := m.kindOf(rest.ID)
				m.refuse(rest, kind, "not attempted: %v", err)
			}
			break
		}
		done++
		if progress != nil {
			progress(done, len(ids))
		}
	}
	if !dryRun {
		m.recheckCopies(want)
	}
	return m.report, nil
}

// recheckCopies walks both stores again after the run. A different copy of a moved entry's file that
// arrived after the walk at the start (a sync pull or an import during a long run) is not seen by the
// entry's own checks, and a later reindex of the target could keep it instead of the moved file. An
// entry with such a copy in either store is reported as left to clean up instead of moved.
func (m *mover) recheckCopies(want map[string]bool) {
	fromFiles, _, fromUnreadable := entryFilesByID(m.from.baseDir, want)
	toFiles, _, toUnreadable := entryFilesByID(m.to.baseDir, want)
	m.reportUnreadable("this store", fromUnreadable)
	m.reportUnreadable("the target", toUnreadable)
	recheck := func(list []MovedEntry) []MovedEntry {
		kept := []MovedEntry{}
		for _, e := range list {
			if reason := m.differentCopyNow(e, fromFiles[e.ID], toFiles[e.ID]); reason != "" {
				m.needsCleanup(MoveID{Line: e.Line, ID: e.ID}, e.Kind, []string{reason})
				continue
			}
			kept = append(kept, e)
		}
		return kept
	}
	m.report.Moved = recheck(m.report.Moved)
	m.report.AlreadyInTarget = recheck(m.report.AlreadyInTarget)
}

// differentCopyNow returns why an entry in the target must be cleaned up when either store now holds
// a copy of its file that differs from the target's file, or "".
func (m *mover) differentCopyNow(e MovedEntry, fromRels, toRels []string) string {
	ours, err := os.ReadFile(joinStore(m.to, e.File))
	if err != nil {
		return fmt.Sprintf("its file in the target, %s, cannot be read after the run: %v", e.File, err)
	}
	for _, s := range []struct {
		name  string
		store *FileStore
		rels  []string
	}{{"the target", m.to, toRels}, {"this store", m.from, fromRels}} {
		for _, c := range s.store.copiesOf(e.Kind, e.ID, s.rels) {
			if !bytes.Equal(c.data, ours) {
				return fmt.Sprintf("a different copy of its file arrived in %s during the run, at %s; compare it with the target's file %s and remove one by hand", s.name, c.rel, e.File)
			}
		}
	}
	return ""
}

// moveTestHook, when a test sets it, runs at the named steps of an entry's move.
var moveTestHook func(step string)

func moveStep(step string) {
	if moveTestHook != nil {
		moveTestHook(step)
	}
}

type mover struct {
	from, to      *FileStore
	dryRun        bool
	fromFiles     map[string][]string // the files of each listed id in this store, from a walk at the start
	toFiles       map[string][]string // the same in the target
	fromNoteFiles []string            // the ids of every note file in this store, sorted, from the same walk
	toNoteFiles   []string            // the same in the target
	inTarget      map[string]bool     // notes in the target after this run (or, dry, that would be)
	refusedNotes  map[string]bool     // notes this run refused or did not attempt: they stay here
	unreadable    map[string]bool     // the unreadable files already reported, by store and path
	report        *MoveReport
}

// kindOf is the entry's kind in this store, else in the target, else "".
func (m *mover) kindOf(id string) (string, error) {
	for _, fs := range []*FileStore{m.from, m.to} {
		var typ string
		err := fs.db.QueryRow(`SELECT type FROM entries WHERE id = ?`, id).Scan(&typ)
		if err == nil {
			return typ, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
	}
	return "", nil
}

func (m *mover) refuse(e MoveID, kind, format string, args ...interface{}) {
	m.report.Refused = append(m.report.Refused, MoveIssue{Line: e.Line, ID: e.ID, Kind: kind, Reason: fmt.Sprintf(format, args...)})
	if kind == "note" {
		m.refusedNotes[e.ID] = true
	}
}

func (m *mover) needsCleanup(e MoveID, kind string, problems []string) {
	m.report.NeedsCleanup = append(m.report.NeedsCleanup, MoveIssue{Line: e.Line, ID: e.ID, Kind: kind, Reason: strings.Join(problems, "; ")})
	if kind == "note" {
		m.inTarget[e.ID] = true
	}
}

// reportUnreadable warns, once each, about the files and directories of a store that a walk could not
// read: the move cannot tell whether they hold a copy of an entry it moves, which a reindex would bring
// back once they can be read.
func (m *mover) reportUnreadable(store string, list []unreadablePath) {
	for _, item := range list {
		if key := store + "\x00" + item.rel; !m.unreadable[key] {
			m.unreadable[key] = true
			m.warn("%s: %s cannot be read (%v), so the move cannot tell whether it holds a copy of an entry it moves; check it by hand", store, item.rel, item.err)
		}
	}
}

func (m *mover) warn(format string, args ...interface{}) {
	m.report.Warnings = append(m.report.Warnings, fmt.Sprintf(format, args...))
}

func (m *mover) moved(e MoveID, kind, title, file, note string) {
	m.report.Moved = append(m.report.Moved, MovedEntry{Line: e.Line, ID: e.ID, Kind: kind, Title: title, File: file, Note: note})
	if kind == "note" {
		m.inTarget[e.ID] = true
	}
}

// missing reports an id that neither index lists. When a store holds a file for it outside its
// index, the entry is refused instead, naming that file: moving it needs that store reindexed first.
func (m *mover) missing(e MoveID) {
	for _, s := range []struct {
		name  string
		store *FileStore
		files []string
	}{{"this store", m.from, m.fromFiles[e.ID]}, {"the target", m.to, m.toFiles[e.ID]}} {
		for _, rel := range s.files {
			if kind := kindOfPath(rel); kind != "" && len(s.store.copiesOf(kind, e.ID, []string{rel})) > 0 {
				m.refuse(e, kind, "in neither store's index, but %s holds its file %s; reindex it, then run the move again", s.name, rel)
				return
			}
		}
	}
	m.report.Missing = append(m.report.Missing, MoveIssue{Line: e.Line, ID: e.ID, Reason: "in neither store's index (ids are full ids, not prefixes)"})
}

// moveOne moves one entry under both stores' locks. It returns an error only when the locks cannot be
// taken, which ends the run.
func (m *mover) moveOne(e MoveID) error {
	if !m.dryRun {
		unlock, err := lockBoth(m.from, m.to)
		if err != nil {
			return err
		}
		defer unlock()
	}
	src, err := m.from.indexRow(e.ID)
	if err != nil {
		m.refuse(e, "", "read this store's index: %v", err)
		return nil
	}
	dst, err := m.to.indexRow(e.ID)
	if err != nil {
		m.refuse(e, "", "read the target's index: %v", err)
		return nil
	}
	switch {
	case src == nil && dst == nil: // removed by someone else since the run started
		m.missing(e)
	case src == nil:
		m.alreadyInTarget(e, dst)
	default:
		m.moveFromHere(e, src, dst)
	}
	return nil
}

// moveFromHere moves an entry this store's index lists.
func (m *mover) moveFromHere(e MoveID, src, dst *indexRow) {
	kind := src.typ
	if !validEntryPath(kind, src.filepath) {
		m.refuse(e, kind, "its file path %q is not where a %s lives", src.filepath, kind)
		return
	}
	if dst != nil && (dst.typ != kind || !validEntryPath(kind, dst.filepath)) {
		m.refuse(e, kind, "the target's index has a stale row for it (a %s at %q); reindex the target, then run the move again", dst.typ, dst.filepath)
		return
	}
	srcPath, err := movePath(m.from, m.to, src.filepath)
	if err != nil {
		m.refuse(e, kind, "its file: %v", err)
		return
	}
	data, readErr := os.ReadFile(srcPath)
	if os.IsNotExist(readErr) {
		m.finishFromTarget(e, kind, src, dst, srcPath, readErr)
		return
	}
	if readErr != nil {
		m.refuse(e, kind, "its file %s cannot be read: %v", src.filepath, readErr)
		return
	}
	f, err := parseEntry(kind, data)
	if err != nil {
		m.refuse(e, kind, "its file %s does not parse: %v", src.filepath, err)
		return
	}
	if f.id != e.ID {
		m.refuse(e, kind, "its file %s holds another id (%s)", src.filepath, f.id)
		return
	}
	if kind == "session" {
		if reason := m.relatedNotesStay(e.ID, data); reason != "" {
			m.refuse(e, kind, "%s", reason)
			return
		}
	}

	rel, note, install, err := m.placeInTarget(kind, e.ID, src.filepath, srcPath, dst, data)
	if err != nil {
		m.refuse(e, kind, "%v", err)
		return
	}
	if m.dryRun {
		m.finish(e, kind, f.title, rel, note, m.leftovers(e.ID, kind, data, src.filepath, rel))
		return
	}

	// 1. Into the target: the file (durably, never over another file), then its rows.
	moveStep("before-install")
	linked := false
	if install {
		if rel, linked, err = m.installFile(kind, e.ID, rel, src.filepath, data); err != nil {
			m.refuse(e, kind, "write the target file: %v", err)
			return
		}
	}
	moveStep("after-install")
	if err := m.writeTargetRows(f, rel); err != nil {
		m.refuse(e, kind, "write the target's index: %v%s", err, m.rollBack(e.ID, linked, rel))
		return
	}
	m.to.clearPending(e.ID)
	moveStep("after-target-rows")
	m.to.removeStaleTemps(rel, data)

	// 2. Out of this store: the file, then the rows. A stop between the two leaves rows without a
	// file here, which the next run finishes.
	if sameResolvedFile(srcPath, joinStore(m.to, rel)) { // checked before writing; never delete the target's file
		m.needsCleanup(e, kind, []string{fmt.Sprintf("its file here is the target's file %s (through a link); left as it is", rel)})
		return
	}
	if !linked { // a file the move found in the target, not one it wrote and synced
		if err := syncFile(joinStore(m.to, rel)); err != nil {
			m.needsCleanup(e, kind, []string{fmt.Sprintf("it is in the target, but its file there %s could not be synced: %v; its file here stays, run the move again", rel, err)})
			return
		}
	}
	changed, aside, err := removeUnchanged(srcPath, data)
	if err != nil {
		m.needsCleanup(e, kind, []string{fmt.Sprintf("it is in the target, but its file here could not be removed: %v; run the move again", err)})
		return
	}
	if changed { // saved by a writer outside claudemem while it moved
		m.needsCleanup(e, kind, []string{fmt.Sprintf("its file here changed during the move (%s); the target holds the version from before the change: compare the two and keep one", m.whereChanged(src.filepath, aside))})
		return
	}
	if err := m.from.removeEntryRows(e.ID); err != nil {
		m.needsCleanup(e, kind, []string{fmt.Sprintf("it is in the target, but its rows here could not be removed: %v; run the move again", err)})
		return
	}
	m.finish(e, kind, f.title, rel, note, m.leftovers(e.ID, kind, data, src.filepath, rel))
}

// finishFromTarget handles an entry whose rows are here but whose file is not: an earlier run put it
// into the target and stopped before removing the rows here. The target's file is checked and its
// rows rebuilt from it, then the rows here are removed. When the earlier run stopped with this store's
// file set aside and changed (saved meanwhile by a writer outside claudemem), the file goes back
// under its name and the rows here stay, as when a run does not stop.
func (m *mover) finishFromTarget(e MoveID, kind string, src, dst *indexRow, srcPath string, readErr error) {
	var rel string
	switch {
	case dst != nil:
		rel = dst.filepath
	default:
		// the target may hold the file without rows, when an earlier run lost them
		copies := m.to.copiesOf(kind, e.ID, m.toFiles[e.ID])
		switch {
		case len(copies) == 0:
			m.refuse(e, kind, "its file %s is in neither store: %v", src.filepath, readErr)
			return
		case len(copies) > 1:
			m.refuse(e, kind, "its file %s is gone from this store, and the target holds %d copies of it outside its index (%s); keep one, reindex the target, then run the move again", src.filepath, len(copies), copyList(copies))
			return
		}
		rel = copies[0].rel
	}
	targetPath, err := movePath(m.to, m.from, rel)
	if err != nil {
		m.refuse(e, kind, "the target's file: %v", err)
		return
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		if copies := m.to.copiesOf(kind, e.ID, m.toFiles[e.ID]); dst != nil && len(copies) > 0 {
			m.refuse(e, kind, "its file %s is gone from this store, and the target's index row for it names %s, which is gone too, while the target holds its file at %s; reindex the target, then run the move again", src.filepath, rel, copyList(copies))
			return
		}
		m.refuse(e, kind, "its file is in neither store (%s here, %s there): %v", src.filepath, rel, err)
		return
	}
	f, err := parseEntry(kind, data)
	if err != nil || f.id != e.ID {
		m.refuse(e, kind, "the target's index row for it names %s, which holds another entry's file; reindex the target, then run the move again", rel)
		return
	}
	if other := m.differentCopy(m.to, kind, e.ID, data, m.toFiles[e.ID]); other != "" {
		m.refuse(e, kind, "the target holds a different file for this id at %s, beside %s; compare them by hand", other, rel)
		return
	}
	if !m.dryRun {
		if err := m.writeTargetRows(f, rel); err != nil {
			m.refuse(e, kind, "write the target's index: %v", err)
			return
		}
		m.to.removeStaleTemps(rel, data)
	}
	if problem := m.putBackChangedFile(e.ID, src.filepath, srcPath, data); problem != "" {
		m.finish(e, kind, f.title, rel, "", []string{problem})
		return
	}
	if !m.dryRun {
		if err := m.from.removeEntryRows(e.ID); err != nil {
			m.needsCleanup(e, kind, []string{fmt.Sprintf("it is in the target, but its rows here could not be removed: %v; run the move again", err)})
			return
		}
	}
	m.finish(e, kind, f.title, rel, "finished an earlier move", m.leftovers(e.ID, kind, data, "", rel))
}

// putBackChangedFile looks for the entry's file of this store set aside by a stopped run (at path's
// set-aside name, while path is free) holding this entry other than data, the version in the target:
// a writer outside claudemem saved it during that run. It puts the file back under its name, or in a
// dry run says it would, and returns why the entry is left to clean up; "" when there is no such file.
func (m *mover) putBackChangedFile(id, rel, path string, data []byte) string {
	aside := setAsidePath(path)
	info, err := os.Lstat(aside)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	held, err := os.ReadFile(aside)
	if err != nil || bytes.Equal(held, data) || frontmatterID(aside) != id {
		return "" // the moved file itself, which the leftovers remove, or not this entry's file
	}
	asideRel := filepath.Join(filepath.Dir(rel), filepath.Base(aside))
	switch {
	case m.dryRun:
		return fmt.Sprintf("its file here changed during an earlier move, which stopped with it set aside at %s: it would go back to %s, and the target holds the version from before the change: compare the two and keep one", asideRel, rel)
	case linkNoReplace(aside, path) == nil:
		os.Remove(aside)
		return fmt.Sprintf("its file here changed during an earlier move, which stopped with it set aside: it is back at %s, and the target holds the version from before the change: compare the two and keep one", rel)
	}
	return fmt.Sprintf("its file here changed during an earlier move, which stopped with it set aside at %s: this may be the newer version; rename it back to %s to keep it", asideRel, rel)
}

// alreadyInTarget handles an entry the target's index lists and this store's does not. Its file in
// the target is checked; copies of it left in this store outside the index (an add-only sync can
// bring one back) are removed when they are the same file. When the target's file is missing, the
// move is finished from such a copy.
func (m *mover) alreadyInTarget(e MoveID, dst *indexRow) {
	kind := dst.typ
	if !validEntryPath(kind, dst.filepath) {
		m.refuse(e, kind, "the target's index has a stale row for it (%q); reindex the target, then run the move again", dst.filepath)
		return
	}
	targetPath, err := movePath(m.to, m.from, dst.filepath)
	if err != nil {
		m.refuse(e, kind, "the target's file: %v", err)
		return
	}
	there, err := os.ReadFile(targetPath)
	if os.IsNotExist(err) {
		m.finishFromCopyHere(e, kind, dst)
		return
	}
	if err != nil {
		m.refuse(e, kind, "the target's file %s cannot be read: %v", dst.filepath, err)
		return
	}
	if f, perr := parseEntryFile(kind, there); perr != nil || f.id != e.ID {
		m.refuse(e, kind, "the target's index row for it names %s, which holds another entry's file; reindex the target, then run the move again", dst.filepath)
		return
	}
	if kind == "note" {
		m.inTarget[e.ID] = true
	}
	if !m.to.hasVectorsFor(e.ID) { // an earlier run put it there; the target has not embedded it yet
		m.report.VectorsToEmbed++
	}
	if problems := m.leftovers(e.ID, kind, there, "", dst.filepath); len(problems) > 0 {
		m.needsCleanup(e, kind, problems)
		return
	}
	m.report.AlreadyInTarget = append(m.report.AlreadyInTarget, MovedEntry{Line: e.Line, ID: e.ID, Kind: kind, Title: dst.title, File: dst.filepath})
}

// finishFromCopyHere finishes the move of an entry that the target's index lists without its file,
// from the one copy of the file that this store holds outside its index.
func (m *mover) finishFromCopyHere(e MoveID, kind string, dst *indexRow) {
	copies := m.from.copiesOf(kind, e.ID, m.fromFiles[e.ID])
	if len(copies) == 0 {
		m.refuse(e, kind, "the target's index lists it, but its file %s is in neither store; reindex the target", dst.filepath)
		return
	}
	for _, c := range copies[1:] {
		if !bytes.Equal(c.data, copies[0].data) {
			m.refuse(e, kind, "the target lacks its file, and this store holds different copies of it (%s, %s); compare them by hand", copies[0].rel, c.rel)
			return
		}
	}
	data := copies[0].data
	f, err := parseEntry(kind, data)
	if err != nil {
		m.refuse(e, kind, "its file %s does not parse: %v", copies[0].rel, err)
		return
	}
	if kind == "session" {
		if reason := m.relatedNotesStay(e.ID, data); reason != "" {
			m.refuse(e, kind, "%s", reason)
			return
		}
	}
	rel, _, install, err := m.placeInTarget(kind, e.ID, dst.filepath, joinStore(m.from, copies[0].rel), dst, data)
	if err != nil {
		m.refuse(e, kind, "%v", err)
		return
	}
	note := "finished an earlier move: its file was missing in the target"
	if !m.dryRun {
		linked := false
		if install {
			if rel, linked, err = m.installFile(kind, e.ID, rel, copies[0].rel, data); err != nil {
				m.refuse(e, kind, "write the target file: %v", err)
				return
			}
		}
		if err := m.writeTargetRows(f, rel); err != nil {
			m.refuse(e, kind, "write the target's index: %v%s", err, m.rollBack(e.ID, linked, rel))
			return
		}
		m.to.clearPending(e.ID)
		m.to.removeStaleTemps(rel, data)
	}
	m.finish(e, kind, f.title, rel, note, m.leftovers(e.ID, kind, data, "", rel))
}

// rollBack removes the file a failed entry put into the target (linked: this run created it), and
// returns what the report adds when that removal fails: the file stays there, without rows, and its
// record in the target's move journal stays too, so that a later run removes it.
func (m *mover) rollBack(id string, linked bool, rel string) string {
	if !linked {
		return ""
	}
	if err := os.Remove(joinStore(m.to, rel)); err != nil && !os.IsNotExist(err) {
		return fmt.Sprintf("; the file it put into the target stays there at %s, without rows, since it could not be removed: %v", rel, err)
	}
	m.to.clearPending(id)
	return ""
}

// copyList names the files of copies, for a report.
func copyList(copies []fileCopy) string {
	rels := make([]string, len(copies))
	for i, c := range copies {
		rels[i] = c.rel
	}
	return strings.Join(rels, ", ")
}

// finish reports an entry that is now in the target: moved, or left to clean up when something of
// it stays in this store. A dry run counts it to embed here; a move counts it when its rows go into
// the target (writeTargetRows), also when it stops before the entry leaves this store.
func (m *mover) finish(e MoveID, kind, title, rel, note string, problems []string) {
	if m.dryRun {
		m.report.VectorsToEmbed++
	}
	if len(problems) > 0 {
		m.needsCleanup(e, kind, problems)
		return
	}
	m.moved(e, kind, title, rel, note)
}
