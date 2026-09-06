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

	// --- Verify: the vectors table should now be v23-shaped (v21→v22→v23 chain) ---
	kind, err := detectVectorsSchema(db)
	if err != nil {
		t.Fatalf("detect schema: %v", err)
	}
	if kind != schemaV23 {
		t.Fatalf("expected v23 schema after migration, got %d", kind)
	}

	// Preserved rows must carry chunk=0 (they were single-vector docs).
	var nonZeroChunks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE chunk != 0`).Scan(&nonZeroChunks); err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if nonZeroChunks != 0 {
		t.Fatalf("expected all preserved rows at chunk=0, got %d non-zero", nonZeroChunks)
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

func (f *fakeEmbedder) Available() error { return nil }
func (f *fakeEmbedder) Name() string     { return f.name }
func (f *fakeEmbedder) Model() string    { return f.model }
func (f *fakeEmbedder) Dimensions() int  { return f.dim }
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

// secondaryIndexNames returns the names of the idx_* indexes attached to the
// given table. Migrations that RENAME the vectors table carry its indexes
// along, so asserting on tbl_name is what proves they ended up on the live table.
func secondaryIndexNames(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name LIKE 'idx_%'`, table)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan index name: %v", err)
		}
		names[n] = true
	}
	return names
}

// TestMigrateV22ToV23_KeepsSecondaryIndexes reproduces the Codex P2 finding on
// the v23 PR: ALTER TABLE vectors RENAME TO vectors_v22 carries
// idx_vectors_backend / idx_vectors_doc over to the renamed table, so the
// CREATE INDEX IF NOT EXISTS statements in v23Schema are no-ops and DROP TABLE
// vectors_v22 deletes both indexes. Every store migrated by the pre-release
// build (the gengar-eu hub, 2026-09-01) ended up with only the PK autoindex.
func TestMigrateV22ToV23_KeepsSecondaryIndexes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE vector_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("setup meta: %v", err)
	}
	if _, err := db.Exec(v22Schema); err != nil {
		t.Fatalf("setup v22: %v", err)
	}
	blob := make([]byte, 4*4)
	if _, err := db.Exec(`INSERT INTO vectors (doc_id, backend, model, dim, vector, created_at)
		VALUES ('docA', 'static', 'static-v1', 4, ?, '2026-09-01T00:00:00Z')`, blob); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	if got := secondaryIndexNames(t, db, "vectors"); !got["idx_vectors_backend"] || !got["idx_vectors_doc"] {
		t.Fatalf("v22 fixture should start with both secondary indexes, got %v", got)
	}

	if _, err := NewVectorStore(db, NewTFIDFEmbedder()); err != nil {
		t.Fatalf("NewVectorStore (migration): %v", err)
	}

	kind, err := detectVectorsSchema(db)
	if err != nil || kind != schemaV23 {
		t.Fatalf("expected v23 after migration, got kind=%d err=%v", kind, err)
	}
	got := secondaryIndexNames(t, db, "vectors")
	if !got["idx_vectors_backend"] || !got["idx_vectors_doc"] {
		t.Fatalf("secondary indexes lost in v22→v23 migration: have %v on vectors", got)
	}
	var stale int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'vectors_v22'`).Scan(&stale); err != nil {
		t.Fatalf("check renamed table: %v", err)
	}
	if stale != 0 {
		t.Fatalf("vectors_v22 should be dropped after migration")
	}
}

// TestInitSchema_V23RecreatesMissingIndexes covers stores that were already
// migrated by a build without the fix above: opening them must heal the
// missing secondary indexes without touching any row.
func TestInitSchema_V23RecreatesMissingIndexes(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE vector_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE vectors (
			doc_id     TEXT    NOT NULL,
			chunk      INTEGER NOT NULL,
			backend    TEXT    NOT NULL,
			model      TEXT    NOT NULL,
			dim        INTEGER NOT NULL,
			vector     BLOB    NOT NULL,
			created_at TEXT    NOT NULL,
			PRIMARY KEY (doc_id, chunk, backend, model)
		);`); err != nil {
		t.Fatalf("setup index-less v23: %v", err)
	}
	blob := make([]byte, 4*4)
	if _, err := db.Exec(`INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
		VALUES ('docA', 0, 'static', 'static-v1', 4, ?, '2026-09-01T00:00:00Z')`, blob); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	if got := secondaryIndexNames(t, db, "vectors"); len(got) != 0 {
		t.Fatalf("fixture should have no secondary indexes, got %v", got)
	}

	if _, err := NewVectorStore(db, NewTFIDFEmbedder()); err != nil {
		t.Fatalf("NewVectorStore (open v23): %v", err)
	}

	got := secondaryIndexNames(t, db, "vectors")
	if !got["idx_vectors_backend"] || !got["idx_vectors_doc"] {
		t.Fatalf("opening an index-less v23 store must recreate the secondary indexes, have %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vectors`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows must be untouched, count=%d err=%v", n, err)
	}
}
