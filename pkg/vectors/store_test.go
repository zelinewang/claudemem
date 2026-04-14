package vectors

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	return db
}

func TestNewVectorStore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("NewVectorStore failed: %v", err)
	}

	count, err := vs.Count()
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 vectors, got %d", count)
	}
}

func TestVectorStore_RebuildIndex(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("NewVectorStore failed: %v", err)
	}

	docs := []Document{
		{ID: "1", Text: "authentication via OAuth tokens and JWT"},
		{ID: "2", Text: "database migration with Alembic and SQLAlchemy"},
		{ID: "3", Text: "API rate limiting and throttling configuration"},
		{ID: "4", Text: "user login and session authentication management"},
		{ID: "5", Text: "PostgreSQL database schema and table design"},
	}

	err = vs.RebuildIndex(docs)
	if err != nil {
		t.Fatalf("RebuildIndex failed: %v", err)
	}

	count, err := vs.Count()
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 vectors, got %d", count)
	}
}

func TestVectorStore_Search(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("NewVectorStore failed: %v", err)
	}

	docs := []Document{
		{ID: "1", Text: "authentication via OAuth tokens and JWT for secure login"},
		{ID: "2", Text: "database migration with Alembic and SQLAlchemy ORM"},
		{ID: "3", Text: "API rate limiting and throttling configuration tuning"},
		{ID: "4", Text: "user login and session authentication management system"},
		{ID: "5", Text: "PostgreSQL database schema table design normalization"},
		{ID: "6", Text: "Redis cache setup configuration performance tuning"},
	}

	err = vs.RebuildIndex(docs)
	if err != nil {
		t.Fatalf("RebuildIndex failed: %v", err)
	}

	// Search for auth-related content
	results, err := vs.Search("authentication login session", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// Top result should be auth-related (ID 1 or 4)
	topID := results[0].ID
	if topID != "1" && topID != "4" {
		t.Errorf("expected top result to be auth-related (1 or 4), got %s", topID)
	}

	// Verify results are sorted by similarity descending
	for i := 1; i < len(results); i++ {
		if results[i].Similarity > results[i-1].Similarity {
			t.Errorf("results not sorted: index %d similarity %f > index %d similarity %f",
				i, results[i].Similarity, i-1, results[i-1].Similarity)
		}
	}
}

func TestVectorStore_Search_EmptyIndex(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("NewVectorStore failed: %v", err)
	}

	results, err := vs.Search("some query", 10)
	if err != nil {
		t.Fatalf("Search on empty index should not error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results from empty index, got %v", results)
	}
}

func TestVectorStore_RemoveDocument(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("NewVectorStore failed: %v", err)
	}

	docs := []Document{
		{ID: "1", Text: "authentication tokens"},
		{ID: "2", Text: "database migration"},
	}

	err = vs.RebuildIndex(docs)
	if err != nil {
		t.Fatalf("RebuildIndex failed: %v", err)
	}

	err = vs.RemoveDocument("1")
	if err != nil {
		t.Fatalf("RemoveDocument failed: %v", err)
	}

	count, err := vs.Count()
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 vector after removal, got %d", count)
	}
}

func TestVectorStore_PersistState(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Build index and persist state
	vs1, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("NewVectorStore failed: %v", err)
	}

	docs := []Document{
		{ID: "1", Text: "authentication tokens OAuth JWT secure login"},
		{ID: "2", Text: "database migration Alembic SQLAlchemy schema"},
		{ID: "3", Text: "API rate limiting throttling performance"},
	}

	err = vs1.RebuildIndex(docs)
	if err != nil {
		t.Fatalf("RebuildIndex failed: %v", err)
	}

	// Create new VectorStore on the same DB — should load persisted state
	vs2, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("NewVectorStore (reload) failed: %v", err)
	}

	// Search should work on restored state
	results, err := vs2.Search("authentication login", 3)
	if err != nil {
		t.Fatalf("Search after reload failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results after state restoration, got none")
	}
}

func TestVectorToBlob_Roundtrip(t *testing.T) {
	original := []float32{1.0, -0.5, 0.3, 0.0, -1.0}
	blob := vectorToBlob(original)
	restored := blobToVector(blob)

	if len(restored) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(restored), len(original))
	}

	for i := range original {
		if original[i] != restored[i] {
			t.Errorf("element %d mismatch: %f vs %f", i, original[i], restored[i])
		}
	}
}
