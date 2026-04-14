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
// Schema (v22 — see docs/HYBRID_EMBEDDING_PLAN.md):
//
//	vectors(doc_id, backend, model, dim, vector, created_at) PK(doc_id, backend, model)
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
		return vs.createV22Schema()
	case schemaV21:
		return vs.migrateV21ToV22()
	case schemaV22:
		return nil
	}
	return fmt.Errorf("unknown vectors schema kind %d", kind)
}

type vectorsSchemaKind int

const (
	schemaNone vectorsSchemaKind = iota
	schemaV21                    // (id, vector)
	schemaV22                    // (doc_id, backend, model, dim, vector, created_at)
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

func (vs *VectorStore) createV22Schema() error {
	_, err := vs.db.Exec(v22Schema)
	return err
}

// migrateV21ToV22 is the preservation-first migration. Rather than dropping
// the old flat (id, vector) table and forcing a costly re-embed, we tag
// every existing row with the backend that produced it and copy forward.
// If vector_meta.index_backend is absent (pre-66c3fdc installs), we fall
// back to a conservative ("tfidf", "tfidf") tuple — users can re-run
// setup to re-embed with a real backend later.
func (vs *VectorStore) migrateV21ToV22() error {
	indexedBackend := readMeta(vs.db, "index_backend")
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

func readMeta(db *sql.DB, key string) string {
	var v string
	db.QueryRow(`SELECT value FROM vector_meta WHERE key=?`, key).Scan(&v)
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

// IndexDocument adds/updates a single document's vector under the active
// (backend, model). Forgiving by design: embed failures are logged and
// skipped so a down backend does not block the write path. The health
// subsystem (P5) can heal missing rows later via `claudemem repair`.
func (vs *VectorStore) IndexDocument(id, text string) error {
	vec, err := vs.embedder.Embed(TruncateForEmbed(text), InputTypeDocument)
	if err != nil {
		fmt.Fprintf(os.Stderr, "index %s skipped (%s:%s embed failed: %v) — run `claudemem repair` to retry\n",
			shortID(id), vs.embedder.Name(), vs.embedder.Model(), err)
		return nil
	}
	if vec == nil {
		return nil // TF-IDF returns nil before vocabulary is built
	}

	_, err = vs.db.Exec(`
		INSERT OR REPLACE INTO vectors
			(doc_id, backend, model, dim, vector, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, vs.embedder.Name(), vs.embedder.Model(), len(vec),
		vectorToBlob(vec), time.Now().UTC().Format(time.RFC3339))
	return err
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

	var results []SearchResult
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
		if sim > 0.01 {
			results = append(results, SearchResult{ID: id, Similarity: sim})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iter: %w", err)
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

// RebuildIndex embeds every document with the ACTIVE backend+model, tagging
// each row accordingly. Existing rows under OTHER (backend, model) tuples
// are preserved (cross-machine / switch-back use cases). Only rows under
// the CURRENT backend+model are wiped and rebuilt.
//
// For TF-IDF embedders, this also rebuilds the vocabulary from the corpus.
func (vs *VectorStore) RebuildIndex(documents []Document) error {
	if tf, ok := vs.embedder.(*TFIDFEmbedder); ok {
		corpus := make([]string, len(documents))
		for i, d := range documents {
			corpus[i] = d.Text
		}
		tf.BuildVocab(corpus)
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

	if len(documents) == 0 {
		return tx.Commit()
	}

	stmt, err := tx.Prepare(`
		INSERT INTO vectors (doc_id, backend, model, dim, vector, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	const batchSize = 50
	now := time.Now().UTC().Format(time.RFC3339)
	backend, model := vs.embedder.Name(), vs.embedder.Model()

	for i := 0; i < len(documents); i += batchSize {
		end := i + batchSize
		if end > len(documents) {
			end = len(documents)
		}
		batch := documents[i:end]

		texts := make([]string, len(batch))
		for j, d := range batch {
			texts[j] = TruncateForEmbed(d.Text)
		}
		embeddings, err := vs.embedder.EmbedBatch(texts, InputTypeDocument)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"embed batch failed at offset %d (%s:%s): %v — retrying per-doc\n",
				i, backend, model, err)
			for _, d := range batch {
				vec, singleErr := vs.embedder.Embed(TruncateForEmbed(d.Text), InputTypeDocument)
				if singleErr != nil || vec == nil {
					fmt.Fprintf(os.Stderr, "  skip %s: %v\n", shortID(d.ID), singleErr)
					continue
				}
				if _, err := stmt.Exec(d.ID, backend, model, len(vec), vectorToBlob(vec), now); err != nil {
					return fmt.Errorf("insert vector for %s: %w", d.ID, err)
				}
			}
			continue
		}

		for j, d := range batch {
			vec := embeddings[j]
			if vec == nil {
				continue
			}
			if _, err := stmt.Exec(d.ID, backend, model, len(vec), vectorToBlob(vec), now); err != nil {
				return fmt.Errorf("insert vector for %s: %w", d.ID, err)
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

func (vs *VectorStore) saveIndexBackend() {
	vs.db.Exec(
		`INSERT OR REPLACE INTO vector_meta (key, value) VALUES ('index_backend', ?)`,
		vs.EmbeddingBackend())
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
