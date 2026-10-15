package storage

import (
	"database/sql"
	"errors"
	"fmt"
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
	return tx.Commit()
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

// hasVectorsByDoc reports whether the store's index has a vectors table keyed by doc_id (every layout
// since v22; a v21 table has none and is migrated on its next open with semantic search).
func hasVectorsByDoc(db *sql.DB) bool {
	var n int
	return db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('vectors') WHERE name = 'doc_id'`).Scan(&n) == nil && n == 1
}

// relatedNotesStay checks the notes a session file lists under its Related Notes sections, read
// from the file's text (the parsed session keeps only the last such section, and drops a line without
// an id). It returns why the session must stay, or "" when every listed note is in the target, moves
// there in this run, or is in neither store (a dead link in both, so only a warning). A note counts as
// in a store when its index lists it or one of its note files holds it (the index can lag behind the
// files), and a note this run refused stays here.
func (m *mover) relatedNotesStay(sessionID string, data []byte) string {
	items, bad := relatedNoteItems(data)
	if bad != "" {
		return fmt.Sprintf("its Related Notes section has a line that names no note as claudemem writes it (- `id` — \"title\"): %q; fix or remove the line first", bad)
	}
	for _, item := range items {
		rn := parseRelatedNoteLine(item)
		if rn.ID == "" {
			return fmt.Sprintf("its Related Notes line %q names no note id in backticks; fix or remove the line first", item)
		}
		if len(rn.ID) < 8 {
			return fmt.Sprintf("it lists the related note %q, too short an id to name one note; write at least its first 8 characters", rn.ID)
		}
		here, err := notesWithPrefix(m.from, rn.ID)
		if err != nil {
			return fmt.Sprintf("check its related note %s: %v", rn.ID, err)
		}
		there, err := notesWithPrefix(m.to, rn.ID)
		if err != nil {
			return fmt.Sprintf("check its related note %s: %v", rn.ID, err)
		}
		filesThere := idsWithPrefix(m.toNoteFiles, rn.ID)
		ids := map[string]bool{}
		for _, list := range [][]string{here, there, idsWithPrefix(m.fromNoteFiles, rn.ID), filesThere} {
			for _, id := range list {
				ids[id] = true
			}
		}
		for id := range m.refusedNotes {
			if strings.HasPrefix(id, rn.ID) {
				ids[id] = true
			}
		}
		switch {
		case len(ids) == 0:
			m.warn("session %s lists the related note %s (%q), which is in neither store", sessionID, rn.ID, rn.Title)
			continue
		case len(ids) > 1:
			return fmt.Sprintf("it lists the related note %s (%q), which matches several notes; write its full id", rn.ID, rn.Title)
		}
		var id string
		for id = range ids {
		}
		switch {
		case m.refusedNotes[id]:
			return fmt.Sprintf("it lists the related note %s (%q), which stays in this store: its move was refused", rn.ID, rn.Title)
		case m.inTarget[id]:
		case contains(there, id), contains(filesThere, id):
		default:
			return fmt.Sprintf("it lists the related note %s (%q), which stays in this store; list that note's id too, or take the link out first", rn.ID, rn.Title)
		}
	}
	return ""
}

// relatedNoteItems returns the list items of every Related Notes section of a session file, or the
// first line there that is not part of one.
func relatedNoteItems(data []byte) (items []string, bad string) {
	_, rest, _, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, ""
	}
	inSection := false
	for _, line := range strings.Split(strings.TrimPrefix(rest, "\n---\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			inSection = strings.ToLower(strings.TrimPrefix(line, "## ")) == "related notes"
			continue
		}
		if !inSection {
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
