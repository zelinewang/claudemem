package storage

import (
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

func TestVerifyIntegrity_CleanStore(t *testing.T) {
	store := setupTestStore(t)

	// Add distinct notes in different categories
	note1 := models.NewNote("api", "REST API Design Patterns", "GET and POST endpoints")
	_, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() 1 failed: %v", err)
	}

	note2 := models.NewNote("docs", "User Onboarding Guide", "How to use the system")
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() 2 failed: %v", err)
	}

	// Add sessions — NewSession(title, branch, project, sessionID)
	session1 := models.NewSession("Sprint Planning", "main", "backend", "sid-1")
	_, err = store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() 1 failed: %v", err)
	}

	session2 := models.NewSession("Code Review", "develop", "frontend", "sid-2")
	_, err = store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() 2 failed: %v", err)
	}

	// Verify integrity
	result, err := store.VerifyIntegrity()
	if err != nil {
		t.Fatalf("VerifyIntegrity() failed: %v", err)
	}

	if !result.InSync {
		t.Errorf("InSync = false, want true for clean store")
	}
	if len(result.OrphanedEntries) != 0 {
		t.Errorf("OrphanedEntries count = %d, want 0", len(result.OrphanedEntries))
	}
}

func TestReindex_RebuildsSearch(t *testing.T) {
	store := setupTestStore(t)

	// Add entries with VERY distinct names to avoid dedup
	note1 := models.NewNote("astronomy", "Supernova Classification System", "reindextest keyword here")
	_, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() 1 failed: %v", err)
	}

	note2 := models.NewNote("biology", "Photosynthesis Mechanism Details", "another reindextest content")
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() 2 failed: %v", err)
	}

	// Search should work initially
	results1, err := store.Search("reindextest", "", 10)
	if err != nil {
		t.Fatalf("Search() before deletion failed: %v", err)
	}
	if len(results1) < 2 {
		t.Errorf("Initial search results = %d, want >= 2", len(results1))
	}

	// Manually delete all FTS entries
	_, err = store.db.Exec("DELETE FROM memory_fts")
	if err != nil {
		t.Fatalf("Failed to delete FTS entries: %v", err)
	}

	// Search should return nothing now
	results2, err := store.Search("reindextest", "", 10)
	if err != nil {
		// FTS5 may error on empty table — that's ok
		t.Logf("Search after FTS deletion returned error (expected): %v", err)
	} else if len(results2) != 0 {
		t.Errorf("Search after FTS deletion = %d results, want 0", len(results2))
	}

	// Reindex
	count, err := store.Reindex()
	if err != nil {
		t.Fatalf("Reindex() failed: %v", err)
	}
	if count < 2 {
		t.Errorf("Reindex() returned %d, want >= 2", count)
	}

	// Search should work again
	results3, err := store.Search("reindextest", "", 10)
	if err != nil {
		t.Fatalf("Search() after reindex failed: %v", err)
	}
	if len(results3) < 2 {
		t.Errorf("Search after reindex = %d results, want >= 2", len(results3))
	}
}

func TestReindex_ReturnsCount(t *testing.T) {
	store := setupTestStore(t)

	// Add distinct notes (very different titles/categories to avoid dedup)
	categories := []string{"astronomy", "biology", "chemistry", "geology", "physics"}
	for i, cat := range categories {
		note := models.NewNote(cat, cat+" Encyclopedia Entry "+string(rune('A'+i)), "Content for "+cat)
		_, err := store.AddNote(note)
		if err != nil {
			t.Fatalf("AddNote() %d failed: %v", i, err)
		}
	}

	// Reindex and check count matches at least the number of notes
	count, err := store.Reindex()
	if err != nil {
		t.Fatalf("Reindex() failed: %v", err)
	}
	if count < 5 {
		t.Errorf("Reindex() count = %d, want >= 5", count)
	}
}

func TestStats_Accurate(t *testing.T) {
	store := setupTestStore(t)

	// Add 3 notes in 2 categories (distinct titles)
	note1 := models.NewNote("api", "REST Endpoint Documentation", "API docs")
	note1.Tags = []string{"rest", "api"}
	_, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() 1 failed: %v", err)
	}

	note2 := models.NewNote("api", "GraphQL Schema Reference", "GraphQL docs")
	note2.Tags = []string{"graphql", "api"}
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() 2 failed: %v", err)
	}

	note3 := models.NewNote("docs", "User Manual Complete Guide", "End user docs")
	note3.Tags = []string{"manual", "docs"}
	_, err = store.AddNote(note3)
	if err != nil {
		t.Fatalf("AddNote() 3 failed: %v", err)
	}

	// Add 2 sessions — NewSession(title, branch, project, sessionID)
	session1 := models.NewSession("Sprint Planning Discussion", "main", "backend-svc", "sid-1")
	_, err = store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() 1 failed: %v", err)
	}

	session2 := models.NewSession("Code Review Meeting", "develop", "frontend-app", "sid-2")
	_, err = store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() 2 failed: %v", err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats() failed: %v", err)
	}

	if stats.TotalNotes != 3 {
		t.Errorf("TotalNotes = %d, want 3", stats.TotalNotes)
	}
	if stats.TotalSessions != 2 {
		t.Errorf("TotalSessions = %d, want 2", stats.TotalSessions)
	}
	if stats.StorageSize <= 0 {
		t.Errorf("StorageSize = %d, want > 0", stats.StorageSize)
	}
}

func TestStats_EmptyStore(t *testing.T) {
	store := setupTestStore(t)

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats() on empty store failed: %v", err)
	}

	if stats.TotalNotes != 0 {
		t.Errorf("TotalNotes = %d, want 0", stats.TotalNotes)
	}
	if stats.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", stats.TotalSessions)
	}
}
