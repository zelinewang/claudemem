package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zelinewang/claudemem/pkg/vectors"
)

// InitVectorStore initializes the vector store for semantic search.
// This should be called after NewFileStore when the feature is enabled.
func (fs *FileStore) InitVectorStore() error {
	if fs.db == nil {
		return fmt.Errorf("database not initialized")
	}

	vs, err := vectors.NewVectorStore(fs.db)
	if err != nil {
		return fmt.Errorf("failed to initialize vector store: %w", err)
	}

	fs.vectorStore = vs
	return nil
}

// HasVectorStore returns true if semantic search is initialized.
func (fs *FileStore) HasVectorStore() bool {
	return fs.vectorStore != nil
}

// IndexNoteVector indexes a note's content for semantic search.
// No-op if vector store is not initialized.
func (fs *FileStore) IndexNoteVector(id, title, content string, tags []string) {
	if fs.vectorStore == nil {
		return
	}
	// Combine title, content, and tags for richer semantic representation
	text := title + " " + content + " " + strings.Join(tags, " ")
	_ = fs.vectorStore.IndexDocument(id, text) // best effort
}

// IndexSessionVector indexes a session's content for semantic search.
// No-op if vector store is not initialized.
func (fs *FileStore) IndexSessionVector(id, title, searchableContent string, tags []string) {
	if fs.vectorStore == nil {
		return
	}
	text := title + " " + searchableContent + " " + strings.Join(tags, " ")
	_ = fs.vectorStore.IndexDocument(id, text) // best effort
}

// RemoveVector removes a document's vector from the store.
// No-op if vector store is not initialized.
func (fs *FileStore) RemoveVector(id string) {
	if fs.vectorStore == nil {
		return
	}
	_ = fs.vectorStore.RemoveDocument(id) // best effort
}

// SemanticSearch performs semantic search using TF-IDF vectors.
// Returns results that can be merged with FTS5 results for hybrid ranking.
func (fs *FileStore) SemanticSearch(query string, limit int) ([]SearchResult, error) {
	if fs.vectorStore == nil {
		return nil, fmt.Errorf("semantic search not enabled; run: claudemem config set features.semantic_search true")
	}

	vectorResults, err := fs.vectorStore.Search(query, limit*2) // fetch more for merging
	if err != nil {
		return nil, fmt.Errorf("semantic search failed: %w", err)
	}

	if len(vectorResults) == 0 {
		return nil, nil
	}

	// Enrich vector results with entry metadata from the entries table
	var results []SearchResult
	for _, vr := range vectorResults {
		var r SearchResult
		var tagsStr, category, date, branch, project, createdStr string

		err := fs.db.QueryRow(`
			SELECT id, type, title, category, tags, created, date_str, branch, project
			FROM entries WHERE id = ?
		`, vr.ID).Scan(
			&r.ID, &r.Type, &r.Title, &category, &tagsStr, &createdStr,
			&date, &branch, &project,
		)
		if err != nil {
			continue // entry not found (orphan vector), skip
		}

		r.Category = category
		r.Date = date
		r.Branch = branch
		r.Project = project
		r.Score = float64(vr.Similarity)
		r.Created, _ = parseCreatedTimestamp(createdStr)

		if tagsStr != "" {
			r.Tags = strings.Fields(tagsStr)
		} else {
			r.Tags = []string{}
		}

		// Get preview from FTS content
		var preview string
		fs.db.QueryRow(`SELECT substr(content, 1, 200) FROM memory_fts WHERE id = ?`, vr.ID).Scan(&preview)
		r.Preview = strings.TrimSpace(preview)
		if len(r.Preview) > 100 {
			r.Preview = r.Preview[:100] + "..."
		}

		results = append(results, r)
	}

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// HybridSearch combines FTS5 and semantic search results using reciprocal rank fusion.
// FTS results are weighted slightly higher (0.6) than semantic results (0.4)
// since exact keyword matches are generally more precise for memory retrieval.
func (fs *FileStore) HybridSearch(query string, opts SearchOpts) ([]SearchResult, error) {
	// Get FTS5 results
	ftsResults, err := fs.SearchWithOpts(opts)
	if err != nil {
		return nil, err
	}

	// If no vector store, return FTS results only
	if fs.vectorStore == nil {
		return ftsResults, nil
	}

	// Get semantic results
	semanticResults, err := fs.SemanticSearch(query, opts.Limit)
	if err != nil {
		// Semantic search failed; fall back to FTS only
		return ftsResults, nil
	}

	// Reciprocal Rank Fusion (RRF)
	// Score = sum of 1/(k+rank) for each result list where the doc appears
	const k = 60.0 // RRF constant (standard value)
	const ftsWeight = 0.6
	const semanticWeight = 0.4

	scores := make(map[string]float64)
	resultMap := make(map[string]SearchResult)

	for rank, r := range ftsResults {
		scores[r.ID] += ftsWeight * (1.0 / (k + float64(rank+1)))
		resultMap[r.ID] = r
	}

	for rank, r := range semanticResults {
		scores[r.ID] += semanticWeight * (1.0 / (k + float64(rank+1)))
		if _, exists := resultMap[r.ID]; !exists {
			resultMap[r.ID] = r
		}
	}

	// Build final results with fused scores
	var results []SearchResult
	for id, score := range scores {
		r := resultMap[id]
		r.Score = score
		results = append(results, r)
	}

	sortResultsByScore(results)

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// ReindexVectors rebuilds the entire vector index from all notes and sessions on disk.
// Returns the number of documents indexed.
func (fs *FileStore) ReindexVectors() (int, error) {
	if fs.vectorStore == nil {
		return 0, fmt.Errorf("semantic search not enabled; run: claudemem config set features.semantic_search true")
	}

	var docs []vectors.Document

	// Collect notes
	if _, err := os.Stat(fs.notesDir); err == nil {
		filepath.Walk(fs.notesDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			note, parseErr := ParseNoteMarkdown(data)
			if parseErr != nil || note.ID == "" {
				return nil
			}

			text := note.Title + " " + note.Content + " " + strings.Join(note.Tags, " ")
			docs = append(docs, vectors.Document{
				ID:   note.ID,
				Text: text,
			})
			return nil
		})
	}

	// Collect sessions
	if _, err := os.Stat(fs.sessionsDir); err == nil {
		filepath.Walk(fs.sessionsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			session, parseErr := ParseSessionMarkdown(data)
			if parseErr != nil || session.ID == "" {
				return nil
			}

			text := session.Title + " " + session.GetSearchableContent() + " " + strings.Join(session.Tags, " ")
			docs = append(docs, vectors.Document{
				ID:   session.ID,
				Text: text,
			})
			return nil
		})
	}

	// Rebuild the index
	if err := fs.vectorStore.RebuildIndex(docs); err != nil {
		return 0, fmt.Errorf("failed to rebuild vector index: %w", err)
	}

	return len(docs), nil
}
