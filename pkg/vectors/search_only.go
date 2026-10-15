package vectors

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// OpenForSearch opens a database's vectors for searching only, with an embedder that belongs to
// another store: the store that reads this one. It creates, migrates and stamps nothing, so it works
// on a database opened read-only. It needs the current layout (v23), refuses an index stamped by a
// newer claudemem, and refuses a TF-IDF embedder, whose vocabulary is built from one store's own
// corpus. Searches then match only this database's rows of the embedder's backend, model and
// dimensions.
func OpenForSearch(db *sql.DB, embedder Embedder) (*VectorStore, error) {
	if embedder == nil {
		return nil, fmt.Errorf("vectors.OpenForSearch: no embedder")
	}
	if embedder.Name() == "tfidf" {
		return nil, fmt.Errorf("TF-IDF vectors belong to the store that built them")
	}
	stamped, err := readMeta(db, vectorsSchemaKey)
	if err != nil && !isNoSuchTable(err) {
		return nil, fmt.Errorf("read vectors schema: %w", err)
	}
	if stamped != "" {
		n, convErr := strconv.Atoi(stamped)
		if convErr != nil {
			return nil, fmt.Errorf("vector_meta.%s is %q, not a number", vectorsSchemaKey, stamped)
		}
		if n > currentVectorsSchema {
			return nil, &ErrIndexNewerThanBinary{IndexSchema: n, BinarySchema: currentVectorsSchema}
		}
	}
	kind, err := detectVectorsSchema(db)
	if err != nil {
		return nil, err
	}
	if kind != schemaV23 {
		return nil, fmt.Errorf("no vectors in the current layout")
	}
	return &VectorStore{db: db, embedder: embedder}, nil
}

func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table")
}

// WithEmbedder is the same vectors searched and written with another embedder of the same backend.
func (vs *VectorStore) WithEmbedder(e Embedder) *VectorStore {
	return &VectorStore{db: vs.db, embedder: e}
}

// CacheQueryEmbeddings wraps an embedder so that a query is embedded once per command, however many
// stores it searches: Embed of the same text and input type returns the first result. Batches,
// availability and identity pass through.
func CacheQueryEmbeddings(e Embedder) Embedder {
	if _, ok := e.(*queryCache); ok {
		return e
	}
	return &queryCache{Embedder: e, seen: map[queryKey][]float32{}}
}

type queryKey struct {
	text string
	t    InputType
}

type queryCache struct {
	Embedder
	mu   sync.Mutex
	seen map[queryKey][]float32
}

func (c *queryCache) Embed(text string, t InputType) ([]float32, error) {
	key := queryKey{text: text, t: t}
	c.mu.Lock()
	vec, ok := c.seen[key]
	c.mu.Unlock()
	if ok {
		return vec, nil
	}
	vec, err := c.Embedder.Embed(text, t)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.seen[key] = vec
	c.mu.Unlock()
	return vec, nil
}
