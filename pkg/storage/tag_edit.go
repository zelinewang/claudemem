package storage

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// TagEditResult describes one tag edit: the entry's tags before and after, and whether the edit
// changed anything (an edit that adds tags already present and removes absent ones does not).
// IndexRefreshed reports that the file already had the requested tags but the index rows did not
// match it, and were rebuilt from the file.
type TagEditResult struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Title          string   `json:"title"`
	Before         []string `json:"before"`
	After          []string `json:"after"`
	Changed        bool     `json:"changed"`
	IndexRefreshed bool     `json:"index_refreshed,omitempty"`
}

// EditTags adds and removes tags on the note or session with exactly this id (no prefix match).
//
// Only the tags change. The file's frontmatter `tags` list is rewritten in place and every other
// frontmatter value is kept (see rewriteFrontmatterTags); the entries row and the FTS row take the new
// tags in one transaction, built as a reindex would build them. The body, the timestamps and the
// vectors are not touched: retagging thousands of entries must not make every note look updated today
// (context inject orders notes by `updated`) nor re-embed the store. Under the store lock, so a concurrent writer cannot interleave.
//
// An edit that changes nothing writes nothing, so running it again is harmless. dryRun computes the
// result without writing. An unknown id fails with ErrEntryNotFound, an id of the other kind with
// ErrWrongKind.
func (fs *FileStore) EditTags(kind, id string, add, remove []string, dryRun bool) (*TagEditResult, error) {
	return fs.editTagsLocking(kind, id, add, remove, dryRun, 0)
}

// editTagsLocking validates the edit, takes the store lock unless dryRun, and applies it.
// ftsRowID is the rowid of the entry's memory_fts row when the caller knows it, else 0 (see
// deleteFTSRow).
func (fs *FileStore) editTagsLocking(kind, id string, add, remove []string, dryRun bool, ftsRowID int64) (*TagEditResult, error) {
	if kind != "note" && kind != "session" {
		return nil, fmt.Errorf("unknown kind %q (want note or session)", kind)
	}
	if err := ValidateTagEdit(add, remove); err != nil {
		return nil, err
	}
	if dryRun {
		return fs.editTags(kind, id, add, remove, true, 0)
	}
	unlock, err := fs.lockStore()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return fs.editTags(kind, id, add, remove, false, ftsRowID)
}

func (fs *FileStore) editTags(kind, id string, add, remove []string, dryRun bool, ftsRowID int64) (*TagEditResult, error) {
	var entryType, rel string
	err := fs.db.QueryRow(`SELECT type, filepath FROM entries WHERE id = ?`, id).Scan(&entryType, &rel)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %s %s", ErrEntryNotFound, kind, id)
	}
	if err != nil {
		return nil, fmt.Errorf("look up %s: %w", id, err)
	}
	if entryType != kind {
		return nil, fmt.Errorf("%w: %s is a %s, not a %s", ErrWrongKind, id, entryType, kind)
	}

	path := filepath.Join(fs.baseDir, rel)
	if err := validateFilepathWithinBase(fs.baseDir, path); err != nil {
		return nil, fmt.Errorf("path validation failed: %w", err)
	}
	oldData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	old, err := parseEntryFile(kind, oldData)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if old.id != id {
		return nil, fmt.Errorf("%s carries id %q, not %s: the index is stale, run `claudemem reindex`", rel, old.id, id)
	}

	newData, changed, err := rewriteFrontmatterTags(oldData, add, remove)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	res := &TagEditResult{ID: id, Kind: kind, Title: old.title, Before: old.tags, After: old.tags}
	if !changed {
		// The file is as requested. An index row that disagrees with it (a file edited by hand or
		// brought by a sync, not yet reindexed) is rebuilt from the file, so search sees the same tags.
		stale, err := fs.tagRowsStale(id, old.tags, ftsRowID)
		if err != nil || !stale {
			return res, err
		}
		res.IndexRefreshed = true
		if dryRun {
			return res, nil
		}
		return res, fs.writeTagRows(id, old, ftsRowID)
	}
	if err := verifyTagRewrite(oldData, newData); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	updated, err := parseEntryFile(kind, newData)
	if err != nil || updated.id != id {
		return nil, fmt.Errorf("%s: the rewritten file does not parse back as %s (%v)", rel, id, err)
	}
	if err := validateTags(updated.tags); err != nil {
		return nil, err
	}
	res.After = updated.tags
	res.Changed = true
	if dryRun {
		return res, nil
	}

	// The same order as updateNoteLocked: the new content goes to a unique temp file, the index
	// commits, then the temp file replaces the original in one rename.
	tmpFile, err := os.CreateTemp(filepath.Dir(path), "."+strings.TrimSuffix(filepath.Base(path), ".md")+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(newData); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := fs.writeTagRows(id, updated, ftsRowID); err != nil {
		os.Remove(tmpPath)
		return nil, err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// The index is committed but the rename failed: copy the temp content, never an empty read
		// (the same recovery as updateNoteLocked).
		data, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return nil, fmt.Errorf("%s %s: index updated but the file could not be replaced (rename: %v; temp: %w)", kind, id, err, readErr)
		}
		if werr := os.WriteFile(path, data, 0600); werr != nil {
			return nil, fmt.Errorf("%s %s: index updated but the file could not be replaced: %w (temp file kept at %s)", kind, id, werr, tmpPath)
		}
		os.Remove(tmpPath)
	}
	return res, nil
}

// writeTagRows sets the entry's tags in its entries row and rebuilds its memory_fts row from f, in one
// transaction, the way Reindex builds them.
func (fs *FileStore) writeTagRows(id string, f *entryFields, ftsRowID int64) error {
	tx, err := fs.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	joined := strings.Join(f.tags, " ")
	if _, err := tx.Exec(`UPDATE entries SET tags = ? WHERE id = ?`, joined, id); err != nil {
		return fmt.Errorf("failed to update entry tags: %w", err)
	}
	if err := deleteFTSRow(tx, id, ftsRowID); err != nil {
		return fmt.Errorf("failed to delete from FTS: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, ?)`,
		id, f.title, f.ftsContent, joined); err != nil {
		return fmt.Errorf("failed to insert into FTS: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	return nil
}

// tagRowsStale reports whether the entry's entries row or memory_fts row carries other tags than
// tags, or the entry has not exactly one memory_fts row. With a known rowid the FTS row is read
// directly; otherwise by id, which scans the FTS table (fine for one edit, avoided in bulk runs).
func (fs *FileStore) tagRowsStale(id string, tags []string, ftsRowID int64) (bool, error) {
	joined := strings.Join(tags, " ")
	var indexed string
	if err := fs.db.QueryRow(`SELECT tags FROM entries WHERE id = ?`, id).Scan(&indexed); err != nil {
		return false, fmt.Errorf("read the index row of %s: %w", id, err)
	}
	if indexed != joined {
		return true, nil
	}
	if ftsRowID > 0 {
		var ftsTags string
		err := fs.db.QueryRow(`SELECT tags FROM memory_fts WHERE rowid = ? AND id = ?`, ftsRowID, id).Scan(&ftsTags)
		if err == nil {
			return ftsTags != joined, nil
		}
		if err != sql.ErrNoRows {
			return false, fmt.Errorf("read the FTS row of %s: %w", id, err)
		}
	}
	var rows int
	var minTags, maxTags sql.NullString
	if err := fs.db.QueryRow(`SELECT COUNT(*), MIN(tags), MAX(tags) FROM memory_fts WHERE id = ?`, id).Scan(&rows, &minTags, &maxTags); err != nil {
		return false, fmt.Errorf("read the FTS row of %s: %w", id, err)
	}
	return rows != 1 || minTags.String != joined || maxTags.String != joined, nil
}

// deleteFTSRow removes the entry's memory_fts row. memory_fts.id is UNINDEXED, so deleting by id
// scans the whole FTS table; a bulk run that knows the rowid deletes by rowid instead, guarded by
// the id so a stale rowid can never remove another entry's row. Anything else, including a guarded
// delete that found no row, falls back to the delete by id.
func deleteFTSRow(tx *sql.Tx, id string, rowID int64) error {
	if rowID > 0 {
		res, err := tx.Exec(`DELETE FROM memory_fts WHERE rowid = ? AND id = ?`, rowID, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 1 {
			return nil
		}
	}
	_, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, id)
	return err
}

// ftsRowIDs maps each id that has exactly one memory_fts row to that row's rowid, read in one scan.
// An id with several rows (index drift) is left out, so its edit removes all of them by id. nil when
// the scan fails: every edit then deletes by id, slower but the same result.
func (fs *FileStore) ftsRowIDs() map[string]int64 {
	rows, err := fs.db.Query(`SELECT rowid, id FROM memory_fts`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := make(map[string]int64)
	several := make(map[string]bool)
	for rows.Next() {
		var rowID int64
		var id string
		if err := rows.Scan(&rowID, &id); err != nil {
			return nil
		}
		if _, seen := ids[id]; seen {
			several[id] = true
		}
		ids[id] = rowID
	}
	if rows.Err() != nil {
		return nil
	}
	for id := range several {
		delete(ids, id)
	}
	return ids
}

// entryFields are the parts of a note or session file the index is built from.
type entryFields struct {
	id         string
	title      string
	ftsContent string // memory_fts.content, exactly as Reindex builds it
	tags       []string
}

func parseEntryFile(kind string, data []byte) (*entryFields, error) {
	if kind == "note" {
		n, err := ParseNoteMarkdown(data)
		if err != nil {
			return nil, err
		}
		return &entryFields{id: n.ID, title: n.Title, ftsContent: n.Content, tags: n.Tags}, nil
	}
	s, err := ParseSessionMarkdown(data)
	if err != nil {
		return nil, err
	}
	return &entryFields{id: s.ID, title: s.Title, ftsContent: s.GetSearchableContent(), tags: s.Tags}, nil
}

// splitFrontmatter finds the YAML frontmatter the way ParseNoteMarkdown and ParseSessionMarkdown
// do: the file opens with "---\n" and the frontmatter ends at the first "\n---\n", a missing final
// newline being supplied first. It returns the YAML text, the rest of the file from that "\n---\n"
// on, and whether the final newline was supplied.
func splitFrontmatter(content string) (frontmatter, rest string, appended bool, err error) {
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
		appended = true
	}
	if !strings.HasPrefix(content, "---\n") {
		return "", "", false, fmt.Errorf("missing YAML frontmatter")
	}
	end := strings.Index(content[4:], "\n---\n")
	if end == -1 {
		return "", "", false, fmt.Errorf("invalid YAML frontmatter")
	}
	end += 4
	return content[4:end], content[end:], appended, nil
}

// rewriteFrontmatterTags returns data with only the frontmatter's top-level `tags` list edited: the
// string tags in remove are dropped and the tags in add appended unless present, both compared
// case-insensitively (EditTagList's rule). The frontmatter is re-encoded from its own yaml.Node, so
// values, key order, quoting styles and keys claudemem does not know survive, and the body is copied
// verbatim. A file written by claudemem changes only in its tags lines; hand-written frontmatter may
// also lose blank lines and have its comments and nested indentation re-spaced. changed is false, and
// data is returned as it is, when the edit is already in place.
func rewriteFrontmatterTags(data []byte, add, remove []string) (out []byte, changed bool, err error) {
	frontmatter, rest, appended, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, false, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatter), &doc); err != nil {
		return nil, false, fmt.Errorf("parse frontmatter: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("frontmatter is not a YAML mapping")
	}
	mapping := doc.Content[0]

	var list *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Value == "tags" {
			list = mapping.Content[i+1]
			break
		}
	}
	missing := list == nil
	switch {
	case missing:
		list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	case list.Kind == yaml.ScalarNode && list.ShortTag() == "!!null":
		// `tags:` with no value: an empty list
		*list = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: list.Line, Column: list.Column}
	case list.Kind != yaml.SequenceNode:
		return nil, false, fmt.Errorf("frontmatter tags is not a list; fix the file by hand")
	}

	// Parsing keeps only string items as tags, so only string items are matched and counted;
	// anything else in the list is kept as it is.
	isTag := func(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.ShortTag() == "!!str" }
	items := make([]*yaml.Node, 0, len(list.Content)+len(add))
	var kept []string
	for _, item := range list.Content {
		if isTag(item) {
			if containsFold(remove, item.Value) {
				changed = true
				continue
			}
			kept = append(kept, item.Value)
		}
		items = append(items, item)
	}
	for _, tag := range add {
		if !containsFold(kept, tag) {
			// Node.Encode quotes the way yaml.Marshal does ("true", "on", "123" stay strings)
			item := &yaml.Node{}
			if err := item.Encode(tag); err != nil {
				return nil, false, fmt.Errorf("encode tag %q: %w", tag, err)
			}
			items = append(items, item)
			kept = append(kept, tag)
			changed = true
		}
	}
	if !changed {
		return data, false, nil
	}
	if len(list.Content) == 0 {
		// yaml.Marshal writes an empty list as `[]` and a full one as a block list: an empty list
		// that gains tags takes the block form, so the file reads as if claudemem had written it.
		list.Style &^= yaml.FlowStyle
	}
	list.Content = items
	if missing {
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tags"}, list)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4) // yaml.Marshal's indent, which wrote every claudemem file
	if err := enc.Encode(&doc); err != nil {
		return nil, false, fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, false, fmt.Errorf("encode frontmatter: %w", err)
	}
	result := "---\n" + strings.TrimSuffix(buf.String(), "\n") + rest
	if appended {
		result = strings.TrimSuffix(result, "\n")
	}
	return []byte(result), true, nil
}

// verifyTagRewrite proves a rewrite changed nothing but the tags: the two frontmatters decode to the
// same values once `tags` is set aside, and the bodies are byte-identical. It stands between the
// encoder and thousands of files in a bulk run; a file it cannot prove is left untouched.
func verifyTagRewrite(oldData, newData []byte) error {
	oldFM, oldRest, _, err := splitFrontmatter(string(oldData))
	if err != nil {
		return err
	}
	newFM, newRest, _, err := splitFrontmatter(string(newData))
	if err != nil {
		return fmt.Errorf("rewritten file: %w", err)
	}
	if oldRest != newRest {
		return fmt.Errorf("tag rewrite would change the body; file left untouched")
	}
	var before, after map[string]interface{}
	if err := yaml.Unmarshal([]byte(oldFM), &before); err != nil {
		return fmt.Errorf("parse frontmatter: %w", err)
	}
	if err := yaml.Unmarshal([]byte(newFM), &after); err != nil {
		return fmt.Errorf("parse rewritten frontmatter: %w", err)
	}
	delete(before, "tags")
	delete(after, "tags")
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("tag rewrite would change other frontmatter fields; file left untouched")
	}
	return nil
}
