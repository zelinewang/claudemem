package vectors

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

// VectorStore manages TF-IDF vectors stored in SQLite for semantic search.
// Vectors are stored as BLOBs alongside their document IDs.
type VectorStore struct {
	db         *sql.DB
	vectorizer *Vectorizer
}

// NewVectorStore creates a new VectorStore using the provided SQLite database.
// It creates the necessary tables if they don't exist.
func NewVectorStore(db *sql.DB) (*VectorStore, error) {
	vs := &VectorStore{
		db:         db,
		vectorizer: NewVectorizer(),
	}

	if err := vs.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to init vector schema: %w", err)
	}

	// Try to load persisted vectorizer state
	vs.loadVectorizerState()

	return vs, nil
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
// The text is vectorized using TF-IDF and stored as a BLOB.
func (vs *VectorStore) IndexDocument(id, text string) error {
	vec := vs.vectorizer.Vectorize(text)
	if vec == nil {
		return nil // no vocabulary yet, skip silently
	}

	blob := vectorToBlob(vec)

	_, err := vs.db.Exec(`
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

// Search performs semantic search by computing the TF-IDF vector for the query
// and finding the most similar documents via cosine similarity.
// Returns up to limit results sorted by similarity (highest first).
func (vs *VectorStore) Search(query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	queryVec := vs.vectorizer.Vectorize(query)
	if queryVec == nil {
		return nil, nil // no vocabulary, return empty
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
// Each document is a (id, text) pair. This rebuilds the vocabulary and re-vectorizes everything.
func (vs *VectorStore) RebuildIndex(documents []Document) error {
	// Clear existing vectors
	if _, err := vs.db.Exec(`DELETE FROM vectors`); err != nil {
		return fmt.Errorf("failed to clear vectors: %w", err)
	}

	if len(documents) == 0 {
		return nil
	}

	// Extract text corpus for vocabulary building
	corpus := make([]string, len(documents))
	for i, doc := range documents {
		corpus[i] = doc.Text
	}

	// Build vocabulary from corpus
	vs.vectorizer.BuildVocab(corpus)

	// Vectorize and store each document
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	// Persist vectorizer state
	return vs.saveVectorizerState()
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
