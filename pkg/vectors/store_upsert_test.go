package vectors

import (
	"strings"
	"testing"
)

// holeyEmbedder mimics a backend that returns a positional nil instead of an
// error for one input (Gemini/OpenAI/Voyage adapters can leave holes): the
// vector for any text containing "TAILMARKER" is nil, all others are a
// constant unit vector.
type holeyEmbedder struct{}

func (holeyEmbedder) Available() error { return nil }
func (holeyEmbedder) Name() string     { return "holey" }
func (holeyEmbedder) Model() string    { return "v1" }
func (holeyEmbedder) Dimensions() int  { return 2 }
func (holeyEmbedder) Embed(text string, t InputType) ([]float32, error) {
	out, err := holeyEmbedder{}.EmbedBatch([]string{text}, t)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}
func (holeyEmbedder) EmbedBatch(texts []string, _ InputType) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, s := range texts {
		if strings.Contains(s, "TAILMARKER") {
			continue // positional hole
		}
		out[i] = []float32{1, 0}
	}
	return out, nil
}

// TestVectorStore_UpsertRejectsDocWithMissingChunk (Codex P2 on the v23 PR):
// a long document whose LAST chunk came back nil must not be stored as a
// shorter "complete" document — otherwise MissingDocumentIDs and health call
// it covered while its tail is unsearchable.
func TestVectorStore_UpsertRejectsDocWithMissingChunk(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	vs, err := NewVectorStore(db, holeyEmbedder{})
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}

	long, _ := longASCIIDoc(60) // several chunks; the marker sits in the tail chunk
	if n := len(ChunkForEmbed(long)); n < 2 {
		t.Fatalf("fixture must chunk, got %d chunk(s)", n)
	}
	docs := []Document{
		{ID: "long", Text: long},
		{ID: "short", Text: "a short note that embeds fine"},
	}
	indexed, err := vs.UpsertDocuments(docs)
	if err != nil {
		t.Fatalf("UpsertDocuments: %v", err)
	}
	if indexed != 1 {
		t.Fatalf("want 1 document indexed (the short one), got %d", indexed)
	}
	var longRows, shortRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = 'long'`).Scan(&longRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = 'short'`).Scan(&shortRows); err != nil {
		t.Fatal(err)
	}
	if longRows != 0 || shortRows != 1 {
		t.Fatalf("want long=0 rows (rejected), short=1 row; got long=%d short=%d", longRows, shortRows)
	}
}
