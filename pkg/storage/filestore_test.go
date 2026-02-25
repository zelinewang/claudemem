package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// setupTestStore creates a temporary FileStore for testing
func setupTestStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestFileStore_NoteWithSessionID(t *testing.T) {
	store := setupTestStore(t)

	// Create note with session_id in metadata
	note := models.NewNote("api-docs", "Rate Limits", "100 requests per minute")
	note.Metadata["session_id"] = "ref-1"

	// Add the note
	result, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}
	if result.NoteID == "" {
		t.Errorf("AddNote() should return NoteID")
	}

	// Retrieve the note
	retrieved, err := store.GetNote(result.NoteID)
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}

	// Verify session_id is preserved
	if retrieved.Metadata["session_id"] != "ref-1" {
		t.Errorf("Metadata['session_id'] = %q, want 'ref-1'", retrieved.Metadata["session_id"])
	}
}

func TestFileStore_NoteDedupPreservesMetadata(t *testing.T) {
	store := setupTestStore(t)

	// Add first note with session_id
	note1 := models.NewNote("workflows", "Auth Flow", "JWT process v1")
	note1.Metadata["session_id"] = "ref-1"
	note1.Metadata["version"] = "1"

	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}

	// Add second note with same category+title but different session_id
	note2 := models.NewNote("workflows", "Auth Flow", "JWT process v2")
	note2.Metadata["session_id"] = "ref-2"
	note2.Metadata["version"] = "2"
	note2.Metadata["extra"] = "new-field"

	result2, err := store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Should be merged (deduped)
	if result2.Action != "merged" {
		t.Errorf("Action = %q, want 'merged'", result2.Action)
	}
	if result2.NoteID != result1.NoteID {
		t.Errorf("Should return same NoteID for merged note")
	}

	// Retrieve and verify metadata was updated
	retrieved, err := store.GetNote(result1.NoteID)
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}

	// Newer metadata should overwrite
	if retrieved.Metadata["session_id"] != "ref-2" {
		t.Errorf("Metadata['session_id'] = %q, want 'ref-2' (newer)", retrieved.Metadata["session_id"])
	}
	if retrieved.Metadata["version"] != "2" {
		t.Errorf("Metadata['version'] = %q, want '2'", retrieved.Metadata["version"])
	}
	if retrieved.Metadata["extra"] != "new-field" {
		t.Errorf("Metadata['extra'] = %q, want 'new-field'", retrieved.Metadata["extra"])
	}
	// Dedup merge APPENDS content (not replaces), so both versions should be present
	if !strings.Contains(retrieved.Content, "JWT process v1") {
		t.Errorf("Content should contain original 'JWT process v1', got %q", retrieved.Content)
	}
	if !strings.Contains(retrieved.Content, "JWT process v2") {
		t.Errorf("Content should contain appended 'JWT process v2', got %q", retrieved.Content)
	}
}

func TestFileStore_SessionWithAllNewFields(t *testing.T) {
	store := setupTestStore(t)

	// Create session with all new fields populated
	session := models.NewSession("Debug Session", "fix/bug", "myapp", "sid-123")
	session.Summary = "Fixed authentication bug"
	session.WhatHappened = "Users reported login failures.\nInvestigated logs.\nFound timezone issue."
	session.Insights = []string{
		"Always use UTC",
		"Add more logging",
		"Test with different timezones",
	}
	session.RelatedNotes = []models.RelatedNote{
		{ID: "note-abc", Title: "Auth Best Practices", Category: "security"},
		{ID: "note-def", Title: "Timezone Guide", Category: "docs"},
	}

	// Save session
	result, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Retrieve session
	retrieved, err := store.GetSession(result.SessionID)
	if err != nil {
		t.Fatalf("GetSession() failed: %v", err)
	}

	// Verify all new fields are preserved
	if retrieved.WhatHappened != session.WhatHappened {
		t.Errorf("WhatHappened not preserved.\nGot: %q\nWant: %q",
			retrieved.WhatHappened, session.WhatHappened)
	}

	if len(retrieved.Insights) != 3 {
		t.Errorf("Insights length = %d, want 3", len(retrieved.Insights))
	} else {
		for i, insight := range session.Insights {
			if retrieved.Insights[i] != insight {
				t.Errorf("Insights[%d] = %q, want %q", i, retrieved.Insights[i], insight)
			}
		}
	}

	if len(retrieved.RelatedNotes) != 2 {
		t.Errorf("RelatedNotes length = %d, want 2", len(retrieved.RelatedNotes))
	} else {
		for i, rn := range session.RelatedNotes {
			// Full UUID should be preserved (no truncation in markdown storage)
			if retrieved.RelatedNotes[i].ID != rn.ID {
				t.Errorf("RelatedNotes[%d].ID = %q, want %q (full UUID should be preserved)",
					i, retrieved.RelatedNotes[i].ID, rn.ID)
			}
			if retrieved.RelatedNotes[i].Title != rn.Title {
				t.Errorf("RelatedNotes[%d].Title = %q, want %q",
					i, retrieved.RelatedNotes[i].Title, rn.Title)
			}
			if retrieved.RelatedNotes[i].Category != rn.Category {
				t.Errorf("RelatedNotes[%d].Category = %q, want %q",
					i, retrieved.RelatedNotes[i].Category, rn.Category)
			}
		}
	}
}

func TestFileStore_SessionDedup_UpdatesNewFields(t *testing.T) {
	store := setupTestStore(t)

	// Same session_id = same conversation → should merge
	session1 := models.NewSession("Work Session", "main", "project", "same-work-session")
	session1.WhatHappened = "version 1 of what happened"
	session1.Insights = []string{"insight 1"}

	result1, err := store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() first failed: %v", err)
	}

	// Same session_id → same conversation → merge
	session2 := models.NewSession("Work Session Updated", "main", "project", "same-work-session")
	session2.WhatHappened = "version 2 of what happened"
	session2.Insights = []string{"insight 1", "insight 2"}
	session2.RelatedNotes = []models.RelatedNote{
		{ID: "new-note", Title: "New Reference", Category: "docs"},
	}

	result2, err := store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() second failed: %v", err)
	}

	// Should be updated (deduped)
	if result2.Action != "updated" {
		t.Errorf("Action = %q, want 'updated'", result2.Action)
	}

	// Retrieve and verify new fields were updated
	retrieved, err := store.GetSession(result1.SessionID)
	if err != nil {
		t.Fatalf("GetSession() failed: %v", err)
	}

	// Session dedup now MERGES (not overwrites) — both versions should be present
	if !strings.Contains(retrieved.WhatHappened, "version 1 of what happened") {
		t.Errorf("WhatHappened should contain v1, got %q", retrieved.WhatHappened)
	}
	if !strings.Contains(retrieved.WhatHappened, "version 2 of what happened") {
		t.Errorf("WhatHappened should contain v2, got %q", retrieved.WhatHappened)
	}
	// Insights merged: v1 had 1 item, v2 has 2 → total should be 2 (v1's "insight 1" deduped with v2's "insight 1")
	if len(retrieved.Insights) < 2 {
		t.Errorf("Insights should have >= 2 merged items, got %d: %v", len(retrieved.Insights), retrieved.Insights)
	}
	if len(retrieved.RelatedNotes) != 1 {
		t.Errorf("RelatedNotes should have 1 item, got %d", len(retrieved.RelatedNotes))
	}
}

func TestFileStore_Search_FindsWhatHappened(t *testing.T) {
	store := setupTestStore(t)

	// Add session with unique word in WhatHappened
	session := models.NewSession("Test", "main", "proj", "sid")
	session.WhatHappened = "Investigated the xyzabc123 error in production"

	_, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Force reindex to ensure search index is updated
	if _, err := store.Reindex(); err != nil {
		t.Fatalf("Reindex() failed: %v", err)
	}

	// Search for unique word
	results, err := store.Search("xyzabc123", "", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Search should find 1 result, got %d", len(results))
	}
	if len(results) > 0 && results[0].Type != "session" {
		t.Errorf("Result should be a session, got %q", results[0].Type)
	}
}

func TestFileStore_Search_FindsInsight(t *testing.T) {
	store := setupTestStore(t)

	// Add session with unique insight
	session := models.NewSession("Learning", "main", "proj", "sid")
	session.Insights = []string{
		"Regular insight",
		"The uniqueinsightword789 pattern is important",
		"Another insight",
	}

	_, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Force reindex
	if _, err := store.Reindex(); err != nil {
		t.Fatalf("Reindex() failed: %v", err)
	}

	// Search for unique word in insight
	results, err := store.Search("uniqueinsightword789", "", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Search should find 1 result, got %d", len(results))
	}
}

func TestFileStore_Search_FindsRelatedNoteTitle(t *testing.T) {
	store := setupTestStore(t)

	// Add session with related note having unique title
	session := models.NewSession("Work", "main", "proj", "sid")
	session.RelatedNotes = []models.RelatedNote{
		{ID: "note1", Title: "Common Note", Category: "docs"},
		{ID: "note2", Title: "The uniquenoteref456 Document", Category: "api"},
	}

	_, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Force reindex
	if _, err := store.Reindex(); err != nil {
		t.Fatalf("Reindex() failed: %v", err)
	}

	// Search for unique word in related note title
	results, err := store.Search("uniquenoteref456", "", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Search should find 1 result, got %d", len(results))
	}
}

func TestFileStore_OldSessionBackwardCompat(t *testing.T) {
	store := setupTestStore(t)

	// Directly write an old-format session markdown file
	oldMarkdown := `---
id: old-format-session
type: session
title: Old Session
date: "2024-01-10"
branch: legacy
project: oldapp
session_id: old-sid
tags: [old]
created: "2024-01-10T12:00:00Z"
---

## Summary
This is an old format session without new fields.

## Key Decisions
- Use old format
- Keep compatibility

## What Changed
- ` + "`legacy.go`" + ` — Updated for compatibility
`

	// Write file directly to sessions directory (flat, not nested)
	sessionFile := filepath.Join(store.sessionsDir, "2024-01-10_legacy_old-format_old-session.md")
	if err := os.WriteFile(sessionFile, []byte(oldMarkdown), 0600); err != nil {
		t.Fatalf("Failed to write old session file: %v", err)
	}

	// Reindex to pick up the manually written file
	if _, err := store.Reindex(); err != nil {
		t.Fatalf("Reindex() should handle old format files, got error: %v", err)
	}

	// Try to retrieve the session
	retrieved, err := store.GetSession("old-format-session")
	if err != nil {
		t.Fatalf("GetSession() should handle old format, got error: %v", err)
	}

	// Verify old fields are present
	if retrieved.Summary != "This is an old format session without new fields." {
		t.Errorf("Summary not parsed correctly from old format")
	}
	if len(retrieved.Decisions) != 2 {
		t.Errorf("Decisions not parsed, got %d items", len(retrieved.Decisions))
	}

	// Verify new fields have safe defaults
	if retrieved.WhatHappened != "" {
		t.Errorf("WhatHappened should be empty for old format, got %q", retrieved.WhatHappened)
	}
	if retrieved.Insights == nil {
		t.Errorf("Insights should not be nil even for old format")
	}
	if len(retrieved.Insights) != 0 {
		t.Errorf("Insights should be empty for old format, got %v", retrieved.Insights)
	}
	if retrieved.RelatedNotes == nil {
		t.Errorf("RelatedNotes should not be nil even for old format")
	}
	if len(retrieved.RelatedNotes) != 0 {
		t.Errorf("RelatedNotes should be empty for old format, got %v", retrieved.RelatedNotes)
	}
}

func TestFileStore_ListNotes(t *testing.T) {
	store := setupTestStore(t)

	// Add multiple notes (use distinct titles to avoid fuzzy dedup)
	note1 := models.NewNote("api", "REST Endpoint Guidelines", "Content about REST")
	note2 := models.NewNote("docs", "Database Migration Steps", "Content about migrations")
	note3 := models.NewNote("api", "Authentication Token Handling", "Content about tokens")

	for _, note := range []*models.Note{note1, note2, note3} {
		if _, err := store.AddNote(note); err != nil {
			t.Fatalf("AddNote() failed: %v", err)
		}
	}

	// List all notes
	allNotes, err := store.ListNotes("")
	if err != nil {
		t.Fatalf("ListNotes() failed: %v", err)
	}
	if len(allNotes) != 3 {
		t.Errorf("ListNotes() returned %d notes, want 3", len(allNotes))
	}

	// List by category
	apiNotes, err := store.ListNotes("api")
	if err != nil {
		t.Fatalf("ListNotes() with category failed: %v", err)
	}
	if len(apiNotes) != 2 {
		t.Errorf("ListNotes('api') returned %d notes, want 2", len(apiNotes))
	}
}

func TestFileStore_ListSessions(t *testing.T) {
	store := setupTestStore(t)

	// Add sessions on different dates
	session1 := models.NewSession("Session 1", "main", "proj1", "sid1")
	session1.Date = "2024-01-15"

	session2 := models.NewSession("Session 2", "main", "proj1", "sid2")
	session2.Date = "2024-01-16"

	session3 := models.NewSession("Session 3", "feature", "proj2", "sid3")
	session3.Date = "2024-01-15"

	for _, session := range []*models.Session{session1, session2, session3} {
		if _, err := store.SaveSession(session); err != nil {
			t.Fatalf("SaveSession() failed: %v", err)
		}
	}

	// List all sessions
	allSessions, err := store.ListSessions(SessionListOpts{Limit: 10})
	if err != nil {
		t.Fatalf("ListSessions() failed: %v", err)
	}
	if len(allSessions) < 2 { // Might be deduped
		t.Errorf("ListSessions() returned %d sessions, want at least 2", len(allSessions))
	}

	// Sessions should be sorted by date (newest first)
	if len(allSessions) >= 2 {
		if allSessions[0].Date < allSessions[1].Date {
			t.Errorf("Sessions should be sorted newest first")
		}
	}
}

func TestFileStore_DeleteNote(t *testing.T) {
	store := setupTestStore(t)

	// Add a note
	note := models.NewNote("test", "Delete Me", "Content")
	result, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	// Verify it exists
	_, err = store.GetNote(result.NoteID)
	if err != nil {
		t.Fatalf("Note should exist before deletion")
	}

	// Delete it
	err = store.DeleteNote(result.NoteID)
	if err != nil {
		t.Fatalf("DeleteNote() failed: %v", err)
	}

	// Verify it's gone
	_, err = store.GetNote(result.NoteID)
	if err == nil {
		t.Errorf("Note should not exist after deletion")
	}
}

