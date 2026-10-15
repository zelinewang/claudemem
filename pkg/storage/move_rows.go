package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The index rows of a move: what the target's rows are built from, and the related-note check that
// keeps a session with its notes.

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

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// parsedEntry is an entry file read for a move: its id and its index rows, as Reindex builds them.
type parsedEntry struct {
	id, typ, title, category, branch, project, sessionID, dateStr, tags string
	created, updated                                                    interface{} // Unix seconds for a note, an ISO time for a session
	ftsContent                                                          string
}

func parseEntry(kind string, data []byte) (*parsedEntry, error) {
	switch kind {
	case "note":
		n, err := ParseNoteMarkdown(data)
		if err != nil {
			return nil, err
		}
		return &parsedEntry{id: n.ID, typ: "note", title: n.Title, category: n.Category, sessionID: noteSessionID(n),
			tags: strings.Join(n.Tags, " "), created: n.Created.Unix(), updated: n.Updated.Unix(), ftsContent: n.Content}, nil
	case "session":
		s, err := ParseSessionMarkdown(data)
		if err != nil {
			return nil, err
		}
		created := s.Created.Format("2006-01-02T15:04:05Z")
		return &parsedEntry{id: s.ID, typ: "session", title: s.Title, branch: s.Branch, project: s.Project, sessionID: s.SessionID,
			dateStr: s.Date, tags: strings.Join(s.Tags, " "), created: created, updated: created, ftsContent: s.GetSearchableContent()}, nil
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

// writeTargetRows writes the entry's rows into the target in one transaction, built from its file
// as a reindex builds them, replacing the target's entries and search rows for it. No vectors go
// along, and the target's own vectors for it are removed: nothing shows that any vector was
// embedded from this file (a reindex, a pull or a tag edit rebuilds the search row and keeps the
// vectors), and stale ones would let a search find words the file no longer has. The target embeds
// the entry itself (`reindex --vectors --missing`). The target's access log is left as it is, and
// nothing of this store's is copied.
func (m *mover) writeTargetRows(f *parsedEntry, rel string) error {
	targetVectors := hasVectorsByDoc(m.to.db) // before Begin: the transaction holds the store's one connection
	tx, err := m.to.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{`DELETE FROM entries WHERE id = ?`, `DELETE FROM memory_fts WHERE id = ?`}
	if targetVectors {
		stmts = append(stmts, `DELETE FROM vectors WHERE doc_id = ?`)
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt, f.id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO entries (id, type, title, category, branch, project, session_id, date_str, tags, filepath, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, f.id, f.typ, f.title, f.category, f.branch, f.project,
		f.sessionID, f.dateStr, f.tags, rel, f.created, f.updated); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, ?)`, f.id, f.title, f.ftsContent, f.tags); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.report.VectorsToEmbed++ // in the target now, without vectors
	return nil
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
	return tx.Commit()
}

// hasVectorsFor reports whether the store's index holds a vector row for the entry, of any backend.
func (fs *FileStore) hasVectorsFor(id string) bool {
	if !hasVectorsByDoc(fs.db) {
		return false
	}
	var n int
	return fs.db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, id).Scan(&n) == nil && n > 0
}

// hasVectorsByDoc reports whether the store's index has a vectors table keyed by doc_id (every layout
// since v22; a v21 table has none and is migrated on its next open with semantic search).
func hasVectorsByDoc(db *sql.DB) bool {
	var n int
	return db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('vectors') WHERE name = 'doc_id'`).Scan(&n) == nil && n == 1
}

// relatedNotesStay checks the notes a session file lists under its Related Notes sections, read
// from the file's text (the parsed session keeps only the last such section, and drops a line without
// an id). Every id in backticks on a line counts, not only the first. Then it checks every note id
// anywhere else in the file (fileNoteRefs): a hand edit can name a note under a heading this does not
// read as Related Notes, or without backticks. It returns why the session must stay, or "" when every
// note it names is in the target, moves there in this run, or is in neither store (for a listed note,
// a dead link in both, so only a warning). A note counts as in a store when its index lists it or one
// of its note files holds it (the index can lag behind the files), and a note this run refused stays
// here.
func (m *mover) relatedNotesStay(sessionID string, data []byte) string {
	items, bad := relatedNoteItems(data)
	if bad != "" {
		return fmt.Sprintf("its Related Notes section has a line that names no note as claudemem writes it (- `id` — \"title\"): %q; fix or remove the line first", bad)
	}
	checked := map[string]bool{}
	for _, item := range items {
		rn := parseRelatedNoteLine(item)
		if rn.ID == "" {
			return fmt.Sprintf("its Related Notes line %q names no note id in backticks; fix or remove the line first", item)
		}
		if reason := m.relatedNoteStays(sessionID, item, rn.ID, rn.Title, true); reason != "" {
			return reason
		}
		checked[rn.ID] = true
		// further ids on the line: another link joined onto it, or a nested item
		spans := backtickSpans(item)
		for i := 1; i < len(spans); i++ {
			if ref := spans[i]; len(ref) >= 8 && looksLikeNoteID(ref) {
				if reason := m.relatedNoteStays(sessionID, item, ref, "", false); reason != "" {
					return reason
				}
				checked[ref] = true
			}
		}
	}
	for _, ref := range fileNoteRefs(data) {
		if checked[ref.id] {
			continue
		}
		checked[ref.id] = true
		if reason := m.namedNoteStays(ref); reason != "" {
			return reason
		}
	}
	return ""
}

// relatedNoteStays checks one note ref of a Related Notes line, the line's first ref (which must name
// a note) or a further id on it. It returns why the session must stay, or "".
func (m *mover) relatedNoteStays(sessionID, item, ref, title string, first bool) string {
	name := fmt.Sprintf("%s (%q)", ref, title)
	if !first {
		name = fmt.Sprintf("%s (on the line %q)", ref, item)
	}
	if len(ref) < 8 {
		return fmt.Sprintf("it lists the related note %q, too short an id to name one note; write at least its first 8 characters", ref)
	}
	ids, there, filesThere, err := m.notesNamed(ref)
	if err != nil {
		return fmt.Sprintf("check its related note %s: %v", ref, err)
	}
	switch {
	case len(ids) == 0 && !looksLikeNoteID(ref):
		return fmt.Sprintf("its Related Notes line %q names %q in backticks, which is no note id; fix or remove the line first", item, ref)
	case len(ids) == 0:
		m.warn("session %s lists the related note %s, which is in neither store", sessionID, name)
		return ""
	case len(ids) > 1:
		return fmt.Sprintf("it lists the related note %s, which matches several notes; write its full id", name)
	}
	return m.noteStays("lists the related note "+name, "take the link out", ids, there, filesThere)
}

// namedNoteStays checks a note id found elsewhere in a session file. One that names no note is not a
// note ref; one that names several, or a note that stays here, keeps the session.
func (m *mover) namedNoteStays(ref noteRef) string {
	ids, there, filesThere, err := m.notesNamed(ref.id)
	if err != nil {
		return fmt.Sprintf("check the note %s it names: %v", ref.id, err)
	}
	name := fmt.Sprintf("%s (on the line %q)", ref.id, ref.line)
	switch {
	case len(ids) == 0:
		return ""
	case len(ids) > 1:
		return fmt.Sprintf("it names the note %s, which matches several notes; write its full id", name)
	}
	return m.noteStays("names the note "+name, "take that mention out", ids, there, filesThere)
}

// notesNamed returns the notes whose id starts with ref, in either store's index, note files or this
// run's refusals, and of those the ones the target's index lists and the target's note files hold.
func (m *mover) notesNamed(ref string) (ids map[string]bool, there, filesThere []string, err error) {
	here, err := notesWithPrefix(m.from, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	if there, err = notesWithPrefix(m.to, ref); err != nil {
		return nil, nil, nil, err
	}
	filesThere = idsWithPrefix(m.toNoteFiles, ref)
	ids = map[string]bool{}
	for _, list := range [][]string{here, there, idsWithPrefix(m.fromNoteFiles, ref), filesThere} {
		for _, id := range list {
			ids[id] = true
		}
	}
	for id := range m.refusedNotes {
		if strings.HasPrefix(id, ref) {
			ids[id] = true
		}
	}
	return ids, there, filesThere, nil
}

// noteStays returns why a session that names a note (one of ids, the only one) must stay: the note
// stays in this store. what says how the session names it, fix how to take it out.
func (m *mover) noteStays(what, fix string, ids map[string]bool, there, filesThere []string) string {
	var id string
	for id = range ids {
	}
	switch {
	case m.refusedNotes[id]:
		return fmt.Sprintf("it %s, which stays in this store: its move was refused", what)
	case m.inTarget[id]:
	case contains(there, id), contains(filesThere, id):
	default:
		return fmt.Sprintf("it %s, which stays in this store; list that note's id too, or %s first", what, fix)
	}
	return ""
}

// noteRef is a note id found in a session file, and the line it is on.
type noteRef struct{ id, line string }

// uuidPattern is a lowercase UUID, the form of claudemem's ids.
var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// fileNoteRefs returns the note ids a session file names anywhere, frontmatter included, in order and
// once each: every lowercase UUID, and every id or id prefix of 8 or more characters in backticks.
func fileNoteRefs(data []byte) []noteRef {
	var refs []noteRef
	seen := map[string]bool{}
	add := func(id, line string) {
		if !seen[id] {
			seen[id] = true
			refs = append(refs, noteRef{id: id, line: strings.TrimSpace(line)})
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		for _, id := range uuidPattern.FindAllString(line, -1) {
			add(id, line)
		}
		for _, span := range backtickSpans(line) {
			if span = strings.TrimSpace(span); len(span) >= 8 && looksLikeNoteID(span) {
				add(span, line)
			}
		}
	}
	return refs
}

// backtickSpans returns the text between each pair of backticks on a line, in order.
func backtickSpans(line string) []string {
	var spans []string
	for {
		open := strings.IndexByte(line, '`')
		if open < 0 {
			return spans
		}
		end := strings.IndexByte(line[open+1:], '`')
		if end < 0 {
			return spans
		}
		spans = append(spans, line[open+1:open+1+end])
		line = line[open+1+end+1:]
	}
}

// relatedNoteItems returns the list items of every Related Notes section of a session file, or the
// first line there that is not part of one. A Related Notes section is one under any Markdown heading
// with that title, at any level, in any case and with a closing colon: a hand edit can leave the notes
// listed under a heading the session parser no longer reads. The section ends at the next heading of
// its level or above; a deeper heading inside it is returned as a line that is not part of it.
func relatedNoteItems(data []byte) (items []string, bad string) {
	_, rest, _, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, ""
	}
	sectionLevel := 0 // of the Related Notes heading above; 0 outside such a section
	for _, line := range strings.Split(strings.TrimPrefix(rest, "\n---\n"), "\n") {
		if level, title := markdownHeading(line); level > 0 {
			switch {
			case isRelatedNotesTitle(title):
				sectionLevel = level
			case sectionLevel > 0 && level > sectionLevel:
				return nil, line
			default:
				sectionLevel = 0
			}
			continue
		}
		if sectionLevel == 0 {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			items = append(items, trimmed[2:])
		case strings.HasPrefix(line, "  ") && len(items) > 0:
			items[len(items)-1] += " " + trimmed
		default:
			return nil, line
		}
	}
	return items, ""
}

// markdownHeading returns the level and text of a Markdown ATX heading: up to three spaces, one to
// six #, then a space or the end of the line, with a closing run of # dropped. It returns 0 for any
// other line.
func markdownHeading(line string) (int, string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0, ""
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, ""
	}
	text := trimmed[level:]
	if text != "" && !strings.ContainsAny(text[:1], " \t\r") {
		return 0, ""
	}
	text = strings.TrimSpace(text)
	if open := strings.TrimRight(text, "#"); open != text && (open == "" || strings.ContainsAny(open[len(open)-1:], " \t")) {
		text = strings.TrimSpace(open)
	}
	return level, text
}

// isRelatedNotesTitle reports whether a heading's text is Related Notes, in any case and spacing,
// with or without a closing colon.
func isRelatedNotesTitle(title string) bool {
	title = strings.TrimSuffix(strings.TrimSpace(title), ":")
	return strings.EqualFold(strings.Join(strings.Fields(title), " "), "related notes")
}

// looksLikeNoteID reports whether ref can be the start of a note id as claudemem makes them: a
// lowercase UUID (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx).
func looksLikeNoteID(ref string) bool {
	const form = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
	if ref == "" || len(ref) > len(form) {
		return false
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		switch {
		case form[i] == '-':
			if c != '-' {
				return false
			}
		case !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f'):
			return false
		}
	}
	return true
}

// notesWithPrefix returns the ids of up to two notes whose id starts with ref.
func notesWithPrefix(fs *FileStore, ref string) ([]string, error) {
	rows, err := fs.db.Query(`SELECT id FROM entries WHERE type = 'note' AND substr(id, 1, ?) = ? LIMIT 2`, len(ref), ref)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
