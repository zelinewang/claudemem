package vectors

// TFIDFEmbedder adapts the TF-IDF Vectorizer to the Embedder interface.
//
// TF-IDF is intentionally a first-class backend choice, not a silent fallback.
// It is useful for:
//   - airgapped environments (no daemon, no API key required)
//   - CI / automated tests
//   - users who prefer keyword-ish semantic search without runtime dependencies
//
// The underlying Vectorizer is built from the indexed corpus, so dimensions
// are determined by vocabulary size and only stabilize after RebuildIndex
// is called. Until then, Dimensions() returns 0.
type TFIDFEmbedder struct {
	vectorizer *Vectorizer
}

// NewTFIDFEmbedder creates a TF-IDF-backed Embedder.
func NewTFIDFEmbedder() *TFIDFEmbedder {
	return &TFIDFEmbedder{vectorizer: NewVectorizer()}
}

// WrapVectorizer builds a TFIDFEmbedder around an existing Vectorizer.
// Used when loading persisted state from the DB.
func WrapVectorizer(v *Vectorizer) *TFIDFEmbedder {
	return &TFIDFEmbedder{vectorizer: v}
}

// Vectorizer returns the underlying Vectorizer for persistence code that
// needs to export/import state. Intentionally package-private concept
// exposed only via this explicit accessor.
func (e *TFIDFEmbedder) Vectorizer() *Vectorizer {
	return e.vectorizer
}

// Available always returns nil — TF-IDF has no external dependency.
func (e *TFIDFEmbedder) Available() error {
	return nil
}

// Embed produces a TF-IDF vector. Returns nil (not an error) if vocabulary
// is empty; callers should trigger a RebuildIndex in that case.
func (e *TFIDFEmbedder) Embed(text string, _ InputType) ([]float32, error) {
	vec := e.vectorizer.Vectorize(text)
	return vec, nil
}

// EmbedBatch processes each text individually (TF-IDF has no batch speedup).
func (e *TFIDFEmbedder) EmbedBatch(texts []string, _ InputType) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.vectorizer.Vectorize(t)
	}
	return out, nil
}

// Name returns "tfidf".
func (e *TFIDFEmbedder) Name() string { return "tfidf" }

// Model returns "tfidf" (no concept of model variants).
func (e *TFIDFEmbedder) Model() string { return "tfidf" }

// Dimensions returns the current vocabulary size.
// Returns 0 before the first BuildVocab.
func (e *TFIDFEmbedder) Dimensions() int {
	return e.vectorizer.VocabSize()
}

// BuildVocab rebuilds the vocabulary from a fresh corpus.
// Called from RebuildIndex to reset the TF-IDF state before re-embedding.
func (e *TFIDFEmbedder) BuildVocab(corpus []string) {
	e.vectorizer.BuildVocab(corpus)
}
