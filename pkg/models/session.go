package models

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Session represents a Claude conversation session
type Session struct {
	ID        string    `yaml:"id" json:"id"`
	Type      string    `yaml:"type" json:"type"`
	Title     string    `yaml:"title" json:"title"`
	Date      string    `yaml:"date" json:"date"`       // YYYY-MM-DD
	Branch    string    `yaml:"branch" json:"branch"`
	Project   string    `yaml:"project" json:"project"`
	SessionID string    `yaml:"session_id" json:"session_id"`
	Tags      []string  `yaml:"tags,flow" json:"tags"`
	Created   time.Time `yaml:"created" json:"created"`

	// Structured body sections
	Summary    string           `yaml:"-" json:"summary"`
	Decisions  []string         `yaml:"-" json:"decisions"`
	Changes    []FileChange     `yaml:"-" json:"changes"`
	Problems   []ProblemSolution `yaml:"-" json:"problems"`
	Questions  []string         `yaml:"-" json:"questions"`
	NextSteps  []string         `yaml:"-" json:"next_steps"`
}

// FileChange represents a file that was modified
type FileChange struct {
	Path        string `json:"path"`
	Description string `json:"description"`
}

// ProblemSolution represents a problem encountered and its solution
type ProblemSolution struct {
	Problem  string `json:"problem"`
	Solution string `json:"solution"`
}

// NewSession creates a new session with generated ID and current timestamp
func NewSession(title, branch, project, sessionID string) *Session {
	now := time.Now()
	return &Session{
		ID:        uuid.New().String(),
		Type:      "session",
		Title:     title,
		Date:      now.Format("2006-01-02"),
		Branch:    branch,
		Project:   project,
		SessionID: sessionID,
		Tags:      []string{},
		Created:   now,
		Decisions: []string{},
		Changes:   []FileChange{},
		Problems:  []ProblemSolution{},
		Questions: []string{},
		NextSteps: []string{},
	}
}

// GetSearchableContent flattens all sections into searchable text
func (s *Session) GetSearchableContent() string {
	var parts []string

	// Add basic fields
	parts = append(parts, s.Title)
	parts = append(parts, s.Summary)

	// Add decisions
	for _, d := range s.Decisions {
		parts = append(parts, d)
	}

	// Add changes
	for _, c := range s.Changes {
		parts = append(parts, fmt.Sprintf("%s %s", c.Path, c.Description))
	}

	// Add problems and solutions
	for _, p := range s.Problems {
		parts = append(parts, fmt.Sprintf("%s %s", p.Problem, p.Solution))
	}

	// Add questions
	parts = append(parts, s.Questions...)

	// Add next steps
	parts = append(parts, s.NextSteps...)

	// Add tags
	parts = append(parts, strings.Join(s.Tags, " "))

	return strings.Join(parts, " ")
}

// GetID returns the session ID
func (s *Session) GetID() string {
	return s.ID
}

// GetType returns "session"
func (s *Session) GetType() string {
	return s.Type
}

// GetTitle returns the session title
func (s *Session) GetTitle() string {
	return s.Title
}

// GetContent returns searchable content
func (s *Session) GetContent() string {
	return s.GetSearchableContent()
}

// GetTags returns the session tags
func (s *Session) GetTags() []string {
	return s.Tags
}

// GetCreated returns the creation time
func (s *Session) GetCreated() time.Time {
	return s.Created
}