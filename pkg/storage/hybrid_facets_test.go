package storage

import (
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/vectors"
)

// keywordEmbedder embeds a text as the vector of the first keyword it contains, else as fallback, so
// a test decides the semantic ranking.
type keywordEmbedder struct {
	keywords []string
	vecs     [][]float32
	fallback []float32
}

func (e *keywordEmbedder) Available() error { return nil }
func (e *keywordEmbedder) Name() string     { return "fake-cloud" }
func (e *keywordEmbedder) Model() string    { return "keyword-v1" }
func (e *keywordEmbedder) Dimensions() int  { return len(e.fallback) }
func (e *keywordEmbedder) Embed(text string, _ vectors.InputType) ([]float32, error) {
	for i, k := range e.keywords {
		if strings.Contains(text, k) {
			return e.vecs[i], nil
		}
	}
	return e.fallback, nil
}
func (e *keywordEmbedder) EmbedBatch(texts []string, t vectors.InputType) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i], _ = e.Embed(text, t)
	}
	return out, nil
}

// With a facet, hybrid search keeps the semantic-only hits that match it. It filtered the semantic
// hits in place into the slice it was still reading, so a semantic-only hit ranked between two
// keyword hits was overwritten and lost.
func TestHybridSearch_KeepsSemanticOnlyHitsThatMatchTheFacets(t *testing.T) {
	store := setupTestStore(t)
	emb := &keywordEmbedder{
		keywords: []string{"doc-one", "doc-two", "doc-three"},
		vecs:     [][]float32{{1, 0.1, 0, 0}, {1, 0.3, 0, 0}, {1, 0.6, 0, 0}},
		fallback: []float32{1, 0, 0, 0},
	}
	vs, err := vectors.NewVectorStore(store.db, emb)
	if err != nil {
		t.Fatal(err)
	}
	store.vectorStore = vs
	one := addTaggedNote(t, store, "c1", "First", "doc-one facetkw", "team")
	two := addTaggedNote(t, store, "c2", "Second", "doc-two only semantic", "team")
	three := addTaggedNote(t, store, "c3", "Third", "doc-three facetkw", "team")

	results, err := store.HybridSearch("facetkw", SearchOpts{Query: "facetkw", Tags: []string{"team"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultIDs(results); !sameSet(got, []string{one.ID, two.ID, three.ID}) {
		t.Errorf("hybrid search --tag team = %v, want the two keyword hits and the semantic-only one %s", got, two.ID)
	}
}
