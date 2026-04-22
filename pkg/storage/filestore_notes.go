package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
)

// AddNote adds a new note to the store
func (fs *FileStore) AddNote(note *models.Note) (*AddNoteResult, error) {
	// Validate inputs
	if _, err := sanitizePath(note.Category); err != nil {
		return nil, fmt.Errorf("invalid category: %w", err)
	}
	if err := validateTitle(note.Title); err != nil {
		return nil, err
	}
	if err := validateContent(note.Content); err != nil {
		return nil, err
	}
	if err := validateTags(note.Tags); err != nil {
		return nil, err
	}

	// ── Dedup check: same category + exact or similar title → merge ──
	existingID, existingFpath := fs.findDedupCandidate(note.Category, note.Title, note.ID)

	if existingID != "" {
		existingNote, readErr := fs.readNoteFile(filepath.Join(fs.baseDir, existingFpath))
		if readErr == nil {
			// Append new content with timestamp separator (only if content differs)
			if existingNote.Content != note.Content && note.Content != "" {
				separator := fmt.Sprintf("\n\n--- Updated %s ---\n", time.Now().Format("2006-01-02 15:04"))
				existingNote.Content += separator + note.Content
			}
			// Merge tags (deduplicate)
			existingNote.Tags = mergeTags(existingNote.Tags, note.Tags)
			// Merge metadata (new values override, existing keys preserved)
			if existingNote.Metadata == nil {
				existingNote.Metadata = make(map[string]string)
			}
			for k, v := range note.Metadata {
				if v != "" {
					existingNote.Metadata[k] = v
				}
			}
			existingNote.Updated = time.Now()

			if err := fs.UpdateNote(existingNote); err != nil {
				return nil, fmt.Errorf("failed to merge note: %w", err)
			}
			return &AddNoteResult{
				Action:   "merged",
				NoteID:   existingNote.ID,
				Title:    existingNote.Title,
				Category: existingNote.Category,
			}, nil
		}
	}

	// ── Normal create path ──
	categoryDir := filepath.Join(fs.notesDir, note.Category)
	if err := os.MkdirAll(categoryDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create category directory: %w", err)
	}

	filename := Slugify(note.Title)
	notePath := filepath.Join(categoryDir, filename)

	if err := validateFilepathWithinBase(fs.baseDir, notePath); err != nil {
		return nil, fmt.Errorf("path validation failed: %w", err)
	}

	relPath := filepath.Join("notes", note.Category, filename)

	content := FormatNoteMarkdown(note)

	if err := os.WriteFile(notePath, []byte(content), 0600); err != nil {
		return nil, fmt.Errorf("failed to write note file: %w", err)
	}

	tx, err := fs.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO entries (id, type, title, category, session_id, tags, filepath, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, note.ID, note.Type, note.Title, note.Category, noteSessionID(note),
		strings.Join(note.Tags, " "), relPath, note.Created.Unix(), note.Updated.Unix())
	if err != nil {
		return nil, fmt.Errorf("failed to insert entry: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO memory_fts (id, title, content, tags)
		VALUES (?, ?, ?, ?)
	`, note.ID, note.Title, note.Content, strings.Join(note.Tags, " "))
	if err != nil {
		return nil, fmt.Errorf("failed to insert into FTS: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Index for semantic search (best effort, no-op if disabled)
	fs.IndexNoteVector(note.ID, note.Title, note.Content, note.Tags)

	return &AddNoteResult{
		Action:   "created",
		NoteID:   note.ID,
		Title:    note.Title,
		Category: note.Category,
	}, nil
}

// mergeTags combines two tag slices, removing duplicates (case-insensitive)
func mergeTags(existing, new []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, t := range existing {
		lower := strings.ToLower(strings.TrimSpace(t))
		if lower != "" && !seen[lower] {
			seen[lower] = true
			result = append(result, strings.TrimSpace(t))
		}
	}
	for _, t := range new {
		lower := strings.ToLower(strings.TrimSpace(t))
		if lower != "" && !seen[lower] {
			seen[lower] = true
			result = append(result, strings.TrimSpace(t))
		}
	}
	return result
}

// GetNote retrieves a note by ID (supports prefix matching)
func (fs *FileStore) GetNote(id string) (*models.Note, error) {
	var fpath string
	err := fs.db.QueryRow(`
		SELECT filepath FROM entries
		WHERE (id = ? OR id LIKE ? || '%') AND type = 'note'
		ORDER BY id
		LIMIT 1
	`, id, id).Scan(&fpath)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("note not found: %s", id)
		}
		return nil, fmt.Errorf("failed to query note: %w", err)
	}

	return fs.readNoteFile(filepath.Join(fs.baseDir, fpath))
}

// GetNoteByTitle retrieves a note by category and title
func (fs *FileStore) GetNoteByTitle(category, title string) (*models.Note, error) {
	if _, err := sanitizePath(category); err != nil {
		return nil, fmt.Errorf("invalid category: %w", err)
	}
	// Try direct path first
	filename := Slugify(title)
	directPath := filepath.Join(fs.notesDir, category, filename)

	if _, err := os.Stat(directPath); err == nil {
		return fs.readNoteFile(directPath)
	}

	// Fallback to database query
	var fpath string
	err := fs.db.QueryRow(`
		SELECT filepath FROM entries
		WHERE category = ? AND title = ? AND type = 'note'
		LIMIT 1
	`, category, title).Scan(&fpath)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("note not found: %s/%s", category, title)
		}
		return nil, fmt.Errorf("failed to query note: %w", err)
	}

	return fs.readNoteFile(filepath.Join(fs.baseDir, fpath))
}

// ListNotes lists all notes or notes in a specific category
func (fs *FileStore) ListNotes(category string) ([]*models.Note, error) {
	var query string
	var args []interface{}

	if category == "" {
		query = `SELECT filepath FROM entries WHERE type = 'note' ORDER BY category, title`
	} else {
		if _, err := sanitizePath(category); err != nil {
			return nil, fmt.Errorf("invalid category: %w", err)
		}
		query = `SELECT filepath FROM entries WHERE type = 'note' AND category = ? ORDER BY title`
		args = append(args, category)
	}

	rows, err := fs.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query notes: %w", err)
	}
	defer rows.Close()

	var notes []*models.Note
	for rows.Next() {
		var fpath string
		if err := rows.Scan(&fpath); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		note, err := fs.readNoteFile(filepath.Join(fs.baseDir, fpath))
		if err != nil {
			continue // skip unreadable files
		}
		notes = append(notes, note)
	}

	return notes, nil
}

// UpdateNote updates an existing note
func (fs *FileStore) UpdateNote(note *models.Note) error {
	// Validate inputs
	if _, err := sanitizePath(note.Category); err != nil {
		return fmt.Errorf("invalid category: %w", err)
	}
	if err := validateTitle(note.Title); err != nil {
		return err
	}
	if err := validateContent(note.Content); err != nil {
		return err
	}
	if err := validateTags(note.Tags); err != nil {
		return err
	}

	// Check old entry exists
	var oldFpath string
	err := fs.db.QueryRow(`SELECT filepath FROM entries WHERE id = ?`, note.ID).Scan(&oldFpath)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("note not found: %s", note.ID)
		}
		return fmt.Errorf("failed to query note: %w", err)
	}

	// Prepare new file
	note.Updated = time.Now()
	categoryDir := filepath.Join(fs.notesDir, note.Category)
	if err := os.MkdirAll(categoryDir, 0700); err != nil {
		return fmt.Errorf("failed to create category directory: %w", err)
	}
	filename := Slugify(note.Title)
	newPath := filepath.Join(categoryDir, filename)
	newRelPath := filepath.Join("notes", note.Category, filename)
	content := FormatNoteMarkdown(note)

	// Write new file to temp location first
	tmpPath := newPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Single transaction: delete old entries, insert new
	tx, err := fs.db.Begin()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM entries WHERE id = ?`, note.ID); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to delete old entry: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, note.ID); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to delete from FTS: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO entries (id, type, title, category, session_id, tags, filepath, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		note.ID, note.Type, note.Title, note.Category, noteSessionID(note),
		strings.Join(note.Tags, " "), newRelPath, note.Created.Unix(), note.Updated.Unix())
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to insert new entry: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, ?)`,
		note.ID, note.Title, note.Content, strings.Join(note.Tags, " "))
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to insert into FTS: %w", err)
	}

	if err := tx.Commit(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to commit: %w", err)
	}

	// Transaction committed successfully. Now do filesystem operations.
	oldFullPath := filepath.Join(fs.baseDir, oldFpath)
	os.Remove(oldFullPath) // Best effort: if this fails, we have a stale file but DB is correct

	// Rename temp file to final path
	if err := os.Rename(tmpPath, newPath); err != nil {
		// Critical: DB is committed but file rename failed. Try to copy.
		data, _ := os.ReadFile(tmpPath)
		os.WriteFile(newPath, data, 0600)
		os.Remove(tmpPath)
	}

	// Update semantic search index (best effort, no-op if disabled)
	fs.IndexNoteVector(note.ID, note.Title, note.Content, note.Tags)

	return nil
}

// DeleteNote deletes a note by ID (supports prefix matching)
func (fs *FileStore) DeleteNote(id string) error {
	var fpath, fullID, title string
	err := fs.db.QueryRow(`
		SELECT filepath, id, title FROM entries
		WHERE (id = ? OR id LIKE ? || '%') AND type = 'note'
		ORDER BY id
		LIMIT 1
	`, id, id).Scan(&fpath, &fullID, &title)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("note not found: %s", id)
		}
		return fmt.Errorf("failed to query note: %w", err)
	}

	// Delete file
	fullPath := filepath.Join(fs.baseDir, fpath)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	// Delete from database
	tx, err := fs.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM entries WHERE id = ?`, fullID); err != nil {
		return fmt.Errorf("failed to delete entry: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, fullID); err != nil {
		return fmt.Errorf("failed to delete from FTS: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Remove from semantic search index (best effort, no-op if disabled)
	fs.RemoveVector(fullID)

	return nil
}

// SearchNotes searches for notes matching the query
func (fs *FileStore) SearchNotes(query string, category string, tags []string) ([]*models.Note, error) {
	var args []interface{}
	ftsQuery := `
		SELECT f.id, f.rank, e.filepath
		FROM memory_fts f
		JOIN entries e ON f.id = e.id
		WHERE memory_fts MATCH ? AND e.type = 'note'
	`
	args = append(args, sanitizeFTSQuery(query))

	if category != "" {
		ftsQuery += ` AND e.category = ?`
		args = append(args, category)
	}

	ftsQuery += ` ORDER BY f.rank LIMIT 50`

	rows, err := fs.db.Query(ftsQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search notes: %w", err)
	}
	defer rows.Close()

	var results []*models.Note
	for rows.Next() {
		var id, fpath string
		var rank float64
		if err := rows.Scan(&id, &rank, &fpath); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		note, err := fs.readNoteFile(filepath.Join(fs.baseDir, fpath))
		if err != nil {
			continue
		}

		// Post-filter by tags if provided
		if len(tags) > 0 && !noteHasAllTags(note, tags) {
			continue
		}

		results = append(results, note)
	}

	return results, nil
}

// GetCategories returns all categories with counts
func (fs *FileStore) GetCategories() ([]CategoryInfo, error) {
	rows, err := fs.db.Query(`
		SELECT category, COUNT(*) as cnt
		FROM entries
		WHERE type = 'note'
		GROUP BY category
		ORDER BY category
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query categories: %w", err)
	}
	defer rows.Close()

	var categories []CategoryInfo
	for rows.Next() {
		var cat CategoryInfo
		if err := rows.Scan(&cat.Name, &cat.Count); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		categories = append(categories, cat)
	}

	return categories, nil
}

// GetTags returns all unique tags
func (fs *FileStore) GetTags() ([]string, error) {
	rows, err := fs.db.Query(`
		SELECT DISTINCT tags FROM entries WHERE type = 'note' AND tags != ''
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query tags: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]bool)
	for rows.Next() {
		var tagsStr string
		if err := rows.Scan(&tagsStr); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		for _, tag := range strings.Fields(tagsStr) {
			seen[tag] = true
		}
	}

	var tags []string
	for tag := range seen {
		tags = append(tags, tag)
	}

	// Sort alphabetically
	for i := 0; i < len(tags); i++ {
		for j := i + 1; j < len(tags); j++ {
			if tags[j] < tags[i] {
				tags[i], tags[j] = tags[j], tags[i]
			}
		}
	}

	return tags, nil
}

// readNoteFile reads and parses a note markdown file
func (fs *FileStore) readNoteFile(fullPath string) (*models.Note, error) {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read note file: %w", err)
	}
	return ParseNoteMarkdown(data)
}

// findDedupCandidate finds an existing note to merge with.
// First tries exact title match, then fuzzy match (>50% word overlap in same category).
func (fs *FileStore) findDedupCandidate(category, title, excludeID string) (string, string) {
	// Layer 1: Exact title match
	var id, fpath string
	err := fs.db.QueryRow(`
		SELECT id, filepath FROM entries
		WHERE category = ? AND title = ? AND type = 'note'
		LIMIT 1
	`, category, title).Scan(&id, &fpath)
	if err == nil && id != excludeID {
		return id, fpath
	}

	// Layer 2: Fuzzy title match — find notes in same category with >50% word overlap
	newWords := titleWords(title)
	if len(newWords) == 0 {
		return "", ""
	}

	rows, err := fs.db.Query(`
		SELECT id, title, filepath FROM entries
		WHERE category = ? AND type = 'note'
	`, category)
	if err != nil {
		return "", ""
	}
	defer rows.Close()

	bestID, bestFpath := "", ""
	bestOverlap := 0.0

	for rows.Next() {
		var eid, etitle, efpath string
		rows.Scan(&eid, &etitle, &efpath)
		if eid == excludeID {
			continue
		}

		existingWords := titleWords(etitle)
		if len(existingWords) == 0 {
			continue
		}

		// Skip fuzzy matching for very short titles (< 2 significant words).
		// Short titles like "Note 1" and "Note 3" would falsely match because
		// the only significant word is "note" (100% overlap).
		if len(newWords) < 2 || len(existingWords) < 2 {
			continue
		}

		// Calculate word overlap ratio
		overlap := wordOverlap(newWords, existingWords)
		if overlap > bestOverlap && overlap >= 0.5 {
			bestOverlap = overlap
			bestID = eid
			bestFpath = efpath
		}
	}

	return bestID, bestFpath
}

// stopWords are common English words that don't carry semantic meaning for dedup matching
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"that": true, "this": true, "are": true, "was": true, "were": true,
	"been": true, "have": true, "has": true, "had": true, "not": true,
	"but": true, "all": true, "can": true, "her": true, "his": true,
	"how": true, "its": true, "may": true, "new": true, "now": true,
	"old": true, "see": true, "way": true, "who": true, "did": true,
	"get": true, "let": true, "say": true, "she": true, "too": true,
	"use": true, "our": true, "out": true,
}

// titleWords extracts lowercase significant words from a title
// Ignores short words (< 3 chars) and common stop words
func titleWords(title string) map[string]bool {
	words := make(map[string]bool)
	for _, w := range strings.Fields(strings.ToLower(title)) {
		// Skip very short words and common stop words
		if len(w) >= 3 && !stopWords[w] {
			words[w] = true
		}
	}
	return words
}

// wordOverlap calculates the ratio of shared words between two word sets
func wordOverlap(a, b map[string]bool) float64 {
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	// Use the smaller set as denominator to be more generous with matching
	smaller := len(a)
	if len(b) < smaller {
		smaller = len(b)
	}
	if smaller == 0 {
		return 0
	}
	return float64(shared) / float64(smaller)
}

// noteSessionID safely extracts session_id from note metadata
func noteSessionID(note *models.Note) string {
	if note.Metadata == nil {
		return ""
	}
	return note.Metadata["session_id"]
}

// FindNotesBySessionRef finds all notes linked to a session via metadata.session_id
func (fs *FileStore) FindNotesBySessionRef(sessionRef string) ([]models.RelatedNote, error) {
	if sessionRef == "" {
		return nil, nil
	}
	rows, err := fs.db.Query(`
		SELECT id, title, category FROM entries
		WHERE type = 'note' AND session_id = ?
	`, sessionRef)
	if err != nil {
		return nil, fmt.Errorf("failed to query notes by session ref: %w", err)
	}
	defer rows.Close()

	var notes []models.RelatedNote
	for rows.Next() {
		var rn models.RelatedNote
		if err := rows.Scan(&rn.ID, &rn.Title, &rn.Category); err != nil {
			continue
		}
		notes = append(notes, rn)
	}
	return notes, nil
}

// noteHasAllTags checks if a note has all required tags (case-insensitive)
func noteHasAllTags(note *models.Note, required []string) bool {
	for _, req := range required {
		found := false
		for _, tag := range note.Tags {
			if strings.EqualFold(tag, req) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
