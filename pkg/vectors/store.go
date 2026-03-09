package vectors

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// VectorStore manages embedding vectors stored in SQLite for semantic search.
// Supports two backends: Ollama (real embeddings) and TF-IDF (fallback).
// Ollama provides true semantic understanding; TF-IDF provides keyword similarity.
type VectorStore struct {
	db         *sql.DB
	vectorizer *Vectorizer     // TF-IDF fallback
	ollama     *OllamaEmbedder // Ollama primary (nil if unavailable)
	useOllama  bool            // whether Ollama is active for this session
}

// NewVectorStore creates a new VectorStore using the provided SQLite database.
// It tries to connect to Ollama for real embeddings; falls back to TF-IDF.
func NewVectorStore(db *sql.DB) (*VectorStore, error) {
	vs := &VectorStore{
		db:         db,
		vectorizer: NewVectorizer(),
	}

	if err := vs.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to init vector schema: %w", err)
	}

	// Try to load persisted vectorizer state (TF-IDF fallback)
	vs.loadVectorizerState()

	// Try Ollama — primary path for real semantic search
	vs.tryInitOllama("")

	return vs, nil
}

// NewVectorStoreWithModel creates a VectorStore with a specific Ollama model.
func NewVectorStoreWithModel(db *sql.DB, model string) (*VectorStore, error) {
	vs := &VectorStore{
		db:         db,
		vectorizer: NewVectorizer(),
	}

	if err := vs.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to init vector schema: %w", err)
	}

	vs.loadVectorizerState()
	vs.tryInitOllama(model)

	return vs, nil
}

// tryInitOllama attempts to connect to a local Ollama instance.
func (vs *VectorStore) tryInitOllama(model string) {
	embedder := NewOllamaEmbedder(model)
	if embedder.Available() {
		vs.ollama = embedder
		vs.useOllama = true
	}
}

// UsingOllama returns true if Ollama embeddings are active.
func (vs *VectorStore) UsingOllama() bool {
	return vs.useOllama
}

// EmbeddingBackend returns "ollama" or "tfidf" to indicate active backend.
func (vs *VectorStore) EmbeddingBackend() string {
	if vs.useOllama {
		return "ollama:" + vs.ollama.Model()
	}
	return "tfidf"
}

// initSchema creates the vector storage tables.
func (vs *VectorStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS vectors (
		id TEXT PRIMARY KEY,
		vector BLOB NOT NULL
	);
	CREATE TABLE IF NOT EXISTS vector_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`

	_, err := vs.db.Exec(schema)
	return err
}

// IndexDocument adds or updates a document's vector in the store.
// Uses a single backend per session: Ollama when available, TF-IDF otherwise.
// Never mixes backends — this prevents dimension mismatches that make documents invisible.
func (vs *VectorStore) IndexDocument(id, text string) error {
	var vec []float32
	var err error

	if vs.useOllama {
		// Truncate to fit model context window, then embed
		vec, err = vs.ollama.Embed(TruncateForEmbed(text))
		if err != nil {
			// Ollama failed even after truncation — skip this document for semantic search.
			// Better absent than invisible (TF-IDF would create wrong-dimension vector).
			fmt.Fprintf(os.Stderr, "ollama embed failed for %s (skipping): %v\n", id[:8], err)
			return nil
		}
	} else {
		vec = vs.vectorizer.Vectorize(text)
	}

	if vec == nil {
		return nil
	}

	blob := vectorToBlob(vec)
	_, err = vs.db.Exec(`
		INSERT OR REPLACE INTO vectors (id, vector)
		VALUES (?, ?)
	`, id, blob)
	return err
}

// RemoveDocument removes a document's vector from the store.
func (vs *VectorStore) RemoveDocument(id string) error {
	_, err := vs.db.Exec(`DELETE FROM vectors WHERE id = ?`, id)
	return err
}

// SearchResult represents a semantic search result with similarity score.
type SearchResult struct {
	ID         string  `json:"id"`
	Similarity float32 `json:"similarity"`
}

// Search performs semantic search by finding the most similar documents.
// Uses Ollama embeddings if available, falls back to TF-IDF cosine similarity.
// Returns up to limit results sorted by similarity (highest first).
func (vs *VectorStore) Search(query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	var queryVec []float32
	if vs.useOllama {
		var err error
		queryVec, err = vs.ollama.Embed(TruncateForEmbed(query))
		if err != nil {
			// Ollama failed for query — cannot search (mixing backends would miss documents)
			return nil, nil
		}
	} else {
		queryVec = vs.vectorizer.Vectorize(query)
	}
	if queryVec == nil {
		return nil, nil
	}

	// Scan all vectors and compute similarity
	// For the typical claudemem scale (<10K docs), brute-force is fine.
	rows, err := vs.db.Query(`SELECT id, vector FROM vectors`)
	if err != nil {
		return nil, fmt.Errorf("failed to query vectors: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}

		docVec := blobToVector(blob)
		if len(docVec) != len(queryVec) {
			continue // dimension mismatch (stale vector from old vocab), skip
		}

		similarity := CosineSimilarity(queryVec, docVec)
		if similarity > 0.01 { // filter out near-zero matches
			results = append(results, SearchResult{
				ID:         id,
				Similarity: similarity,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	// Sort by similarity descending
	sortResultsBySimilarity(results)

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// RebuildIndex rebuilds the entire vector index from the provided documents.
// Uses Ollama batch embedding if available, falls back to TF-IDF.
func (vs *VectorStore) RebuildIndex(documents []Document) error {
	// Clear existing vectors
	if _, err := vs.db.Exec(`DELETE FROM vectors`); err != nil {
		return fmt.Errorf("failed to clear vectors: %w", err)
	}

	if len(documents) == 0 {
		return nil
	}

	tx, err := vs.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO vectors (id, vector) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	if vs.useOllama {
		// Ollama path: batch embed in chunks of 50
		batchSize := 50
		for i := 0; i < len(documents); i += batchSize {
			end := i + batchSize
			if end > len(documents) {
				end = len(documents)
			}
			batch := documents[i:end]

			texts := make([]string, len(batch))
			for j, doc := range batch {
				texts[j] = TruncateForEmbed(doc.Text)
			}

			embeddings, embErr := vs.ollama.EmbedBatch(texts)
			if embErr != nil {
				// Batch failed — retry individually with truncation (never mix in TF-IDF)
				fmt.Fprintf(os.Stderr, "ollama batch failed at offset %d, retrying per-doc with truncation: %v\n", i, embErr)
				for _, doc := range batch {
					vec, singleErr := vs.ollama.Embed(TruncateForEmbed(doc.Text))
					if singleErr != nil {
						// Still failed after truncation — skip (better absent than wrong-dimension)
						fmt.Fprintf(os.Stderr, "  skipping %s: %v\n", doc.ID[:8], singleErr)
						continue
					}
					blob := vectorToBlob(vec)
					if _, err := stmt.Exec(doc.ID, blob); err != nil {
						return fmt.Errorf("failed to insert vector for %s: %w", doc.ID, err)
					}
				}
				continue
			}

			for j, doc := range batch {
				blob := vectorToBlob(embeddings[j])
				if _, err := stmt.Exec(doc.ID, blob); err != nil {
					return fmt.Errorf("failed to insert vector for %s: %w", doc.ID, err)
				}
			}
		}
	} else {
		// TF-IDF fallback path
		corpus := make([]string, len(documents))
		for i, doc := range documents {
			corpus[i] = doc.Text
		}
		vs.vectorizer.BuildVocab(corpus)

		for _, doc := range documents {
			vec := vs.vectorizer.Vectorize(doc.Text)
			if vec == nil {
				continue
			}
			blob := vectorToBlob(vec)
			if _, err := stmt.Exec(doc.ID, blob); err != nil {
				return fmt.Errorf("failed to insert vector for %s: %w", doc.ID, err)
			}
		}

	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	// Persist TF-IDF vectorizer state (after commit, outside transaction)
	if !vs.useOllama {
		return vs.saveVectorizerState()
	}

	return nil
}

// Count returns the number of vectors in the store.
func (vs *VectorStore) Count() (int, error) {
	var count int
	err := vs.db.QueryRow(`SELECT COUNT(*) FROM vectors`).Scan(&count)
	return count, err
}

// Document represents a document to be indexed.
type Document struct {
	ID   string
	Text string
}

// saveVectorizerState persists the vectorizer state to the database.
func (vs *VectorStore) saveVectorizerState() error {
	state := vs.vectorizer.ExportState()
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal vectorizer state: %w", err)
	}

	_, err = vs.db.Exec(`
		INSERT OR REPLACE INTO vector_meta (key, value)
		VALUES ('vectorizer_state', ?)
	`, string(data))
	return err
}

// loadVectorizerState restores the vectorizer state from the database.
func (vs *VectorStore) loadVectorizerState() {
	var data string
	err := vs.db.QueryRow(`
		SELECT value FROM vector_meta WHERE key = 'vectorizer_state'
	`).Scan(&data)
	if err != nil {
		return // no state yet, that's fine
	}

	var state VectorizerState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return // corrupted state, will be rebuilt on next reindex
	}

	vs.vectorizer.ImportState(&state)
}

// vectorToBlob converts a float32 vector to a byte slice (little-endian IEEE 754).
func vectorToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// blobToVector converts a byte slice back to a float32 vector.
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

// sortResultsBySimilarity sorts results by similarity descending (insertion sort).
func sortResultsBySimilarity(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Similarity > results[j-1].Similarity; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}
