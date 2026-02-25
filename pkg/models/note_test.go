package models

import (
	"testing"
)

func TestNewNote_MetadataInitialized(t *testing.T) {
	note := NewNote("api-specs", "TikTok API Limits", "Rate limit is 100 requests per minute")

	// Check basic fields
	if note.ID == "" {
		t.Errorf("ID should not be empty")
	}
	if note.Type != "note" {
		t.Errorf("Type should be 'note', got %q", note.Type)
	}
	if note.Category != "api-specs" {
		t.Errorf("Category should be 'api-specs', got %q", note.Category)
	}
	if note.Title != "TikTok API Limits" {
		t.Errorf("Title should be 'TikTok API Limits', got %q", note.Title)
	}
	if note.Content != "Rate limit is 100 requests per minute" {
		t.Errorf("Content mismatch, got %q", note.Content)
	}

	// Check Tags is initialized (not nil)
	if note.Tags == nil {
		t.Errorf("Tags should be initialized (not nil)")
	}
	if len(note.Tags) != 0 {
		t.Errorf("Tags should be empty slice, got %v", note.Tags)
	}

	// Check Metadata is initialized (not nil)
	if note.Metadata == nil {
		t.Errorf("Metadata should be initialized (not nil)")
	}
	if len(note.Metadata) != 0 {
		t.Errorf("Metadata should be empty map, got %v", note.Metadata)
	}

	// Verify we can write to metadata without panic
	note.Metadata["session_id"] = "ref-123"
	if note.Metadata["session_id"] != "ref-123" {
		t.Errorf("Should be able to write to Metadata map")
	}

	// Check timestamps are set
	if note.Created.IsZero() {
		t.Errorf("Created time should be set")
	}
	if note.Updated.IsZero() {
		t.Errorf("Updated time should be set")
	}
	if !note.Created.Equal(note.Updated) {
		t.Errorf("Initially, Created and Updated should be equal")
	}
}

func TestNote_EntryInterface(t *testing.T) {
	// This should compile - verifies Note implements Entry interface
	var _ Entry = &Note{}

	note := NewNote("docs", "Test Note", "Some content here")
	note.Tags = []string{"important", "api"}

	// Test interface methods
	if note.GetID() != note.ID {
		t.Errorf("GetID() = %q, want %q", note.GetID(), note.ID)
	}
	if note.GetType() != "note" {
		t.Errorf("GetType() = %q, want 'note'", note.GetType())
	}
	if note.GetTitle() != "Test Note" {
		t.Errorf("GetTitle() = %q, want 'Test Note'", note.GetTitle())
	}
	if note.GetContent() != "Some content here" {
		t.Errorf("GetContent() = %q, want 'Some content here'", note.GetContent())
	}
	tags := note.GetTags()
	if len(tags) != 2 || tags[0] != "important" || tags[1] != "api" {
		t.Errorf("GetTags() = %v, want [important, api]", tags)
	}
	if note.GetCreated().IsZero() {
		t.Errorf("GetCreated() should return non-zero time")
	}
}