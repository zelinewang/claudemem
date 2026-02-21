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
func (fs *FileStore) AddNote(note *models.Note) error {
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

	// Create category directory if it doesn't exist
	categoryDir := filepath.Join(fs.notesDir, note.Category)
	if err := os.MkdirAll(categoryDir, 0700); err != nil {
		return fmt.Errorf("failed to create category directory: %w", err)
	}

	// Generate filename from title
	filename := Slugify(note.Title)
	notePath := filepath.Join(categoryDir, filename)
	relPath := filepath.Join("notes", note.Category, filename)

	// Format note as markdown
	content := FormatNoteMarkdown(note)

	// Write file
	if err := os.WriteFile(notePath, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write note file: %w", err)
	}

	// Insert into database
	tx, err := fs.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO entries (id, type, title, category, tags, filepath, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, note.ID, note.Type, note.Title, note.Category, strings.Join(note.Tags, " "),
		relPath, note.Created.Unix(), note.Updated.Unix())
	if err != nil {
		return fmt.Errorf("failed to insert entry: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO memory_fts (id, title, content, tags)
		VALUES (?, ?, ?, ?)
	`, note.ID, note.Title, note.Content, strings.Join(note.Tags, " "))
	if err != nil {
		return fmt.Errorf("failed to insert into FTS: %w", err)
	}

	return tx.Commit()
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
	_, err = tx.Exec(`INSERT INTO entries (id, type, title, category, tags, filepath, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		note.ID, note.Type, note.Title, note.Category, strings.Join(note.Tags, " "),
		newRelPath, note.Created.Unix(), note.Updated.Unix())
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

	return tx.Commit()
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
	args = append(args, query)

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
