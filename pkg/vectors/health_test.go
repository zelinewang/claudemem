package vectors

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupHealthTestDB creates a temp DB with entries + memory_fts + v22 vectors
// tables, plus temp notes/sessions directories. Returns paths and cleanup.
func setupHealthTestDB(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	db := setupTestDB(t)

	schema := `
	CREATE TABLE entries (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		title TEXT NOT NULL,
		filepath TEXT NOT NULL
	);
	CREATE VIRTUAL TABLE memory_fts USING fts5(id UNINDEXED, title, content, tags);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// Also make sure the v22 vectors schema exists (initSchema creates it)
	if _, err := NewVectorStore(db, NewTFIDFEmbedder()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	base := t.TempDir()
	notes := filepath.Join(base, "notes")
	sessions := filepath.Join(base, "sessions")
	os.MkdirAll(notes, 0755)
	os.MkdirAll(sessions, 0755)
	return db, notes, sessions
}

func seedNote(t *testing.T, db *sql.DB, notesDir, id, title string) {
	t.Helper()
	path := filepath.Join(notesDir, id+".md")
	if err := os.WriteFile(path, []byte("content for "+id), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries (id, type, title, filepath) VALUES (?, 'note', ?, ?)`,
		id, title, "notes/"+id+".md"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, '')`,
		id, title, "content"); err != nil {
		t.Fatal(err)
	}
}

// TestHealthCheck_HealthyState verifies that when filesystem, entries, FTS,
// and vectors all agree, the check returns Healthy()=true with no issues.
func TestHealthCheck_HealthyState(t *testing.T) {
	db, notes, sessions := setupHealthTestDB(t)
	defer db.Close()

	seedNote(t, db, notes, "doc1", "First Note")
	seedNote(t, db, notes, "doc2", "Second Note")

	// Build TF-IDF vectors matching entries
	vs, _ := NewVectorStore(db, NewTFIDFEmbedder())
	_ = vs.RebuildIndex([]Document{
		{ID: "doc1", Text: "first note content"},
		{ID: "doc2", Text: "second note content"},
	})

	r, err := CheckHealth(HealthInputs{
		DB: db, NotesDir: notes, SessionsDir: sessions,
		Embedder: NewTFIDFEmbedder(),
	})
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}

	if !r.Healthy() {
		t.Errorf("expected healthy state, got issues: %v", r.Issues)
	}
	if r.MarkdownFiles != 2 || r.EntriesTotal != 2 || r.FTSTotal != 2 {
		t.Errorf("count mismatch: md=%d entries=%d fts=%d", r.MarkdownFiles, r.EntriesTotal, r.FTSTotal)
	}
}

// TestHealthCheck_DetectsFTSDrift inserts an entry without its FTS row
// and verifies I2 fires.
func TestHealthCheck_DetectsFTSDrift(t *testing.T) {
	db, notes, sessions := setupHealthTestDB(t)
	defer db.Close()

	seedNote(t, db, notes, "doc1", "First")
	// Intentionally skip FTS insert
	path := filepath.Join(notes, "orphan.md")
	os.WriteFile(path, []byte("x"), 0644)
	db.Exec(`INSERT INTO entries (id, type, title, filepath) VALUES ('orphan', 'note', 'Orphan', 'notes/orphan.md')`)
	// No FTS row for 'orphan'

	r, _ := CheckHealth(HealthInputs{
		DB: db, NotesDir: notes, SessionsDir: sessions,
	})
	if r.I2EntriesMatchesFTS {
		t.Error("expected I2 drift (missing FTS row), got pass")
	}
	found := false
	for _, issue := range r.Issues {
		if contains1(issue, "I2:") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an I2 issue in report, got: %v", r.Issues)
	}
}

// TestHealthCheck_DetectsMissingVectorsForBackend verifies I3 fires when
// entries exist but the active backend has no embeddings for them
// (post-backend-switch pre-reindex state).
func TestHealthCheck_DetectsMissingVectorsForBackend(t *testing.T) {
	db, notes, sessions := setupHealthTestDB(t)
	defer db.Close()

	seedNote(t, db, notes, "doc1", "First")
	seedNote(t, db, notes, "doc2", "Second")
	// Entries + FTS present. Vectors table intentionally empty.

	r, _ := CheckHealth(HealthInputs{
		DB: db, NotesDir: notes, SessionsDir: sessions,
		Embedder: NewTFIDFEmbedder(),
	})
	if r.I3VectorsMatchActiveBackend {
		t.Error("expected I3 drift (no vectors for active backend), got pass")
	}
}

// TestHealthCheckDeep_DetectsOrphanRows exercises I4.
func TestHealthCheckDeep_DetectsOrphanRows(t *testing.T) {
	db, notes, sessions := setupHealthTestDB(t)
	defer db.Close()

	seedNote(t, db, notes, "doc1", "Valid")
	// Insert an orphan FTS row pointing at a non-existent entry
	db.Exec(`INSERT INTO memory_fts (id, title, content, tags) VALUES ('ghost', 'Ghost', 'x', '')`)

	r, err := CheckHealthDeep(HealthInputs{
		DB: db, NotesDir: notes, SessionsDir: sessions,
		Embedder: NewTFIDFEmbedder(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.I4NoOrphanRows {
		t.Error("expected I4 drift (ghost FTS row), got pass")
	}
}

// helper (avoid importing strings just for this)
func contains1(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// seedVector inserts one chunk-0 row for the tfidf backend used by the health
// fixtures; docID may deliberately not exist in entries (an orphan).
func seedVector(t *testing.T, db *sql.DB, docID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
		VALUES (?, 0, 'tfidf', 'tfidf', 2, X'0000803F0000803F', '2026-01-01T00:00:00Z')`, docID); err != nil {
		t.Fatalf("seed vector %s: %v", docID, err)
	}
}

// TestHealthCheck_I3IgnoresOrphanVectorRows_NoMasking: an orphan vector row
// (doc_id whose entry was deleted) must not be counted as coverage — before
// the fix, 2 covered docs + 1 orphan == 3 entries made I3 PASS although doc3
// had no vector.
func TestHealthCheck_I3IgnoresOrphanVectorRows_NoMasking(t *testing.T) {
	db, notes, sessions := setupHealthTestDB(t)
	defer db.Close()
	emb := NewTFIDFEmbedder()

	seedNote(t, db, notes, "doc1", "First")
	seedNote(t, db, notes, "doc2", "Second")
	seedNote(t, db, notes, "doc3", "Third")
	seedVector(t, db, "doc1")
	seedVector(t, db, "doc2")
	seedVector(t, db, "ghost") // parent entry deleted

	r, err := CheckHealth(HealthInputs{DB: db, NotesDir: notes, SessionsDir: sessions, Embedder: emb})
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	if r.I3VectorsMatchActiveBackend {
		t.Fatalf("I3 passed although doc3 has no vector — the orphan row masked it; issues: %v", r.Issues)
	}
	joined := strings.Join(r.Issues, "\n")
	if !strings.Contains(joined, "missing 1") {
		t.Fatalf("I3 message should name 1 missing document, got: %q", joined)
	}
}

// TestHealthCheck_I3PassesWithOrphanWhenAllEntriesCovered reproduces the
// 2026-09-08 Mac state: every entry has a vector plus one orphan row. I3 must
// PASS (coverage is complete); the orphan belongs to I4 (deep check) only, so
// `repair` deletes one row instead of re-embedding the whole store.
func TestHealthCheck_I3PassesWithOrphanWhenAllEntriesCovered(t *testing.T) {
	db, notes, sessions := setupHealthTestDB(t)
	defer db.Close()
	emb := NewTFIDFEmbedder()

	seedNote(t, db, notes, "doc1", "First")
	seedNote(t, db, notes, "doc2", "Second")
	seedVector(t, db, "doc1")
	seedVector(t, db, "doc2")
	seedVector(t, db, "ghost")

	r, err := CheckHealthDeep(HealthInputs{DB: db, NotesDir: notes, SessionsDir: sessions, Embedder: emb})
	if err != nil {
		t.Fatalf("CheckHealthDeep: %v", err)
	}
	if !r.I3VectorsMatchActiveBackend {
		t.Fatalf("I3 should pass: both entries covered, orphan must not count; issues: %v", r.Issues)
	}
	if r.I4NoOrphanRows {
		t.Fatalf("I4 should report the orphan vector row; issues: %v", r.Issues)
	}
}
