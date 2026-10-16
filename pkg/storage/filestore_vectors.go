package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

// InitVectorStore initializes the vector store for semantic search using
// the configured embedding backend. Config keys consulted:
//
//	embedding.backend       (default: "tfidf" on fresh install)
//	embedding.model         (backend-specific default)
//	embedding.dimensions    (matryoshka truncation; 0 = native)
//	embedding.endpoint      (optional URL override, e.g., Ollama on a remote host)
//	embedding.api_key_env   (name of the env var holding the API key for cloud)
//	embedding.project       (Vertex AI project ID; falls back to GOOGLE_CLOUD_PROJECT)
//	embedding.location      (Vertex AI region; falls back to GOOGLE_CLOUD_LOCATION)
//
// For P1 this only supports Ollama and TF-IDF; Gemini/Voyage/OpenAI land in
// later phases. The backend is constructed but NOT pinged here — availability
// is checked at use time so write-path ops stay forgiving and read-path ops
// can surface the error with recovery instructions (P4).
func (fs *FileStore) InitVectorStore() error {
	if fs.db == nil {
		return fmt.Errorf("database not initialized")
	}
	if fs.readOnly {
		// its settings must not choose a backend: a reader brings its own (AttachReadVectors)
		return fmt.Errorf("a store opened read-only uses the embedder of the store that reads it")
	}

	bc, err := loadBackendConfig(fs.baseDir)
	if err != nil {
		return fmt.Errorf("load embedding config: %w", err)
	}
	embedder, err := vectors.BuildEmbedder(bc)
	if err != nil {
		return fmt.Errorf("build embedder (%s:%s): %w — run `claudemem setup` to fix",
			bc.Backend, bc.Model, err)
	}

	vs, err := vectors.NewVectorStore(fs.db, embedder)
	if err != nil {
		return fmt.Errorf("failed to initialize vector store: %w", err)
	}
	// vectors only for the entries the index lists, checked as they are written
	if err := vs.RequireDocumentRows("entries"); err != nil {
		return err
	}
	fs.vectorStore = vs
	return nil
}

// loadBackendConfig reads embedding.* keys from ~/.claudemem/config.json
// and resolves the API key from the named env var (never stored on disk).
func loadBackendConfig(storeDir string) (vectors.BackendConfig, error) {
	cfg, err := config.Load(storeDir)
	if err != nil {
		return vectors.BackendConfig{}, err
	}
	backend := cfg.GetString("embedding.backend")
	if backend == "" {
		backend = "tfidf" // first-run default; user can upgrade via `claudemem setup`
	}
	bc := vectors.BackendConfig{
		Backend:  backend,
		Model:    cfg.GetString("embedding.model"),
		Endpoint: cfg.GetString("embedding.endpoint"),
		Dim:      cfg.GetInt("embedding.dimensions"),
		Project:  firstNonEmpty(cfg.GetString("embedding.project"), os.Getenv("GOOGLE_CLOUD_PROJECT"), os.Getenv("GEMINI_VERTEX_PROJECT")),
		Location: firstNonEmpty(cfg.GetString("embedding.location"), os.Getenv("GOOGLE_CLOUD_LOCATION"), os.Getenv("GEMINI_VERTEX_LOCATION")),
	}
	if keyEnv := cfg.GetString("embedding.api_key_env"); keyEnv != "" {
		bc.APIKey = os.Getenv(keyEnv)
	}
	return bc, nil
}

// activeVectorBackend is the backend and model whose vectors the store's searches and backfills use:
// those of its vector store, or else the ones its settings name, without contacting the backend. A
// move reads them from its target also in a dry run, which opens the target read-only: the target's
// own settings choose its backend. ok is false when the settings cannot be read or name no backend
// claudemem knows.
func (fs *FileStore) activeVectorBackend() (backend, model string, ok bool) {
	if fs.vectorStore != nil {
		e := fs.vectorStore.Embedder()
		return e.Name(), e.Model(), true
	}
	bc, err := loadBackendConfig(fs.baseDir)
	if err != nil {
		return "", "", false
	}
	e, err := vectors.BuildEmbedder(bc)
	if err != nil {
		return "", "", false
	}
	return e.Name(), e.Model(), true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// AttachReadVectors lets a store opened read-only answer vector searches with the embedder of the
// store that reads it, over its own vectors of that embedder's backend, model and dimensions. This
// store's embedding settings are never read: it does not choose a backend, an endpoint or a key.
// Nothing in its index is created, migrated or stamped. A TF-IDF embedder is refused (its
// vocabulary belongs to the other store's corpus), as is an index without current-layout vectors.
func (fs *FileStore) AttachReadVectors(e vectors.Embedder) error {
	vs, err := vectors.OpenForSearch(fs.db, e)
	if err != nil {
		return err
	}
	fs.vectorStore = vs
	return nil
}

// ShareQueryEmbedder returns this store's embedder, made to embed each query once, for the stores
// this one reads; it also uses that embedder itself from now on. nil when the store has no vector
// search, or only TF-IDF, which cannot be shared.
func (fs *FileStore) ShareQueryEmbedder() vectors.Embedder {
	if fs.vectorStore == nil {
		return nil
	}
	e := fs.vectorStore.Embedder()
	if e == nil || e.Name() == "tfidf" {
		return nil
	}
	cached := vectors.CacheQueryEmbeddings(e)
	fs.vectorStore = fs.vectorStore.WithEmbedder(cached)
	return cached
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

// PruneStaleVectors deletes vector rows produced by embedding backends other
// than the active one. See VectorStore.PruneInactiveBackends for semantics.
func (fs *FileStore) PruneStaleVectors() (map[string]int, error) {
	if fs.vectorStore == nil {
		return nil, fmt.Errorf("no active embedding backend (vector store not initialized)")
	}
	unlock, err := fs.lockStore()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return fs.vectorStore.PruneInactiveBackends()
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

	// Get semantic results. CRITICAL: when the configured embedding backend
	// is unreachable, SemanticSearch returns ErrBackendUnavailable. We MUST
	// propagate that to the caller — otherwise the fail-loud UX in
	// cmd/search.go never triggers and we silently degrade to FTS-only,
	// violating the "no silent fallback" design rule.
	//
	// For all OTHER errors (e.g. transient DB read issues), keep the old
	// forgiving behavior so hybrid search stays useful.
	semanticResults, err := fs.SemanticSearch(query, opts.Limit)
	if err != nil {
		if vectors.IsBackendUnavailable(err) {
			return nil, err
		}
		return ftsResults, nil
	}

	// Build ID set of FTS results that pass facet filters (source of truth for filtering)
	ftsIDs := make(map[string]bool, len(ftsResults))
	for _, r := range ftsResults {
		ftsIDs[r.ID] = true
	}

	// Filter semantic results: only keep those that also pass facet filters.
	// If no facet filters active (no type/category/tag/date), keep all semantic results. The type
	// counts as a facet: without it `search --type session` let semantic-only notes through.
	hasFacets := opts.Type != "" || opts.Category != "" || len(opts.Tags) > 0 || opts.After != "" || opts.Before != ""
	if hasFacets {
		// A new slice: filtering into semanticResults[:0] overwrote hits the second loop still reads.
		filtered := make([]SearchResult, 0, len(semanticResults))
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
		semanticResults = fs.keepResultsWithTags(filtered, opts.Tags)
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

	if opts.Type != "" {
		query += " AND type = ?"
		args = append(args, opts.Type)
	}
	if opts.Category != "" {
		query += " AND category = ?"
		args = append(args, opts.Category)
	}
	tagFilter, tagArgs := tagsWhere("tags", opts.Tags)
	query += tagFilter
	args = append(args, tagArgs...)
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

// collectVectorDocuments reads the documents to embed from the store's note and session files, and
// keeps those whose entries the index lists: a file the index does not list gets no vectors (the vector
// store writes them only for listed entries), so embedding it would cost a call for nothing. It returns
// how many documents it left out for that.
func (fs *FileStore) collectVectorDocuments() (docs []vectors.Document, unlisted int, err error) {
	listed, err := fs.indexedIDs()
	if err != nil {
		return nil, 0, err
	}

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
			if !listed[note.ID] {
				unlisted++
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
			if !listed[session.ID] {
				unlisted++
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

	return docs, unlisted, nil
}

// indexedIDs returns the ids of the entries the index lists.
func (fs *FileStore) indexedIDs() (map[string]bool, error) {
	rows, err := fs.db.Query(`SELECT id FROM entries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	indexed := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		indexed[id] = true
	}
	return indexed, rows.Err()
}

// IndexMissingVectors embeds only documents that do not yet have a vector row
// for the active backend. TF-IDF still needs a full corpus rebuild because its
// vocabulary is corpus-wide.
// IndexMissingVectors embeds only the documents that have no vector for the
// active backend. It returns how many it embedded and how many were missing;
// indexed < missing means the backend skipped documents (logged per document
// on stderr) and the caller should report a partial result, not success.
func (fs *FileStore) IndexMissingVectors() (indexed, missing int, err error) {
	if fs.vectorStore == nil {
		return 0, 0, fmt.Errorf("semantic search not enabled; run: claudemem config set features.semantic_search true")
	}

	docs, unlisted, err := fs.collectVectorDocuments()
	if err != nil {
		return 0, 0, fmt.Errorf("read the entries the index lists: %w", err)
	}
	if strings.HasPrefix(fs.VectorBackend(), "tfidf:") {
		// TF-IDF needs the whole corpus; a "backfill" is always a full rebuild.
		left, err := fs.vectorStore.RebuildIndexGated(docs, fs.vectorGate())
		if err != nil {
			return 0, len(docs), fmt.Errorf("failed to rebuild vector index: %w", err)
		}
		warnUnlisted(unlisted + left)
		return len(docs) - left, len(docs) - left, nil
	}

	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	missingIDs, err := fs.vectorStore.MissingDocumentIDs(ids)
	if err != nil {
		return 0, 0, err
	}
	missingDocs := make([]vectors.Document, 0, len(missingIDs))
	for _, doc := range docs {
		if missingIDs[doc.ID] {
			missingDocs = append(missingDocs, doc)
		}
	}
	indexed, left, err := fs.vectorStore.UpsertDocumentsGated(missingDocs, fs.vectorGate())
	if err == nil {
		warnUnlisted(unlisted + left)
	}
	return indexed, len(missingDocs) - left, err
}

// ReindexVectors rebuilds the entire vector index from all notes and sessions on disk.
// Returns the number of documents indexed.
func (fs *FileStore) ReindexVectors() (int, error) {
	if fs.vectorStore == nil {
		return 0, fmt.Errorf("semantic search not enabled; run: claudemem config set features.semantic_search true")
	}

	docs, unlisted, err := fs.collectVectorDocuments()
	if err != nil {
		return 0, fmt.Errorf("read the entries the index lists: %w", err)
	}
	left, err := fs.vectorStore.RebuildIndexGated(docs, fs.vectorGate())
	if err != nil {
		return 0, fmt.Errorf("failed to rebuild vector index: %w", err)
	}
	warnUnlisted(unlisted + left)

	return len(docs) - left, nil
}

// warnUnlisted says how many of the entries read from files got no vectors because the index does
// not list them: files that came in outside claudemem, which a reindex lists, or entries removed
// while the others were embedded, which need nothing.
func warnUnlisted(n int) {
	if n > 0 {
		fmt.Fprintf(os.Stderr, "warning: the index does not list %d of the entries read from files, so they got no vectors: if their files are still there, run `claudemem reindex`, then `claudemem reindex --vectors --missing`\n", n)
	}
}

// vectorGate is the gate of a bulk vector write of documents read from files: it runs the write
// under the store lock, where a document counts as present while the index lists it. The documents
// were read and embedded without the lock (embedding can take minutes), and a move or a delete may
// have taken some out since: their vectors are not written back, also when a copy of a file came back
// meanwhile, since they would belong to no entry. The vector store checks the same again in the
// transaction that writes them (RequireDocumentRows).
func (fs *FileStore) vectorGate() vectors.Gate {
	return func(write func(present func(id string) bool) error) error {
		unlock, err := fs.lockStore()
		if err != nil {
			return err
		}
		defer unlock()
		indexed, err := fs.indexedIDs()
		if err != nil {
			return err
		}
		return write(func(id string) bool { return indexed[id] })
	}
}
