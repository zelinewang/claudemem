package storage

import (
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
)

// AddNoteResult describes what happened when adding a note
type AddNoteResult struct {
	Action   string `json:"action"`   // "created", "merged"
	NoteID   string `json:"note_id"`
	Title    string `json:"title"`
	Category string `json:"category"`
}

// NoteStore defines operations for notes
type NoteStore interface {
	AddNote(note *models.Note) (*AddNoteResult, error)
	GetNote(id string) (*models.Note, error)
	GetNoteByTitle(category, title string) (*models.Note, error)
	ListNotes(category string) ([]*models.Note, error)
	UpdateNote(note *models.Note) error
	DeleteNote(id string) error
	SearchNotes(query, category string, tags []string) ([]*models.Note, error)
	GetCategories() ([]CategoryInfo, error)
	GetTags() ([]string, error)
	FindNotesBySessionRef(sessionRef string) ([]models.RelatedNote, error)
}

// SessionStore defines operations for sessions
type SessionStore interface {
	SaveSession(session *models.Session) (*SaveSessionResult, error)
	GetSession(id string) (*models.Session, error)
	ListSessions(opts SessionListOpts) ([]*models.Session, error)
	SearchSessions(query string, opts SessionListOpts) ([]*models.Session, error)
	ResolveSessionID(project string, window time.Duration) (string, bool, error)
}

// UnifiedStore combines all store interfaces
type UnifiedStore interface {
	NoteStore
	SessionStore
	Search(query, entryType string, limit int) ([]SearchResult, error)
	Stats() (*StoreStats, error)
	MigrateBraindump(sourceDir string) (*MigrateResult, error)
	MigrateClaudeDone(sourceDir string) (*MigrateResult, error)
	VerifyIntegrity() (*VerifyResult, error)
	RepairIntegrity() (int, error)
	Reindex() (int, error)
	Close() error
}

// SessionListOpts provides filtering options for listing sessions
type SessionListOpts struct {
	Branch    string
	Project   string
	StartDate string // YYYY-MM-DD
	EndDate   string // YYYY-MM-DD
	Limit     int
}

// SearchResult represents a unified search result
type SearchResult struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Title    string    `json:"title"`
	Category string    `json:"category,omitempty"`
	Date     string    `json:"date,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	Project  string    `json:"project,omitempty"`
	Tags     []string  `json:"tags"`
	Score    float64   `json:"score"`
	Preview  string    `json:"preview"`
	Created  time.Time `json:"created"`
}

// StoreStats provides statistics about the store
type StoreStats struct {
	TotalNotes    int            `json:"total_notes"`
	TotalSessions int            `json:"total_sessions"`
	Categories    []CategoryInfo `json:"categories"`
	TopTags       []TagInfo      `json:"top_tags"`
	RecentEntries []SearchResult `json:"recent_entries"`
	StorageSize   int64          `json:"storage_size_bytes"`
}

// CategoryInfo provides information about a category
type CategoryInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// TagInfo provides information about a tag
type TagInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}