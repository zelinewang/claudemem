package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zanelabz/claudemem/pkg/models"
	_ "modernc.org/sqlite"
)

// FileStore implements UnifiedStore using filesystem and SQLite
type FileStore struct {
	baseDir     string
	notesDir    string
	sessionsDir string
	indexDir    string
	db          *sql.DB
}

// NewFileStore creates a new file-based store
func NewFileStore(baseDir string) (*FileStore, error) {
	fs := &FileStore{
		baseDir:     baseDir,
		notesDir:    filepath.Join(baseDir, "notes"),
		sessionsDir: filepath.Join(baseDir, "sessions"),
		indexDir:    filepath.Join(baseDir, ".index"),
	}

	// Create directories
	dirs := []string{fs.notesDir, fs.sessionsDir, fs.indexDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Open SQLite database
	dbPath := filepath.Join(fs.indexDir, "search.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	fs.db = db

	// Initialize schema
	if err := fs.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return fs, nil
}

// initSchema creates the database tables
func (fs *FileStore) initSchema() error {
	// Create entries table
	entriesSchema := `
	CREATE TABLE IF NOT EXISTS entries (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		title TEXT NOT NULL,
		category TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '',
		date_str TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '',
		filepath TEXT NOT NULL,
		created TEXT NOT NULL,
		updated TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_entries_type ON entries(type);
	CREATE INDEX IF NOT EXISTS idx_entries_date ON entries(date_str);
	CREATE INDEX IF NOT EXISTS idx_entries_category ON entries(category);
	CREATE INDEX IF NOT EXISTS idx_entries_branch ON entries(branch);`

	if _, err := fs.db.Exec(entriesSchema); err != nil {
		return fmt.Errorf("failed to create entries table: %w", err)
	}

	// Create FTS5 table for full-text search
	ftsSchema := `
	CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
		id UNINDEXED,
		title,
		content,
		tags
	);`

	if _, err := fs.db.Exec(ftsSchema); err != nil {
		return fmt.Errorf("failed to create FTS table: %w", err)
	}

	return nil
}

// Close closes the database connection
func (fs *FileStore) Close() error {
	if fs.db != nil {
		return fs.db.Close()
	}
	return nil
}

// NoteStore methods
func (fs *FileStore) AddNote(note *models.Note) error {
	return fmt.Errorf("not implemented")
}

func (fs *FileStore) GetNote(id string) (*models.Note, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fs *FileStore) GetNoteByTitle(category, title string) (*models.Note, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fs *FileStore) ListNotes(category string) ([]*models.Note, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fs *FileStore) UpdateNote(note *models.Note) error {
	return fmt.Errorf("not implemented")
}

func (fs *FileStore) DeleteNote(id string) error {
	return fmt.Errorf("not implemented")
}

func (fs *FileStore) SearchNotes(query, category string, tags []string) ([]*models.Note, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fs *FileStore) GetCategories() ([]CategoryInfo, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fs *FileStore) GetTags() ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

// SessionStore methods
func (fs *FileStore) SaveSession(session *models.Session) error {
	return fmt.Errorf("not implemented")
}

func (fs *FileStore) GetSession(id string) (*models.Session, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fs *FileStore) ListSessions(opts SessionListOpts) ([]*models.Session, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fs *FileStore) SearchSessions(query string, opts SessionListOpts) ([]*models.Session, error) {
	return nil, fmt.Errorf("not implemented")
}

// UnifiedStore methods
func (fs *FileStore) Search(query, entryType string, limit int) ([]SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fs *FileStore) Stats() (*StoreStats, error) {
	return nil, fmt.Errorf("not implemented")
}