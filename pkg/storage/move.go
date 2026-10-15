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

// MoveIssue is an entry that MoveEntries did not move.
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
	Warnings        []string     `json:"warnings"`
	// VectorsToEmbed counts the moved entries that took no vectors along: TF-IDF vectors are built
	// against one store's own corpus and are never copied, and a store may have none.
	VectorsToEmbed int `json:"vectors_to_embed"`
}

// MoveEntries moves the listed notes and sessions from one store to another, keeping each entry's
// id, its file (under the same name, unless another file holds that name in the target) and its
// index rows: the entries row, the search row, the access log and the vectors (except TF-IDF ones).
//
// Each entry moves on its own, under the write locks of both stores (taken in one order, so two
// moves in opposite directions cannot deadlock): the file goes to the target first, through a temp
// file, with its index rows in one transaction; then the file and the rows leave this store. A run
// that stops half way leaves every entry whole in one store or in both, and running the same ids
// again finishes it: an entry already in the target is reported as such, and a copy in both stores
// with the same file is removed from this store. Two different files under one id are refused.
//
// Notes move before sessions. A session moves only if every note it lists as related is in the
// target, or moved there in this run: a session file names its related notes by title, so it
// never moves without them. A note may move alone. A dry run changes nothing.
func MoveEntries(from, to *FileStore, ids []MoveID, dryRun bool, progress func(done, total int)) (*MoveReport, error) {
	if sameStoreDir(from.baseDir, to.baseDir) {
		return nil, fmt.Errorf("the target is this store itself")
	}
	m := &mover{
		from:     from,
		to:       to,
		dryRun:   dryRun,
		vectors:  hasChunkedVectors(from.db) && hasChunkedVectors(to.db),
		inTarget: map[string]bool{},
		report: &MoveReport{
			DryRun: dryRun, Entries: len(ids),
			Moved: []MovedEntry{}, AlreadyInTarget: []MovedEntry{},
			Missing: []MoveIssue{}, Refused: []MoveIssue{}, Warnings: []string{},
		},
	}

	var notes, sessions []MoveID
	for _, e := range ids {
		switch m.kindOf(e.ID) {
		case "note":
			notes = append(notes, e)
		case "session":
			sessions = append(sessions, e)
		default:
			m.report.Missing = append(m.report.Missing, MoveIssue{Line: e.Line, ID: e.ID, Reason: "in neither store's index (ids are full ids, not prefixes)"})
		}
	}
	done := len(m.report.Missing)
	for _, list := range [][]MoveID{notes, sessions} {
		for _, e := range list {
			m.moveOne(e)
			done++
			if progress != nil {
				progress(done, len(ids))
			}
		}
	}
	return m.report, nil
}

type mover struct {
	from, to *FileStore
	dryRun   bool
	vectors  bool            // both stores have the chunked vectors table, so vector rows can be copied
	inTarget map[string]bool // notes in the target after their move in this run (or, dry, that would be)
	report   *MoveReport
}

// indexRow is an entry's row in the entries table.
type indexRow struct {
	id, typ, title, category, branch, project, sessionID, dateStr, tags, filepath, created, updated string
}

func (fs *FileStore) indexRow(id string) (*indexRow, error) {
	r := &indexRow{}
	err := fs.db.QueryRow(`SELECT id, type, title, category, branch, project, session_id, date_str, tags, filepath, created, updated
		FROM entries WHERE id = ?`, id).Scan(&r.id, &r.typ, &r.title, &r.category, &r.branch, &r.project,
		&r.sessionID, &r.dateStr, &r.tags, &r.filepath, &r.created, &r.updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// kindOf is the entry's kind in this store, else in the target, else "".
func (m *mover) kindOf(id string) string {
	for _, fs := range []*FileStore{m.from, m.to} {
		var typ string
		if err := fs.db.QueryRow(`SELECT type FROM entries WHERE id = ?`, id).Scan(&typ); err == nil {
			return typ
		}
	}
	return ""
}

func (m *mover) refuse(e MoveID, kind, format string, args ...interface{}) {
	m.report.Refused = append(m.report.Refused, MoveIssue{Line: e.Line, ID: e.ID, Kind: kind, Reason: fmt.Sprintf(format, args...)})
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

func (m *mover) moveOne(e MoveID) {
	if !m.dryRun {
		unlock, err := lockBoth(m.from, m.to)
		if err != nil {
			m.refuse(e, "", "%v", err)
			return
		}
		defer unlock()
	}
	src, err := m.from.indexRow(e.ID)
	if err != nil {
		m.refuse(e, "", "read this store's index: %v", err)
		return
	}
	dst, err := m.to.indexRow(e.ID)
	if err != nil {
		m.refuse(e, "", "read the target's index: %v", err)
		return
	}
	if src == nil {
		if dst == nil { // removed by someone else since the run started
			m.report.Missing = append(m.report.Missing, MoveIssue{Line: e.Line, ID: e.ID, Reason: "in neither store's index"})
			return
		}
		m.alreadyInTarget(e, dst)
		return
	}

	kind := src.typ
	if !validEntryPath(kind, src.filepath) {
		m.refuse(e, kind, "its file path %q is not where a %s lives", src.filepath, kind)
		return
	}
	srcPath := filepath.Join(m.from.baseDir, src.filepath)
	data, readErr := os.ReadFile(srcPath)
	if os.IsNotExist(readErr) && dst != nil {
		// An earlier run moved the file and stopped before this store's rows were removed.
		if _, err := os.Stat(filepath.Join(m.to.baseDir, dst.filepath)); err != nil {
			m.refuse(e, kind, "its file is in neither store (%s here, %s there): %v", src.filepath, dst.filepath, err)
			return
		}
		if !m.dryRun {
			if err := m.from.removeEntryRows(e.ID); err != nil {
				m.refuse(e, kind, "it is in the target, but its rows here could not be removed: %v; run the move again", err)
				return
			}
		}
		m.moved(e, kind, dst.title, dst.filepath, "finished an earlier move")
		return
	}
	if readErr != nil {
		m.refuse(e, kind, "its file %s cannot be read: %v", src.filepath, readErr)
		return
	}
	fields, err := parseEntryFile(kind, data)
	if err != nil || fields.id != e.ID {
		m.refuse(e, kind, "its file %s does not parse as this %s (%v)", src.filepath, kind, err)
		return
	}
	if kind == "session" {
		if reason := m.relatedNotesStay(data); reason != "" {
			m.refuse(e, kind, "%s", reason)
			return
		}
	}

	// Where the file goes in the target, and what is there already.
	rel, note := src.filepath, ""
	writeFile, writeRows := true, true
	if dst != nil {
		// The target has this entry's rows (an earlier run, or a copy indexed there): its file must
		// be this file, or missing (then it is written).
		rel, writeRows, note = dst.filepath, false, "finished an earlier move"
		existing, err := os.ReadFile(filepath.Join(m.to.baseDir, rel))
		switch {
		case err == nil && bytes.Equal(existing, data):
			writeFile = false
		case err == nil:
			m.refuse(e, kind, "the target holds a different file for this id (%s there, %s here); compare them by hand", rel, src.filepath)
			return
		case !os.IsNotExist(err):
			m.refuse(e, kind, "the target's file %s cannot be read: %v", rel, err)
			return
		}
	} else {
		free, err := m.to.freeEntryPath(kind, src.filepath, e.ID)
		if err != nil {
			m.refuse(e, kind, "%v", err)
			return
		}
		if free != src.filepath {
			note = fmt.Sprintf("written as %s: %s is taken in the target", free, src.filepath)
		}
		rel = free
		if existing, err := os.ReadFile(filepath.Join(m.to.baseDir, rel)); err == nil {
			// freeEntryPath only returns a taken path that holds this entry's own file: an earlier
			// run wrote it and stopped before its rows.
			if !bytes.Equal(existing, data) {
				m.refuse(e, kind, "the target holds a different file for this id at %s; compare them by hand", rel)
				return
			}
			writeFile, note = false, "finished an earlier move"
		}
	}
	targetPath := filepath.Join(m.to.baseDir, rel)
	if err := validateFilepathWithinBase(m.to.baseDir, targetPath); err != nil {
		m.refuse(e, kind, "%v", err)
		return
	}

	if m.dryRun {
		m.moved(e, kind, fields.title, rel, note)
		if writeRows && !m.hasCopyableVectors(e.ID) {
			m.report.VectorsToEmbed++
		}
		return
	}

	// 1. The file and the rows into the target: the file through a temp file, renamed into place
	// once the rows are committed (the order every write of this package uses).
	var tmpPath string
	if writeFile {
		if tmpPath, err = writeTemp(targetPath, data); err != nil {
			m.refuse(e, kind, "write the target file: %v", err)
			return
		}
	}
	if writeRows {
		copied, err := m.copyRows(src, rel, fields)
		if err != nil {
			if tmpPath != "" {
				os.Remove(tmpPath)
			}
			m.refuse(e, kind, "write the target's index: %v", err)
			return
		}
		if !copied {
			m.report.VectorsToEmbed++
		}
	}
	if tmpPath != "" {
		if err := os.Rename(tmpPath, targetPath); err != nil {
			os.Remove(tmpPath)
			m.refuse(e, kind, "put the target file in place: %v; run the move again", err)
			return
		}
	}

	// 2. Out of this store: the file, then the rows. A stop between the two leaves rows without a
	// file here, which the next run finishes.
	if err := validateFilepathWithinBase(m.from.baseDir, srcPath); err != nil {
		m.refuse(e, kind, "%v", err)
		return
	}
	if err := os.Remove(srcPath); err != nil && !os.IsNotExist(err) {
		m.refuse(e, kind, "it is in the target, but its file here could not be removed: %v; run the move again", err)
		return
	}
	if err := m.from.removeEntryRows(e.ID); err != nil {
		m.refuse(e, kind, "it is in the target, but its rows here could not be removed: %v; run the move again", err)
		return
	}
	m.moved(e, kind, fields.title, rel, note)
}

// alreadyInTarget reports an entry that is in the target and not in this store, and removes a copy
// of its file that was left here without index rows (an add-only sync can bring one back) when it
// is the same file.
func (m *mover) alreadyInTarget(e MoveID, dst *indexRow) {
	m.report.AlreadyInTarget = append(m.report.AlreadyInTarget, MovedEntry{Line: e.Line, ID: e.ID, Kind: dst.typ, Title: dst.title, File: dst.filepath})
	if dst.typ == "note" {
		m.inTarget[e.ID] = true
	}
	if !validEntryPath(dst.typ, dst.filepath) {
		return
	}
	left := filepath.Join(m.from.baseDir, dst.filepath)
	here, err := os.ReadFile(left)
	if err != nil {
		return
	}
	if f, perr := parseEntryFile(dst.typ, here); perr != nil || f.id != e.ID {
		return
	}
	there, err := os.ReadFile(filepath.Join(m.to.baseDir, dst.filepath))
	if err != nil || !bytes.Equal(here, there) {
		m.warn("%s %s: a different copy of its file sits in this store at %s, outside the index; left as it is", dst.typ, e.ID, dst.filepath)
		return
	}
	if m.dryRun {
		m.warn("%s %s: a leftover copy of its file sits in this store at %s; it would be removed", dst.typ, e.ID, dst.filepath)
		return
	}
	if err := os.Remove(left); err != nil {
		m.warn("%s %s: a leftover copy of its file sits in this store at %s and could not be removed: %v", dst.typ, e.ID, dst.filepath, err)
		return
	}
	m.warn("%s %s: removed a leftover copy of its file from this store (%s)", dst.typ, e.ID, dst.filepath)
}

// relatedNotesStay names the first related note that the session lists and the target would not
// hold, or returns "". A session file names a related note by its id or by the first 8 characters of
// it, so both forms are matched.
func (m *mover) relatedNotesStay(data []byte) string {
	session, err := ParseSessionMarkdown(data)
	if err != nil {
		return fmt.Sprintf("its file does not parse: %v", err)
	}
	for _, rn := range session.RelatedNotes {
		if rn.ID == "" || m.movedNote(rn.ID) || hasNote(m.to, rn.ID) {
			continue
		}
		if hasNote(m.from, rn.ID) {
			return fmt.Sprintf("it lists the related note %s (%q), which stays in this store; list that note's id too, or take the link out first", rn.ID, rn.Title)
		}
		m.warn("session %s lists the related note %s (%q), which is in neither store", session.ID, rn.ID, rn.Title)
	}
	return ""
}

// movedNote reports whether a note whose id is, or starts with, ref is in the target after this run.
func (m *mover) movedNote(ref string) bool {
	for id := range m.inTarget {
		if strings.HasPrefix(id, ref) {
			return true
		}
	}
	return false
}

// hasNote reports whether the store has a note whose id is, or starts with, ref.
func hasNote(fs *FileStore, ref string) bool {
	var one int
	return fs.db.QueryRow(`SELECT 1 FROM entries WHERE type = 'note' AND substr(id, 1, ?) = ? LIMIT 1`, len(ref), ref).Scan(&one) == nil
}

// copyRows writes the entry's rows into the target in one transaction, replacing any rows the
// target has for this id: the entries row as it is here (with the target's file path), the search
// row as it is here (rebuilt from the file when this store has none or several), the access log,
// and the vectors other than TF-IDF ones. It reports whether any vector was copied.
func (m *mover) copyRows(src *indexRow, rel string, fields *entryFields) (bool, error) {
	title, content, tags := fields.title, fields.ftsContent, strings.Join(fields.tags, " ")
	var n int
	if err := m.from.db.QueryRow(`SELECT COUNT(*) FROM memory_fts WHERE id = ?`, src.id).Scan(&n); err != nil {
		return false, err
	}
	if n == 1 {
		if err := m.from.db.QueryRow(`SELECT title, content, tags FROM memory_fts WHERE id = ?`, src.id).Scan(&title, &content, &tags); err != nil {
			return false, err
		}
	} else {
		m.warn("%s %s: this store has %d search rows for it; the target's was built from its file", src.typ, src.id, n)
	}

	type accessRow struct{ at, typ string }
	var accesses []accessRow
	rows, err := m.from.db.Query(`SELECT accessed_at, access_type FROM access_log WHERE entry_id = ? ORDER BY rowid`, src.id)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var a accessRow
		if err := rows.Scan(&a.at, &a.typ); err != nil {
			rows.Close()
			return false, err
		}
		accesses = append(accesses, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	type vectorRow struct {
		chunk          int
		backend, model string
		dim            int
		vector         []byte
		createdAt      string
	}
	var vecs []vectorRow
	if m.vectors {
		rows, err := m.from.db.Query(`SELECT chunk, backend, model, dim, vector, created_at FROM vectors
			WHERE doc_id = ? AND backend != 'tfidf' ORDER BY backend, model, chunk`, src.id)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var v vectorRow
			if err := rows.Scan(&v.chunk, &v.backend, &v.model, &v.dim, &v.vector, &v.createdAt); err != nil {
				rows.Close()
				return false, err
			}
			vecs = append(vecs, v)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, err
		}
	}

	// Asked before Begin: the store has one connection, which the transaction then holds.
	targetVectors := hasVectorsByDoc(m.to.db)
	tx, err := m.to.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := deleteEntryRowsTx(tx, src.id, targetVectors); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO entries (id, type, title, category, branch, project, session_id, date_str, tags, filepath, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, src.id, src.typ, src.title, src.category, src.branch, src.project,
		src.sessionID, src.dateStr, src.tags, rel, src.created, src.updated); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, ?)`, src.id, title, content, tags); err != nil {
		return false, err
	}
	for _, a := range accesses {
		if _, err := tx.Exec(`INSERT INTO access_log (entry_id, accessed_at, access_type) VALUES (?, ?, ?)`, src.id, a.at, a.typ); err != nil {
			return false, err
		}
	}
	for _, v := range vecs {
		if _, err := tx.Exec(`INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			src.id, v.chunk, v.backend, v.model, v.dim, v.vector, v.createdAt); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return len(vecs) > 0, nil
}

// hasCopyableVectors reports whether this store has vectors for the entry that a move would copy.
func (m *mover) hasCopyableVectors(id string) bool {
	if !m.vectors {
		return false
	}
	var n int
	if err := m.from.db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = ? AND backend != 'tfidf'`, id).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// removeEntryRows removes an entry's rows from this store in one transaction: the entries row, the
// search rows, the access log and the vectors.
func (fs *FileStore) removeEntryRows(id string) error {
	vectors := hasVectorsByDoc(fs.db) // before Begin: the transaction holds the store's one connection
	tx, err := fs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteEntryRowsTx(tx, id, vectors); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteEntryRowsTx(tx *sql.Tx, id string, vectors bool) error {
	stmts := []string{
		`DELETE FROM entries WHERE id = ?`,
		`DELETE FROM memory_fts WHERE id = ?`,
		`DELETE FROM access_log WHERE entry_id = ?`,
	}
	if vectors {
		stmts = append(stmts, `DELETE FROM vectors WHERE doc_id = ?`)
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt, id); err != nil {
			return err
		}
	}
	return nil
}

// freeEntryPath returns where an entry's file goes in this store: rel when no other entry's row
// names it and no other file sits there (a file holding this entry itself counts as free), else the
// first such "<name>-N.md" next to it.
func (fs *FileStore) freeEntryPath(kind, rel, id string) (string, error) {
	dir := filepath.Dir(rel)
	base := strings.TrimSuffix(filepath.Base(rel), ".md")
	for i := 1; i <= 1000; i++ {
		name := base + ".md"
		if i > 1 {
			name = fmt.Sprintf("%s-%d.md", base, i)
		}
		candidate := filepath.Join(dir, name)
		var owner string
		err := fs.db.QueryRow(`SELECT id FROM entries WHERE filepath = ?`, candidate).Scan(&owner)
		if err != nil && err != sql.ErrNoRows {
			return "", fmt.Errorf("check who owns %s in the target: %w", candidate, err)
		}
		if err == nil && owner != id {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(fs.baseDir, candidate))
		if readErr == nil {
			if f, perr := parseEntryFile(kind, data); perr != nil || f.id != id {
				continue
			}
		} else if !os.IsNotExist(readErr) {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("no free file name for %s in the target", rel)
}

// validEntryPath reports whether rel is where a file of this kind lives: notes/<category>/<name>.md
// or sessions/<name>.md, inside the store.
func validEntryPath(kind, rel string) bool {
	if filepath.IsAbs(rel) || strings.Contains(filepath.ToSlash(rel), "..") || !strings.HasSuffix(rel, ".md") {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch kind {
	case "note":
		return len(parts) == 3 && parts[0] == "notes"
	case "session":
		return len(parts) == 2 && parts[0] == "sessions"
	}
	return false
}

// writeTemp writes data to a new temp file next to path and returns its name; the caller renames it
// over path. The name starts with a dot and does not end in .md, so a reindex never picks it up.
func writeTemp(path string, data []byte) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "."+strings.TrimSuffix(filepath.Base(path), ".md")+".*.tmp")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// lockBoth takes the write locks of both stores, always in the same order (by resolved directory),
// so two moves running in opposite directions cannot deadlock.
func lockBoth(a, b *FileStore) (func(), error) {
	first, second := a, b
	if resolvedDir(b.baseDir) < resolvedDir(a.baseDir) {
		first, second = b, a
	}
	unlockFirst, err := first.lockStore()
	if err != nil {
		return nil, err
	}
	unlockSecond, err := second.lockStore()
	if err != nil {
		unlockFirst()
		return nil, err
	}
	return func() {
		unlockSecond()
		unlockFirst()
	}, nil
}

// sameStoreDir reports whether two store directories are one, also when reached by a symlink.
func sameStoreDir(a, b string) bool {
	return resolvedDir(a) == resolvedDir(b)
}

// resolvedDir is dir as an absolute path with symlinks resolved, as far as they resolve.
func resolvedDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return filepath.Clean(dir)
}

// hasVectorsByDoc reports whether the store's index has a vectors table keyed by doc_id (every layout
// since v22; a v21 table has none and is migrated on its next open with semantic search).
func hasVectorsByDoc(db *sql.DB) bool {
	var n int
	return db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('vectors') WHERE name = 'doc_id'`).Scan(&n) == nil && n == 1
}

// hasChunkedVectors reports whether the store's vectors table has the current layout, with a chunk
// column, so its rows can be copied as they are.
func hasChunkedVectors(db *sql.DB) bool {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('vectors')`)
	if err != nil {
		return false
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			cols[name] = true
		}
	}
	return cols["doc_id"] && cols["chunk"] && cols["backend"] && cols["model"] && cols["dim"] && cols["vector"] && cols["created_at"]
}
