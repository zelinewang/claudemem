package models

import (
	"time"

	"github.com/google/uuid"
)

// Note represents a categorized memory entry
type Note struct {
	ID       string            `yaml:"id" json:"id"`
	Type     string            `yaml:"type" json:"type"`
	Category string            `yaml:"category" json:"category"`
	Title    string            `yaml:"title" json:"title"`
	Content  string            `yaml:"-" json:"content"`
	Tags     []string          `yaml:"tags,flow" json:"tags"`
	Created  time.Time         `yaml:"created" json:"created"`
	Updated  time.Time         `yaml:"updated" json:"updated"`
	Metadata map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// NewNote creates a new note with generated ID and timestamps
func NewNote(category, title, content string) *Note {
	now := time.Now()
	return &Note{
		ID:       uuid.New().String(),
		Type:     "note",
		Category: category,
		Title:    title,
		Content:  content,
		Tags:     []string{},
		Created:  now,
		Updated:  now,
		Metadata: make(map[string]string),
	}
}

// GetID returns the note ID
func (n *Note) GetID() string {
	return n.ID
}

// GetType returns "note"
func (n *Note) GetType() string {
	return n.Type
}

// GetTitle returns the note title
func (n *Note) GetTitle() string {
	return n.Title
}

// GetContent returns the note content
func (n *Note) GetContent() string {
	return n.Content
}

// GetTags returns the note tags
func (n *Note) GetTags() []string {
	return n.Tags
}

// GetCreated returns the creation time
func (n *Note) GetCreated() time.Time {
	return n.Created
}