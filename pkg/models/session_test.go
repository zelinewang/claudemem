package models

import (
	"strings"
	"testing"
)

func TestNewSession_InitializesAllFields(t *testing.T) {
	session := NewSession("Test Title", "main", "myproject", "session-123")

	// Check basic fields
	if session.ID == "" {
		t.Errorf("ID should not be empty")
	}
	if session.Type != "session" {
		t.Errorf("Type should be 'session', got %q", session.Type)
	}
	if session.Title != "Test Title" {
		t.Errorf("Title should be 'Test Title', got %q", session.Title)
	}
	if session.Branch != "main" {
		t.Errorf("Branch should be 'main', got %q", session.Branch)
	}
	if session.Project != "myproject" {
		t.Errorf("Project should be 'myproject', got %q", session.Project)
	}
	if session.SessionID != "session-123" {
		t.Errorf("SessionID should be 'session-123', got %q", session.SessionID)
	}

	// Check date format
	if len(session.Date) != 10 || session.Date[4] != '-' || session.Date[7] != '-' {
		t.Errorf("Date should be in YYYY-MM-DD format, got %q", session.Date)
	}

	// Check that all slices are initialized (not nil)
	if session.Tags == nil {
		t.Errorf("Tags should be initialized (not nil)")
	}
	if len(session.Tags) != 0 {
		t.Errorf("Tags should be empty slice, got %v", session.Tags)
	}

	if session.Decisions == nil {
		t.Errorf("Decisions should be initialized (not nil)")
	}
	if session.Changes == nil {
		t.Errorf("Changes should be initialized (not nil)")
	}
	if session.Problems == nil {
		t.Errorf("Problems should be initialized (not nil)")
	}
	if session.Insights == nil {
		t.Errorf("Insights should be initialized (not nil)")
	}
	if len(session.Insights) != 0 {
		t.Errorf("Insights should be empty slice, got %v", session.Insights)
	}
	if session.Questions == nil {
		t.Errorf("Questions should be initialized (not nil)")
	}
	if session.NextSteps == nil {
		t.Errorf("NextSteps should be initialized (not nil)")
	}
	if session.RelatedNotes == nil {
		t.Errorf("RelatedNotes should be initialized (not nil)")
	}
	if len(session.RelatedNotes) != 0 {
		t.Errorf("RelatedNotes should be empty slice, got %v", session.RelatedNotes)
	}

	// Check new string fields default to empty
	if session.WhatHappened != "" {
		t.Errorf("WhatHappened should be empty string, got %q", session.WhatHappened)
	}

	// Check Created time is set
	if session.Created.IsZero() {
		t.Errorf("Created time should be set")
	}
}

func TestSession_GetSearchableContent_AllFields(t *testing.T) {
	session := NewSession("Test Session", "feature/test", "myproject", "session-456")

	// Set all fields
	session.Summary = "This is a summary"
	session.WhatHappened = "investigated bug in authentication"
	session.Insights = []string{"learned about JWT", "discovered rate limiting"}
	session.RelatedNotes = []RelatedNote{
		{ID: "abc123", Title: "My Note", Category: "api-specs"},
		{ID: "def456", Title: "Auth Guide", Category: "security"},
	}
	session.Decisions = []string{"Use RSA keys", "Implement retry logic"}
	session.Changes = []FileChange{
		{Path: "/src/auth.go", Description: "Added JWT validation"},
	}
	session.Problems = []ProblemSolution{
		{Problem: "Token expired", Solution: "Refresh mechanism"},
	}
	session.Questions = []string{"How to handle refresh?"}
	session.NextSteps = []string{"Write tests", "Document API"}
	session.Tags = []string{"auth", "security"}

	content := session.GetSearchableContent()

	// Check that all important text appears in searchable content
	expectedPhrases := []string{
		"Test Session",                    // title
		"This is a summary",               // summary
		"investigated bug",                // what happened
		"learned about JWT",               // insight 1
		"discovered rate limiting",        // insight 2
		"My Note",                         // related note 1 title
		"Auth Guide",                      // related note 2 title
		"Use RSA keys",                    // decision
		"Added JWT validation",            // change description
		"Token expired",                   // problem
		"Refresh mechanism",               // solution
		"How to handle refresh",           // question
		"Write tests",                     // next step
		"auth security",                   // tags (joined)
	}

	for _, phrase := range expectedPhrases {
		if !strings.Contains(content, phrase) {
			t.Errorf("GetSearchableContent() should contain %q, got:\n%s", phrase, content)
		}
	}
}

func TestSession_GetSearchableContent_EmptyNewFields(t *testing.T) {
	session := NewSession("Empty Session", "main", "project", "sid")

	// Leave WhatHappened, Insights, RelatedNotes empty
	session.Summary = "Just a summary"

	// Should not crash
	content := session.GetSearchableContent()

	if !strings.Contains(content, "Empty Session") {
		t.Errorf("GetSearchableContent() should contain title, got: %s", content)
	}
	if !strings.Contains(content, "Just a summary") {
		t.Errorf("GetSearchableContent() should contain summary, got: %s", content)
	}
}

func TestSession_EntryInterface(t *testing.T) {
	// This should compile - verifies Session implements Entry interface
	var _ Entry = &Session{}

	session := NewSession("Test", "main", "proj", "sid")
	session.Summary = "Test content"
	session.Tags = []string{"tag1", "tag2"}

	// Test interface methods
	if session.GetID() != session.ID {
		t.Errorf("GetID() = %q, want %q", session.GetID(), session.ID)
	}
	if session.GetType() != "session" {
		t.Errorf("GetType() = %q, want 'session'", session.GetType())
	}
	if session.GetTitle() != "Test" {
		t.Errorf("GetTitle() = %q, want 'Test'", session.GetTitle())
	}
	if session.GetContent() == "" {
		t.Errorf("GetContent() should not be empty")
	}
	tags := session.GetTags()
	if len(tags) != 2 || tags[0] != "tag1" || tags[1] != "tag2" {
		t.Errorf("GetTags() = %v, want [tag1, tag2]", tags)
	}
	if session.GetCreated().IsZero() {
		t.Errorf("GetCreated() should return non-zero time")
	}
}

func TestRelatedNote_Struct(t *testing.T) {
	rn := RelatedNote{
		ID:       "note-12345",
		Title:    "Important Note",
		Category: "documentation",
	}

	if rn.ID != "note-12345" {
		t.Errorf("ID = %q, want 'note-12345'", rn.ID)
	}
	if rn.Title != "Important Note" {
		t.Errorf("Title = %q, want 'Important Note'", rn.Title)
	}
	if rn.Category != "documentation" {
		t.Errorf("Category = %q, want 'documentation'", rn.Category)
	}
}