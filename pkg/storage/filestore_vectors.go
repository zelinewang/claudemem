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

// VectorBackend returns the active embedding backend name.
func (fs *FileStore) VectorBackend() string {
	if fs.vectorStore == nil {
		return "none"
	}
	return fs.vectorStore.EmbeddingBackend()
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

// HybridSearch combines FTS5 and semantic search using min-max normalized score fusion.
//
// Algorithm (industry standard, used by Weaviate relativeScoreFusion and OpenSearch):
//  1. Run both FTS5 and semantic search
//  2. Min-max normalize each result set's scores to [0, 1]
//  3. Convex combination: final = α * norm_fts + (1-α) * norm_semantic
//  4. Documents appearing in both lists get contributions from both sides
//
// Weight α = 0.7 (keyword-heavy) because claudemem queries are predominantly
// exact keyword lookups (note titles, tech terms). Semantic fills gaps when
// FTS5 has no keyword match. Researched from Weaviate, Elasticsearch, OpenSearch.
func (fs *FileStore) HybridSearch(query string, opts SearchOpts) ([]SearchResult, error) {
	// Get FTS5 results (respects all facet filters: category, tags, date range)
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
		return ftsResults, nil
	}

	// Build ID set of FTS results that pass facet filters (source of truth for filtering)
	ftsIDs := make(map[string]bool, len(ftsResults))
	for _, r := range ftsResults {
		ftsIDs[r.ID] = true
	}

	// Filter semantic results: only keep those that also pass facet filters.
	// If no facet filters active (no category/tag/date), keep all semantic results.
	hasFacets := opts.Category != "" || len(opts.Tags) > 0 || opts.After != "" || opts.Before != ""
	if hasFacets {
		filtered := semanticResults[:0]
		for _, r := range semanticResults {
			if ftsIDs[r.ID] {
				filtered = append(filtered, r)
			}
		}
		// Also add semantic-only results that match filters via DB check
		for _, r := range semanticResults {
			if !ftsIDs[r.ID] && fs.matchesFacets(r.ID, opts) {
				filtered = append(filtered, r)
			}
		}
		semanticResults = filtered
	}

	// If either side is empty, return the other directly
	if len(ftsResults) == 0 && len(semanticResults) == 0 {
		return nil, nil
	}
	if len(semanticResults) == 0 {
		return ftsResults, nil
	}
	if len(ftsResults) == 0 {
		return semanticResults, nil
	}

	// Min-max normalize FTS scores to [0, 1]
	ftsMin, ftsMax := ftsResults[0].Score, ftsResults[0].Score
	for _, r := range ftsResults[1:] {
		if r.Score < ftsMin {
			ftsMin = r.Score
		}
		if r.Score > ftsMax {
			ftsMax = r.Score
		}
	}
	ftsRange := ftsMax - ftsMin
	if ftsRange == 0 {
		ftsRange = 1 // avoid division by zero when all scores are equal
	}

	// Min-max normalize semantic scores to [0, 1]
	semMin, semMax := semanticResults[0].Score, semanticResults[0].Score
	for _, r := range semanticResults[1:] {
		if r.Score < semMin {
			semMin = r.Score
		}
		if r.Score > semMax {
			semMax = r.Score
		}
	}
	semRange := semMax - semMin
	if semRange == 0 {
		semRange = 1
	}

	// Convex combination: α * norm_fts + (1-α) * norm_semantic
	const alpha = 0.7 // keyword weight (researched: claudemem is keyword-heavy use case)

	scores := make(map[string]float64)
	resultMap := make(map[string]SearchResult)

	for _, r := range ftsResults {
		normScore := (r.Score - ftsMin) / ftsRange
		scores[r.ID] += alpha * normScore
		resultMap[r.ID] = r
	}

	for _, r := range semanticResults {
		normScore := (r.Score - semMin) / semRange
		scores[r.ID] += (1 - alpha) * normScore
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

	// Log access for hybrid results
	for _, r := range results {
		fs.LogAccess(r.ID, "search_hit")
	}

	return results, nil
}

// matchesFacets checks if an entry passes the facet filters via DB lookup.
func (fs *FileStore) matchesFacets(id string, opts SearchOpts) bool {
	query := `SELECT 1 FROM entries WHERE id = ?`
	args := []interface{}{id}

	if opts.Category != "" {
		query += " AND category = ?"
		args = append(args, opts.Category)
	}
	if len(opts.Tags) > 0 {
		for _, tag := range opts.Tags {
			query += " AND tags LIKE ?"
			args = append(args, "%"+tag+"%")
		}
	}
	if opts.After != "" {
		query += " AND date_str >= ?"
		args = append(args, opts.After)
	}
	if opts.Before != "" {
		query += " AND date_str <= ?"
		args = append(args, opts.Before)
	}

	var exists int
	err := fs.db.QueryRow(query, args...).Scan(&exists)
	return err == nil
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
