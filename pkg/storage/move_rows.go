package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

// The index rows of a move: what the target's rows are built from, the vectors that go along, and
// the related-note check that keeps a session with its notes.

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

// embeddingTuple is an embedding backend, model and vector size.
type embeddingTuple struct {
	backend, model string
	dim            int
}

// targetEmbedding is what the target's semantic search embeds with, from its settings (nothing is
// contacted and no key is read), or nil when its semantic search is off or its backend is unknown.
func targetEmbedding(to *FileStore) *embeddingTuple {
	cfg, err := config.Load(to.baseDir)
	if err != nil || !cfg.GetBool("features.semantic_search") {
		return nil
	}
	bc, err := loadBackendConfig(to.baseDir)
	if err != nil {
		return nil
	}
	bc.APIKey = ""
	emb, err := vectors.BuildEmbedder(bc)
	if err != nil {
		return nil
	}
	return &embeddingTuple{backend: emb.Name(), model: emb.Model(), dim: emb.Dimensions()}
}

// serves reports whether a vector row of this backend, model and size serves the target's semantic
// search; with it off, any vector other than a TF-IDF one counts.
func (m *mover) serves(backend, model string, dim int) bool {
	if m.active == nil {
		return backend != "tfidf"
	}
	if backend != m.active.backend || model != m.active.model {
		return false
	}
	return backend == "tfidf" || dim == m.active.dim // TF-IDF sizes follow the corpus
}

type vectorRow struct {
	chunk          int
	backend, model string
	dim            int
	vector         []byte
	createdAt      string
}

// rowPlan is what writing an entry's rows into the target does with its vectors.
type rowPlan struct {
	vectors    []vectorRow // this store's vectors that go along
	keepTarget bool        // the target's vectors for it stay: its search row already matches the file
	embed      bool        // afterwards the target has no vectors for it that its semantic search uses
}

// planRows decides which vectors go along with an entry whose file is f. This store's vectors go
// along only when its search row matches the file (they were embedded from it), are not TF-IDF ones
// (built against this store's corpus), and are not of the target's backend and model at another
// size; the target's own vectors for it stay only when its search row matches the file too.
func (m *mover) planRows(src *indexRow, f *parsedEntry) (*rowPlan, error) {
	plan := &rowPlan{}
	match, err := searchRowMatches(m.to, f)
	if err != nil {
		return nil, err
	}
	plan.keepTarget = match
	if src != nil && m.vectors {
		if match, err := searchRowMatches(m.from, f); err != nil {
			return nil, err
		} else if match {
			rows, err := m.from.db.Query(`SELECT chunk, backend, model, dim, vector, created_at FROM vectors
				WHERE doc_id = ? AND backend != 'tfidf' ORDER BY backend, model, chunk`, src.id)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var v vectorRow
				if err := rows.Scan(&v.chunk, &v.backend, &v.model, &v.dim, &v.vector, &v.createdAt); err != nil {
					rows.Close()
					return nil, err
				}
				if m.active != nil && v.backend == m.active.backend && v.model == m.active.model && v.dim != m.active.dim {
					continue
				}
				plan.vectors = append(plan.vectors, v)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}

	plan.embed = true
	for _, v := range plan.vectors {
		if m.serves(v.backend, v.model, v.dim) {
			plan.embed = false
		}
	}
	if plan.keepTarget && plan.embed && hasVectorsByDoc(m.to.db) {
		rows, err := m.to.db.Query(`SELECT backend, model, dim FROM vectors WHERE doc_id = ?`, f.id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var backend, model string
			var dim int
			if err := rows.Scan(&backend, &model, &dim); err != nil {
				rows.Close()
				return nil, err
			}
			if m.serves(backend, model, dim) {
				plan.embed = false
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// searchRowMatches reports whether the store has exactly one search row for the entry, with the
// file's title and content.
func searchRowMatches(fs *FileStore, f *parsedEntry) (bool, error) {
	rows, err := fs.db.Query(`SELECT title, content FROM memory_fts WHERE id = ?`, f.id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	n, match := 0, false
	for rows.Next() {
		var title, content string
		if err := rows.Scan(&title, &content); err != nil {
			return false, err
		}
		n++
		match = title == f.title && content == f.ftsContent
	}
	return n == 1 && match, rows.Err()
}

// writeTargetRows writes the entry's rows into the target in one transaction, built from its file
// as a reindex builds them, replacing the target's entries and search rows for it; its vectors are
// replaced by those of the plan unless the plan keeps them. The target's access log is left as it
// is, and nothing of this store's is copied.
func (m *mover) writeTargetRows(f *parsedEntry, rel string, plan *rowPlan) error {
	targetVectors := hasVectorsByDoc(m.to.db) // before Begin: the transaction holds the store's one connection
	tx, err := m.to.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{`DELETE FROM entries WHERE id = ?`, `DELETE FROM memory_fts WHERE id = ?`}
	if targetVectors && !plan.keepTarget {
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
	for _, v := range plan.vectors {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			f.id, v.chunk, v.backend, v.model, v.dim, v.vector, v.createdAt); err != nil {
			return err
		}
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

// relatedNotesStay checks the notes a session file lists under its Related Notes sections, read
// from the file's text (the parsed session keeps only the last such section, and drops a line without
// an id). It returns why the session must stay, or "" when every listed note is in the target, moves
// there in this run, or is in neither store (a dead link in both, so only a warning).
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
		ids := map[string]bool{}
		for _, id := range append(here, there...) {
			ids[id] = true
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
		case contains(there, id):
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
