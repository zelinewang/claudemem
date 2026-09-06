package vectors

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// VectorStore manages per-document embedding vectors stored in SQLite.
//
// Schema (v23 — see docs/HYBRID_EMBEDDING_PLAN.md):
//
//	vectors(doc_id, chunk, backend, model, dim, vector, created_at)
//	PK(doc_id, chunk, backend, model)
//
// v23 adds chunk: documents longer than the embedder's input budget are split
// by ChunkForEmbed into overlapping chunks, one vector row each; Search
// aggregates chunk similarities per doc_id with max. Short documents keep a
// single chunk=0 row, identical to v22 behavior.
//
// Rationale for the composite key: two machines can share the same markdown
// corpus via git and each embed with a different backend (e.g., web_dev uses
// Gemini cloud; MacBook uses local Ollama). Both sets of vectors coexist in
// the same table; each machine's searches filter by its active backend.
// This also lets "switch backend" be an O(new rows) operation instead of
// O(full reindex) — old rows stay queryable if you switch back.
//
// The store holds exactly one Embedder (the active backend). Read-path
// failures (search) bubble up ErrBackendUnavailable to the caller — no
// silent fallback. Write-path failures (index) are logged and skipped so
// a down backend does not block note/session creation; `claudemem repair`
// heals missing vectors later.
type VectorStore struct {
	db       *sql.DB
	embedder Embedder
}

// NewVectorStore creates a store bound to a specific Embedder. The embedder
// is not pinged here; callers that care about freshness should call
// embedder.Available() themselves before constructing the store.
func NewVectorStore(db *sql.DB, embedder Embedder) (*VectorStore, error) {
	if embedder == nil {
		return nil, fmt.Errorf("vectors.NewVectorStore: embedder must not be nil (pass NewTFIDFEmbedder for tests)")
	}
	vs := &VectorStore{db: db, embedder: embedder}
	if err := vs.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to init vector schema: %w", err)
	}
	// TF-IDF embedders persist their vocabulary in vector_meta; restore it.
	if tf, ok := embedder.(*TFIDFEmbedder); ok {
		vs.loadTFIDFState(tf)
	}
	return vs, nil
}

// Embedder returns the active backend. Exposed so callers (e.g., health
// checks, CLI "stats" output) can inspect Name()/Model()/Dimensions()
// without reaching into the store's internals.
func (vs *VectorStore) Embedder() Embedder { return vs.embedder }

// EmbeddingBackend returns the "backend:model" tuple string used in
// diagnostic output. Preserved for compatibility with existing callers
// (filestore_vectors.go, stats command).
func (vs *VectorStore) EmbeddingBackend() string {
	return vs.embedder.Name() + ":" + vs.embedder.Model()
}

// initSchema creates or migrates the vectors + vector_meta tables.
// On first run with the old v21 schema (id, vector), this migrates rows
// in place, tagging them with whatever backend previously produced them.
// This preserves MacBook's real Ollama vectors across the upgrade.
func (vs *VectorStore) initSchema() error {
	// vector_meta is stable across versions
	if _, err := vs.db.Exec(`
		CREATE TABLE IF NOT EXISTS vector_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`); err != nil {
		return err
	}

	kind, err := detectVectorsSchema(vs.db)
	if err != nil {
		return err
	}
	switch kind {
	case schemaNone:
		return vs.createV23Schema()
	case schemaV21:
		if err := vs.migrateV21ToV22(); err != nil {
			return err
		}
		return vs.migrateV22ToV23()
	case schemaV22:
		return vs.migrateV22ToV23()
	case schemaV23:
		return vs.ensureVectorsIndexes()
	}
	return fmt.Errorf("unknown vectors schema kind %d", kind)
}

type vectorsSchemaKind int

const (
	schemaNone vectorsSchemaKind = iota
	schemaV21                    // (id, vector)
	schemaV22                    // (doc_id, backend, model, dim, vector, created_at)
	schemaV23                    // (doc_id, chunk, backend, model, dim, vector, created_at)
)

func detectVectorsSchema(db *sql.DB) (vectorsSchemaKind, error) {
	rows, err := db.Query(`PRAGMA table_info(vectors)`)
	if err != nil {
		return schemaNone, fmt.Errorf("pragma table_info: %w", err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return schemaNone, err
		}
		cols = append(cols, name)
	}
	if len(cols) == 0 {
		return schemaNone, nil
	}
	has := map[string]bool{}
	for _, c := range cols {
		has[c] = true
	}
	if has["doc_id"] && has["backend"] && has["model"] && has["chunk"] {
		return schemaV23, nil
	}
	if has["doc_id"] && has["backend"] && has["model"] {
		return schemaV22, nil
	}
	if has["id"] && has["vector"] {
		return schemaV21, nil
	}
	return schemaNone, fmt.Errorf("vectors table has unexpected columns: %v", cols)
}

const v22Schema = `
CREATE TABLE vectors (
	doc_id     TEXT    NOT NULL,
	backend    TEXT    NOT NULL,
	model      TEXT    NOT NULL,
	dim        INTEGER NOT NULL,
	vector     BLOB    NOT NULL,
	created_at TEXT    NOT NULL,
	PRIMARY KEY (doc_id, backend, model)
);
CREATE INDEX IF NOT EXISTS idx_vectors_backend ON vectors(backend, model);
CREATE INDEX IF NOT EXISTS idx_vectors_doc ON vectors(doc_id);
`

// vectorsIndexes is the idempotent secondary-index DDL for the live vectors
// table. It is run on every open of an already-v23 store (so stores migrated
// by a build that lost the indexes heal on their next start) and is what the
// migrations rely on after the rename dance below.
const vectorsIndexes = `
CREATE INDEX IF NOT EXISTS idx_vectors_backend ON vectors(backend, model);
CREATE INDEX IF NOT EXISTS idx_vectors_doc ON vectors(doc_id);
`

const v23Schema = `
CREATE TABLE vectors (
	doc_id     TEXT    NOT NULL,
	chunk      INTEGER NOT NULL,
	backend    TEXT    NOT NULL,
	model      TEXT    NOT NULL,
	dim        INTEGER NOT NULL,
	vector     BLOB    NOT NULL,
	created_at TEXT    NOT NULL,
	PRIMARY KEY (doc_id, chunk, backend, model)
);
CREATE INDEX IF NOT EXISTS idx_vectors_backend ON vectors(backend, model);
CREATE INDEX IF NOT EXISTS idx_vectors_doc ON vectors(doc_id);
`

func (vs *VectorStore) createV23Schema() error {
	_, err := vs.db.Exec(v23Schema)
	return err
}

// ensureVectorsIndexes recreates the secondary indexes if a previous build
// lost them (see migrateV22ToV23). Idempotent and row-preserving.
func (vs *VectorStore) ensureVectorsIndexes() error {
	if _, err := vs.db.Exec(vectorsIndexes); err != nil {
		return fmt.Errorf("ensure vectors indexes: %w", err)
	}
	return nil
}

// migrateV22ToV23 adds the chunk column preservation-first: existing rows
// keep their vectors as chunk 0 (they remain searchable immediately); a later
// `reindex --vectors` re-chunks long documents for full coverage.
func (vs *VectorStore) migrateV22ToV23() error {
	tx, err := vs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE vectors RENAME TO vectors_v22`); err != nil {
		return fmt.Errorf("rename v22: %w", err)
	}
	// The rename carries idx_vectors_backend / idx_vectors_doc over to
	// vectors_v22. Drop them here so v23Schema can create them on the new
	// table; otherwise IF NOT EXISTS skips them and DROP TABLE vectors_v22
	// below silently deletes both.
	if _, err := tx.Exec(`
		DROP INDEX IF EXISTS idx_vectors_backend;
		DROP INDEX IF EXISTS idx_vectors_doc`); err != nil {
		return fmt.Errorf("drop v22 indexes: %w", err)
	}
	if _, err := tx.Exec(v23Schema); err != nil {
		return fmt.Errorf("create v23: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
		SELECT doc_id, 0, backend, model, dim, vector, created_at FROM vectors_v22`); err != nil {
		return fmt.Errorf("copy rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE vectors_v22`); err != nil {
		return fmt.Errorf("drop v22: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr,
		"claudemem: migrated vectors table from v22 to v23 (%d existing rows kept as chunk 0)\n",
		countRows(vs.db, "vectors"))
	return nil
}

// migrateV21ToV22 is the preservation-first migration. Rather than dropping
// the old flat (id, vector) table and forcing a costly re-embed, we tag
// every existing row with the backend that produced it and copy forward.
// If vector_meta.index_backend is absent (pre-66c3fdc installs), we fall
// back to a conservative ("tfidf", "tfidf") tuple — users can re-run
// setup to re-embed with a real backend later. A REAL DB error reading
// the meta row is fatal: silently falling back to tfidf would mis-tag
// MacBook's real Ollama embeddings.
func (vs *VectorStore) migrateV21ToV22() error {
	indexedBackend, err := readMeta(vs.db, "index_backend")
	if err != nil {
		return fmt.Errorf("read vector_meta.index_backend (required for migration): %w", err)
	}
	backend, model := parseBackendTuple(indexedBackend)

	dim, err := firstRowDim(vs.db)
	if err != nil {
		return fmt.Errorf("inspect existing vector dim: %w", err)
	}

	tx, err := vs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE vectors RENAME TO vectors_v21`); err != nil {
		return fmt.Errorf("rename v21: %w", err)
	}
	if _, err := tx.Exec(v22Schema); err != nil {
		return fmt.Errorf("create v22: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO vectors (doc_id, backend, model, dim, vector, created_at)
		SELECT id, ?, ?, ?, vector, ? FROM vectors_v21`,
		backend, model, dim, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("copy rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE vectors_v21`); err != nil {
		return fmt.Errorf("drop v21: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr,
		"claudemem: migrated vectors table from v21 to v22 (tagged %d existing rows as %s:%s @%dd)\n",
		countRows(vs.db, "vectors"), backend, model, dim)
	return nil
}

// parseBackendTuple splits "ollama:nomic-embed-text" into ("ollama",
// "nomic-embed-text"). A bare string (legacy "tfidf") gets duplicated.
func parseBackendTuple(s string) (backend, model string) {
	if s == "" {
		return "tfidf", "tfidf"
	}
	if i := strings.Index(s, ":"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, s
}

// readMeta returns the vector_meta value for a key. Distinguishes between
// "not set" (returns empty string + nil) and "DB error" (returns empty +
// error). The migration path MUST treat a real DB error as fatal because
// silently returning "" would mis-tag every preserved vector as tfidf.
func readMeta(db *sql.DB, key string) (string, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM vector_meta WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// readMetaOrEmpty is the forgiving variant for callers that don't care
// about distinguishing "missing" from "error" (e.g. diagnostic health
// output where we'd rather show "unknown" than abort).
func readMetaOrEmpty(db *sql.DB, key string) string {
	v, _ := readMeta(db, key)
	return v
}

func firstRowDim(db *sql.DB) (int, error) {
	var blob []byte
	err := db.QueryRow(`SELECT vector FROM vectors LIMIT 1`).Scan(&blob)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(blob) / 4, nil
}

func countRows(db *sql.DB, table string) int {
	var n int
	db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n)
	return n
}

// IndexDocument adds/updates a single document's vector row(s) under the
// active (backend, model). Long documents are split by ChunkForEmbed into one
// row per chunk. Forgiving by design: embed failures are logged and skipped
// so a down backend does not block the write path. The health subsystem (P5)
// heals missing rows later via `claudemem repair`.
func (vs *VectorStore) IndexDocument(id, text string) error {
	chunks := ChunkForEmbed(text)
	vecs, err := vs.embedder.EmbedBatch(chunks, InputTypeDocument)
	if err != nil {
		fmt.Fprintf(os.Stderr, "index %s skipped (%s:%s embed failed: %v) — run `claudemem repair` to retry\n",
			shortID(id), vs.embedder.Name(), vs.embedder.Model(), err)
		return nil
	}
	// All-or-nothing per doc: a partially indexed doc would look covered to
	// MissingDocumentIDs while silently losing its tail.
	for _, vec := range vecs {
		if vec == nil {
			return nil // TF-IDF returns nil before vocabulary is built
		}
	}

	backend, model := vs.embedder.Name(), vs.embedder.Model()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := vs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Delete-then-insert shrinks away stale chunk rows when a doc that used
	// to be N chunks now fits in fewer.
	if _, err := tx.Exec(`DELETE FROM vectors WHERE doc_id = ? AND backend = ? AND model = ?`,
		id, backend, model); err != nil {
		return fmt.Errorf("clear stale chunks for %s: %w", shortID(id), err)
	}
	for i, vec := range vecs {
		if _, err := tx.Exec(`
			INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, i, backend, model, len(vec), vectorToBlob(vec), now); err != nil {
			return fmt.Errorf("insert chunk %d for %s: %w", i, shortID(id), err)
		}
	}
	return tx.Commit()
}

// RemoveDocument removes ALL vectors for a given doc (across any backends
// that have rows for it). This matches the filesystem truth — if a markdown
// note is deleted, its embeddings are garbage regardless of backend.
func (vs *VectorStore) RemoveDocument(id string) error {
	_, err := vs.db.Exec(`DELETE FROM vectors WHERE doc_id = ?`, id)
	return err
}

// SearchResult is a single semantic search hit.
type SearchResult struct {
	ID         string  `json:"id"`
	Similarity float32 `json:"similarity"`
}

// Search performs semantic search over vectors produced by the ACTIVE
// (backend, model). Read-path contract: if the backend is unreachable,
// this propagates the error to the caller (no silent fallback). The CLI
// layer translates it into fail-loud / interactive recovery (P4).
func (vs *VectorStore) Search(query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	queryVec, err := vs.embedder.Embed(TruncateForEmbed(query), InputTypeQuery)
	if err != nil {
		return nil, err // bubble up; do NOT degrade
	}
	if queryVec == nil {
		return nil, nil
	}

	rows, err := vs.db.Query(`
		SELECT doc_id, vector FROM vectors
		WHERE backend = ? AND model = ?`,
		vs.embedder.Name(), vs.embedder.Model())
	if err != nil {
		return nil, fmt.Errorf("query vectors: %w", err)
	}
	defer rows.Close()

	// v23: a long document owns one row per chunk — aggregate to the doc by
	// max similarity (a hit anywhere in the doc credits the doc).
	best := map[string]float32{}
	queryDim := len(queryVec)
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		docVec := blobToVector(blob)
		if len(docVec) != queryDim {
			continue
		}
		sim := CosineSimilarity(queryVec, docVec)
		if sim > best[id] {
			best[id] = sim
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iter: %w", err)
	}

	var results []SearchResult
	for id, sim := range best {
		if sim > 0.01 {
			results = append(results, SearchResult{ID: id, Similarity: sim})
		}
	}

	sortResultsBySimilarity(results)
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// Document is the input shape for RebuildIndex.
type Document struct {
	ID   string
	Text string
}

type indexedDocumentVector struct {
	docID  string
	chunk  int
	vector []float32
}

// RebuildIndex embeds every document with the ACTIVE backend+model, tagging
// each row accordingly. Existing rows under OTHER (backend, model) tuples
// are preserved (cross-machine / switch-back use cases). Only rows under
// the CURRENT backend+model are wiped and rebuilt.
//
// For TF-IDF embedders, this also rebuilds the vocabulary from the corpus.
// It prepares all embeddings before mutating SQLite so a provider failure
// cannot leave the active backend with a partial or empty index.
func (vs *VectorStore) RebuildIndex(documents []Document) error {
	if err := vs.embedder.Available(); err != nil {
		return err
	}

	if tf, ok := vs.embedder.(*TFIDFEmbedder); ok {
		corpus := make([]string, len(documents))
		for i, d := range documents {
			corpus[i] = d.Text
		}
		tf.BuildVocab(corpus)
	}

	indexed, err := vs.embedDocumentsForRebuild(documents)
	if err != nil {
		return err
	}

	tx, err := vs.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM vectors WHERE backend = ? AND model = ?`,
		vs.embedder.Name(), vs.embedder.Model()); err != nil {
		return fmt.Errorf("clear active backend rows: %w", err)
	}

	backend, model := vs.embedder.Name(), vs.embedder.Model()
	if len(indexed) > 0 {
		stmt, err := tx.Prepare(`
			INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare insert: %w", err)
		}
		defer stmt.Close()

		now := time.Now().UTC().Format(time.RFC3339)
		for _, doc := range indexed {
			if _, err := stmt.Exec(doc.docID, doc.chunk, backend, model, len(doc.vector), vectorToBlob(doc.vector), now); err != nil {
				return fmt.Errorf("insert vector for %s chunk %d: %w", doc.docID, doc.chunk, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if tf, ok := vs.embedder.(*TFIDFEmbedder); ok {
		vs.saveTFIDFState(tf)
	}
	vs.saveIndexBackend()
	return nil
}

func (vs *VectorStore) embedDocumentsForRebuild(documents []Document) ([]indexedDocumentVector, error) {
	if len(documents) == 0 {
		return nil, nil
	}

	const batchSize = 50
	backend, model := vs.embedder.Name(), vs.embedder.Model()

	// Flatten documents into chunk jobs: a long document contributes one job
	// per chunk (v23), a short one exactly one job — batching stays dense.
	type chunkJob struct {
		docID string
		chunk int
		text  string
	}
	var jobs []chunkJob
	for _, d := range documents {
		for i, c := range ChunkForEmbed(d.Text) {
			jobs = append(jobs, chunkJob{docID: d.ID, chunk: i, text: c})
		}
	}

	indexed := make([]indexedDocumentVector, 0, len(jobs))

	for i := 0; i < len(jobs); i += batchSize {
		end := i + batchSize
		if end > len(jobs) {
			end = len(jobs)
		}
		batch := jobs[i:end]

		texts := make([]string, len(batch))
		for j, jb := range batch {
			texts[j] = jb.text
		}
		embeddings, err := vs.embedder.EmbedBatch(texts, InputTypeDocument)
		if err != nil {
			return nil, fmt.Errorf("embed batch at offset %d (%s:%s): %w", i, backend, model, err)
		}
		if len(embeddings) != len(batch) {
			return nil, fmt.Errorf("embed batch at offset %d (%s:%s): got %d vectors for %d chunks",
				i, backend, model, len(embeddings), len(batch))
		}

		for j, jb := range batch {
			vec := embeddings[j]
			if vec == nil {
				return nil, fmt.Errorf("embed batch at offset %d (%s:%s): nil vector for %s",
					i+j, backend, model, shortID(jb.docID))
			}
			indexed = append(indexed, indexedDocumentVector{docID: jb.docID, chunk: jb.chunk, vector: vec})
		}
	}

	return indexed, nil
}

// MissingDocumentIDs reports which IDs do not yet have a vector row for the
// active backend+model. It intentionally ignores rows from other backends so
// cross-machine sync can preserve each machine's embedding choice.
func (vs *VectorStore) MissingDocumentIDs(ids []string) (map[string]bool, error) {
	missing := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return missing, nil
	}
	for _, id := range ids {
		missing[id] = true
	}

	rows, err := vs.db.Query(`
		SELECT doc_id FROM vectors WHERE backend = ? AND model = ?`,
		vs.embedder.Name(), vs.embedder.Model())
	if err != nil {
		return nil, fmt.Errorf("query active vector ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		delete(missing, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return missing, nil
}

// UpsertDocuments embeds and stores the given documents under the active
// backend+model without touching other documents' rows. It is used by sync
// pull to fill gaps cheaply after remote markdown changes. Long documents are
// chunked (v23); each doc is written delete-then-insert so a doc whose chunk
// count shrank does not leave stale rows behind.
func (vs *VectorStore) UpsertDocuments(documents []Document) (int, error) {
	if len(documents) == 0 {
		return 0, nil
	}
	if err := vs.embedder.Available(); err != nil {
		return 0, err
	}

	const batchSize = 50
	now := time.Now().UTC().Format(time.RFC3339)
	backend, model := vs.embedder.Name(), vs.embedder.Model()
	indexed := 0

	// insertDoc replaces all of a doc's chunk rows atomically.
	insertDoc := func(id string, vecs [][]float32) error {
		tx, err := vs.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`DELETE FROM vectors WHERE doc_id = ? AND backend = ? AND model = ?`,
			id, backend, model); err != nil {
			return fmt.Errorf("clear stale chunks for %s: %w", shortID(id), err)
		}
		for i, vec := range vecs {
			if _, err := tx.Exec(`
				INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				id, i, backend, model, len(vec), vectorToBlob(vec), now); err != nil {
				return fmt.Errorf("insert vector for %s chunk %d: %w", shortID(id), i, err)
			}
		}
		return tx.Commit()
	}

	for i := 0; i < len(documents); i += batchSize {
		end := i + batchSize
		if end > len(documents) {
			end = len(documents)
		}
		batch := documents[i:end]

		// Flatten the batch into chunk jobs, remembering each job's doc.
		var jobTexts []string
		jobDoc := make([]int, 0, len(batch)) // job index -> index into batch
		jobChunk := make([]int, 0, len(batch))
		for di, d := range batch {
			for ci, c := range ChunkForEmbed(d.Text) {
				jobTexts = append(jobTexts, c)
				jobDoc = append(jobDoc, di)
				jobChunk = append(jobChunk, ci)
			}
		}

		embeddings, err := vs.embedder.EmbedBatch(jobTexts, InputTypeDocument)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"embed batch failed at offset %d (%s:%s): %v — retrying per-doc\n",
				i, backend, model, err)
			for _, d := range batch {
				chunks := ChunkForEmbed(d.Text)
				vecs, singleErr := vs.embedder.EmbedBatch(chunks, InputTypeDocument)
				if singleErr != nil {
					fmt.Fprintf(os.Stderr, "  skip %s: %v\n", shortID(d.ID), singleErr)
					continue
				}
				ok := true
				for _, v := range vecs {
					if v == nil {
						ok = false
						break
					}
				}
				if !ok {
					continue
				}
				if err := insertDoc(d.ID, vecs); err != nil {
					return indexed, err
				}
				indexed++
			}
			continue
		}

		// Group chunk vectors back per doc (preserving batch order).
		perDoc := make([][]indexedDocumentVector, len(batch))
		for j, vec := range embeddings {
			if vec == nil {
				continue
			}
			di := jobDoc[j]
			perDoc[di] = append(perDoc[di], indexedDocumentVector{docID: batch[di].ID, chunk: jobChunk[j], vector: vec})
		}
		for di, d := range batch {
			chunks := perDoc[di]
			if len(chunks) == 0 {
				continue
			}
			vecs := make([][]float32, len(chunks))
			for _, c := range chunks {
				vecs[c.chunk] = c.vector
			}
			full := true
			for _, v := range vecs {
				if v == nil {
					full = false
					break
				}
			}
			if !full {
				continue
			}
			if err := insertDoc(d.ID, vecs); err != nil {
				return indexed, err
			}
			indexed++
		}
	}

	vs.saveIndexBackend()
	return indexed, nil
}

// Count returns the number of vector rows under the active (backend, model).
func (vs *VectorStore) Count() (int, error) {
	var n int
	err := vs.db.QueryRow(`
		SELECT COUNT(*) FROM vectors WHERE backend = ? AND model = ?`,
		vs.embedder.Name(), vs.embedder.Model()).Scan(&n)
	return n, err
}

// CountAll returns the total number of vector rows across all backends.
func (vs *VectorStore) CountAll() (int, error) {
	var n int
	err := vs.db.QueryRow(`SELECT COUNT(*) FROM vectors`).Scan(&n)
	return n, err
}

// PruneInactiveBackends deletes vector rows stored under (backend, model)
// tuples other than the active embedder's, returning per-"backend:model"
// deleted counts. Rows under other tuples are normally PRESERVED (see
// RebuildIndex) so a machine can switch back to a previous backend without
// a full re-embed; this is the explicit opt-in reclaim path behind
// `claudemem repair --prune-stale`. Count + delete run in one transaction
// so the returned counts always match what was actually removed.
func (vs *VectorStore) PruneInactiveBackends() (map[string]int, error) {
	backend, model := vs.embedder.Name(), vs.embedder.Model()

	tx, err := vs.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT backend, model, COUNT(*) FROM vectors
		WHERE NOT (backend = ? AND model = ?)
		GROUP BY backend, model`, backend, model)
	if err != nil {
		return nil, fmt.Errorf("count stale vectors: %w", err)
	}
	deleted := make(map[string]int)
	for rows.Next() {
		var b, m string
		var n int
		if err := rows.Scan(&b, &m, &n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan stale vector count: %w", err)
		}
		deleted[b+":"+m] = n
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if len(deleted) == 0 {
		return deleted, nil
	}

	if _, err := tx.Exec(`DELETE FROM vectors WHERE NOT (backend = ? AND model = ?)`,
		backend, model); err != nil {
		return nil, fmt.Errorf("delete stale vectors: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return deleted, nil
}

// saveTFIDFState persists the TF-IDF vocabulary to vector_meta.
func (vs *VectorStore) saveTFIDFState(tf *TFIDFEmbedder) error {
	state := tf.Vectorizer().ExportState()
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal tfidf state: %w", err)
	}
	_, err = vs.db.Exec(`
		INSERT OR REPLACE INTO vector_meta (key, value)
		VALUES ('vectorizer_state', ?)`, string(data))
	return err
}

func (vs *VectorStore) loadTFIDFState(tf *TFIDFEmbedder) {
	var data string
	if err := vs.db.QueryRow(`
		SELECT value FROM vector_meta WHERE key = 'vectorizer_state'
	`).Scan(&data); err != nil {
		return
	}
	var state VectorizerState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return
	}
	tf.Vectorizer().ImportState(&state)
}

// saveIndexBackend writes the active (backend:model) to vector_meta for
// diagnostics only — per-doc metadata is the real source of truth now.
// Errors are logged but not fatal: this runs after a successful commit
// and a missed update just triggers a false-positive I5 warning.
func (vs *VectorStore) saveIndexBackend() {
	if _, err := vs.db.Exec(
		`INSERT OR REPLACE INTO vector_meta (key, value) VALUES ('index_backend', ?)`,
		vs.EmbeddingBackend()); err != nil {
		fmt.Fprintf(os.Stderr,
			"warn: could not update vector_meta.index_backend (%v)\n", err)
	}
}

// --- helpers ---

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// vectorToBlob converts a float32 vector to a little-endian IEEE 754 byte slice.
func vectorToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func blobToVector(blob []byte) []float32 {
	if len(blob)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(blob)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return vec
}

func sortResultsBySimilarity(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Similarity > results[j-1].Similarity; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}
