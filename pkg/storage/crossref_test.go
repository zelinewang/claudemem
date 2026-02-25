package storage

import (
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

func TestCrossRef_NoteSessionRoundTrip(t *testing.T) {
	store := setupTestStore(t)

	// Step 1: Create a note with session_id metadata
	note := models.NewNote("architecture", "System Design", "Microservices architecture overview")
	note.Tags = []string{"design", "microservices"}
	note.Metadata = map[string]string{
		"session_id": "sess-001",
		"author":     "John Doe",
	}
	noteResult, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	// Step 2: Create a session referencing this note
	session := models.NewSession("Design Review", "2024-01-15", "main", "backend")
	session.SessionID = "sess-001"
	session.Summary = "Reviewed microservices architecture"
	session.RelatedNotes = []models.RelatedNote{
		{
			ID:       noteResult.NoteID,
			Title:    "System Design",
			Category: "architecture",
		},
	}
	sessionResult, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Step 3: Retrieve the session and verify it has the related note
	retrievedSession, err := store.GetSession(sessionResult.SessionID)
	if err != nil {
		t.Fatalf("GetSession() failed: %v", err)
	}

	if len(retrievedSession.RelatedNotes) != 1 {
		t.Errorf("RelatedNotes count = %d, want 1", len(retrievedSession.RelatedNotes))
	}
	if len(retrievedSession.RelatedNotes) > 0 {
		relNote := retrievedSession.RelatedNotes[0]
		if relNote.ID != noteResult.NoteID {
			t.Errorf("RelatedNote ID = %q, want %q", relNote.ID, noteResult.NoteID)
		}
		if relNote.Title != "System Design" {
			t.Errorf("RelatedNote Title = %q, want 'System Design'", relNote.Title)
		}
		if relNote.Category != "architecture" {
			t.Errorf("RelatedNote Category = %q, want 'architecture'", relNote.Category)
		}
	}

	// Step 4: Retrieve the note and verify it has session_id
	retrievedNote, err := store.GetNote(noteResult.NoteID)
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}

	if retrievedNote.Metadata["session_id"] != "sess-001" {
		t.Errorf("Note session_id = %q, want 'sess-001'", retrievedNote.Metadata["session_id"])
	}

	// Step 5: Verify both entities point to each other
	if retrievedSession.SessionID != "sess-001" {
		t.Errorf("Session ID mismatch")
	}
	if retrievedNote.Metadata["session_id"] != retrievedSession.SessionID {
		t.Errorf("Note session_id doesn't match session ID")
	}
}

func TestCrossRef_FullUUIDPreserved(t *testing.T) {
	store := setupTestStore(t)

	// Create a note with full UUID
	note := models.NewNote("docs", "API Reference", "REST API documentation")
	noteResult, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	// Full UUID should be 36 chars (8-4-4-4-12 format)
	if len(noteResult.NoteID) != 36 {
		t.Errorf("Note ID length = %d, want 36 (full UUID)", len(noteResult.NoteID))
	}

	// Create session with related note using full UUID
	session := models.NewSession("API Review", "2024-01-15", "main", "api-project")
	session.RelatedNotes = []models.RelatedNote{
		{
			ID:       noteResult.NoteID, // Full UUID
			Title:    "API Reference",
			Category: "docs",
		},
	}
	sessionResult, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Retrieve session and check related note ID is full UUID
	retrievedSession, err := store.GetSession(sessionResult.SessionID)
	if err != nil {
		t.Fatalf("GetSession() failed: %v", err)
	}

	if len(retrievedSession.RelatedNotes) != 1 {
		t.Fatalf("Expected 1 related note, got %d", len(retrievedSession.RelatedNotes))
	}

	relatedNoteID := retrievedSession.RelatedNotes[0].ID
	if relatedNoteID != noteResult.NoteID {
		t.Errorf("Related note ID = %q, want %q", relatedNoteID, noteResult.NoteID)
	}
	if len(relatedNoteID) != 36 {
		t.Errorf("Related note ID length = %d, want 36 (full UUID preserved)", len(relatedNoteID))
	}
}

func TestCrossRef_PrefixLookup(t *testing.T) {
	store := setupTestStore(t)

	// Create a note
	note := models.NewNote("config", "Database Config", "Connection pool settings")
	noteResult, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	fullID := noteResult.NoteID
	prefix8 := fullID[:8]

	// Retrieve note using first 8 chars of UUID
	retrievedByPrefix, err := store.GetNote(prefix8)
	if err != nil {
		t.Fatalf("GetNote() with prefix failed: %v", err)
	}

	// Should get the same note
	if retrievedByPrefix.ID != fullID {
		t.Errorf("Retrieved note ID = %q, want %q", retrievedByPrefix.ID, fullID)
	}
	if retrievedByPrefix.Title != "Database Config" {
		t.Errorf("Retrieved note Title = %q, want 'Database Config'", retrievedByPrefix.Title)
	}

	// Also verify full ID lookup still works
	retrievedByFull, err := store.GetNote(fullID)
	if err != nil {
		t.Fatalf("GetNote() with full ID failed: %v", err)
	}

	if retrievedByFull.ID != fullID {
		t.Errorf("Full ID lookup failed")
	}
}

func TestCrossRef_StaleReference(t *testing.T) {
	store := setupTestStore(t)

	// Create a note
	note := models.NewNote("temp", "Temporary Note", "This will be deleted")
	noteResult, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	// Create session referencing the note
	session := models.NewSession("Planning", "2024-01-15", "main", "project")
	session.RelatedNotes = []models.RelatedNote{
		{
			ID:       noteResult.NoteID,
			Title:    "Temporary Note",
			Category: "temp",
		},
	}
	sessionResult, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Delete the note
	err = store.DeleteNote(noteResult.NoteID)
	if err != nil {
		t.Fatalf("DeleteNote() failed: %v", err)
	}

	// Retrieve session - should still work
	retrievedSession, err := store.GetSession(sessionResult.SessionID)
	if err != nil {
		t.Fatalf("GetSession() failed after note deletion: %v", err)
	}

	// Session should still list the related note (session is independent)
	if len(retrievedSession.RelatedNotes) != 1 {
		t.Errorf("RelatedNotes count = %d, want 1 (preserved after deletion)", len(retrievedSession.RelatedNotes))
	}

	// But trying to get the note should fail
	_, err = store.GetNote(noteResult.NoteID)
	if err == nil {
		t.Errorf("GetNote() should fail for deleted note")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Error should mention 'not found': %v", err)
	}
}

func TestCrossRef_MultipleNotesOneSession(t *testing.T) {
	store := setupTestStore(t)

	sessionID := "multi-ref-001"

	// Create 3 notes all referencing the same session
	note1 := models.NewNote("api", "Auth API", "Authentication endpoints")
	note1.Metadata = map[string]string{"session_id": sessionID}
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() 1 failed: %v", err)
	}

	note2 := models.NewNote("docs", "Auth Docs", "How to use authentication")
	note2.Metadata = map[string]string{"session_id": sessionID}
	result2, err := store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() 2 failed: %v", err)
	}

	note3 := models.NewNote("tests", "Auth Tests", "Test cases for auth")
	note3.Metadata = map[string]string{"session_id": sessionID}
	result3, err := store.AddNote(note3)
	if err != nil {
		t.Fatalf("AddNote() 3 failed: %v", err)
	}

	// Create session referencing all 3 notes
	session := models.NewSession("Auth Implementation", "2024-01-15", "main", "backend")
	session.SessionID = sessionID
	session.Summary = "Implemented authentication system"
	session.RelatedNotes = []models.RelatedNote{
		{ID: result1.NoteID, Title: "Auth API", Category: "api"},
		{ID: result2.NoteID, Title: "Auth Docs", Category: "docs"},
		{ID: result3.NoteID, Title: "Auth Tests", Category: "tests"},
	}
	sessionResult, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	// Retrieve session and verify all 3 notes are linked
	retrievedSession, err := store.GetSession(sessionResult.SessionID)
	if err != nil {
		t.Fatalf("GetSession() failed: %v", err)
	}

	if len(retrievedSession.RelatedNotes) != 3 {
		t.Errorf("RelatedNotes count = %d, want 3", len(retrievedSession.RelatedNotes))
	}

	// Build map of related note IDs
	relatedIDs := make(map[string]bool)
	for _, rn := range retrievedSession.RelatedNotes {
		relatedIDs[rn.ID] = true
	}

	// Verify all 3 notes are referenced
	if !relatedIDs[result1.NoteID] {
		t.Errorf("Missing note 1 in related notes")
	}
	if !relatedIDs[result2.NoteID] {
		t.Errorf("Missing note 2 in related notes")
	}
	if !relatedIDs[result3.NoteID] {
		t.Errorf("Missing note 3 in related notes")
	}

	// Verify each note points back to the session
	for i, noteID := range []string{result1.NoteID, result2.NoteID, result3.NoteID} {
		retrieved, err := store.GetNote(noteID)
		if err != nil {
			t.Fatalf("GetNote() %d failed: %v", i+1, err)
		}
		if retrieved.Metadata["session_id"] != sessionID {
			t.Errorf("Note %d session_id = %q, want %q", i+1, retrieved.Metadata["session_id"], sessionID)
		}
	}
}

func TestCrossRef_SessionPrefixLookup(t *testing.T) {
	store := setupTestStore(t)

	// Create a session
	session := models.NewSession("Sprint Planning", "2024-01-15", "main", "webapp")
	session.Summary = "Planned sprint tasks"
	sessionResult, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	fullID := sessionResult.SessionID
	prefix8 := fullID[:8]

	// Retrieve session using first 8 chars of UUID
	retrievedByPrefix, err := store.GetSession(prefix8)
	if err != nil {
		t.Fatalf("GetSession() with prefix failed: %v", err)
	}

	// Should get the same session
	if retrievedByPrefix.ID != fullID {
		t.Errorf("Retrieved session ID = %q, want %q", retrievedByPrefix.ID, fullID)
	}
	if retrievedByPrefix.Title != "Sprint Planning" {
		t.Errorf("Retrieved session Title = %q, want 'Sprint Planning'", retrievedByPrefix.Title)
	}

	// Also verify full ID lookup still works
	retrievedByFull, err := store.GetSession(fullID)
	if err != nil {
		t.Fatalf("GetSession() with full ID failed: %v", err)
	}

	if retrievedByFull.ID != fullID {
		t.Errorf("Full ID lookup failed")
	}
}

func TestCrossRef_UpdatePreservesReferences(t *testing.T) {
	store := setupTestStore(t)

	// Create a note with session reference
	note := models.NewNote("workflow", "CI/CD Pipeline", "GitHub Actions workflow")
	note.Metadata = map[string]string{
		"session_id": "cicd-001",
		"version":    "1",
	}
	noteResult, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	// Update the note (change content but keep session_id)
	retrievedNote, err := store.GetNote(noteResult.NoteID)
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}

	retrievedNote.Content = "Updated GitHub Actions workflow with new steps"
	retrievedNote.Metadata["version"] = "2"
	// session_id should remain "cicd-001"

	err = store.UpdateNote(retrievedNote)
	if err != nil {
		t.Fatalf("UpdateNote() failed: %v", err)
	}

	// Retrieve updated note
	updatedNote, err := store.GetNote(noteResult.NoteID)
	if err != nil {
		t.Fatalf("GetNote() after update failed: %v", err)
	}

	// Verify session_id is preserved
	if updatedNote.Metadata["session_id"] != "cicd-001" {
		t.Errorf("session_id = %q, want 'cicd-001' (preserved after update)", updatedNote.Metadata["session_id"])
	}
	if updatedNote.Metadata["version"] != "2" {
		t.Errorf("version = %q, want '2'", updatedNote.Metadata["version"])
	}
	if !strings.Contains(updatedNote.Content, "Updated GitHub Actions") {
		t.Errorf("Content not updated")
	}
}