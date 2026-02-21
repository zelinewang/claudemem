package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zelinewang/claudemem/pkg/models"
)

// MigrateResult holds migration statistics
type MigrateResult struct {
	Imported int
	Skipped  int
	Errors   []string
}

// MigrateBraindump imports notes from a braindump store (~/.braindump/)
func (fs *FileStore) MigrateBraindump(sourceDir string) (*MigrateResult, error) {
	result := &MigrateResult{}

	notesDir := filepath.Join(sourceDir)
	if _, err := os.Stat(notesDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("braindump directory not found: %s", sourceDir)
	}

	// Walk through all markdown files in the braindump directory
	err := filepath.Walk(notesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip non-markdown, index directory, and directories
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		// Skip the .index directory
		rel, _ := filepath.Rel(notesDir, path)
		if strings.HasPrefix(rel, ".index") || strings.HasPrefix(rel, ".") {
			return nil
		}

		// Read and parse the markdown file
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("read %s: %v", rel, readErr))
			return nil
		}

		// Try to parse as note markdown (braindump uses same YAML frontmatter format)
		note, parseErr := parseBraindumpNote(data, rel)
		if parseErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("parse %s: %v", rel, parseErr))
			return nil
		}

		// Check if already imported (by ID)
		var existing int
		fs.db.QueryRow("SELECT COUNT(*) FROM entries WHERE id = ?", note.ID).Scan(&existing)
		if existing > 0 {
			result.Skipped++
			return nil
		}

		// Import the note (AddNote handles dedup automatically)
		if _, err := fs.AddNote(note); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("import %s: %v", rel, err))
			return nil
		}

		result.Imported++
		return nil
	})

	return result, err
}

// parseBraindumpNote parses a braindump markdown file into a Note
func parseBraindumpNote(data []byte, relPath string) (*models.Note, error) {
	// Try standard frontmatter parsing first
	note, err := ParseNoteMarkdown(data)
	if err == nil && note.ID != "" {
		return note, nil
	}

	// Fallback: create note from file content without frontmatter
	content := strings.TrimSpace(string(data))

	// Extract category from directory path
	dir := filepath.Dir(relPath)
	category := "imported"
	if dir != "." && dir != "" {
		category = strings.ReplaceAll(dir, string(filepath.Separator), "-")
	}

	// Extract title from filename
	title := strings.TrimSuffix(filepath.Base(relPath), ".md")
	title = strings.ReplaceAll(title, "-", " ")
	title = strings.Title(title)

	note = models.NewNote(category, title, content)
	// Sanitize category
	if _, err := sanitizePath(category); err != nil {
		note.Category = "imported"
	}

	return note, nil
}

// MigrateClaudeDone imports sessions from a claude-done store (~/.claude-done/)
func (fs *FileStore) MigrateClaudeDone(sourceDir string) (*MigrateResult, error) {
	result := &MigrateResult{}

	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("claude-done directory not found: %s", sourceDir)
	}

	// claude-done stores files as: YYYY-MM-DD_branch_sessionid_kebab-title.md
	filenamePattern := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(.+?)_([a-f0-9]{8})_(.+)\.md$`)

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		// Parse filename
		matches := filenamePattern.FindStringSubmatch(entry.Name())

		// Read file content
		data, readErr := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if readErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("read %s: %v", entry.Name(), readErr))
			continue
		}

		var session *models.Session

		if matches != nil {
			// Structured filename: date_branch_sessionid_title.md
			date := matches[1]
			branch := matches[2]
			sessionID := matches[3]
			title := strings.ReplaceAll(matches[4], "-", " ")
			title = strings.Title(title)

			session = models.NewSession(title, branch, "", sessionID)
			session.Date = date
		} else {
			// Unstructured filename: just use the filename as title
			title := strings.TrimSuffix(entry.Name(), ".md")
			title = strings.ReplaceAll(title, "-", " ")
			session = models.NewSession(title, "unknown", "", "imported")
		}

		// Parse the content for session sections
		content := string(data)
		if strings.Contains(content, "## ") {
			// Has section headers — parse them
			parsed, parseErr := ParseSessionMarkdown(data)
			if parseErr == nil {
				session.Summary = parsed.Summary
				session.Decisions = parsed.Decisions
				session.Changes = parsed.Changes
				session.Problems = parsed.Problems
				session.Questions = parsed.Questions
				session.NextSteps = parsed.NextSteps
			} else {
				session.Summary = strings.TrimSpace(content)
			}
		} else {
			session.Summary = strings.TrimSpace(content)
		}

		// Check if already imported
		var existing int
		fs.db.QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type = 'session'",
			session.SessionID).Scan(&existing)
		if existing > 0 {
			result.Skipped++
			continue
		}

		// Import the session
		if err := fs.SaveSession(session); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("import %s: %v", entry.Name(), err))
			continue
		}

		result.Imported++
	}

	return result, nil
}

// VerifyIntegrity checks DB-file consistency and reports/repairs issues
func (fs *FileStore) VerifyIntegrity() (*VerifyResult, error) {
	result := &VerifyResult{}

	// Check all DB entries have corresponding files
	rows, err := fs.db.Query("SELECT id, type, filepath FROM entries")
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, entryType, fpath string
		rows.Scan(&id, &entryType, &fpath)

		fullPath := filepath.Join(fs.baseDir, fpath)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			result.OrphanedEntries = append(result.OrphanedEntries, OrphanEntry{
				ID: id, Type: entryType, Path: fpath,
			})
		}
	}

	// Check FTS count matches entries count
	var entryCount, ftsCount int
	fs.db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&entryCount)
	fs.db.QueryRow("SELECT COUNT(*) FROM memory_fts").Scan(&ftsCount)

	result.EntryCount = entryCount
	result.FTSCount = ftsCount
	result.InSync = entryCount == ftsCount && len(result.OrphanedEntries) == 0

	return result, nil
}

// RepairIntegrity removes orphaned DB entries
func (fs *FileStore) RepairIntegrity() (int, error) {
	result, err := fs.VerifyIntegrity()
	if err != nil {
		return 0, err
	}

	if result.InSync {
		return 0, nil
	}

	tx, err := fs.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	removed := 0
	for _, orphan := range result.OrphanedEntries {
		tx.Exec("DELETE FROM entries WHERE id = ?", orphan.ID)
		tx.Exec("DELETE FROM memory_fts WHERE id = ?", orphan.ID)
		removed++
	}

	return removed, tx.Commit()
}

// VerifyResult holds integrity check results
type VerifyResult struct {
	EntryCount      int           `json:"entry_count"`
	FTSCount        int           `json:"fts_count"`
	InSync          bool          `json:"in_sync"`
	OrphanedEntries []OrphanEntry `json:"orphaned_entries,omitempty"`
}

// OrphanEntry represents a DB entry without a corresponding file
type OrphanEntry struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Path string `json:"path"`
}

// Reindex rebuilds the SQLite index from markdown files on disk.
// Used after import to populate the search index.
func (fs *FileStore) Reindex() (int, error) {
	// Clear existing index
	fs.db.Exec("DELETE FROM entries")
	fs.db.Exec("DELETE FROM memory_fts")

	count := 0

	// Reindex notes
	if _, err := os.Stat(fs.notesDir); err == nil {
		filepath.Walk(fs.notesDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			note, parseErr := ParseNoteMarkdown(data)
			if parseErr != nil || note.ID == "" {
				return nil
			}

			rel, _ := filepath.Rel(fs.baseDir, path)
			fs.db.Exec(`INSERT OR IGNORE INTO entries (id, type, title, category, tags, filepath, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				note.ID, "note", note.Title, note.Category,
				strings.Join(note.Tags, " "), rel,
				note.Created.Unix(), note.Updated.Unix())
			fs.db.Exec(`INSERT OR IGNORE INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, ?)`,
				note.ID, note.Title, note.Content, strings.Join(note.Tags, " "))
			count++
			return nil
		})
	}

	// Reindex sessions
	if _, err := os.Stat(fs.sessionsDir); err == nil {
		filepath.Walk(fs.sessionsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			session, parseErr := ParseSessionMarkdown(data)
			if parseErr != nil || session.ID == "" {
				return nil
			}

			rel, _ := filepath.Rel(fs.baseDir, path)
			fs.db.Exec(`INSERT OR IGNORE INTO entries (id, type, title, branch, project, session_id, date_str, tags, filepath, created, updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				session.ID, "session", session.Title, session.Branch, session.Project,
				session.SessionID, session.Date, strings.Join(session.Tags, " "),
				rel, session.Created.Format("2006-01-02T15:04:05Z"),
				session.Created.Format("2006-01-02T15:04:05Z"))
			fs.db.Exec(`INSERT OR IGNORE INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, ?)`,
				session.ID, session.Title, session.GetSearchableContent(),
				strings.Join(session.Tags, " "))
			count++
			return nil
		})
	}

	return count, nil
}

// Ensure sql import is used
var _ = sql.ErrNoRows
