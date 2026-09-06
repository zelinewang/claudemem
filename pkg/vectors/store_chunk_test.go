package vectors

import (
	"math"
	"testing"
)

// staticEmbedder returns a fixed query vector for any input — lets Search
// tests control cosine outcomes exactly, independent of text hashing.
type staticEmbedder struct {
	queryVec []float32
}

func (s *staticEmbedder) Available() error { return nil }
func (s *staticEmbedder) Name() string     { return "static" }
func (s *staticEmbedder) Model() string    { return "static-v1" }
func (s *staticEmbedder) Dimensions() int  { return len(s.queryVec) }
func (s *staticEmbedder) Embed(string, InputType) ([]float32, error) {
	return s.queryVec, nil
}
func (s *staticEmbedder) EmbedBatch(texts []string, _ InputType) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = s.queryVec
	}
	return out, nil
}

func insertChunkRow(t *testing.T, vs *VectorStore, docID string, chunk int, vec []float32) {
	t.Helper()
	_, err := vs.db.Exec(`
		INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
		VALUES (?, ?, 'static', 'static-v1', ?, ?, '2026-01-01T00:00:00Z')`,
		docID, chunk, len(vec), vectorToBlob(vec))
	if err != nil {
		t.Fatalf("insert chunk row: %v", err)
	}
}

// TestVectorStore_SearchAggregatesChunksByMax: a long doc whose only matching
// chunk is NOT chunk 0 must still be found, credited with the chunk's score,
// and appear exactly once.
func TestVectorStore_SearchAggregatesChunksByMax(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, &staticEmbedder{queryVec: []float32{0, 1, 0, 0}})
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}

	// long doc: chunk 0 orthogonal to query (sim 0), chunk 1 identical (sim 1)
	insertChunkRow(t, vs, "long-doc", 0, []float32{1, 0, 0, 0})
	insertChunkRow(t, vs, "long-doc", 1, []float32{0, 1, 0, 0})
	// short doc: halfway similar on its only chunk
	half := float32(math.Sqrt(0.5))
	insertChunkRow(t, vs, "short-doc", 0, []float32{0, half, half, 0})

	results, err := vs.Search("anything — staticEmbedder ignores text", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 docs (deduped by doc_id), got %d: %v", len(results), results)
	}
	if results[0].ID != "long-doc" {
		t.Errorf("top result should be long-doc (max over chunks), got %s", results[0].ID)
	}
	if math.Abs(float64(results[0].Similarity-1.0)) > 1e-5 {
		t.Errorf("long-doc similarity should be 1.0 (best chunk), got %f", results[0].Similarity)
	}
	if results[1].ID != "short-doc" || math.Abs(float64(results[1].Similarity)-math.Sqrt(0.5)) > 1e-4 {
		t.Errorf("short-doc should score sqrt(0.5)≈0.7071, got %s %f", results[1].ID, results[1].Similarity)
	}
}

// TestVectorStore_IndexDocumentChunksLongDoc: one long document produces one
// row per chunk, keyed 0..N-1.
func TestVectorStore_IndexDocumentChunksLongDoc(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, &fakeEmbedder{name: "gemini", model: "test-v1", dim: 4})
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}

	text, _ := longASCIIDoc(120)
	wantChunks := len(ChunkForEmbed(text))
	if wantChunks < 2 {
		t.Fatalf("test doc must chunk (got %d)", wantChunks)
	}

	if err := vs.IndexDocument("doc-long", text); err != nil {
		t.Fatalf("IndexDocument: %v", err)
	}

	var rows int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM vectors WHERE doc_id = 'doc-long' AND backend = 'gemini' AND model = 'test-v1'`).
		Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != wantChunks {
		t.Errorf("expected %d chunk rows, got %d", wantChunks, rows)
	}
	// chunk ids must be exactly 0..N-1
	var maxChunk, distinctChunks int
	if err := db.QueryRow(`
		SELECT COALESCE(MAX(chunk), -1), COUNT(DISTINCT chunk) FROM vectors WHERE doc_id = 'doc-long'`).
		Scan(&maxChunk, &distinctChunks); err != nil {
		t.Fatalf("chunk stats: %v", err)
	}
	if maxChunk != rows-1 || distinctChunks != rows {
		t.Errorf("chunk ids not contiguous 0..N-1: rows=%d max=%d distinct=%d", rows, maxChunk, distinctChunks)
	}
}

// TestVectorStore_IndexDocumentShrinksStaleChunks: re-indexing a doc that now
// fits in one chunk must delete the stale extra chunk rows.
func TestVectorStore_IndexDocumentShrinksStaleChunks(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, &fakeEmbedder{name: "gemini", model: "test-v1", dim: 4})
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}

	longText, _ := longASCIIDoc(120)
	if err := vs.IndexDocument("doc-x", longText); err != nil {
		t.Fatalf("index long: %v", err)
	}
	var before int
	db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = 'doc-x'`).Scan(&before)
	if before < 2 {
		t.Fatalf("setup: expected multi-chunk doc, got %d rows", before)
	}

	if err := vs.IndexDocument("doc-x", "now it is short"); err != nil {
		t.Fatalf("index short: %v", err)
	}
	var after int
	db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = 'doc-x'`).Scan(&after)
	if after != 1 {
		t.Errorf("expected 1 chunk row after shrink, got %d (stale chunks left behind)", after)
	}
}

// TestVectorStore_UpsertDocumentsChunks: the sync-pull path must also chunk
// and must shrink stale rows.
func TestVectorStore_UpsertDocumentsChunks(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	vs, err := NewVectorStore(db, &fakeEmbedder{name: "gemini", model: "test-v1", dim: 4})
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}

	longText, _ := longASCIIDoc(120)
	docs := []Document{
		{ID: "up-long", Text: longText},
		{ID: "up-short", Text: "small note"},
	}
	n, err := vs.UpsertDocuments(docs)
	if err != nil {
		t.Fatalf("UpsertDocuments: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 docs upserted, got %d", n)
	}
	var longRows, shortRows int
	db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = 'up-long'`).Scan(&longRows)
	db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = 'up-short'`).Scan(&shortRows)
	if longRows < 2 {
		t.Errorf("up-long should have multiple chunk rows, got %d", longRows)
	}
	if shortRows != 1 {
		t.Errorf("up-short should have 1 chunk row, got %d", shortRows)
	}

	// Shrink path: upsert the long doc as short text → stale chunks gone.
	if _, err := vs.UpsertDocuments([]Document{{ID: "up-long", Text: "shrunk"}}); err != nil {
		t.Fatalf("upsert shrink: %v", err)
	}
	db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = 'up-long'`).Scan(&longRows)
	if longRows != 1 {
		t.Errorf("after shrink: expected 1 row for up-long, got %d", longRows)
	}
}

// TestHealthCheck_I3CountsDistinctDocs: with v23 chunking, a doc with 3 chunk
// rows must count ONCE toward entry parity.
func TestHealthCheck_I3CountsDistinctDocs(t *testing.T) {
	db, notes, sessions := setupHealthTestDB(t)
	defer db.Close()

	emb := NewTFIDFEmbedder()
	if _, err := NewVectorStore(db, emb); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	seedNote(t, db, notes, "doc1", "First")
	seedNote(t, db, notes, "doc2", "Second")

	// doc1 indexed as 3 chunks, doc2 as 1 — both covered.
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(`
			INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
			VALUES (?, ?, 'tfidf', 'tfidf', 2, X'0000803F0000803F', '2026-01-01T00:00:00Z')`,
			"doc1", i); err != nil {
			t.Fatalf("seed chunk: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
		VALUES ('doc2', 0, 'tfidf', 'tfidf', 2, X'0000803F0000803F', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed chunk: %v", err)
	}

	r, err := CheckHealth(HealthInputs{DB: db, NotesDir: notes, SessionsDir: sessions, Embedder: emb})
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	if !r.I3VectorsMatchActiveBackend {
		t.Errorf("I3 should pass with 2 entries covered by 4 chunk rows (2 distinct docs), issues: %v", r.Issues)
	}
}
