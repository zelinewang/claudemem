package storage

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
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
	// something left here): a move carries no vectors, so the target embeds each of them itself.
	VectorsToEmbed int `json:"vectors_to_embed"`
}

// MoveEntries moves the listed notes and sessions from one store to another, keeping each entry's
// id and its file (under the same name, unless another file holds that name in the target). The
// target's index rows are built from the file, as a reindex builds them. No vectors go along, and
// the target's own vectors for a moved entry are removed: nothing shows that a store's vectors were
// embedded from the file as it is now, so the target embeds the moved entries itself (the report
// counts them). The access log is not carried over: it records what a person read, and stays out
// of the target.
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
// A note may move alone. A dry run changes nothing; it also runs on stores opened read-only.
func MoveEntries(from, to *FileStore, ids []MoveID, dryRun bool, progress func(done, total int)) (*MoveReport, error) {
	if sameStoreDir(from.baseDir, to.baseDir) {
		return nil, fmt.Errorf("the target is this store itself")
	}
	if StoresOverlap(from.baseDir, to.baseDir) {
		return nil, fmt.Errorf("the two stores overlap: one sits inside the other")
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
	want := make(map[string]bool, len(ids))
	for _, e := range ids {
		want[e.ID] = true
	}
	m.fromFiles, m.fromNoteFiles = entryFilesByID(from.baseDir, want)
	m.toFiles, m.toNoteFiles = entryFilesByID(to.baseDir, want)

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
	return m.report, nil
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
		m.finishFromTarget(e, kind, src, dst, readErr)
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
	linked := false
	if install {
		if rel, linked, err = m.installFile(kind, e.ID, rel, data); err != nil {
			m.refuse(e, kind, "write the target file: %v", err)
			return
		}
	}
	if err := m.writeTargetRows(f, rel); err != nil {
		if linked {
			os.Remove(joinStore(m.to, rel))
		}
		m.refuse(e, kind, "write the target's index: %v", err)
		return
	}
	m.to.removeStaleTemps(kind, e.ID, rel)

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
	if err := os.Remove(srcPath); err != nil && !os.IsNotExist(err) {
		m.needsCleanup(e, kind, []string{fmt.Sprintf("it is in the target, but its file here could not be removed: %v; run the move again", err)})
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
// rows rebuilt from it, then the rows here are removed.
func (m *mover) finishFromTarget(e MoveID, kind string, src, dst *indexRow, readErr error) {
	var rel string
	switch {
	case dst != nil:
		rel = dst.filepath
	default:
		// the target may hold the file without rows, when an earlier run lost them
		copies := m.to.copiesOf(kind, e.ID, m.toFiles[e.ID])
		if len(copies) != 1 {
			m.refuse(e, kind, "its file %s cannot be read: %v", src.filepath, readErr)
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
		m.to.removeStaleTemps(kind, e.ID, rel)
		if err := m.from.removeEntryRows(e.ID); err != nil {
			m.needsCleanup(e, kind, []string{fmt.Sprintf("it is in the target, but its rows here could not be removed: %v; run the move again", err)})
			return
		}
	}
	m.finish(e, kind, f.title, rel, "finished an earlier move", m.leftovers(e.ID, kind, data, "", rel))
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
			if rel, linked, err = m.installFile(kind, e.ID, rel, data); err != nil {
				m.refuse(e, kind, "write the target file: %v", err)
				return
			}
		}
		if err := m.writeTargetRows(f, rel); err != nil {
			if linked {
				os.Remove(joinStore(m.to, rel))
			}
			m.refuse(e, kind, "write the target's index: %v", err)
			return
		}
		m.to.removeStaleTemps(kind, e.ID, rel)
	}
	m.finish(e, kind, f.title, rel, note, m.leftovers(e.ID, kind, data, "", rel))
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
