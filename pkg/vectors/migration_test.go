package vectors

import (
	"database/sql"
	"encoding/binary"
	"math"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateV21ToV22_PreservesVectors simulates the MacBook upgrade path:
// a pre-refactor DB already has a flat (id, vector) vectors table populated
// with Ollama/nomic-embed-text 768-dim rows. The migration must:
//
//  1. Detect the v21 schema via PRAGMA table_info
//  2. Read vector_meta.index_backend to learn the originating backend
//  3. Copy every row into the new schema, tagged with (ollama, nomic-embed-text, 768)
//  4. Preserve the exact vector bytes so CosineSimilarity results don't change
//  5. Drop the old table
//
// If this test fails, real users' Ollama-embedded knowledge gets lost on upgrade.
func TestMigrateV21ToV22_PreservesVectors(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// --- Set up a realistic v21 database ---
	if _, err := db.Exec(`
		CREATE TABLE vector_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE vectors (id TEXT PRIMARY KEY, vector BLOB NOT NULL);
	`); err != nil {
		t.Fatalf("setup v21: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vector_meta (key, value) VALUES ('index_backend', 'ollama:nomic-embed-text')`); err != nil {
		t.Fatalf("seed meta: %v", err)
	}
	// Two 768-dim vectors — simulate real Ollama-embedded content
	docA := make768DimVector(0.42)
	docB := make768DimVector(-0.17)
	blobA := make([]byte, 768*4)
	blobB := make([]byte, 768*4)
	for i, v := range docA {
		binary.LittleEndian.PutUint32(blobA[i*4:], math.Float32bits(v))
	}
	for i, v := range docB {
		binary.LittleEndian.PutUint32(blobB[i*4:], math.Float32bits(v))
	}
	if _, err := db.Exec(`INSERT INTO vectors (id, vector) VALUES (?, ?), (?, ?)`,
		"docA", blobA, "docB", blobB); err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	// --- Run the migration via NewVectorStore (it calls initSchema which detects + migrates) ---
	vs, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("NewVectorStore (migration): %v", err)
	}

	// --- Verify: the vectors table should now have 2 v22-shaped rows ---
	kind, err := detectVectorsSchema(db)
	if err != nil {
		t.Fatalf("detect schema: %v", err)
	}
	if kind != schemaV22 {
		t.Fatalf("expected v22 schema after migration, got %d", kind)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vectors`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 preserved rows, got %d", count)
	}

	// Tags must match the legacy backend tuple
	rows, err := db.Query(`SELECT doc_id, backend, model, dim FROM vectors ORDER BY doc_id`)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer rows.Close()
	var seen []string
	for rows.Next() {
		var docID, backend, model string
		var dim int
		if err := rows.Scan(&docID, &backend, &model, &dim); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if backend != "ollama" {
			t.Errorf("doc %s: expected backend=ollama, got %s", docID, backend)
		}
		if model != "nomic-embed-text" {
			t.Errorf("doc %s: expected model=nomic-embed-text, got %s", docID, model)
		}
		if dim != 768 {
			t.Errorf("doc %s: expected dim=768, got %d", docID, dim)
		}
		seen = append(seen, docID)
	}
	if len(seen) != 2 || seen[0] != "docA" || seen[1] != "docB" {
		t.Errorf("expected [docA docB], got %v", seen)
	}

	// vectors_v21 table must be dropped
	var tbl string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='vectors_v21'`).Scan(&tbl)
	if err != sql.ErrNoRows {
		t.Errorf("vectors_v21 table should be dropped after migration (got %q, err=%v)", tbl, err)
	}

	// Re-running NewVectorStore must be idempotent (no re-migration)
	_, err = NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("second NewVectorStore call: %v", err)
	}
	_ = vs
}

// TestMigrateV21ToV22_LegacyTFIDFBackend covers the case where vector_meta
// has the bare string "tfidf" (very old installs before 66c3fdc added the
// backend:model tuple format).
func TestMigrateV21ToV22_LegacyTFIDFBackend(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE vector_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE vectors (id TEXT PRIMARY KEY, vector BLOB NOT NULL);
		INSERT INTO vector_meta (key, value) VALUES ('index_backend', 'tfidf');
	`); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// 100-dim TF-IDF vector
	vec := make([]float32, 100)
	vec[5] = 1.0
	blob := make([]byte, 100*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(v))
	}
	if _, err := db.Exec(`INSERT INTO vectors (id, vector) VALUES (?, ?)`, "xyz", blob); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("migration: %v", err)
	}

	var backend, model string
	var dim int
	if err := db.QueryRow(`SELECT backend, model, dim FROM vectors WHERE doc_id='xyz'`).
		Scan(&backend, &model, &dim); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if backend != "tfidf" || model != "tfidf" || dim != 100 {
		t.Errorf("expected (tfidf, tfidf, 100), got (%s, %s, %d)", backend, model, dim)
	}
}

// TestPerDocMetadata_MixedBackendsCoexist validates the cross-machine sync
// invariant: two different (backend, model) tuples can have vectors for
// the same doc_id, and queries filter by active backend — so the two
// don't pollute each other.
func TestPerDocMetadata_MixedBackendsCoexist(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Machine A — TF-IDF
	vsTFIDF, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err != nil {
		t.Fatalf("init tfidf: %v", err)
	}
	if err := vsTFIDF.RebuildIndex([]Document{
		{ID: "doc1", Text: "authentication tokens OAuth JWT secure"},
		{ID: "doc2", Text: "database migration schema indexing"},
	}); err != nil {
		t.Fatalf("rebuild tfidf: %v", err)
	}

	// Simulate Machine B joining — a different embedder with its own rows
	// for the SAME doc_ids. Use a synthetic "fake-cloud" embedder so we can
	// control the backend name/model/dim for the assertion.
	fake := &fakeEmbedder{name: "fake-cloud", model: "fake-v1", dim: 4}
	vsFake, err := NewVectorStore(db, fake)
	if err != nil {
		t.Fatalf("init fake: %v", err)
	}
	if err := vsFake.RebuildIndex([]Document{
		{ID: "doc1", Text: "some cloud-embedded content"},
		{ID: "doc2", Text: "other cloud-embedded content"},
	}); err != nil {
		t.Fatalf("rebuild fake: %v", err)
	}

	// Total rows should be 4: 2 docs × 2 backends
	total, err := vsFake.CountAll()
	if err != nil || total != 4 {
		t.Errorf("expected 4 total rows after dual-backend reindex, got %d (err=%v)", total, err)
	}

	// Each VectorStore's Count() sees only its own backend
	a, _ := vsTFIDF.Count()
	b, _ := vsFake.Count()
	if a != 2 || b != 2 {
		t.Errorf("expected (tfidf=2, fake=2), got (tfidf=%d, fake=%d)", a, b)
	}
}

// --- Test helpers ---

func make768DimVector(seed float32) []float32 {
	v := make([]float32, 768)
	for i := range v {
		// deterministic but non-trivial — depends on seed and position
		v[i] = seed * float32(math.Sin(float64(i)*0.01))
	}
	return v
}

// fakeEmbedder returns a fixed-dim vector whose content is a hash of the
// input string, so it's deterministic across runs and different-per-text.
type fakeEmbedder struct {
	name, model string
	dim         int
}

func (f *fakeEmbedder) Available() error        { return nil }
func (f *fakeEmbedder) Name() string            { return f.name }
func (f *fakeEmbedder) Model() string           { return f.model }
func (f *fakeEmbedder) Dimensions() int         { return f.dim }
func (f *fakeEmbedder) Embed(text string, _ InputType) ([]float32, error) {
	vec := make([]float32, f.dim)
	for i := 0; i < f.dim; i++ {
		// Fill with a function of the text + position so different texts
		// get different vectors. Cheap hash, not cryptographic.
		h := 0
		for _, r := range text {
			h = h*31 + int(r) + i
		}
		vec[i] = float32(h%1000) / 1000.0
	}
	return vec, nil
}
func (f *fakeEmbedder) EmbedBatch(texts []string, t InputType) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, txt := range texts {
		v, _ := f.Embed(txt, t)
		out[i] = v
	}
	return out, nil
}
